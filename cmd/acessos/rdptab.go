//go:build linux || windows

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"sync"
	"sync/atomic"
	"time"

	"acessos-go/internal/conexoes"
	"acessos-go/internal/rdp"
	"acessos-go/internal/telaproc"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"gio.tools/icons"
)

// A sessão RDP NÃO roda dentro deste processo: ela vive num processo-filho
// (ver internal/telaproc e telaworker.go), e o que existe aqui é a ponta
// que manda entrada e recebe retângulos de tela. O motivo está no
// BACKLOG.md §7 — um crash dentro da libfreerdp levava o app inteiro, com
// todas as abas. Agora leva o filho, esta aba marca "CAIU" e o mesmo
// backoff de sempre religa.

type rdpView struct {
	scale      float32
	offX, offY float32
}

func computeRDPView(size image.Point, fw, fh int, modo int32) rdpView {
	if fw == 0 || fh == 0 {
		return rdpView{scale: 1}
	}
	sx := float32(size.X) / float32(fw)
	sy := float32(size.Y) / float32(fh)
	scale := sx
	if sy < scale {
		scale = sy
	}
	// no 1:1 e no dinâmico não se escala: no primeiro por escolha, no
	// segundo porque quem muda de tamanho é o servidor. E nunca amplia.
	if scale > 1 || modo != modoEncaixar {
		scale = 1
	}
	ow, oh := float32(fw)*scale, float32(fh)*scale
	return rdpView{
		scale: scale,
		offX:  (float32(size.X) - ow) / 2,
		offY:  (float32(size.Y) - oh) / 2,
	}
}

type rdpTab struct {
	w                 *app.Window
	title             string
	host              string
	port              int
	proc              atomic.Pointer[telaproc.Processo]
	stop              chan struct{}
	closeOnce         sync.Once
	viewMu            sync.Mutex
	view              rdpView
	clip              clipboardSync
	mu                sync.Mutex // protege lastSizeRequested/resize* entre a rede e o desenho
	lastSizeRequested image.Point
	// resizeAlvo/resizeArmado formam o debounce do resize dinâmico: ver
	// pedirResizeComDebounce. Sem isto, arrastar a borda da janela
	// mandava um CmdResize A CADA QUADRO da interface (dezenas por
	// segundo) — cada um derruba e remonta as surfaces gfx no servidor
	// (RDPGFX_RESET_GRAPHICS), o que já foi visto travando a tela e até
	// derrubando a sessão no meio do arrasto.
	resizeAlvo   image.Point
	resizeArmado *time.Timer
	lastButtons  pointer.Buttons

	// tela é o último quadro PRONTO para desenhar. Quem monta troca o
	// ponteiro por uma imagem nova e nunca mexe na anterior — é o que
	// permite entregá-la ao Gio sem trava e sem risco de ela mudar
	// debaixo do upload da textura.
	tela atomic.Pointer[image.NRGBA]
	// opCache guarda a ImageOp da última tela publicada: sem isto o Gio
	// remontaria (e reenviaria à GPU) a textura a cada quadro DA
	// INTERFACE, mesmo sem nada ter mudado do lado remoto.
	opCache  paint.ImageOp
	opDaTela *image.NRGBA

	// estado mostrado e controlado pela barra de sessão
	religar     chan struct{}
	auto        atomic.Bool
	clipOn      atomic.Bool
	modo        atomic.Int32
	caiu        atomic.Bool
	fw, fh      atomic.Int32
	nomeConexao string
	cursorAtual atomic.Uint32
	btnRec      widget.Clickable
	btnTeclas   widget.Clickable
	btnAuto     widget.Clickable
	btnClip     widget.Clickable
	btnModo     [3]widget.Clickable

	splash    *splash
	btnSplash widget.Clickable
}

// passosRDP são os únicos três pontos observáveis do lado de cá: o
// processo-filho existe, ele confirmou a conexão, e o primeiro
// retângulo de tela chegou. Não há meio-termo real entre eles (ver
// lacoEventos) — inventar mais passos que isso seria fingir precisão
// que o protocolo não dá.
var passosRDP = []string{"Iniciando processo", "Conectando", "Recebendo tela"}

// No RDP existe um terceiro modo: "Dinâmico" pede ao servidor a resolução
// do tamanho da aba (canal Display Control), em vez de escalar do lado de
// cá. É o melhor dos dois mundos quando o servidor aceita — por isso é o
// padrão aqui, ao contrário do VNC.
const modoDinamico = 2

