// Sessão para PDV **Linux** — o alvo, não a máquina que roda o programa.
//
// O arquivo chamava-se ssh_linux.go, e o Go tratava o sufixo como
// restrição de plataforma: no build para Windows ele era excluído e o
// pacote nem compilava. O código aqui é Go puro e vale em qualquer
// sistema; por isso o nome não pode carregar nome de sistema no fim.
package executor

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"acessos-go/internal/massa/model"
)

// Os marcadores sao escritos quebrados no meio ("__ZE" "ND__") para que
// o eco do proprio comando digitado nunca case com o regex de leitura.
const (
	marcadorSync  = "__ZSYNC__"
	marcadorVivo  = "__ZVIVO__" // fase 1: o shell le a entrada?
	marcadorInic  = "__ZBEGIN__"
	marcadorFim   = "__ZEND__"
	tamChunkB64   = 900 // evita estourar o limite de linha do terminal
	timeoutMarcad = 30 * time.Second
)

var (
	reSync = regexp.MustCompile(marcadorSync)
	reVivo = regexp.MustCompile(marcadorVivo)
	reInic = regexp.MustCompile(marcadorInic)
	reFim  = regexp.MustCompile(`__ZEND__:(\d+):`)
)

// LinuxSSH mantem uma sessao SSH com PTY e um shell interativo vivo.
type LinuxSSH struct {
	cliente *ssh.Client
	sessao  *ssh.Session
	stdin   io.WriteCloser

	dados chan []byte // bytes vindos do shell remoto
	fim   chan error  // fecha quando o leitor morre
	buf   []byte      // resto nao consumido da ultima leitura

	elevado bool
}

func NovoLinuxSSH() *LinuxSSH { return &LinuxSSH{} }

func (l *LinuxSSH) Conectar(host string, cred model.Credencial, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	cfg := &ssh.ClientConfig{
		User:            cred.Usuario,
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
			return fmt.Errorf("%w: usuario ou senha do PDV incorretos", ErrAutenticar)
		}
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}
	_ = conn.SetDeadline(time.Time{})
	l.cliente = ssh.NewClient(c, chans, reqs)

	sess, err := l.cliente.NewSession()
	if err != nil {
		l.Fechar()
		return fmt.Errorf("%w: nao foi possivel abrir sessao: %v", ErrConexao, err)
	}
	l.sessao = sess

	// PTY e obrigatorio: o `su` so le senha de um terminal, nao de pipe.
	modos := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.ECHOCTL:       0,
		ssh.ICANON:        1,
		ssh.TTY_OP_ISPEED: 38400,
		ssh.TTY_OP_OSPEED: 38400,
	}
	if err := sess.RequestPty("dumb", 5000, 500, modos); err != nil {
		l.Fechar()
		return fmt.Errorf("%w: PTY negado: %v", ErrConexao, err)
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		l.Fechar()
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}
	l.stdin = stdin

	// Com PTY, stderr vem misturado no stdout. E o preco de usar `su`.
	stdout, err := sess.StdoutPipe()
	if err != nil {
		l.Fechar()
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}

	l.dados = make(chan []byte, 64)
	l.fim = make(chan error, 1)
	go l.bombear(stdout)

	if err := sess.Shell(); err != nil {
		l.Fechar()
		return fmt.Errorf("%w: shell negado: %v", ErrConexao, err)
	}

	// Descarta banner/MOTD e normaliza o ambiente.
	if err := l.prepararShell(15 * time.Second); err != nil {
		l.Fechar()
		return err
	}
	return nil
}

func (l *LinuxSSH) bombear(r io.Reader) {
	b := make([]byte, 32*1024)
	for {
		n, err := r.Read(b)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, b[:n])
			l.dados <- cp
		}
		if err != nil {
			l.fim <- err
			close(l.dados)
			return
		}
	}
}

func (l *LinuxSSH) enviar(linha string) error {
	if l.stdin == nil {
		return ErrConexao
	}
	_, err := io.WriteString(l.stdin, linha+"\n")
	return err
}

