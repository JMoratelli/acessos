package main

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"acessos-go/internal/hostkey"
	"acessos-go/internal/massa/executor"
	"acessos-go/internal/massa/model"

	"github.com/hinshun/vt10x"
	"golang.org/x/crypto/ssh"
)

// spTerminal: o terminal usa a Plex Mono embutida, um ponto acima do corpo
// — texto de shell é para ser lido por muito tempo seguido.
const spTerminal = unit.Sp(13)

// sshTab é o terminal embutido: sessão SSH + emulador VT (vt10x) + uma
// grade desenhada célula a célula. O vt10x cuida do parsing ANSI (cores,
// cursor, rolagem); daqui pra frente o trabalho é só teclado -> bytes e
// células -> pixels.
//
// scrollHist (rolagem, ver `rolagem` abaixo) usa um patch local do vt10x
// (third_party/vt10x/PATCH.md): a lib de origem não guarda nada do que
// sai por cima da tela.
//
// Limite assumido nesta versão, de propósito: sem relatório de mouse
// (programas que capturam clique/roda dentro do terminal, tipo htop ou
// um menu TUI, não recebem o evento). Shell, logs e edição em tela cheia
// funcionam.
type sshTab struct {
	th *material.Theme
	w  *app.Window
	invalidador
	titulo string
	host   string
	porta  int
	user   string
	senha  string

	term vt10x.Terminal

	mu         sync.Mutex
	entrada    io.WriteCloser // stdin da sessão remota
	sess       *ssh.Session
	cli        *ssh.Client
	estado     string // aviso de ação (copiar, erro de snippets) — NÃO é status de conexão, ver laco()
	jaConectou bool   // true a partir da 2ª sessão desta aba — ver limparTela

	cols, rows int
	mods       modificadores
	foco       widget.Clickable

	// Geometria do último quadro, para o ponteiro virar célula. É medida
	// no desenho (depende da fonte e da escala da tela) e lida no evento,
	// que chega fora do quadro — daí viver aqui sob o mesmo mutex.
	margem    int
	avanco    float64
	alturaCel int

	// Seleção com o mouse, em coordenadas de CÉLULA da tela VISÍVEL no
	// momento (que pode estar rolada pro histórico — ver `rolagem`). Ela
	// não acompanha o conteúdo: se a tela visível mudar (novo output com
	// rolagem em 0, ou o operador rolar), a seleção continua nas mesmas
	// coordenadas, marcando o que estiver ali agora.
	selA, selB celula
	selAtiva   bool
	arrastando bool

	// mouseRelatando: true entre o Press e o Release de uma sequência
	// que está sendo REPORTADA pro remoto (htop, less, mc…) em vez de
	// virar seleção local — decidido uma vez no Press e mantido até o
	// Release, pra um Drag no meio do caminho não mudar de ideia se o
	// programa remoto alternar o modo de mouse bem nesse instante.
	// mouseBotaoRel guarda QUAL botão abriu a sequência, pro Release e o
	// Motion (Drag) do meio saberem qual código xterm usar.
	mouseRelatando bool
	mouseBotaoRel  int

	// rolagem: 0 = acompanhando a saída ao vivo (o fundo da tela); N =
	// rolado N linhas para dentro do histórico. Roda do mouse SEM Ctrl
	// mexe aqui (com Ctrl continua mudando o corpo da fonte, como sempre
	// mudou). Volta a 0 sozinha quando o operador digita algo — é o
	// mesmo gesto de "ler o histórico não deveria travar o comando
	// seguinte" que fez `set_scroll_on_output(False)` no terminal antigo
	// (VTE/GTK), só que aqui em vez de travar o AUTO-SCROLL a rolagem
	// INTEIRA é manual.
	rolagem int

	// painel lateral de snippets
	painelSnips bool
	snips       []model.Snippet
	btnSnips    []widget.Clickable
	listaSnips  widget.List
	filtroSnip  widget.Editor

	// corpo da fonte (Ctrl+roda). O que colar (Ctrl+Shift+V) não mora
	// mais aqui — ver clipboardSistema() em clipboard.go: por aba, só
	// pegava o clipboard de quando ELA estava ativa, e colar numa aba
	// diferente da que copiou não funcionava.
	corpo float32

	religar     chan struct{}
	stop        chan struct{}
	closeOnce   sync.Once
	auto        atomic.Bool
	caiu        atomic.Bool // ver EstadoSessao — setado por gerenciarSessaoRemota
	nomeConexao string
	btnRec      widget.Clickable
	btnAuto     widget.Clickable
	btnSnip     widget.Clickable

	splash    *splash
	btnSplash widget.Clickable

	// ultimaTecla marca a última vez que uma tecla foi enviada — ver
	// cursorAceso().
	ultimaTecla time.Time
}

// piscarPeriodo é o intervalo de troca aceso/apagado do cursor — mesma
// faixa que terminais de verdade usam (gnome-terminal e afins ficam perto
// de 500-600ms).
const piscarPeriodo = 530 * time.Millisecond

// cursorAceso decide se o cursor deve aparecer neste quadro: sempre aceso
// logo depois de uma tecla digitada (sem isto, digitar rápido podia
// "apagar" o cursor bem no instante em que a pessoa está olhando pra
// ele, se o quadro caísse na fase escura do piscar) e alternando num
// período fixo quando ocioso.
func (t *sshTab) cursorAceso() bool {
	t.mu.Lock()
	desdeTecla := time.Since(t.ultimaTecla)
	t.mu.Unlock()
	if desdeTecla < piscarPeriodo {
		return true
	}
	return (time.Now().UnixMilli()/piscarPeriodo.Milliseconds())%2 == 0
}

