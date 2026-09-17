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

// ORÇAMENTO DE MEMÓRIA DAS SESSÕES EM PROCESSO PRÓPRIO
//
// LEIA ISTO ANTES DE MEXER EM QUALQUER COISA QUE CRIE SESSÃO. Cada aba de
// tela remota custa um PROCESSO, não uma goroutine. O que antes crescia
// alguns megabytes por aba hoje cresce um binário inteiro mais os buffers
// da biblioteca C, e some do radar de quem olha só o processo principal.
// Sem teto, a arquitetura que existe para impedir que um crash derrube a
// máquina passaria a ser ela mesma o jeito de derrubar a máquina.
//
// O teto é em MEGABYTES, e não em número de sessões, porque os dois
// protocolos custam coisas muito diferentes. Medido ao vivo, sessão de
// 1024x768, memória PRIVADA (o que manda: o RSS conta binário e
// bibliotecas que todos os filhos compartilham, e por isso triplica o
// custo da segunda sessão em diante):
//
//	RDP  RSS 118 MiB | PSS 94 MiB | PRIVADA 87 MiB
//	VNC  RSS  53 MiB | PSS 31 MiB | PRIVADA 23-26 MiB
//
// (o VNC variou entre medições; o que se reserva abaixo fica ACIMA do
// maior valor observado, nunca na média — reservar menos do que se gasta
// é o mesmo que não ter orçamento)
//
// Um teto único por CONTAGEM erraria nos dois sentidos: o número que
// segura o RDP dentro de 1,5 GiB (17 sessões) proíbe um uso de VNC que já
// existe — o histórico registra operador com ~30 telas VNC abertas ao
// mesmo tempo (ver o comentário sobre wl_data_source em
// cmd/acessos/clipboard.go) —, e o número que permite as 30 telas VNC
// deixaria 30 sessões RDP chegarem a 2,6 GiB.
//
// Refaça a MEDIÇÃO antes de mexer nestes números, não os estime a partir
// do que está escrito aqui: TestAoVivoCustoDeUmFilho (RDP) e
// TestAoVivoVNCCustoDeUmFilho medem um filho de verdade, e
// TestCustoDeBaseDosFilhos mede o custo de carregar sem conectar.
const (
	// OrcamentoMiB é quanta memória as sessões em processo próprio podem
	// somar. 1,5 GiB é o que se gasta sem pensar numa máquina de operação,
	// e nele cabem 51 telas VNC ou 16 sessões RDP — ou seja, as ~30 telas
	// VNC do uso real passam com folga larga.
	OrcamentoMiB = 1536

	// MaxSessoes é um limite de SANIDADE por contagem, além do orçamento:
	// mesmo barato em memória, cada sessão é um processo, um socket e um
	// punhado de descritores. Serve para um erro de laço em algum lugar
	// não abrir centenas de filhos.
	MaxSessoes = 48
)

// custoMiB é o que cada protocolo reserva do orçamento. São as medições
// acima arredondadas para cima, com folga para tela maior que 1024x768
// (1920x1080 acrescenta ~5 MiB de framebuffer, não o dobro).
var custoMiB = map[string]int{
	"rdp": 95,
	"vnc": 30,
}

// custoDesconhecido é o que se reserva para um protocolo que ainda não foi
// medido — o valor do mais caro, para um protocolo novo nunca entrar
// subestimando o próprio peso.
const custoDesconhecido = 95

// CustoDe é quanto do orçamento uma sessão deste protocolo reserva, em
// MiB. Exportada para o diagnóstico e para os testes de medição poderem
// comparar o que MEDIRAM com o que está reservado aqui.
func CustoDe(protocolo string) int {
	if c, ok := custoMiB[protocolo]; ok {
		return c
	}
	return custoDesconhecido
}

var (
	// gastoMiB e vivas são a contabilidade do orçamento. Sobem em Iniciar
	// e descem em Encerrar, uma vez só por Processo (daí o sync.Once):
	// Encerrar é chamado tanto pelo caminho normal quanto por defer em
	// caminho de erro.
	orcamentoMu sync.Mutex
	gastoMiB    int
	vivas       int
)

// ErrLotado é o que Iniciar devolve quando não há orçamento para mais uma
// sessão. Estourar o teto não derruba nada: a aba mostra no log e fica em
// "CAIU" até alguém fechar outra sessão.
type ErrLotado struct {
	Protocolo   string
	CustoMiB    int
	GastoMiB    int
	Orcamento   int
	Sessoes     int
	PorContagem bool
}

func (e *ErrLotado) Error() string {
	if e.PorContagem {
		return fmt.Sprintf(
			"limite de %d sessões remotas simultâneas atingido — feche uma aba de tela antes de abrir outra",
			MaxSessoes)
	}
	return fmt.Sprintf(
		"sem memória reservada para mais uma sessão %s: %d sessões abertas usam ~%d MiB de %d MiB —"+
			" feche uma aba de tela antes de abrir outra",
		e.Protocolo, e.Sessoes, e.GastoMiB, e.Orcamento)
}

// SessoesVivas e GastoMiB são para o diagnóstico (ver diag.go).
func SessoesVivas() int {
	orcamentoMu.Lock()
	defer orcamentoMu.Unlock()
	return vivas
}

func GastoMiB() int {
	orcamentoMu.Lock()
	defer orcamentoMu.Unlock()
	return gastoMiB
}

// reservar tenta encaixar mais uma sessão no orçamento.
func reservar(protocolo string) (int, error) {
	custo := CustoDe(protocolo)
	orcamentoMu.Lock()
	defer orcamentoMu.Unlock()
	if vivas+1 > MaxSessoes {
		return 0, &ErrLotado{Protocolo: protocolo, Sessoes: vivas, PorContagem: true}
	}
	if gastoMiB+custo > OrcamentoMiB {
		return 0, &ErrLotado{
			Protocolo: protocolo, CustoMiB: custo,
			GastoMiB: gastoMiB, Orcamento: OrcamentoMiB, Sessoes: vivas,
		}
	}
	gastoMiB += custo
	vivas++
	return custo, nil
}

func devolver(custo int) {
	orcamentoMu.Lock()
	defer orcamentoMu.Unlock()
	gastoMiB -= custo
	vivas--
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
	custoMiB int
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
	// A reserva vem ANTES de qualquer trabalho: se não há orçamento, não
	// custa nem um fork descobrir isso.
	custo, err := reservar(protocolo)
	if err != nil {
		return nil, err
	}
	p, err := iniciar(protocolo)
	if err != nil {
		devolver(custo)
		return nil, err
	}
	p.custoMiB = custo
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
	p.baixaUma.Do(func() { devolver(p.custoMiB) })
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