var rotulosModoRDP = []string{"Encaixar", "1:1", "Dinâmico"}

func newRDPTab(w *app.Window, spec map[string]string) *rdpTab {
	host := spec["host"]
	port := specInt(spec, "port", 3389)
	t := &rdpTab{
		w:       w,
		title:   rotuloAba(spec, "RDP", host),
		host:    host,
		port:    port,
		stop:    make(chan struct{}),
		religar: make(chan struct{}, 1),
	}
	t.splash = novoSplash(w)
	t.auto.Store(true)
	t.clipOn.Store(true)
	t.modo.Store(modoDinamico)
	t.nomeConexao = spec["rotulo"]
	if spec["auto"] == "0" {
		t.auto.Store(false)
	}
	go t.manageSession(spec["user"], spec["pass"], spec["domain"])
	return t
}

func (t *rdpTab) Title() string { return t.title }
func (t *rdpTab) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	e := estiloDe(conexoes.RDP)
	return e.ic, e.cor(), e.fundo()
}

// EhTelaRemota marca esta aba como tela remota (ver tab.go).
func (t *rdpTab) EhTelaRemota() {}

func (t *rdpTab) Pinned() bool  { return false }
func (t *rdpTab) SoIcone() bool { return false }

func (t *rdpTab) Close() {
	t.closeOnce.Do(func() { close(t.stop) })
}

// manageSession delega o laço de reconexão a gerenciarSessaoRemota (ver
// telatab.go), compartilhado com vncTab. Os dois hooks são o que só o
// RDP precisa: esquecer a resolução já pedida ao servidor antes de cada
// tentativa (senão uma sessão religada fica na resolução de quando
// caiu), e apagar a tela congelada ao perder a sessão — com o chip
// "CAIU" ao lado, uma imagem parada que continua parecendo viva é pior
// que preto.
func (t *rdpTab) manageSession(user, pass, domain string) {
	gerenciarSessaoRemota(sessaoRemotaCfg{
		title:   t.title,
		stop:    t.stop,
		religar: t.religar,
		w:       t.w,
		caiu:    &t.caiu,
		auto:    &t.auto,
		rodar:   func() fimSessao { return t.rodarSessao(user, pass, domain) },
		antesDeConectar: func() {
			t.esquecerTamanho()
			t.splash.iniciar(passosRDP)
		},
		aoTerminar: func() { t.proc.Store(nil); t.tela.Store(nil) },
		aoAguardar: t.splash.aguardar,
	})
}

// esquecerTamanho faz o próximo quadro reenviar a resolução ao servidor.
// Sem isto, uma sessão religada fica na resolução de quando caiu.
func (t *rdpTab) esquecerTamanho() {
	t.mu.Lock()
	t.lastSizeRequested = image.Point{}
	t.mu.Unlock()
}

// resizeDebounce é quanto tempo o tamanho tem que ficar PARADO antes do
// CmdResize sair de verdade. 200ms é curto o bastante pra sentir
// instantâneo depois de soltar a borda, e longo o bastante pra um
// arrasto de vários segundos (60 quadros/s) virar UM pedido só, não uma
// dezena.
const resizeDebounce = 200 * time.Millisecond

// pedirResizeComDebounce troca o pedido imediato por um agendado: cada
// quadro com um tamanho novo só ATUALIZA o alvo e rearma o prazo, nunca
// manda nada direto. Só quando o prazo esgota sem mais nenhuma mudança é
// que dispararResize roda de verdade.
//
// Sem isto, arrastar a borda da janela mandava um CmdResize A CADA
// QUADRO da interface — e cada um faz o servidor derrubar e remontar
// TODAS as surfaces gfx (RDPGFX_RESET_GRAPHICS), o que já foi visto
// deixando a tela preta/lixo durante o arrasto e, num caso pior, caindo
// a sessão no meio do caminho.
func (t *rdpTab) pedirResizeComDebounce(size image.Point) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if size == t.lastSizeRequested {
		return
	}
	t.resizeAlvo = size
	if t.resizeArmado == nil {
		t.resizeArmado = time.AfterFunc(resizeDebounce, t.dispararResize)
		return
	}
	t.resizeArmado.Reset(resizeDebounce)
}

// dispararResize roda numa goroutine própria do time.AfterFunc, nunca no
// laço de quadro. Lê t.proc.Load() (não um valor capturado) porque a
// sessão pode ter religado durante os 200ms de espera.
func (t *rdpTab) dispararResize() {
	t.mu.Lock()
	alvo := t.resizeAlvo
	t.lastSizeRequested = alvo
	t.resizeArmado = nil
	t.mu.Unlock()
	if p := t.proc.Load(); p != nil {
		_ = p.Resize(alvo.X, alvo.Y)
	}
}

