//go:build linux

package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"acessos-go/internal/conexoes"
	"acessos-go/internal/rdp"

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
	sess              atomic.Pointer[rdp.Session]
	stop              chan struct{}
	closeOnce         sync.Once
	viewMu            sync.Mutex
	view              rdpView
	clip              clipboardSync
	mu                sync.Mutex // protege lastSizeRequested entre a rede e o desenho
	lastSizeRequested image.Point
	lastButtons       pointer.Buttons

	// estado mostrado e controlado pela barra de sessão
	religar     chan struct{}
	auto        atomic.Bool
	clipOn      atomic.Bool
	modo        atomic.Int32
	caiu        atomic.Bool
	fw, fh      atomic.Int32
	nomeConexao string
	btnRec      widget.Clickable
	btnTeclas   widget.Clickable
	btnAuto     widget.Clickable
	btnClip     widget.Clickable
	btnModo     [3]widget.Clickable
}

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

func (t *rdpTab) manageSession(user, pass, domain string) {
	attempt := 0

	for {
		sess := rdp.New()
		sess.SetCredentials(user, pass, domain)
		sess.OnUpdate = func(x, y, w, h int) { t.w.Invalidate() }
		sess.OnResize = func(w, h int) { t.w.Invalidate() }
		// Certificado: o callback roda NA THREAD DE REDE do FreeRDP e
		// bloqueia o handshake — é isso que dá sentido à pergunta. A
		// resposta vem da interface por um canal.
		// Canal de display pronto: reenvia o tamanho AGORA. Sem isto, o
		// primeiro pedido (feito no primeiro quadro) caía antes do
		// handshake do canal e era descartado — daí a sessão só se
		// ajustar quando a janela mexia.
		sess.OnDisplayPronto = func() {
			t.mu.Lock()
			t.lastSizeRequested = image.Point{}
			t.mu.Unlock()
			t.w.Invalidate()
		}
		sess.OnCertificado = func(c rdp.Certificado) int {
			resp := make(chan int, 1)
			pedirConfiancaCertificado(t.w, c, func(d int) { resp <- d })
			t.w.Invalidate()
			return <-resp
		}
		sess.OnClipboardText = func(text string) {
			if !t.clipOn.Load() {
				return
			}
			if !t.clip.checkAndSet(text) {
				return
			}
			currentGrab.Load().SetClipboardText(text)
		}

		reg("[%s] conectando a %s:%d…", t.title, t.host, t.port)
		inicio := time.Now()
		if err := sess.Connect(t.host, t.port); err != nil {
			ce := err.(*rdp.ConnectError)
			fmt.Fprintf(os.Stderr, "[%s] falha: %s (auth=%v)\n", t.title, ce.Message, ce.AuthFailed)
			sess.Close()
			return
		}
		reg("[%s] conectado em %s", t.title, time.Since(inicio).Truncate(time.Millisecond))
		attempt = 0

		t.sess.Store(sess)
		t.caiu.Store(false)
		t.w.Invalidate()
		runStop := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- sess.Run(runStop) }()

		manual := false
		select {
		case <-t.stop:
			close(runStop)
			<-done
			sess.Close()
			return
		case <-t.religar:
			manual = true
			close(runStop)
			<-done
			t.sess.Store(nil)
			sess.Close()
		case err := <-done:
			t.sess.Store(nil)
			t.caiu.Store(true)
			sess.Close()
			reg("[%s] sessão caiu: %v", t.title, err)
			t.w.Invalidate()
		}
		if manual {
			attempt = 0
			t.mu.Lock()
			t.lastSizeRequested = image.Point{}
			t.mu.Unlock()
			continue
		}
		if !t.auto.Load() {
			select {
			case <-t.religar:
				attempt = 0
				t.mu.Lock()
				t.lastSizeRequested = image.Point{}
				t.mu.Unlock()
				continue
			case <-t.stop:
				return
			}
		}

		wait := backoffSchedule[min(attempt, len(backoffSchedule)-1)]
		attempt++
		fmt.Printf("[%s] reconectando em %s (tentativa #%d)\n", t.title, wait, attempt)
		select {
		case <-time.After(wait):
		case <-t.religar:
			attempt = 0
		case <-t.stop:
			return
		}
		t.mu.Lock()
		t.lastSizeRequested = image.Point{}
		t.mu.Unlock()
	}
}

