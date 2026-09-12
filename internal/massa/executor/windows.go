package executor

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"acessos-go/internal/massa/model"
)

// ShellWindowsPadrao e o processo aberto do outro lado. `-Command -`
// faz o PowerShell ler os comandos da entrada padrao, o que da uma
// sessao persistente: um `cd` no comando 1 continua valendo no 2,
// igual ao Linux.
const ShellWindowsPadrao = `powershell -NoLogo -NoProfile -NonInteractive -Command -`

// WindowsExec executa comandos em Windows atraves do OpenSSH Server
// nativo (recurso opcional do Win10 1809+ / Server 2019+).
//
// Diferencas relevantes em relacao ao Linux:
//
//   - NAO ha PTY. O `su` do Linux exige terminal; aqui nao existe
//     elevacao dentro da sessao, entao stdout e stderr chegam por
//     canais separados e sao juntados do lado do Go.
//   - NAO ha elevacao. No Windows ou a credencial ja e administrativa,
//     ou nao sobe: o UAC nao se aplica a logon de rede. Elevar()
//     devolve erro de proposito, para nunca falhar em silencio.
//   - O PowerShell trata base64 como UTF-16LE. Por isso a decodificacao
//     do outro lado e explicitamente UTF-8.
//   - Console em Windows pt-BR costuma sair em CP850; forcamos UTF-8 na
//     abertura da sessao, senao acento vira lixo no log.
type WindowsExec struct {
	cliente *ssh.Client
	sessao  *ssh.Session
	stdin   io.WriteCloser

	dados chan []byte
	buf   []byte

	// ShellCmd permite trocar o processo remoto. Serve para teste: a
	// variavel PDVT_WINSHELL aponta para um stub que imita o
	// subconjunto de PowerShell que este protocolo usa.
	ShellCmd string
}

func NovoWindows() (Executor, error) {
	sh := ShellWindowsPadrao
	if v := os.Getenv("PDVT_WINSHELL"); v != "" {
		sh = v
	}
	return &WindowsExec{ShellCmd: sh}, nil
}

