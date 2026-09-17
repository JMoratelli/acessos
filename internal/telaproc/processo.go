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

// REDE DE SEGURANÇA DE MEMÓRIA DAS SESSÕES EM PROCESSO PRÓPRIO
//
// LEIA ISTO ANTES DE MEXER EM QUALQUER COISA QUE CRIE SESSÃO. Cada aba de
// tela remota custa um PROCESSO, não uma goroutine. O que antes crescia
// alguns megabytes por aba hoje cresce um binário inteiro mais os buffers
// da biblioteca C, e some do radar de quem olha só o processo principal.
//
// NÃO existe teto de quantas sessões dá para abrir, de propósito: quem
// decide quantas máquinas precisa olhar ao mesmo tempo é quem está
// operando, não este arquivo. Um número fixo aqui seria sempre errado —
// baixo demais no dia de apuração com trinta caixas na tela, alto demais
// numa máquina pequena.
//
// O que existe é uma rede de segurança: antes de subir mais uma sessão,
// perguntamos ao SISTEMA quanta memória ele ainda tem de verdade, e
// recusamos só quando abrir mais uma deixaria a máquina sem fôlego. É a
// diferença entre "o app decidiu que você já abriu demais" e "a máquina
// não tem mais memória" — a segunda é verdade, a primeira é palpite.
//
// Os custos por protocolo abaixo NÃO são cota: são a ESTIMATIVA do que a
// sessão vai consumir, usada para recusar ANTES de gastar em vez de
// depois. Medidos ao vivo, sessão de 1024x768, memória privada:
//
//	RDP  RSS 118 MiB | PSS 94 MiB | PRIVADA 87 MiB
//	VNC  RSS  53 MiB | PSS 31 MiB | PRIVADA 23-26 MiB
//
// Refaça a MEDIÇÃO antes de mexer nestes números, não os estime a partir
// do que está escrito aqui: TestAoVivoCustoDeUmFilho (RDP) e
// TestAoVivoVNCCustoDeUmFilho medem um filho de verdade, e
// TestCustoDeBaseDosFilhos mede o custo de carregar sem conectar.
const (
	// ReservaMinimaMiB é o fôlego que fica para o RESTO da máquina: o
	// compositor, o navegador, o editor, o próprio processo principal.
	// Abaixo disso o Linux começa a trocar para disco e a máquina trava de
	// um jeito que o usuário culpa o computador, não o app — foi
	// exatamente assim que uma sessão deste projeto terminou, com a
	// máquina inteira no chão.
	ReservaMinimaMiB = 1024

	// MaxSessoes NÃO é política de uso: é guarda contra defeito. Se algum
	// dia um laço com erro chamar Iniciar sem parar, isto impede que ele
	// encha a tabela de processos antes de alguém perceber. É alto de
	// propósito — ninguém abre 256 telas a mão.
	MaxSessoes = 256
)

// custoMiB estima o que cada protocolo consome. Arredondado para cima
// sobre o maior valor medido, com folga para tela maior que 1024x768
// (1920x1080 acrescenta ~5 MiB de framebuffer, não o dobro).
var custoMiB = map[string]int{
	"rdp": 95,
	"vnc": 30,
}

// custoDesconhecido é o que se estima para um protocolo ainda não medido:
// o valor do mais caro, para um protocolo novo nunca entrar subestimando o
// próprio peso.
const custoDesconhecido = 95

// CustoDe é quanto uma sessão deste protocolo deve consumir, em MiB.
// Exportada para o diagnóstico e para os testes de medição poderem
// comparar o que MEDIRAM com o que está estimado aqui.
func CustoDe(protocolo string) int {
	if c, ok := custoMiB[protocolo]; ok {
		return c
	}
	return custoDesconhecido
}

// disponivelMiB é indireção para o teste poder simular uma máquina
// apertada sem precisar realmente encher a memória desta aqui.
var disponivelMiB = memoriaDisponivelMiB

var (
	vivasMu sync.Mutex
	vivas   int
)

// ErrSemMemoria é o que Iniciar devolve quando a máquina não tem fôlego
// para mais uma sessão. Não derruba nada: a aba mostra no log e fica em
// "CAIU" até sobrar memória ou alguém fechar outra sessão.
type ErrSemMemoria struct {
	Protocolo   string
	PrecisaMiB  int
	LivreMiB    int
	ReservaMiB  int
	Sessoes     int
	PorContagem bool
}

func (e *ErrSemMemoria) Error() string {
	if e.PorContagem {
		return fmt.Sprintf(
			"%d sessões remotas abertas — isto é um limite de segurança contra defeito, não de uso;"+
				" se você chegou aqui a mão, algo está errado", MaxSessoes)
	}
	return fmt.Sprintf(
		"memória insuficiente para mais uma sessão %s: ela precisa de ~%d MiB, a máquina tem %d MiB"+
			" livres e %d MiB precisam sobrar para o resto do sistema — feche uma aba de tela"+
			" ou algum outro programa",
		e.Protocolo, e.PrecisaMiB, e.LivreMiB, e.ReservaMiB)
}

// SessoesVivas é quantos filhos estão de pé agora (para o diagnóstico).
func SessoesVivas() int {
	vivasMu.Lock()
	defer vivasMu.Unlock()
	return vivas
}

// reservar decide se cabe mais uma sessão AGORA, olhando a máquina de
// verdade.
func reservar(protocolo string) error {
	custo := CustoDe(protocolo)

	vivasMu.Lock()
	n := vivas
	vivasMu.Unlock()
	if n+1 > MaxSessoes {
		return &ErrSemMemoria{Protocolo: protocolo, Sessoes: n, PorContagem: true}
	}

	livre, ok := disponivelMiB()
	if !ok {
		// Não sabemos medir nesta plataforma: FALHA ABERTO. Uma proteção
		// que não sabe medir não deve virar impedimento.
		vivasMu.Lock()
		vivas++
		vivasMu.Unlock()
		return nil
	}
	if livre-custo < ReservaMinimaMiB {
		return &ErrSemMemoria{
			Protocolo: protocolo, PrecisaMiB: custo,
			LivreMiB: livre, ReservaMiB: ReservaMinimaMiB, Sessoes: n,
		}
	}
	vivasMu.Lock()
	vivas++
	vivasMu.Unlock()
	return nil
}

func devolver() {
	vivasMu.Lock()
	vivas--
	vivasMu.Unlock()
}

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
	// A checagem vem ANTES de qualquer trabalho: se a máquina não tem
	// fôlego, não custa nem um fork descobrir isso.
	if err := reservar(protocolo); err != nil {
		return nil, err
	}
	p, err := iniciar(protocolo)
	if err != nil {
		devolver()
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
	p.baixaUma.Do(devolver)
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

// Tecla manda a tecla no dialeto que a biblioteca do outro lado consome:
// KEYCODE X11 para o RDP (ver internal/rdp.Session.KeyEvent) e KEYSYM X11
// para o VNC (ver internal/vnc.Session.KeyEvent). Quem chama sabe qual dos
// dois é o seu — Tab.HandleKey recebe os dois valores justamente por isso.
func (p *Processo) Tecla(tecla uint32, pressionada bool) error {
	return p.Enviar(CmdTecla, append(i32(int(tecla)), bool1(pressionada)))
}

// PonteiroMascara manda posição e ESTADO DE TODOS os botões de uma vez
// (bit 0 esquerdo, bit 1 meio, bit 2 direito), que é como o VNC fala.
func (p *Processo) PonteiroMascara(x, y, mascara int) error {
	return p.Enviar(CmdPonteiroMascara, i32(x, y, mascara))
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