// rodarSessao delega a rodarSessaoRemota (ver telatab.go), compartilhado
// com vncTab — só como conectar() monta a telaproc.Ligacao muda entre os
// dois protocolos.
func (t *rdpTab) rodarSessao(user, pass, domain string) fimSessao {
	return rodarSessaoRemota("rdp", t.title, t.host, t.port, t.stop, t.religar,
		func(proc *telaproc.Processo) error {
			err := proc.Conectar(telaproc.Ligacao{
				Host: t.host, Porta: t.port,
				Usuario: user, Senha: pass, Dominio: domain,
			})
			if err != nil {
				t.splash.setErro(err.Error())
				return err
			}
			t.splash.avancar(1)
			return nil
		},
		t.lacoEventos,
	)
}

// lacoEventos é o único leitor do canal do filho. Sai quando o filho fecha
// ou morre — e "morre" inclui o SIGSEGV dentro da libfreerdp, que daqui é
// indistinguível de uma desconexão limpa. Essa indistinção é o ponto.
//
// Devolve true quando a sessão terminou por RECUSA do servidor.
func (t *rdpTab) lacoEventos(proc *telaproc.Processo, inicio time.Time) (falhou bool) {
	// acum é a tela remota inteira, montada retângulo a retângulo. Fica
	// nesta goroutine e nunca é entregue ao Gio: o que vai para a
	// interface é sempre uma cópia congelada (ver publicar).
	var acum *image.NRGBA

	for {
		tipo, corpo, err := proc.Ler()
		if err != nil {
			return false
		}
		switch tipo {
		case telaproc.EvtConectado:
			reg("[%s] conectado em %s", t.title, time.Since(inicio).Truncate(time.Millisecond))
			t.proc.Store(proc)
			t.caiu.Store(false)
			t.splash.avancar(2)
			t.w.Invalidate()
			_ = proc.Credito()

		case telaproc.EvtFalha:
			var f telaproc.Falha
			_ = json.Unmarshal(corpo, &f)
			reg("[%s] falha: %s (auth=%v)", t.title, f.Mensagem, f.AuthFalhou)
			t.splash.setErro(f.Mensagem)
			return true

		case telaproc.EvtQuadro:
			q, pix, err := telaproc.DecodificarQuadro(corpo)
			if err != nil {
				reg("[%s] quadro inválido: %v", t.title, err)
				return false
			}
			acum = aplicarQuadro(acum, q, pix)
			t.fw.Store(q.TotalW)
			t.fh.Store(q.TotalH)
			publicarTela(&t.tela, acum)
			t.splash.concluir()
			t.w.Invalidate()
			// O crédito do quadro SEGUINTE só sai agora: é o que impede o
			// filho de encher a fila do socket mais rápido do que isto
			// aqui consome. E sai devagar quando a aba não está à vista —
			// ver intervaloSegundoPlano.
			creditarConformeVisibilidade(proc, ehAbaAtiva(t), t.stop)

		case telaproc.EvtDesconectado:
			reg("[%s] sessão caiu: %s", t.title, string(corpo))
			t.splash.setErro(string(corpo))
			return false

		case telaproc.EvtClipboard:
			texto := string(corpo)
			if !t.clipOn.Load() || !ehAbaAtiva(t) {
				continue
			}
			if !t.clip.checkAndSet(texto) {
				continue
			}
			publicarClipboard(t.w, texto)

		case telaproc.EvtCursor:
			if c, ok := telaproc.LerCursor(corpo); ok {
				t.cursorAtual.Store(c)
				t.w.Invalidate()
			}

		case telaproc.EvtDisplayPronto:
			// O canal Display Control acabou o handshake: reenvia o
			// tamanho AGORA. Sem isto, o primeiro pedido (feito no
			// primeiro quadro) caía antes do handshake e era descartado —
			// daí a sessão só se ajustar quando a janela mexia.
			t.esquecerTamanho()
			t.w.Invalidate()

		case telaproc.EvtCertPedido:
			var c telaproc.Certificado
			if err := json.Unmarshal(corpo, &c); err != nil {
				_ = proc.CertResposta(rdp.CertRecusar)
				continue
			}
			// O filho está com o handshake PARADO esperando isto, então a
			// resposta tem de sair de uma goroutine própria: o diálogo só
			// é respondido no laço de quadro, que precisa deste laço aqui
			// vivo para a aba continuar desenhando enquanto se pergunta.
			go func() {
				resp := make(chan int, 1)
				pedirConfiancaCertificado(t.w, rdp.Certificado{
					Host: c.Host, Porta: c.Porta,
					NomeComum: c.NomeComum, Assunto: c.Assunto,
					Emissor: c.Emissor, Digital: c.Digital,
					DigitalAnterior: c.DigitalAnterior, Mudou: c.Mudou,
				}, func(d int) { resp <- d })
				t.w.Invalidate()
				select {
				case d := <-resp:
					_ = proc.CertResposta(d)
				case <-t.stop:
				}
			}()
		}
	}
}