// UsuarioWindows monta o login aceito pelo OpenSSH do Windows.
// Sem dominio, devolve o usuario puro (conta local da maquina).
func UsuarioWindows(cred model.Credencial) string {
	u := strings.TrimSpace(cred.Usuario)
	d := strings.TrimSpace(cred.Dominio)
	if d == "" {
		return u
	}
	// se ja veio como DOMINIO\usuario ou usuario@dominio, respeita
	if strings.Contains(u, `\`) || strings.Contains(u, "@") {
		return u
	}
	return d + `\` + u
}

func (w *WindowsExec) Conectar(host string, cred model.Credencial, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	cfg := &ssh.ClientConfig{
		User:            UsuarioWindows(cred),
		Auth:            []ssh.AuthMethod{ssh.Password(cred.Senha), senhaInterativa(cred.Senha)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Config:          AlgoritmosLegado(),
		Timeout:         timeout,
	}

	endereco := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		endereco = net.JoinHostPort(host, "22")
	}

	conn, err := net.DialTimeout("tcp", endereco, timeout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout + 10*time.Second))

	c, chans, reqs, err := ssh.NewClientConn(conn, endereco, cfg)
	if err != nil {
		conn.Close()
		if ehAuth(err) {
			return fmt.Errorf("%w: usuario, dominio ou senha incorretos", ErrAutenticar)
		}
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}
	_ = conn.SetDeadline(time.Time{})
	w.cliente = ssh.NewClient(c, chans, reqs)

	sess, err := w.cliente.NewSession()
	if err != nil {
		w.Fechar()
		return fmt.Errorf("%w: nao foi possivel abrir sessao: %v", ErrConexao, err)
	}
	w.sessao = sess

	stdin, err := sess.StdinPipe()
	if err != nil {
		w.Fechar()
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}
	w.stdin = stdin

	stdout, err := sess.StdoutPipe()
	if err != nil {
		w.Fechar()
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		w.Fechar()
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}

	w.dados = make(chan []byte, 64)
	// Sem PTY os dois canais vem separados; juntamos aqui para que a
	// leitura por marcador enxergue um fluxo unico, como no Linux.
	go w.bombear(stdout)
	go w.bombear(stderr)

	if err := sess.Start(w.ShellCmd); err != nil {
		w.Fechar()
		return fmt.Errorf("%w: nao foi possivel iniciar o PowerShell: %v", ErrConexao, err)
	}

	if err := w.prepararShell(20 * time.Second); err != nil {
		w.Fechar()
		return err
	}
	return nil
}

func (w *WindowsExec) bombear(r io.Reader) {
	b := make([]byte, 32*1024)
	for {
		n, err := r.Read(b)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, b[:n])
			w.dados <- cp
		}
		if err != nil {
			return
		}
	}
}

func (w *WindowsExec) enviar(linha string) error {
	if w.stdin == nil {
		return ErrConexao
	}
	_, err := io.WriteString(w.stdin, linha+"\r\n")
	return err
}

// ScriptPreparo normaliza o ambiente do PowerShell na abertura.
// Exportado para poder ser inspecionado em teste.
func ScriptPreparo() []string {
	return []string{
		`$ErrorActionPreference = 'Continue'`,
		`$ProgressPreference = 'SilentlyContinue'`,
		// sem isto, saida com acento em console pt-BR vira lixo
		`try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}`,
		`try { $OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}`,
		`$FormatEnumerationLimit = -1`,
		// marcadores em variavel encurtam cada linha de comando; as
		// aspas partidas impedem que esta linha case com o regex
		`$__ZB = '__ZBE' + 'GIN__'; $__ZE = '__ZE' + 'ND__'; $__ZS = '__ZSY' + 'NC__'`,
	}
}

func (w *WindowsExec) prepararShell(timeout time.Duration) error {
	for _, c := range ScriptPreparo() {
		if err := w.enviar(c); err != nil {
			return fmt.Errorf("%w: %v", ErrConexao, err)
		}
	}
	return w.sincronizar(timeout)
}

func (w *WindowsExec) sincronizar(timeout time.Duration) error {
	if err := w.enviar(`Write-Output $__ZS`); err != nil {
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}
	_, _, err := w.lerAte(regexp.MustCompile(marcadorSync), timeout, 0)
	return err
}

// lerAte funciona igual a versao Linux: acumula ate o regex casar e
// guarda o resto para a proxima leitura.
func (w *WindowsExec) lerAte(re *regexp.Regexp, absoluto, idle time.Duration) (string, string, error) {
	var acc []byte
	acc = append(acc, w.buf...)
	w.buf = nil

	if loc := re.FindIndex(acc); loc != nil {
		w.buf = append([]byte{}, acc[loc[1]:]...)
		return string(acc[:loc[0]]), string(acc[loc[0]:loc[1]]), nil
	}

	var chAbs <-chan time.Time
	if absoluto > 0 {
		t := time.NewTimer(absoluto)
		defer t.Stop()
		chAbs = t.C
	}
	var tIdle *time.Timer
	var chIdle <-chan time.Time
	if idle > 0 {
		tIdle = time.NewTimer(idle)
		defer tIdle.Stop()
		chIdle = tIdle.C
	}

	for {
		select {
		case b, ok := <-w.dados:
			if !ok {
				return string(acc), "", fmt.Errorf("%w: sessao encerrada pelo host", ErrConexao)
			}
			acc = append(acc, b...)
			if tIdle != nil {
				if !tIdle.Stop() {
					select {
					case <-tIdle.C:
					default:
					}
				}
				tIdle.Reset(idle)
			}
			if loc := re.FindIndex(acc); loc != nil {
				w.buf = append([]byte{}, acc[loc[1]:]...)
				return string(acc[:loc[0]]), string(acc[loc[0]:loc[1]]), nil
			}
		case <-chAbs:
			return string(acc), "", ErrTimeout
		case <-chIdle:
			return string(acc), "", ErrIdle
		}
	}
}

// Elevar nao existe no Windows. Devolver erro e proposital: se a
// interface um dia pedir elevacao numa aba Windows, tem de estourar
// aqui, e nao passar batido executando sem privilegio.
func (w *WindowsExec) Elevar(model.Credencial) error {
	return fmt.Errorf("%w: no Windows nao ha elevacao dentro da sessao; "+
		"use uma credencial que ja seja administrativa", ErrElevar)
}

// LinhasComando monta as linhas de PowerShell de um comando. Fica
// separado da execucao para poder ser testado sem rede.
// codigoSaidaPS calcula o exit code combinando LASTEXITCODE (so
// preenchido por executavel nativo) com $? (que cmdlet mexe).
const codigoSaidaPS = `$ZC = 0; ` +
	`if ($LASTEXITCODE -ne $null -and $LASTEXITCODE -ne 0) { $ZC = $LASTEXITCODE } ` +
	`elseif (-not $ZOK) { $ZC = 1 }; ` +
	`Write-Output ("$__ZE" + ":" + $ZC + ":")`

// PodeIrLiteralWin e o equivalente PowerShell de PodeIrLiteral. Alem
// das mesmas regras, considera a crase como escape (e nao como
// delimitador, ao contrario do bash).
func PodeIrLiteralWin(cmd string) bool {
	if cmd == "" || len(cmd) > 1200 {
		return false
	}
	if strings.ContainsAny(cmd, "\n\r") {
		return false
	}
	if strings.Contains(cmd, "__ZBEGIN__") || strings.Contains(cmd, "__ZEND__") ||
		strings.Contains(cmd, "__ZSYNC__") {
		return false
	}

	var aspas byte
	prof := 0
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if c == '`' { // crase e o escape do PowerShell
			i++
			continue
		}
		if aspas != 0 {
			if c == aspas {
				aspas = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			aspas = c
		case '#':
			return false
		case '(', '{', '[':
			prof++
		case ')', '}', ']':
			prof--
			if prof < 0 {
				return false
			}
		}
	}
	if aspas != 0 || prof != 0 {
		return false
	}

	t := strings.TrimRight(cmd, " \t")
	for _, suf := range []string{"`", "|", ";", ","} {
		if strings.HasSuffix(t, suf) {
			return false
		}
	}
	return true
}