func (t *rdpTab) OnLocalClipboard(text string) {
	if !t.clipOn.Load() {
		return
	}
	if !t.clip.checkAndSet(text) {
		return
	}
	if sess := t.sess.Load(); sess != nil {
		sess.SendClipboardText(text)
	}
}

// rdpBGRXToNRGBA é como bgrxToNRGBA (vnctab.go), mas respeitando stride:
// ao contrário do VNC, o gdi do FreeRDP pode alinhar cada linha além de
// w*4 bytes.
func rdpBGRXToNRGBA(buf []byte, w, h, stride int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := buf[y*stride:]
		out := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			b := row[x*4+0]
			g := row[x*4+1]
			r := row[x*4+2]
			out[x*4+0] = r
			out[x*4+1] = g
			out[x*4+2] = b
			out[x*4+3] = 255
		}
	}
	return img
}

func (t *rdpTab) Layout(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	sess := t.sess.Load()

	var buf []byte
	var fw, fh, stride int
	if sess != nil {
		buf, fw, fh, stride = sess.Framebuffer()
	}
	t.fw.Store(int32(fw))
	t.fh.Store(int32(fh))
	v := computeRDPView(size, fw, fh, t.modo.Load())
	t.viewMu.Lock()
	t.view = v
	t.viewMu.Unlock()

	// Resolução dinâmica: acompanha o tamanho da área da aba (canal
	// Display Control) — só reenvia quando muda, mesma razão do rdpview.
	t.mu.Lock()
	pedirResize := sess != nil && size != t.lastSizeRequested && t.modo.Load() == modoDinamico
	if pedirResize {
		t.lastSizeRequested = size
	}
	t.mu.Unlock()
	if pedirResize {
		sess.RequestResize(size.X, size.Y)
	}

	// Clipa à própria área — mesma razão do vnctab.go: sem isto, o
	// preenchimento preto de fundo pinta por cima da barra de abas.
	area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
	defer area.Pop()

	paint.ColorOp{Color: color.NRGBA{A: 255}}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)

	if len(buf) > 0 {
		img := rdpBGRXToNRGBA(buf, fw, fh, stride)
		tr := op.Affine(f32.Affine2D{}.
			Scale(f32.Point{}, f32.Point{X: v.scale, Y: v.scale}).
			Offset(f32.Point{X: v.offX, Y: v.offY}),
		).Push(gtx.Ops)
		imgOp := paint.NewImageOp(img)
		imgOp.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		tr.Pop()
	}

	return layout.Dimensions{Size: size}
}

func (t *rdpTab) HandlePointer(ev pointer.Event, _ image.Point) {
	sess := t.sess.Load()
	if sess == nil {
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

	sess.PointerMove(x, y)

	changed := ev.Buttons ^ t.lastButtons
	if changed&pointer.ButtonPrimary != 0 {
		sess.PointerButton(x, y, 1, ev.Buttons&pointer.ButtonPrimary != 0)
	}
	if changed&pointer.ButtonTertiary != 0 {
		sess.PointerButton(x, y, 2, ev.Buttons&pointer.ButtonTertiary != 0)
	}
	if changed&pointer.ButtonSecondary != 0 {
		sess.PointerButton(x, y, 3, ev.Buttons&pointer.ButtonSecondary != 0)
	}
	t.lastButtons = ev.Buttons
}

func (t *rdpTab) HandleKey(_, keycodeX11 uint32, pressed bool) {
	if sess := t.sess.Load(); sess != nil {
		sess.KeyEvent(keycodeX11, pressed)
	}
}

// ------------------------------------------------ barra de sessão

func (t *rdpTab) EstadoSessao() estadoSessao {
	e := estadoSessao{
		Chip: "AGUARDE", Tipo: "neutro",
		Texto: fmt.Sprintf("%s:%d", t.host, t.port),
	}
	switch {
	case t.sess.Load() != nil:
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
			t.mu.Lock()
			t.lastSizeRequested = image.Point{}
			t.mu.Unlock()
		}
	}
	if t.btnTeclas.Clicked(gtx) {
		menuTeclas(ultimaPosPonteiro(), false, func(ks uint32, pressionada bool) {
			kc, ok := keycodeDoKeysym[ks]
			if !ok {
				return
			}
			if sess := t.sess.Load(); sess != nil {
				sess.KeyEvent(kc, pressionada)
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