// proximaViradaDoCursor é quanto falta para o cursor MUDAR de fase, e é o
// que o desenho usa para pedir o próximo quadro (ver layoutTerminal).
//
// Era um ticker de 530ms numa goroutine por sessão, chamando t.invalidar()
// — que é a rajada de invalidar.go, escrita para dado de rede chegando, e
// que vira até quatro pedidos de quadro da JANELA INTEIRA por piscada. Pior:
// piscava igual com a aba invisível, então cada sessão SSH aberta em
// segundo plano mantinha a janela redesenhando para sempre.
//
// Pedir de dentro do Layout resolve sozinho, porque o Layout só roda para
// a aba que está à vista: aba escondida para de pedir quadro, e o primeiro
// Layout de quando ela volta rearma o piscar. É o mesmo padrão da contagem
// regressiva do confirmdlg.go e do massapausa.go.
//
// São duas fases possíveis a esperar, e vale a mais próxima: a virada do
// relógio (cursorAceso alterna em múltiplos do período) e o fim da janela
// de "aceso porque acabou de digitar".
func (t *sshTab) proximaViradaDoCursor(agora time.Time) time.Duration {
	t.mu.Lock()
	ultima := t.ultimaTecla
	t.mu.Unlock()

	p := piscarPeriodo
	falta := p - time.Duration(agora.UnixMilli()%p.Milliseconds())*time.Millisecond
	if desde := agora.Sub(ultima); desde >= 0 && desde < p && p-desde < falta {
		falta = p - desde
	}
	if falta <= 0 {
		falta = p
	}
	return falta
}

// celula é uma posição na grade: coluna e linha.
type celula struct{ x, y int }

// antesDe põe a seleção em ordem de leitura, para quem desenha e quem
// copia não precisarem saber para que lado o arrasto foi.
func (c celula) antesDe(o celula) bool {
	if c.y != o.y {
		return c.y < o.y
	}
	return c.x < o.x
}

func newSSHTab(w *app.Window, spec map[string]string) (Tab, error) {
	host := spec["host"]
	if host == "" {
		return nil, fmt.Errorf("ssh: falta host")
	}
	porta := 22
	if p, err := strconv.Atoi(spec["port"]); err == nil && p > 0 {
		porta = p
	}
	if spec["user"] == "" {
		return nil, fmt.Errorf("ssh: falta usuário")
	}

	t := &sshTab{
		th:      temaApp,
		w:       w,
		titulo:  rotuloAba(spec, "SSH", host),
		host:    host,
		porta:   porta,
		user:    spec["user"],
		senha:   spec["pass"],
		cols:    80,
		rows:    24,
		stop:    make(chan struct{}),
		religar: make(chan struct{}, 1),
	}
	t.splash = novoSplash(w)
	t.auto.Store(true)
	t.nomeConexao = spec["rotulo"]
	if spec["auto"] == "0" {
		t.auto.Store(false)
	}
	// 20000 linhas é o mesmo teto que o terminal antigo (VTE) usava —
	// scrollback generoso o bastante pra um log comprido sem virar um
	// consumo de memória visível numa sessão de PDV.
	t.term = vt10x.New(vt10x.WithSize(t.cols, t.rows), vt10x.WithWriter(escritorEntrada{t}),
		vt10x.WithScrollback(20000))
	go t.laco()
	return t, nil
}

// escritorEntrada deixa o próprio vt10x responder sequências que pedem
// resposta (consulta de posição do cursor, por exemplo) mandando os bytes
// de volta pelo stdin da sessão.
type escritorEntrada struct{ t *sshTab }

func (e escritorEntrada) Write(p []byte) (int, error) {
	e.t.enviar(p)
	return len(p), nil
}

// invalidar existe para a aba poder ser exercitada sem janela (testes):
// o resto do código chama isto em vez de t.w.Invalidate() direto. A
// insistência contra a guarda mayInvalidate do Gio está em invalidador,
// em invalidar.go — compartilhada com sftpTab, que tem a mesma corrida
// (goroutine de fundo batendo Invalidate() a qualquer momento).
func (t *sshTab) invalidar() {
	t.disparar(t.w)
}

func (t *sshTab) Title() string { return t.titulo }
func (t *sshTab) SoIcone() bool { return false }
func (t *sshTab) Pinned() bool  { return false }
func (t *sshTab) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	return icons.ActionCode, tema.Verde, tema.VerdeFraco
}

// laco entrega o laço de reconexão a gerenciarSessaoRemota (telatab.go),
// o mesmo usado por rdpTab e vncTab — a única peça que não é genérica
// ali é COMO uma tentativa de sessão se conecta, e isso mora inteiro em
// sessao(). Antes o SSH tinha seu próprio laço, com sua própria escala
// de espera (1s dobrando até 8s) e sem nenhuma distinção entre "a rede
// caiu" e "a senha está errada" — via fimFalhou, unificar os três
// também fechou essa segunda parte: senha errada não entra mais no
// backoff automático (ver o comentário em fimFalhou).
func (t *sshTab) laco() {
	gerenciarSessaoRemota(sessaoRemotaCfg{
		title:      t.titulo,
		stop:       t.stop,
		religar:    t.religar,
		w:          t.w,
		caiu:       &t.caiu,
		auto:       &t.auto,
		rodar:      t.sessao,
		aoAguardar: t.splash.aguardar,
	})
}

// passosSSH: dois pontos observáveis sem mexer no formato da conexão —
// o aperto de mão (Dial, que já inclui a autenticação: a lib do Go faz
// as duas coisas numa chamada só) e a montagem da sessão interativa
// (NewSession + PTY + pipes). Não há como separar autenticação de
// handshake TCP sem trocar ssh.Dial por Dial+NewClientConn, e mexer
// nisso não vale o risco só para ganhar um passo a mais no cartão.
var passosSSH = []string{"Conectando", "Abrindo sessão"}