func (t *rdpTab) OnLocalClipboard(text string) {
	if !t.clipOn.Load() {
		return
	}
	if !t.clip.checkAndSet(text) {
		return
	}
	if p := t.proc.Load(); p != nil {
		_ = p.Clipboard(text)
	}
}

func (t *rdpTab) Layout(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	proc := t.proc.Load()
	tela := t.tela.Load()

	fw, fh := 0, 0
	if tela != nil {
		fw, fh = tela.Rect.Dx(), tela.Rect.Dy()
	}
	v := computeRDPView(size, fw, fh, t.modo.Load())
	t.viewMu.Lock()
	t.view = v
	t.viewMu.Unlock()

	// Resolução dinâmica: acompanha o tamanho da área da aba (canal
	// Display Control) — só reenvia quando muda, mesma razão do rdpview,
	// e com debounce (ver o campo resizeArmado): arrastar a borda não
	// pode virar um CmdResize por quadro.
	if proc != nil && t.modo.Load() == modoDinamico {
		t.pedirResizeComDebounce(size)
	}

	// Clipa à própria área — mesma razão do vnctab.go: sem isto, o
	// preenchimento preto de fundo pinta por cima da barra de abas.
	area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
	defer area.Pop()
	pointer.Cursor(t.cursorAtual.Load()).Add(gtx.Ops)

	// Preto só faz sentido como "sem sinal" atrás de uma tela remota de
	// verdade — é letterboxing de vídeo, sempre preto em qualquer app,
	// tema nenhum. Sem tela (splash em cima), quem cobre a área é o
	// fundo do tema: preto fixo no tema claro parecia bug, não vídeo.
	fundo := color.NRGBA{A: 255}
	if tela == nil {
		fundo = tema.Fundo
	}
	paint.ColorOp{Color: fundo}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)

	if tela != nil {
		// Só remonta a ImageOp quando a TELA mudou: a comparação é de
		// ponteiro porque cada publicação é uma imagem nova (ver
		// publicar). Reaproveitar a op é o que faz o Gio reusar a textura
		// já na GPU em vez de reenviá-la a cada quadro da interface.
		if t.opDaTela != tela {
			t.opCache = paint.NewImageOp(tela)
			t.opDaTela = tela
		}
		tr := op.Affine(f32.Affine2D{}.
			Scale(f32.Point{}, f32.Point{X: v.scale, Y: v.scale}).
			Offset(f32.Point{X: v.offX, Y: v.offY}),
		).Push(gtx.Ops)
		t.opCache.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		tr.Pop()
	} else {
		ic, corSelo, _ := t.Selo()
		desenharSplash(gtx, temaApp, t.splash,
			fmt.Sprintf("%s:%d", t.host, t.port), ic, corSelo,
			&t.btnSplash, t.Reconectar)
	}

	return layout.Dimensions{Size: size}
}

func (t *rdpTab) HandlePointer(ev pointer.Event, _ image.Point) {
	proc := t.proc.Load()
	if proc == nil {
		return
	}
	t.viewMu.Lock()
	v := t.view
	t.viewMu.Unlock()
	if v.scale == 0 {
		return
	}
	x := int((ev.Position.X - v.offX) / v.scale)
	y := int((ev.Position.Y - v.offY) / v.scale)

	_ = proc.PonteiroMover(x, y)

	changed := ev.Buttons ^ t.lastButtons
	if changed&pointer.ButtonPrimary != 0 {
		_ = proc.PonteiroBotao(x, y, 1, ev.Buttons&pointer.ButtonPrimary != 0)
	}
	if changed&pointer.ButtonTertiary != 0 {
		_ = proc.PonteiroBotao(x, y, 2, ev.Buttons&pointer.ButtonTertiary != 0)
	}
	if changed&pointer.ButtonSecondary != 0 {
		_ = proc.PonteiroBotao(x, y, 3, ev.Buttons&pointer.ButtonSecondary != 0)
	}
	t.lastButtons = ev.Buttons

	// Roda: nunca era encaminhada — HandlePointer só tratava Move e
	// botão. Ver passosDaRoda pra a convenção de sinal.
	if p := passosDaRoda(ev.Scroll.Y); p != 0 {
		_ = proc.PonteiroRoda(0, p)
	}
	if p := passosDaRoda(ev.Scroll.X); p != 0 {
		_ = proc.PonteiroRoda(1, p)
	}
}