func LinhasComando(cmd string) []string {
	cmd = strings.TrimRight(cmd, " \t\n")

	// Modo literal: o comando vai como foi escrito, para quem olhar a
	// sessao do lado do Windows ver o que foi de fato executado.
	if PodeIrLiteralWin(cmd) {
		return []string{
			`$global:LASTEXITCODE = 0; Write-Output $__ZB; ` +
				cmd + `; $ZOK = $?; ` + codigoSaidaPS,
		}
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(cmd))

	linhas := []string{`$ZB = ''`}
	for i := 0; i < len(b64); i += tamChunkB64 {
		j := i + tamChunkB64
		if j > len(b64) {
			j = len(b64)
		}
		// o alfabeto base64 nao contem aspa simples: quoting seguro
		linhas = append(linhas, `$ZB += '`+b64[i:j]+`'`)
	}

	linhas = append(linhas,
		// UTF-8 explicito: o padrao do PowerShell para base64 e UTF-16LE
		`$ZCMD = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($ZB))`,
		`$global:LASTEXITCODE = 0`,
		`Write-Output $__ZB`,
		// 2>&1 traz o fluxo de erro junto; Out-String -Stream formata
		// objeto de cmdlet em texto legivel
		`try { Invoke-Expression $ZCMD 2>&1 | Out-String -Stream | Write-Output; $ZOK = $? } `+
			`catch { Write-Output $_.Exception.Message; $ZOK = $false }`,
		codigoSaidaPS,
	)
	return linhas
}

func (w *WindowsExec) Rodar(cmd string, timeoutExec, timeoutIdle time.Duration) (string, int, error) {
	if strings.TrimSpace(cmd) == "" {
		return "", 0, nil
	}

	for _, l := range LinhasComando(cmd) {
		if err := w.enviar(l); err != nil {
			return "", -1, fmt.Errorf("%w: %v", ErrConexao, err)
		}
	}

	// descarta o eco e a montagem do $ZB, tudo que vem antes do inicio
	if _, _, err := w.lerAte(regexp.MustCompile(marcadorInic), timeoutExec, timeoutIdle); err != nil {
		return "", -1, err
	}

	saida, casado, err := w.lerAte(reFim, timeoutExec, timeoutIdle)
	saida = limpar(saida)
	if err != nil {
		return saida, -1, err
	}
	return saida, extrairCodigoWin(casado), nil
}

func extrairCodigoWin(s string) int {
	m := reFim.FindStringSubmatch(s)
	if m == nil {
		return -1
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return v
}

func (w *WindowsExec) Fechar() {
	if w.stdin != nil {
		_ = w.enviar("exit")
		_ = w.stdin.Close()
		w.stdin = nil
	}
	if w.sessao != nil {
		_ = w.sessao.Close()
		w.sessao = nil
	}
	if w.cliente != nil {
		_ = w.cliente.Close()
		w.cliente = nil
	}
}