// sessao abre uma sessão e só volta quando ela morre. Devolve fimFalhou
// só quando o servidor recusou a credencial (ver executor.EhAuth) — esse
// caso NUNCA entra no backoff automático de gerenciarSessaoRemota (ver o
// comentário em fimFalhou, telatab.go): tentar de novo sozinho com a
// senha errada não é persistência, é força bruta contra o próprio
// parque, e em domínio Windows chega a bloquear a conta.
func (t *sshTab) sessao() fimSessao {
	t.splash.iniciar(passosSSH)
	cfg := &ssh.ClientConfig{
		User: t.user,
		Auth: []ssh.AuthMethod{ssh.Password(t.senha)},
		// PDV antigo só oferece algoritmo que o Go corta por padrão, e o
		// aperto de mão falha com a rede perfeita. Em rede interna, com
		// alvo conhecido, aceitar o algoritmo velho custa menos que não
		// conseguir administrar a máquina (a mesma decisão do Mass SSH
		// Executer — ver internal/massa/executor.AlgoritmosLegado).
		Config: executor.AlgoritmosLegado(),
		// A identidade do servidor é conferida contra o known_hosts do
		// usuário (ver internal/hostkey). Sem isso, uma máquina no meio
		// do caminho recebe a senha de suporte inteira.
		HostKeyCallback: hostkey.Callback(),
		Timeout:         10 * time.Second,
	}
	inicio := time.Now()
	cli, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", t.host, t.porta), cfg)
	if err != nil {
		// Chave desconhecida ou trocada: a sessão PARA e pergunta. Ao
		// aceitar, tenta de novo na hora (o laço de reconexão faria isso
		// depois, mas com espera — e quem acabou de responder está
		// olhando a tela).
		if ec := erroDeChave(err); ec != nil {
			// PARA e espera a resposta. Sem isso o laço tentava de novo a
			// cada segundo e reabria o diálogo por cima do anterior.
			//
			// O sinal de aceitação usa um canal PRÓPRIO (não t.religar): se
			// usasse t.religar, o sinal mandado por Reconectar() seria
			// consumido bem aqui, e o select externo em laco() (que é quem
			// de fato dispara a reconexão) nunca veria nada — a aba ficava
			// presa em "sessão encerrada — parada" à espera de um segundo
			// clique manual do operador.
			t.splash.setErro(ec.Error() + " — aguardando sua decisão")
			aceito := make(chan struct{}, 1)
			pedirConfiancaHostKey(t.w, ec, func() {
				select {
				case aceito <- struct{}{}:
				default:
				}
			})
			select {
			case <-aceito:
				return t.sessao()
			case <-t.stop:
				return fimParar
			}
		}
		reg("[%s] ssh falhou: %v", t.titulo, err)
		t.splash.setErro(err.Error())
		if executor.EhAuth(err) {
			return fimFalhou
		}
		return fimCaiu
	}
	t.splash.avancar(1)

	sess, err := cli.NewSession()
	if err != nil {
		cli.Close()
		reg("[%s] ssh falhou: %v", t.titulo, err)
		t.splash.setErro(err.Error())
		return fimCaiu
	}

	t.mu.Lock()
	cols, rows := t.cols, t.rows
	t.mu.Unlock()

	modos := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modos); err != nil {
		cli.Close()
		reg("[%s] ssh falhou: %v", t.titulo, err)
		t.splash.setErro(err.Error())
		return fimCaiu
	}
	entrada, err := sess.StdinPipe()
	if err != nil {
		cli.Close()
		reg("[%s] ssh falhou: %v", t.titulo, err)
		t.splash.setErro(err.Error())
		return fimCaiu
	}
	saida, err := sess.StdoutPipe()
	if err != nil {
		cli.Close()
		reg("[%s] ssh falhou: %v", t.titulo, err)
		t.splash.setErro(err.Error())
		return fimCaiu
	}
	sess.Stderr = escritorTerminal{t}

	if t.encerrada() {
		cli.Close()
		return fimParar
	}

	// Vigia: mesma ideia do rodarSessaoRemota em telatab.go (RDP/VNC) —
	// traduz "fechar a aba" e "reconectar agora" num Close() que acorda
	// o Wait() lá embaixo na hora, e guarda QUAL dos dois foi antes de
	// fechar, pra o motivo não virar "use of closed network connection"
	// na tela quando foi só um Reconectar clicado.
	var pedido atomic.Int32
	pedido.Store(int32(fimCaiu))
	saiu := make(chan struct{})
	defer close(saiu)
	go func() {
		select {
		case <-t.stop:
			pedido.Store(int32(fimParar))
		case <-t.religar:
			pedido.Store(int32(fimReligar))
		case <-saiu:
			return
		}
		cli.Close()
	}()

	t.mu.Lock()
	t.cli, t.sess, t.entrada = cli, sess, entrada
	reconexao := t.jaConectou
	t.jaConectou = true
	t.mu.Unlock()
	t.caiu.Store(false)
	reg("[%s] ssh pronto em %s", t.titulo, time.Since(inicio).Truncate(time.Millisecond))
	t.splash.concluir()
	t.invalidar()

	// Numa RECONEXÃO (rede caiu, ou a máquina remota reiniciou), a tela
	// ainda tem o conteúdo da sessão ANTERIOR, parada onde o cursor
	// ficou — no meio da tela, na maioria das vezes. Sem isto, o
	// login/MOTD da sessão NOVA começa a escrever bem ali, por cima do
	// que já estava, e o resultado é texto embaralhado. limparTela
	// empurra o que tem pro histórico (nada se perde, dá pra rolar pra
	// ver) e deixa a tela em branco, cursor no topo, pronta pra sessão
	// nova — o mesmo que aconteceria numa reconexão manual num terminal
	// de verdade, só que sem depender do operador ter apertado Enter
	// antes pra "empurrar" a linha velha.
	if reconexao {
		t.limparTela()
	}

	if err := sess.Shell(); err != nil {
		t.mu.Lock()
		t.cli, t.sess, t.entrada = nil, nil, nil
		t.mu.Unlock()
		cli.Close()
		reg("[%s] ssh falhou: %v", t.titulo, err)
		t.splash.setErro(err.Error())
		return fimSessao(pedido.Load())
	}

	// Parse bloqueia lendo o stdout e alimenta o emulador; cada pedaço
	// lido pede um quadro novo.
	go func() {
		br := bufio.NewReader(leitorAvisado{r: saida, t: t})
		for {
			if err := t.term.Parse(br); err != nil {
				return
			}
		}
	}()

	err = sess.Wait()

	t.mu.Lock()
	t.cli, t.sess, t.entrada = nil, nil, nil
	t.mu.Unlock()
	t.invalidar()

	if fim := fimSessao(pedido.Load()); fim != fimCaiu {
		// Fechar a aba ou clicar em Reconectar venceu: é isso que
		// aconteceu, não importa o que Wait() devolveu — algo como "use
		// of closed network connection", que só confundiria quem
		// olhasse a tela bem nesse instante.
		return fim
	}
	msg := "sessão encerrada"
	if err != nil {
		if _, ok := err.(*ssh.ExitError); !ok {
			// saiu com exit != 0 É o shell terminando, não erro de rede —
			// fica com a mensagem padrão acima, sem logar como falha.
			msg = err.Error()
			reg("[%s] ssh falhou: %v", t.titulo, err)
		}
	}
	t.splash.setErro(msg)
	return fimCaiu
}

// leitorAvisado pede um quadro novo a cada pedaço de saída que chega.
type leitorAvisado struct {
	r io.Reader
	t *sshTab
}

func (l leitorAvisado) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	if n > 0 {
		l.t.invalidar()
	}
	return n, err
}