// passosDaRoda traduz um delta de scroll do Gio pros "passos" que
// rs_ponteiro_roda espera. Um evento por "clique" da roda, mesma
// convenção do scroll local do terminal SSH (ver rolarHistorico em
// sshtab.go): nesta pilha cada pointer.Event de scroll já chega como um
// clique discreto, não como um delta contínuo a fatiar. Sinal: delta<0
// é "roda pra cima" (mesma leitura de rolarHistorico), e o RDP usa
// passos POSITIVOS pra cima (WHEEL_DELTA do Windows).
func passosDaRoda(delta float32) int {
	switch {
	case delta < 0:
		return 1
	case delta > 0:
		return -1
	}
	return 0
}

func (t *rdpTab) HandleKey(_, keycodeX11 uint32, pressed bool) {
	if p := t.proc.Load(); p != nil {
		_ = p.Tecla(keycodeX11, pressed)
	}
}

// ------------------------------------------------ barra de sessão

func (t *rdpTab) EstadoSessao() estadoSessao {
	e := estadoSessao{
		Chip: "AGUARDE", Tipo: "neutro",
		Texto: fmt.Sprintf("%s:%d", t.host, t.port),
	}
	switch {
	case t.proc.Load() != nil:
		e.Chip, e.Tipo = "ATIVO", "ok"
	case t.caiu.Load():
		e.Chip, e.Tipo = "CAIU", "erro"
	}
	if fw, fh := t.fw.Load(), t.fh.Load(); fw > 0 && fh > 0 {
		escala := "1:1"
		t.viewMu.Lock()
		if t.view.scale < 1 {
			escala = "reduzido"
		}
		t.viewMu.Unlock()
		e.Geo = fmt.Sprintf("%dx%d %s", fw, fh, escala)
	}
	return e
}

func (t *rdpTab) ControlesSessao(gtx layout.Context, th *material.Theme) layout.Dimensions {
	for i := range t.btnModo {
		if t.btnModo[i].Clicked(gtx) {
			t.modo.Store(int32(i))
			// voltar pro dinâmico tem que reenviar o tamanho, senão o
			// servidor fica na resolução de quando ele foi desligado.
			t.esquecerTamanho()
		}
	}
	if t.btnTeclas.Clicked(gtx) {
		menuTeclas(ultimaPosPonteiro(), false, func(ks uint32, pressionada bool) {
			kc, ok := keycodeDoKeysym[ks]
			if !ok {
				return
			}
			if p := t.proc.Load(); p != nil {
				_ = p.Tecla(kc, pressionada)
			}
		})
	}
	if t.btnRec.Clicked(gtx) {
		t.Reconectar()
	}
	if t.btnAuto.Clicked(gtx) {
		t.auto.Store(!t.auto.Load())
		gravarPreferencia(t.nomeConexao, "rdp_auto", simNao(t.auto.Load()))
	}
	if t.btnClip.Clicked(gtx) {
		t.clipOn.Store(!t.clipOn.Load())
	}

	// Sem seletor de modo no RDP: 1:1 e Encaixar não fazem sentido aqui,
	// porque o próprio servidor entrega a resolução do tamanho da aba
	// (Display Control). A sessão já nasce encaixada e reacompanha cada
	// redimensionamento — não há o que escolher.
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoTeclas(gtx, th, &t.btnTeclas)
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoSessao(gtx, th, &t.btnRec, "Reconectar")
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return toggleSessao(gtx, th, &t.btnClip, icons.ContentContentCopy, t.clipOn.Load(), tema.Verde)
		}),
		layout.Rigid(layout.Spacer{Width: 3}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return toggleSessao(gtx, th, &t.btnAuto, icons.NavigationRefresh, t.auto.Load(), tema.Azul)
		}),
	)
}

func (t *rdpTab) Reconectar() {
	select {
	case t.religar <- struct{}{}:
	default:
	}
}