// LinhasPreparo devolve o preparo do shell remoto.
//
//	stty -echo        evita o eco (quando o terminal respeita)
//	PS1/PS2/PROMPT_*  tira prompt residual da saida
//	unalias -a        impede que um `alias rm='rm -i'` no .bashrc
//	                  transforme o comando em interativo e trave a fila
//
// Sao DUAS linhas, de proposito.
//
// A primeira so desliga o historico. E a parte mais nova e a que tem
// mais chance de nao ser aceita por um shell diferente; isolada, um
// engasgo nela nao leva junto o resto do preparo, que e o que faz a
// sessao funcionar.
//
// As aspas partidas ('__ZBE”GIN__') fazem o shell concatenar sem que
// a propria linha case com o regex de leitura.
func LinhasPreparo() []string {
	return []string{
		// `set +o history` para de registrar; `unset HISTFILE` impede o
		// bash de gravar no ~/.bash_history ao sair. Sem isto, cada
		// execucao deixa o protocolo no historico do PDV, e um comando
		// que carregue senha ficaria gravado em disco.
		//
		// De proposito NAO usamos `history -c`: isso apagaria o
		// historico legitimo do operador. So paramos de escrever.
		"set +o history 2>/dev/null || true; unset HISTFILE 2>/dev/null || true",

		"stty -echo 2>/dev/null; PS1=''; PS2=''; PROMPT_COMMAND=''; " +
			"unalias -a 2>/dev/null; " +
			"__ZB='__ZBE''GIN__'; __ZE='__ZE''ND__'; __ZS='__ZSY''NC__'; true",
	}
}

// prepararShell normaliza o shell remoto em DUAS fases, de proposito.
//
// Fase 1 sincroniza com um echo cru, antes de mexer em qualquer coisa:
// prova que o shell esta vivo e lendo a entrada. Fase 2 aplica o
// preparo e sincroniza de novo.
//
// Separar as duas distingue "o PDV nao respondeu nada" de "o PDV
// respondia ate a minha linha de preparo derrubar alguma coisa" - que
// sao problemas completamente diferentes e antes apareciam iguais.
//
// Em qualquer falha o texto recebido vai junto no erro. E quase sempre
// nele (MOTD, mensagem de erro do shell) que esta a resposta; a versao
// anterior descartava esses bytes e sobrava so "timeout".
func (l *LinuxSSH) prepararShell(timeout time.Duration) error {
	// Tudo vai numa tacada so e a leitura e que separa as fases. Duas
	// esperas em sequencia custariam um round-trip a mais por PDV, o
	// que em link de filial pesa; com dois marcadores distintos o
	// diagnostico e o mesmo por uma viagem so.
	envio := []string{`echo "__ZVI""VO__"`}
	envio = append(envio, LinhasPreparo()...)
	envio = append(envio, `echo $__ZS`)
	for _, linha := range envio {
		if err := l.enviar(linha); err != nil {
			return fmt.Errorf("%w: %v", ErrConexao, err)
		}
	}

	// --- fase 1: o shell chegou a ler a entrada? ---
	recebido, _, err := l.lerAte(reVivo, timeout, 0)
	if err != nil {
		return fmt.Errorf("%w: o shell do PDV nao respondeu a um echo simples em %s.\n"+
			"Isso costuma ser profile/MOTD esperando entrada, shell travado ou "+
			"login sem shell interativo.\nRecebido antes do silencio:\n%s",
			ErrPreparo, timeout, amostra(recebido))
	}

	// --- fase 2: o preparo foi digerido? ---
	recebido, _, err = l.lerAte(reSync, timeout, 0)
	if err != nil {
		return fmt.Errorf("%w: o shell respondeu ao echo simples, mas parou depois da "+
			"linha de preparo.\nProvavel shell sem suporte a alguma opcao "+
			"(`set +o history`, `unalias`) ou que nao aceita a atribuicao das "+
			"variaveis de marcador.\nRecebido:\n%s",
			ErrPreparo, amostra(recebido))
	}
	return nil
}