// escritorTerminal manda o stderr da sessão para o mesmo emulador.
type escritorTerminal struct{ t *sshTab }

func (e escritorTerminal) Write(p []byte) (int, error) {
	n, err := e.t.term.Write(p)
	e.t.invalidar()
	return n, err
}

func (t *sshTab) setEstado(s string) {
	t.mu.Lock()
	t.estado = s
	t.mu.Unlock()
	t.invalidar()
}

func (t *sshTab) encerrada() bool {
	select {
	case <-t.stop:
		return true
	default:
		return false
	}
}

func (t *sshTab) enviar(b []byte) {
	if len(b) == 0 {
		return
	}
	t.mu.Lock()
	e := t.entrada
	t.mu.Unlock()
	if e != nil {
		e.Write(b)
	}
}

// Close só sinaliza: quem fecha cli/sess de verdade é o vigia dentro de
// sessao() (mesmo padrão do RDP/VNC — ver rodarSessaoRemota em
// telatab.go), reagindo a <-t.stop. Fechar direto aqui, como antes,
// funcionava, mas divergia de como as outras duas abas fecham — e
// "uniformizar" foi o pedido.
func (t *sshTab) Close() {
	t.closeOnce.Do(func() { close(t.stop) })
}

// HandleKey recebe a tecla física já traduzida pelo Wayland (ver
// internal/grab) — é o mesmo caminho do VNC/RDP, e por isso Ctrl, Alt e
// Shift chegam com apertar E soltar, que é o que o terminal precisa.
func (t *sshTab) HandleKey(keysym, _ uint32, pressed bool) {
	if t.mods.anotar(keysym, pressed) {
		return
	}
	if !pressed {
		return
	}
	// Ctrl+Shift+C / Ctrl+Shift+V: copiar e colar. No terminal, Ctrl+C é
	// SIGINT e Ctrl+V é literal-next — por isso o mundo inteiro usa
	// Shift junto aqui, e é o que o app original faz.
	if t.mods.ctrl && t.mods.shift {
		switch keysym {
		case 'C', 'c':
			t.copiarSelecao()
			return
		case 'V', 'v':
			t.colarDoSistema()
			return
		}
	}
	// Digitar volta pro fundo da tela: é o que todo terminal faz quando o
	// operador estava lendo o histórico e retoma o comando — sem isto o
	// que se digita ia aparecer "atrás" da rolagem, fora de vista.
	t.mu.Lock()
	rolava := t.rolagem != 0
	t.rolagem = 0
	t.mu.Unlock()
	if rolava {
		t.invalidar()
	}
	t.mu.Lock()
	t.ultimaTecla = time.Now()
	t.mu.Unlock()
	t.enviar(bytesDaTecla(keysym, t.mods))
}

// copiarSelecao copia o trecho marcado com o mouse. Sem seleção, copia a
// tela inteira — é o comportamento antigo, e continua sendo o que serve
// para colar um erro num chamado sem ter que mirar com o mouse.
func (t *sshTab) copiarSelecao() {
	texto, houve := t.textoSelecionado()
	if !houve {
		// SEM Lock/Unlock em volta: ao contrário de Cell/Cursor/Size (que
		// exigem o chamador travar), o String() do vt10x tranca o mutex
		// POR CONTA PRÓPRIA — travar aqui também travava um mutex não
		// reentrante duas vezes na mesma goroutine e travava o app inteiro
		// (Ctrl+Shift+C sem seleção nenhuma, direto do HandleKey).
		texto = t.term.String()
		publicarClipboard(t.w, strings.TrimRight(texto, "\n \t"))
		t.setEstado("tela copiada")
		return
	}
	publicarClipboard(t.w, texto)
	t.setEstado("seleção copiada")
}

// selecao devolve a marcação em ordem de leitura.
func (t *sshTab) selecao() (a, b celula, ok bool) {
	t.mu.Lock()
	a, b, ok = t.selA, t.selB, t.selAtiva
	t.mu.Unlock()
	if !ok {
		return a, b, false
	}
	if b.antesDe(a) {
		a, b = b, a
	}
	return a, b, true
}

// textoSelecionado monta o texto da marcação, uma linha por linha da
// tela. O espaço à direita é aparado: a grade é sempre retangular, então
// sem isso toda linha copiada viria cheia de espaços até a borda.
func (t *sshTab) textoSelecionado() (string, bool) {
	a, b, ok := t.selecao()
	if !ok {
		return "", false
	}
	t.term.Lock()
	defer t.term.Unlock()
	cols, rows := t.term.Size()
	var linhas []string
	for y := a.y; y <= b.y && y < rows; y++ {
		xi, xf := 0, cols-1
		if y == a.y {
			xi = a.x
		}
		if y == b.y {
			xf = b.x
		}
		var sb strings.Builder
		for x := xi; x <= xf && x < cols; x++ {
			c := t.term.Cell(x, y).Char
			if c == 0 {
				c = ' '
			}
			sb.WriteRune(c)
		}
		linhas = append(linhas, strings.TrimRight(sb.String(), " "))
	}
	texto := strings.Join(linhas, "\n")
	if strings.TrimSpace(texto) == "" {
		return "", false
	}
	return texto, true
}

// colarDoSistema escreve o clipboard no stdin. Sem Enter: quem confirma é
// quem está olhando — colar comando que executa sozinho já derrubou PDV.
//
// Lê o cache GLOBAL (clipboard.go), não um campo desta aba: é o que faz
// colar funcionar em qualquer aba, mesmo quando o que foi copiado veio de
// OUTRA aba deste mesmo app (ex.: copiar na sessão da caixa 101 e colar
// na da 102) — o aviso do Wayland de "clipboard mudou" só chega uma vez,
// para a aba que estiver ativa naquele instante, e nada garante que seja
// esta.
// marcaColadoIni/marcaColadoFim: "bracketed paste" (o mesmo modo que
// xterm/gnome-terminal usam). Sem isto, um programa com editor de linha
// próprio — nano, bash com readline moderno — não tem como saber que
// aqueles bytes vieram de um COLAR, e trata cada '\n' como se tivesse
// sido digitado rápido demais: testado na prática, o nano especificamente
// troca todo '\n' assim recebido por um ESPAÇO, embaralhando o arquivo
// inteiro numa linha só. Com a marcação, ele entende que é um bloco colado
// e preserva as quebras de linha — exatamente o que um terminal de
// verdade manda a cada Ctrl+V.
const (
	marcaColadoIni = "\x1b[200~"
	marcaColadoFim = "\x1b[201~"
)

