package telaproc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ArgWorker é o primeiro argumento que põe o binário em modo filho. O
// processo principal reexecuta A SI MESMO (os.Executable) com
//
//	acessos <ArgWorker> <protocolo> <endereço> <token>
//
// Reexecutar o próprio binário, em vez de instalar um segundo executável,
// é o que mantém o empacotamento intocado: nada muda no Flatpak nem no
// instalador do Windows, e não há como as duas metades saírem de versão.
const ArgWorker = "-tela-worker"

// MaxSessoes é o TETO de sessões remotas em processo próprio, simultâneas,
// no app inteiro — não por aba, não por protocolo: o total.
//
// LEIA ISTO ANTES DE MEXER EM QUALQUER COISA QUE CRIE SESSÃO. Cada aba de
// tela remota agora custa um PROCESSO, não uma goroutine. O que antes
// crescia alguns megabytes por aba hoje cresce um binário inteiro mais o
// framebuffer da máquina remota, e some do radar de quem olha só o
// processo principal. Sem um teto, "abri as abas todas" deixa de ser um
// app pesado e vira uma máquina no chão — que é exatamente o acidente que
// esta arquitetura existe para EVITAR, não para causar.
//
// O número sai de MEDIÇÃO, não de chute. TestAoVivoCustoDeUmFilho
// (cmd/acessos/telaworker_aovivo_test.go) mede um filho de verdade contra
// um servidor real. Medição de referência, sessão de 1024x768:
//
//	RSS 118,3 MiB | PSS 94,5 MiB | PRIVADA 86,7 MiB
//
// O que manda é a PRIVADA (~87 MiB): o RSS conta o binário e as bibliotecas
// que todos os filhos compartilham, então ele triplica o custo da segunda
// sessão em diante. Uma tela maior sobe pouco isso — 1920x1080 acrescenta
// ~5 MiB de framebuffer sobre 1024x768, não o dobro.
//
//	12 sessões x ~87 MiB ~= 1,0 GiB no pior caso.
//
// Um giga é o que se pode gastar sem pensar numa máquina de operação; é daí
// que vem o 12. Mudar este número exige REFAZER a medição, não estimar a
// partir dela: se um dia o filho engordar, o teto antigo passa a valer
// outra coisa em memória.
//
// Cuidado ao portar o VNC para cá: hoje ele roda DENTRO do processo
// principal, e o histórico registra operador com ~30 telas VNC abertas ao
// mesmo tempo (ver o comentário sobre wl_data_source em
// cmd/acessos/clipboard.go). Com VNC também em processo próprio, 30 telas
// passam a bater neste teto — a conta tem que ser refeita com a medição de
// um filho VNC, e não simplesmente dobrada.
//
// Estourar o teto não derruba nada: Iniciar recusa com ErrLotado, a aba
// mostra no log e fica em "CAIU" até alguém fechar outra sessão.
const MaxSessoes = 12

// vivas conta os filhos de pé. Sobe em Iniciar e desce em Encerrar, uma
// vez só por Processo (daí o sync.Once): Encerrar é chamado tanto pelo
// caminho normal quanto por defer em caminho de erro.
var vivas atomic.Int32

// ErrLotado é o que Iniciar devolve quando o teto foi atingido.
var ErrLotado = fmt.Errorf(
	"limite de %d sessões remotas simultâneas atingido — feche uma aba de tela antes de abrir outra",
	MaxSessoes)

// SessoesVivas é quantos filhos estão de pé agora (para o diagnóstico).
func SessoesVivas() int { return int(vivas.Load()) }

// prazoHandshake é quanto esperamos o filho nascer e se apresentar. Ele não
// faz nada antes disso além de abrir um socket, então segundos de sobra
// aqui só servem para máquina muito carregada.
const prazoHandshake = 20 * time.Second

// Processo é a ponta do processo PRINCIPAL: o filho vivo mais o canal
// para ele.
type Processo struct {
	*Conn
	cmd      *exec.Cmd
	baixaUma sync.Once
}

// Iniciar sobe um processo-filho para o protocolo pedido ("rdp") e espera
// ele se conectar de volta e se identificar.
//
// O socket escuta em 127.0.0.1 numa porta efêmera e aceita UMA conexão, que
// precisa apresentar um token de 32 bytes sorteado agora. Loopback já
// restringe muito, mas em máquina compartilhada qualquer processo local
// poderia correr para se conectar primeiro e passar a receber a tela e as
// teclas da sessão — o token é o que fecha essa porta.
func Iniciar(protocolo string) (*Processo, error) {
	// A reserva vem ANTES de qualquer trabalho: se já estamos no teto,
	// não custa nem um fork descobrir isso.
	if vivas.Add(1) > MaxSessoes {
		vivas.Add(-1)
		return nil, ErrLotado
	}
	p, err := iniciar(protocolo)
	if err != nil {
		vivas.Add(-1)
		return nil, err
	}
	return p, nil
}