// amostra recorta o que veio do PDV para caber num log sem virar
// despejo. Mantem o FIM, que e onde costuma estar a mensagem de erro.
func amostra(s string) string {
	s = strings.TrimSpace(limpar(s))
	if s == "" {
		return "  (nada - o PDV nao mandou um unico byte)"
	}
	const max = 800
	if len(s) > max {
		s = "...(cortado)...\n" + s[len(s)-max:]
	}
	var b strings.Builder
	for _, linha := range strings.Split(s, "\n") {
		b.WriteString("  | " + linha + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// sincronizar descarta tudo que houver no buffer ate o marcador chegar.
func (l *LinuxSSH) sincronizar(timeout time.Duration) error {
	if err := l.enviar(`echo $__ZS`); err != nil {
		return fmt.Errorf("%w: %v", ErrConexao, err)
	}
	_, _, err := l.lerAte(reSync, timeout, 0)
	return err
}

// lerAte acumula saida ate o regex casar. Devolve o texto anterior ao
// casamento, o proprio trecho casado, e guarda o resto para a proxima
// leitura.
func (l *LinuxSSH) lerAte(re *regexp.Regexp, absoluto, idle time.Duration) (string, string, error) {
	var acc []byte
	acc = append(acc, l.buf...)
	l.buf = nil

	inicio := time.Now()
	ultimoByte := time.Time{}

	if loc := re.FindIndex(acc); loc != nil {
		l.buf = append([]byte{}, acc[loc[1]:]...)
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
		case b, ok := <-l.dados:
			if !ok {
				return string(acc), "", fmt.Errorf("%w: sessao encerrada pelo host", ErrConexao)
			}
			acc = append(acc, b...)
			ultimoByte = time.Now()
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
				l.buf = append([]byte{}, acc[loc[1]:]...)
				return string(acc[:loc[0]]), string(acc[loc[0]:loc[1]]), nil
			}
		case <-chAbs:
			return string(acc), "", fmt.Errorf("%w apos %s: %s",
				ErrTimeout, time.Since(inicio).Round(time.Second),
				diagnostico(len(acc), inicio, ultimoByte))
		case <-chIdle:
			return string(acc), "", fmt.Errorf("%w apos %s: %s",
				ErrIdle, time.Since(inicio).Round(time.Second),
				diagnostico(len(acc), inicio, ultimoByte))
		}
	}
}

// Elevar executa `su - <usuario>` dentro da sessao ja aberta.
func (l *LinuxSSH) Elevar(cred model.Credencial) error {
	usuario := cred.Usuario
	if usuario == "" {
		usuario = "root"
	}

	if err := l.enviar("su - " + usuario); err != nil {
		return fmt.Errorf("%w: %v", ErrElevar, err)
	}

	// Espera o prompt de senha (Password: / Senha:).
	rePrompt := regexp.MustCompile(`(?i)(password|senha)\s*:`)
	if _, _, err := l.lerAte(rePrompt, 15*time.Second, 0); err != nil {
		return fmt.Errorf("%w: nao veio prompt de senha do su", ErrElevar)
	}
	if err := l.enviar(cred.Senha); err != nil {
		return fmt.Errorf("%w: %v", ErrElevar, err)
	}

	// O shell novo do su tem eco e prompt proprios: normaliza de novo.
	// A espera curta e necessaria: enviar o preparo antes do shell novo
	// assumir a entrada faz as linhas se perderem na troca. Tentei
	// remover para economizar tempo e a elevacao quebrou.
	time.Sleep(300 * time.Millisecond)
	if err := l.prepararShell(15 * time.Second); err != nil {
		return fmt.Errorf("%w: shell do su nao respondeu", ErrElevar)
	}

	// Confirma de verdade que virou root.
	saida, code, err := l.Rodar("id -u", 15*time.Second, 0)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrElevar, err)
	}
	uid := strings.TrimSpace(saida)
	if code != 0 || uid != "0" {
		return fmt.Errorf("%w: senha de %s incorreta ou su bloqueado (uid=%q)", ErrElevar, usuario, uid)
	}
	l.elevado = true
	return nil
}

// Rodar envia o bloco codificado em base64 e le ate o marcador de fim.
//
// O base64 existe para que aspas, barras invertidas, heredocs, acentos e
// quebras de linha cheguem intactos ao outro lado - foi exatamente isso
// que o script antigo quebrava ao interpolar em bash -c '...'.
// O `eval` roda no shell atual, entao `cd /tmp` no comando 1 continua
// valendo no comando 2.
func (l *LinuxSSH) Rodar(cmd string, timeoutExec, timeoutIdle time.Duration) (string, int, error) {
	if strings.TrimSpace(cmd) == "" {
		return "", 0, nil
	}

	for _, linha := range LinhasComandoLinux(cmd) {
		if err := l.enviar(linha); err != nil {
			return "", -1, fmt.Errorf("%w: %v", ErrConexao, err)
		}
	}

	// Tudo que vier antes do marcador de inicio e ruido de protocolo: eco
	// do shell remoto, as linhas de montagem do ZCMD, prompt residual.
	// Descartar por marcador em vez de confiar no `stty -echo` torna a
	// saida limpa mesmo em PDV cujo profile reativa o eco.
	if _, _, err := l.lerAte(reInic, timeoutMarcad, 0); err != nil {
		return "", -1, fmt.Errorf("%w: shell nao respondeu ao marcador de inicio", ErrConexao)
	}

	saida, casado, err := l.lerAte(reFim, timeoutExec, timeoutIdle)
	saida = limpar(saida)
	if err != nil {
		return saida, -1, err
	}
	return saida, extrairCodigo(casado), nil
}