func (t *sshTab) colarDoSistema() {
	texto := clipboardSistema()
	if texto == "" {
		return
	}
	// Só envolve na marcação se a aplicação remota PEDIU (CSI ?2004h) —
	// ver o patch de ModeBracketPaste em third_party/vt10x/PATCH.md. Sem
	// isto, um programa que não pediu (e não entende) recebe os bytes de
	// abertura/fechamento como se tivessem sido digitados de verdade.
	t.term.Lock()
	bracketed := t.term.Mode()&vt10x.ModeBracketPaste != 0
	t.term.Unlock()
	if !bracketed {
		t.enviar([]byte(texto))
		return
	}
	t.enviar([]byte(marcaColadoIni + texto + marcaColadoFim))
}

// celulaEm converte a posição do ponteiro (relativa à área de conteúdo)
// na célula sob ele, presa aos limites da grade — arrastar para fora da
// janela deve estender a seleção até a borda, não perder o evento.
func (t *sshTab) celulaEm(p f32.Point) celula {
	t.mu.Lock()
	margem, avanco, ch, cols, rows := t.margem, t.avanco, t.alturaCel, t.cols, t.rows
	t.mu.Unlock()
	if avanco <= 0 || ch <= 0 {
		return celula{}
	}
	x := int(math.Floor((float64(p.X) - float64(margem)) / avanco))
	y := (int(p.Y) - margem) / ch
	return celula{x: min(max(x, 0), cols-1), y: min(max(y, 0), rows-1)}
}

// modoMouse lê o modo de mouse atual do vt10x — o que o programa remoto
// pediu com CSI ?1000h e afins. ModeMouseMask em zero quer dizer "ninguém
// pediu nada": mouse é só interface local (seleção, roda = scrollback),
// do jeito que sempre foi.
func (t *sshTab) modoMouse() vt10x.ModeFlag {
	t.term.Lock()
	m := t.term.Mode()
	t.term.Unlock()
	return m
}

// HandlePointer: Ctrl+roda muda o corpo da fonte, 6 a 32, como no VTE do
// app original. Sem Ctrl a roda rola o histórico.
//
// Clique/arrasto/roda vão pro REMOTO (htop, less, mc, qualquer coisa que
// pediu relato de mouse) sempre que o vt10x tiver algum modo de mouse
// ligado — é o que falta pra rolar a roda dentro do less ou clicar um
// processo no htop fazer alguma coisa, igual um terminal de verdade.
// Shift força o comportamento LOCAL de propósito (seleção de texto, roda
// = scrollback) mesmo com o modo ligado — a mesma válvula de escape que
// xterm/gnome-terminal têm, pra sempre dar pra copiar um pedaço da tela
// mesmo dentro de um TUI que capturou o mouse. Ctrl continua reservado
// pro zoom da fonte, com ou sem modo de mouse.
func (t *sshTab) HandlePointer(ev pointer.Event, _ image.Point) {
	modo := t.modoMouse()
	relatar := modo&vt10x.ModeMouseMask != 0 && !t.mods.shift

	switch ev.Kind {
	case pointer.Press:
		if ev.Buttons&(pointer.ButtonPrimary|pointer.ButtonSecondary|pointer.ButtonTertiary) == 0 {
			break
		}
		c := t.celulaEm(ev.Position)
		if relatar {
			botao, ok := botaoXterm(ev.Buttons)
			if !ok {
				return
			}
			t.mu.Lock()
			t.mouseRelatando, t.mouseBotaoRel = true, botao
			t.mu.Unlock()
			t.enviar(sequenciaMouse(modo, mousePress, botao, t.mods, c.x, c.y))
			return
		}
		if ev.Buttons&pointer.ButtonPrimary == 0 {
			break
		}
		// Um clique simples LIMPA a seleção: é o que todo terminal faz, e
		// é a única forma de desmarcar sem precisar de outro atalho.
		t.mu.Lock()
		t.selA, t.selB = c, c
		t.selAtiva, t.arrastando = false, true
		t.mu.Unlock()
		t.invalidar()
		return
	case pointer.Drag:
		t.mu.Lock()
		relatando, botao := t.mouseRelatando, t.mouseBotaoRel
		t.mu.Unlock()
		if relatando {
			c := t.celulaEm(ev.Position)
			t.enviar(sequenciaMouse(modo, mouseMotion, botao, t.mods, c.x, c.y))
			return
		}
		t.mu.Lock()
		arrastando := t.arrastando
		t.mu.Unlock()
		if !arrastando {
			return
		}
		c := t.celulaEm(ev.Position)
		t.mu.Lock()
		t.selB, t.selAtiva = c, c != t.selA
		t.mu.Unlock()
		t.invalidar()
		return
	case pointer.Release:
		t.mu.Lock()
		relatando, botao := t.mouseRelatando, t.mouseBotaoRel
		t.mouseRelatando, t.arrastando = false, false
		t.mu.Unlock()
		if relatando {
			c := t.celulaEm(ev.Position)
			t.enviar(sequenciaMouse(modo, mouseRelease, botao, t.mods, c.x, c.y))
			return
		}
		t.invalidar()
		return
	}
	if ev.Kind != pointer.Scroll {
		return
	}
	if t.mods.ctrl || ctrlPressionado() {
		// zoom da fonte, tratado abaixo — reservado independente do modo
		// de mouse, mesma prioridade que já tinha.
	} else if relatar {
		tipo := mouseScrollDown
		if ev.Scroll.Y < 0 {
			tipo = mouseScrollUp
		} else if ev.Scroll.Y == 0 {
			return
		}
		c := t.celulaEm(ev.Position)
		t.enviar(sequenciaMouse(modo, tipo, 0, t.mods, c.x, c.y))
		return
	} else {
		t.rolarHistorico(ev)
		return
	}
	t.mu.Lock()
	corpo := t.corpo
	if corpo == 0 {
		corpo = float32(spTerminal)
	}
	if ev.Scroll.Y < 0 {
		corpo++
	} else if ev.Scroll.Y > 0 {
		corpo--
	}
	if corpo < 6 {
		corpo = 6
	}
	if corpo > 32 {
		corpo = 32
	}
	t.corpo = corpo
	t.mu.Unlock()
	t.invalidar()
}