func iniciar(protocolo string) (*Processo, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("não achei o próprio executável: %w", err)
	}

	tokenBruto := make([]byte, 32)
	if _, err := rand.Read(tokenBruto); err != nil {
		return nil, fmt.Errorf("sem entropia para o token: %w", err)
	}
	token := []byte(hex.EncodeToString(tokenBruto))

	ln, err := escutarLocal()
	if err != nil {
		return nil, err
	}
	defer ln.Close()

	cmd := exec.Command(exe, ArgWorker, protocolo, ln.Addr().String(), string(token))
	// stdout e stderr HERDADOS: é assim que o log do filho (e o da
	// libfreerdp) cai no mesmo lugar do log do processo principal —
	// inclusive no Windows, onde iniciarLog já trocou os handles padrão
	// do processo antes de qualquer conexão existir.
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	semJanela(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("não consegui iniciar o processo da sessão: %w", err)
	}

	p, err := aceitar(ln, token)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	p.cmd = cmd
	return p, nil
}

// escutarLocal abre o ponto de encontro do filho. Loopback e porta
// efêmera: nada disto é alcançável de fora da máquina.
func escutarLocal() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("não consegui abrir o canal local: %w", err)
	}
	return ln, nil
}

func aceitar(ln net.Listener, token []byte) (*Processo, error) {
	if tl, ok := ln.(*net.TCPListener); ok {
		_ = tl.SetDeadline(time.Now().Add(prazoHandshake))
	}
	c, err := ln.Accept()
	if err != nil {
		return nil, fmt.Errorf("o processo da sessão não se conectou: %w", err)
	}
	// Nagle atrapalha aqui: quase todo comando é um pacote minúsculo
	// (mover ponteiro, uma tecla) e esperar por companhia adiciona atraso
	// visível na sessão remota.
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	_ = c.SetReadDeadline(time.Now().Add(prazoHandshake))

	conn := novaConn(c)
	tipo, corpo, err := conn.Ler()
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("o processo da sessão não se apresentou: %w", err)
	}
	if tipo != EvtOla || subtle.ConstantTimeCompare(corpo, token) != 1 {
		c.Close()
		return nil, fmt.Errorf("apresentação inválida no canal da sessão")
	}
	_ = c.SetReadDeadline(time.Time{})
	return &Processo{Conn: conn}, nil
}

// PID é o processo-filho, para o log poder dizer QUAL processo morreu
// quando uma sessão cai — com várias abas abertas, "o filho caiu" sem
// número não ajuda ninguém a cruzar com um coredump.
func (p *Processo) PID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Encerrar fecha o canal e garante que o filho morreu. Fechar o socket já
// basta no caso normal (o filho vê EOF e sai), mas um filho travado dentro
// da biblioteca C não veria nada — daí o prazo e o tiro de misericórdia.
func (p *Processo) Encerrar() {
	p.baixaUma.Do(func() { vivas.Add(-1) })
	_ = p.Fechar()
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	fim := make(chan struct{})
	go func() { _, _ = p.cmd.Process.Wait(); close(fim) }()
	select {
	case <-fim:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-fim
	}
}

// ------------------------------------------------------ comandos tipados

func (p *Processo) Conectar(l Ligacao) error { return p.EnviarJSON(CmdConectar, l) }

func (p *Processo) PonteiroMover(x, y int) error {
	return p.Enviar(CmdPonteiroMover, i32(x, y))
}

// botao: 1=esquerdo 2=meio 3=direito.
func (p *Processo) PonteiroBotao(x, y, botao int, pressionado bool) error {
	return p.Enviar(CmdPonteiroBotao, append(i32(x, y, botao), bool1(pressionado)))
}

// eixo: 0=vertical 1=horizontal.
func (p *Processo) PonteiroRoda(eixo, passos int) error {
	return p.Enviar(CmdPonteiroRoda, i32(eixo, passos))
}

// Tecla recebe o KEYCODE X11, como internal/rdp.Session.KeyEvent.
func (p *Processo) Tecla(keycodeX11 uint32, pressionada bool) error {
	return p.Enviar(CmdTecla, append(i32(int(keycodeX11)), bool1(pressionada)))
}

func (p *Processo) Clipboard(texto string) error {
	return p.Enviar(CmdClipboard, []byte(texto))
}

func (p *Processo) Resize(w, h int) error { return p.Enviar(CmdResize, i32(w, h)) }

// Credito autoriza o filho a mandar mais UM quadro. Sem isso ele fica
// calado: é o controle de fluxo que impede uma sessão muito ativa de
// encher a fila do socket mais rápido do que a interface desenha.
func (p *Processo) Credito() error { return p.Enviar(CmdCredito, nil) }

func (p *Processo) CertResposta(decisao int) error {
	return p.Enviar(CmdCertResposta, []byte{byte(decisao)})
}