// PodeIrLiteral decide se um comando pode ser concatenado direto na
// linha de execucao, em vez de viajar em base64.
//
// O criterio e deliberadamente conservador: na duvida, base64. O risco
// do modo literal nao e corromper dados (o shell executa exatamente o
// que foi escrito), e sim engolir o marcador de fim - um `#` solto
// comenta o resto da linha, uma aspa aberta faz o shell continuar
// esperando. Nesses casos a leitura estouraria no timeout.
//
// Por isso exigimos: uma linha so, aspas e parenteses equilibrados,
// nenhum `#` fora de aspas, e um final que nao deixe a linha pendurada.
func PodeIrLiteral(cmd string) bool {
	if cmd == "" || len(cmd) > 1500 {
		return false
	}
	if strings.ContainsAny(cmd, "\n\r") {
		return false
	}
	// nao deixar o comando forjar nossos proprios marcadores
	if strings.Contains(cmd, "__ZBEGIN__") || strings.Contains(cmd, "__ZEND__") ||
		strings.Contains(cmd, "__ZSYNC__") {
		return false
	}

	var aspas byte
	prof := 0 // parenteses e chaves
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		if c == '\\' { // escape consome o proximo byte
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
		case '\'', '"', '`':
			aspas = c
		case '#':
			// comentario engoliria o marcador de fim
			return false
		case '(', '{':
			prof++
		case ')', '}':
			prof--
			if prof < 0 {
				return false
			}
		}
	}
	if aspas != 0 || prof != 0 {
		return false
	}

	// final que deixaria a linha incompleta ou mudaria a semantica do
	// `; echo` que vem depois
	t := strings.TrimRight(cmd, " \t")
	for _, suf := range []string{"\\", "&", "|", "&&", "||", ";", "<", ">", ">>"} {
		if strings.HasSuffix(t, suf) {
			return false
		}
	}
	return true
}

// limiteLinhaUnica e o tamanho maximo de uma linha enviada de uma vez.
// O limite duro do modo canonico do terminal costuma ser 4096 bytes
// (MAX_CANON); ficamos bem abaixo por seguranca.
const limiteLinhaUnica = 2000

// LinhasComandoLinux monta o que vai pela sessao para executar um
// comando. Na esmagadora maioria dos casos devolve UMA linha.
//
// O comando viaja em base64 porque foi assim que se resolveu de vez o
// problema de aspas, heredoc, barra invertida e acento. So que cada
// linha extra e ruido para quem observa a sessao do lado do PDV, entao
// so fatiamos quando o comando e grande demais para uma linha.
func LinhasComandoLinux(cmd string) []string {
	cmd = strings.TrimRight(cmd, " \t\n")

	// Modo literal: o comando vai como voce escreveu, sem base64.
	// Quem estiver olhando a sessao do lado do PDV ve exatamente o que
	// foi executado, e nao um blob. So e usado quando o comando passa
	// na checagem conservadora de PodeIrLiteral.
	if PodeIrLiteral(cmd) {
		return []string{`echo $__ZB; ` + cmd + `; echo "$__ZE:$?:"`}
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(cmd))

	executa := func(fonte string) string {
		return `echo $__ZB; eval "$(printf '%s' ` + fonte +
			` | base64 -d)"; echo "$__ZE:$?:"`
	}

	// cabe tudo numa linha
	if len(b64)+80 <= limiteLinhaUnica {
		return []string{executa("'" + b64 + "'")}
	}

	// comando grande: monta a variavel em pedacos
	linhas := []string{"ZCMD=''"}
	for i := 0; i < len(b64); i += tamChunkB64 {
		j := i + tamChunkB64
		if j > len(b64) {
			j = len(b64)
		}
		// o alfabeto base64 nao contem aspa simples: quoting seguro
		linhas = append(linhas, "ZCMD=$ZCMD'"+b64[i:j]+"'")
	}
	return append(linhas, executa(`"$ZCMD"`))
}

func extrairCodigo(s string) int {
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

func limpar(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Trim(s, "\n")
}

func (l *LinuxSSH) Fechar() {
	if l.stdin != nil {
		_ = l.enviar("exit")
		_ = l.stdin.Close()
		l.stdin = nil
	}
	if l.sessao != nil {
		_ = l.sessao.Close()
		l.sessao = nil
	}
	if l.cliente != nil {
		_ = l.cliente.Close()
		l.cliente = nil
	}
}

// senhaInterativa cobre servidores configurados com keyboard-interactive
// em vez de password puro.
func senhaInterativa(senha string) ssh.AuthMethod {
	return ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		respostas := make([]string, len(questions))
		for i := range questions {
			respostas[i] = senha
		}
		return respostas, nil
	})
}

func ehAuth(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unable to authenticate") ||
		strings.Contains(s, "permission denied") ||
		strings.Contains(s, "no supported methods")
}

// diagnostico descreve o que a sessao produziu antes de estourar. Sem
// isto, "timeout de execucao" nao distingue comando lento de sessao
// morta, que sao problemas completamente diferentes.
func diagnostico(bytes int, inicio, ultimo time.Time) string {
	if bytes == 0 {
		return "nenhum byte recebido - o PDV nao respondeu nada (sessao travada, " +
			"comando esperando entrada, ou link caiu)"
	}
	return fmt.Sprintf("%d bytes recebidos, ultimo ha %s - o comando estava "+
		"produzindo saida, provavelmente so precisa de mais tempo",
		bytes, time.Since(ultimo).Round(time.Second))
}