// rolarHistorico move `rolagem` (em linhas) e prende o resultado entre 0
// (fundo, ao vivo) e o total guardado — travar em vez de deixar passar
// evita rolar "além" do que existe e mostrar tela em branco.
func (t *sshTab) rolarHistorico(ev pointer.Event) {
	passo := 0
	switch {
	case ev.Scroll.Y < 0:
		passo = 3 // roda pra cima: revela linhas mais ANTIGAS
	case ev.Scroll.Y > 0:
		passo = -3
	default:
		return
	}
	t.term.Lock()
	hist := t.term.HistoryLen()
	t.term.Unlock()

	t.mu.Lock()
	t.rolagem = min(max(t.rolagem+passo, 0), hist)
	t.mu.Unlock()
	t.invalidar()
}

// corpoAtual é o tamanho da fonte em uso.
func (t *sshTab) corpoAtual() unit.Sp {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.corpo == 0 {
		return spTerminal
	}
	return unit.Sp(t.corpo)
}

// ------------------------------------------------------------- desenho

func (t *sshTab) Layout(gtx layout.Context) layout.Dimensions {
	if t.painelSnips {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Flexed(1, t.layoutTerminal),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				larg := gtx.Dp(320)
				if larg > gtx.Constraints.Max.X/2 {
					larg = gtx.Constraints.Max.X / 2
				}
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
				return t.layoutSnips(gtx)
			}),
		)
	}
	return t.layoutTerminal(gtx)
}

func (t *sshTab) layoutTerminal(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, tema.TermBg, clip.Rect{Max: size}.Op())

	avanco, alturaCel := t.medidaCelula(gtx)
	if avanco <= 0 || alturaCel <= 0 {
		return layout.Dimensions{Size: size}
	}
	margem := gtx.Dp(6)
	cols := max(20, int(float64(size.X-2*margem)/avanco))
	rows := max(4, (size.Y-2*margem)/alturaCel)
	t.redimensionar(cols, rows)

	t.mu.Lock()
	t.margem, t.avanco, t.alturaCel = margem, avanco, alturaCel
	t.mu.Unlock()

	defer op.Offset(image.Pt(margem, margem)).Push(gtx.Ops).Pop()
	defer clip.Rect{Max: image.Pt(size.X-margem, size.Y-margem)}.Push(gtx.Ops).Pop()
	t.desenharGrade(gtx, cols, rows, avanco, alturaCel)
	icSsh, corSsh, _ := t.Selo()
	desenharSplash(gtx, t.th, t.splash,
		fmt.Sprintf("%s@%s:%d", t.user, t.host, t.porta), icSsh, corSsh,
		&t.btnSplash, t.Reconectar)

	// t.estado NÃO carrega mais mensagem de conexão (isso é só o splash
	// desenhado acima agora: passo, erro, contagem de reconexão — ver
	// laco()/sessao()). O que sobra aqui é aviso de ação (copiar tela,
	// copiar seleção, erro de snippets — ver os setEstado() fora da
	// conexão) e o aviso de histórico, que se somam quando os dois
	// acontecem juntos.
	t.mu.Lock()
	texto := t.estado
	rolagem := t.rolagem
	t.mu.Unlock()
	if rolagem > 0 {
		// avisa que a tela não é mais a ao vivo — sem isto, rolar pro
		// histórico e esquecer disso parece a sessão ter travado.
		nota := fmt.Sprintf("↑ histórico — %d linha(s) acima (digite algo pra voltar ao fim)", rolagem)
		if texto != "" {
			texto += "   •   " + nota
		} else {
			texto = nota
		}
	}
	if texto != "" {
		txt(t.th, fonteMono, spCorpo, texto, tema.AtencaoFg).Layout(gtx)
	}
	return layout.Dimensions{Size: size}
}

// medidaCelula mede o avanço da monoespaçada uma vez por quadro: é a
// única forma honesta de saber o tamanho da célula, já que ele depende da
// fonte embutida e da escala da tela.
//
// O avanço volta em FLOAT de propósito. Arredondar aqui e posicionar cada
// coluna em col*int(avanço) desalinha o desenho: dentro de um trecho de
// texto o shaper usa o avanço real, então o erro de arredondamento se
// acumula letra a letra e, lá pela coluna 30, o cursor já aparece
// quatro caracteres fora do lugar (visto na prática contra um cmd.exe).
func (t *sshTab) medidaCelula(gtx layout.Context) (float64, int) {
	const amostra = 40
	gtxM := gtx
	gtxM.Constraints.Min = image.Point{}
	gtxM.Constraints.Max = image.Pt(1<<20, 1<<20)
	macro := op.Record(gtx.Ops)
	d := txt(t.th, fonteMono, t.corpoAtual(), strings.Repeat("M", amostra), tema.TermFg).Layout(gtxM)
	macro.Stop() // medição: nada disso vai pra tela
	return float64(d.Size.X) / amostra, d.Size.Y
}

func (t *sshTab) redimensionar(cols, rows int) {
	t.mu.Lock()
	mudou := cols != t.cols || rows != t.rows
	t.cols, t.rows = cols, rows
	sess := t.sess
	t.mu.Unlock()
	if !mudou {
		return
	}
	// A marcação é em coordenadas de tela; mudou a grade, ela não quer
	// dizer mais nada. A rolagem também volta ao fundo: o número de
	// linhas por tela mudou, então "rolado N linhas" não aponta mais
	// pro mesmo lugar.
	t.mu.Lock()
	t.selAtiva, t.arrastando = false, false
	t.rolagem = 0
	t.mu.Unlock()
	t.term.Resize(cols, rows)
	if sess != nil {
		sess.WindowChange(rows, cols)
	}
}

func (t *sshTab) desenharGrade(gtx layout.Context, cols, rows int, avanco float64, ch int) {
	// px converte coluna em pixel com o avanço REAL: o texto de um trecho
	// é desenhado a partir de px(col) e daí em diante o próprio shaper
	// avança, então fundo, glifo e cursor caem sempre na mesma grade.
	px := func(col int) int { return int(math.Round(float64(col) * avanco)) }
	selA, selB, temSel := t.selecao()
	t.mu.Lock()
	rolagem := t.rolagem
	t.mu.Unlock()

	t.term.Lock()
	defer t.term.Unlock()

	tcols, trows := t.term.Size()
	if tcols < cols {
		cols = tcols
	}
	if trows < rows {
		rows = trows
	}

	// cel: a célula da linha VISÍVEL y, que tanto pode vir da tela ao
	// vivo quanto do histórico — é o único ponto que sabe a diferença;
	// todo o resto da função só desenha o que `cel` devolver. hist é o
	// total de linhas de scrollback guardadas; rolagem, quanto se rolou
	// pra dentro dele (0 = fundo, ao vivo). Ver o comentário do campo
	// `rolagem` na struct pra conta completa.
	hist := t.term.HistoryLen()
	cel := func(x, y int) vt10x.Glyph {
		idx := hist - rolagem + y
		if idx < hist {
			return t.term.HistoryCell(x, idx)
		}
		return t.term.Cell(x, idx-hist)
	}

	for y := 0; y < rows; y++ {
		// primeiro os fundos da linha, depois o texto: assim uma célula
		// com fundo não apaga o glifo da vizinha.
		x := 0
		for x < cols {
			g := cel(x, y)
			bg, temBg := corDeFundo(g.BG)
			if !temBg {
				x++
				continue
			}
			fim := x + 1
			for fim < cols {
				g2 := cel(fim, y)
				if c2, ok := corDeFundo(g2.BG); !ok || c2 != bg {
					break
				}
				fim++
			}
			r := clip.Rect{Min: image.Pt(px(x), y*ch), Max: image.Pt(px(fim), (y+1)*ch)}
			paint.FillShape(gtx.Ops, bg, r.Op())
			x = fim
		}

		// A marcação entra DEPOIS dos fundos e ANTES dos glifos: por cima
		// do texto, ela suja a leitura justamente do trecho que a pessoa
		// quer conferir antes de copiar. tema.TermSel é sólido de
		// propósito (ver o comentário no campo, em tema.go) — o mesmo
		// alfa calculado para o terminal escuro lavava quase invisível
		// sobre o fundo branco do terminal claro.
		if temSel && y >= selA.y && y <= selB.y {
			xi, xf := 0, cols-1
			if y == selA.y {
				xi = selA.x
			}
			if y == selB.y {
				xf = selB.x
			}
			if xi <= xf {
				paint.FillShape(gtx.Ops, tema.TermSel, clip.Rect{
					Min: image.Pt(px(xi), y*ch),
					Max: image.Pt(px(xf+1), (y+1)*ch),
				}.Op())
			}
		}

		x = 0
		for x < cols {
			g := cel(x, y)
			fg := corDeTexto(g.FG)
			var sb strings.Builder
			inicio := x
			for x < cols {
				g2 := cel(x, y)
				if corDeTexto(g2.FG) != fg {
					break
				}
				c := g2.Char
				if c == 0 {
					c = ' '
				}
				sb.WriteRune(c)
				x++
			}
			s := strings.TrimRight(sb.String(), " ")
			if s == "" {
				continue
			}
			st := op.Offset(image.Pt(px(inicio), y*ch)).Push(gtx.Ops)
			gtxL := gtx
			gtxL.Constraints.Min = image.Point{}
			gtxL.Constraints.Max = image.Pt(1<<20, ch)
			txt(t.th, fonteMono, t.corpoAtual(), s, fg).Layout(gtxL)
			st.Pop()
		}
	}

	// Cursor só faz sentido no fundo da tela: rolado pro histórico, a
	// posição dele não corresponde a linha nenhuma das que estão à vista.
	//
	// Barra piscante, não mais o bloco sólido: mais perto do que
	// alacritty/gnome-terminal mostram. t.cursorAceso() decide o piscar —
	// sempre visível logo depois de uma tecla digitada (senão o cursor
	// "sumiria" bem no instante em que a pessoa está olhando pra ele), e
	// alternando num período fixo quando ocioso.
	//
	// O pedido do próximo quadro fica FORA do if de baixo de propósito: se
	// ficasse dentro, só a fase ACESA rearmaria o piscar, e o cursor
	// apagaria de vez na primeira virada.
	if rolagem == 0 && t.term.CursorVisible() {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(t.proximaViradaDoCursor(gtx.Now))})
	}
	if rolagem == 0 && t.term.CursorVisible() && t.cursorAceso() {
		c := t.term.Cursor()
		if c.X < cols && c.Y < rows {
			larg := max(gtx.Dp(2), 1)
			r := clip.Rect{
				Min: image.Pt(px(c.X), c.Y*ch),
				Max: image.Pt(px(c.X)+larg, (c.Y+1)*ch),
			}
			paint.FillShape(gtx.Ops, tema.Azul, r.Op())
		}
	}
}

func corVT(c vt10x.Color) (color.NRGBA, bool) {
	switch {
	case c == vt10x.DefaultFG || c == vt10x.DefaultBG:
		return color.NRGBA{}, false
	case c < 16:
		// tema.Ansi, não uma paleta fixa: o terminal claro precisa de
		// tons recalibrados pro fundo claro (ver o comentário em
		// temaClaro), não da mesma paleta com o fundo trocado por baixo.
		return tema.Ansi[c], true
	case c < 256:
		return corXterm(int(c)), true
	}
	// acima de 256 o vt10x guarda cor de 24 bits crua
	v := uint32(c)
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}, true
}

func corDeTexto(c vt10x.Color) color.NRGBA {
	if got, ok := corVT(c); ok {
		return got
	}
	return tema.TermFg
}

// corDeFundo devolve (cor, precisa pintar). O fundo padrão não é pintado:
// o retângulo do terminal inteiro já foi preenchido com ele.
func corDeFundo(c vt10x.Color) (color.NRGBA, bool) {
	return corVT(c)
}

// corXterm converte um índice 16..255 da paleta xterm: 6x6x6 de cor e 24
// de cinza.
func corXterm(i int) color.NRGBA {
	if i >= 232 {
		v := uint8(8 + (i-232)*10)
		return color.NRGBA{R: v, G: v, B: v, A: 0xff}
	}
	i -= 16
	nivel := func(n int) uint8 {
		if n == 0 {
			return 0
		}
		return uint8(55 + n*40)
	}
	return color.NRGBA{R: nivel(i / 36), G: nivel((i / 6) % 6), B: nivel(i % 6), A: 0xff}
}

// ------------------------------------------------ barra de sessão

func (t *sshTab) EstadoSessao() estadoSessao {
	t.mu.Lock()
	viva := t.sess != nil
	cols, rows := t.cols, t.rows
	t.mu.Unlock()

	e := estadoSessao{
		Chip: "AGUARDE", Tipo: "neutro",
		Texto: fmt.Sprintf("%s@%s:%d", t.user, t.host, t.porta),
		Geo:   fmt.Sprintf("%dx%d", cols, rows),
	}
	switch {
	case viva:
		e.Chip, e.Tipo = "ATIVO", "ok"
	case t.caiu.Load():
		e.Chip, e.Tipo = "CAIU", "erro"
	}
	return e
}

func (t *sshTab) ControlesSessao(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Os três botões abaixo ficam LOGO ACIMA do terminal, e o operador
	// volta a digitar comandos assim que clica em qualquer um deles. Sem
	// tirar o foco de teclado do Gio depois do clique, o botão continua
	// focado e um Enter/Espaço do próprio comando SSH (super comum) é lido
	// como um novo clique nele — reconectando, religando auto-reconexão ou
	// reabrindo o painel de snippets sozinho, mesmo com o texto chegando
	// certinho na sessão remota (que recebe a tecla por um caminho à
	// parte, ver internal/grab).
	if t.btnRec.Clicked(gtx) {
		t.Reconectar()
		gtx.Execute(key.FocusCmd{Tag: nil})
	}
	if t.btnAuto.Clicked(gtx) {
		t.auto.Store(!t.auto.Load())
		gravarPreferencia(t.nomeConexao, "ssh_auto", simNao(t.auto.Load()))
		gtx.Execute(key.FocusCmd{Tag: nil})
	}
	if t.btnSnip.Clicked(gtx) {
		// Painel LATERAL, não janela: um modal em cima do terminal tapa
		// justamente a saída que se quer consultar antes de escolher o
		// comando. Aqui a lista fica ao lado e o terminal continua vivo.
		t.painelSnips = !t.painelSnips
		if t.painelSnips && t.snips == nil {
			t.recarregarSnips()
		}
		gtx.Execute(key.FocusCmd{Tag: nil})
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoSessao(gtx, th, &t.btnSnip, "Snippets")
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoSessao(gtx, th, &t.btnRec, "Reconectar")
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return toggleSessao(gtx, th, &t.btnAuto, icons.NavigationRefresh, t.auto.Load(), tema.Azul)
		}),
	)
}

// Reconectar só sinaliza — mesmo padrão do RDP/VNC (rdpTab.Reconectar,
// vncTab.Reconectar). Quem de fato fecha cli/sess é o vigia dentro de
// sessao(), reagindo a <-t.religar: fechar direto aqui TAMBÉM fechava
// (funcionava), mas duplicava o que o vigia já faz e divergia de como as
// outras duas abas reconectam manualmente.
func (t *sshTab) Reconectar() {
	select {
	case t.religar <- struct{}{}:
	default:
	}
}

// limparTela empurra o conteúdo atual da tela pro histórico (rolando,
// não apagando) e leva o cursor pro topo — ver o comentário em sessao()
// sobre por que isto roda no começo de toda RECONEXÃO.
func (t *sshTab) limparTela() {
	t.mu.Lock()
	rows := t.rows
	t.mu.Unlock()
	if rows <= 0 {
		return
	}
	t.term.Write([]byte(strings.Repeat("\n", rows) + "\x1b[H"))
	t.invalidar()
}

// ------------------------------------------------ painel de snippets

func (t *sshTab) recarregarSnips() {
	s, err := carregarSnippets(caminhoSnippets(caminhoINI))
	if err != nil {
		t.setEstado(fmt.Sprintf("snippets: %v", err))
		return
	}
	t.snips = s
	t.btnSnips = make([]widget.Clickable, len(s))
	t.listaSnips.Axis = layout.Vertical
	t.filtroSnip.SingleLine = true
}

// layoutSnips é a coluna lateral: filtro em cima, lista embaixo. Clicar
// DIGITA o comando na sessão, sem Enter — quem confirma é quem está
// olhando a tela. Um comando de Windows num PDV Linux é erro caro, então a
// plataforma de cada snippet fica visível na linha.
func (t *sshTab) layoutSnips(gtx layout.Context) layout.Dimensions {
	th := t.th
	termo := strings.ToLower(strings.TrimSpace(t.filtroSnip.Text()))

	var visiveis []int
	for i, s := range t.snips {
		if termo == "" || strings.Contains(strings.ToLower(s.Descricao), termo) ||
			strings.Contains(strings.ToLower(s.Comando), termo) {
			visiveis = append(visiveis, i)
		}
	}

	for _, i := range visiveis {
		if t.btnSnips[i].Clicked(gtx) {
			t.enviar([]byte(t.snips[i].Comando))
		}
	}

	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			size := gtx.Constraints.Min
			paint.FillShape(gtx.Ops, tema.Abas2, clip.Rect{Max: size}.Op())
			paint.FillShape(gtx.Ops, tema.Borda2, clip.Rect{Max: image.Pt(1, size.Y)}.Op())
			return layout.Dimensions{Size: size}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return caixaEditor(gtx, th, &t.filtroSnip, "filtrar snippets…", 0)
					}),
					espaco(6),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return material.List(th, &t.listaSnips).Layout(gtx, len(visiveis), func(gtx layout.Context, k int) layout.Dimensions {
							i := visiveis[k]
							s := t.snips[i]
							return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return t.btnSnips[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Background{}.Layout(gtx,
										func(gtx layout.Context) layout.Dimensions {
											fundo, borda := tema.Cartao, transparente
											if t.btnSnips[i].Hovered() {
												fundo, borda = tema.Hover, tema.Borda2
											}
											superficie(gtx, gtx.Constraints.Min, fundo, borda, 6)
											return layout.Dimensions{Size: gtx.Constraints.Min}
										},
										func(gtx layout.Context) layout.Dimensions {
											gtx.Constraints.Min.X = gtx.Constraints.Max.X
											return layout.Inset{Top: 5, Bottom: 5, Left: 7, Right: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
												return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
													layout.Rigid(func(gtx layout.Context) layout.Dimensions {
														return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
															layout.Rigid(func(gtx layout.Context) layout.Dimensions {
																return selinhoPlataforma(gtx, th, s.Plataforma)
															}),
															layout.Rigid(layout.Spacer{Width: 5}.Layout),
															layout.Flexed(1, rotulo(th, fonteSans, spSecundario, s.Descricao, tema.Texto)),
														)
													}),
													layout.Rigid(rotuloLinha(th, fonteMono, spCardMeta, primeiraLinha(s.Comando), tema.Fraco)),
												)
											})
										},
									)
								})
							})
						})
					}),
				)
			})
		},
	)
}
