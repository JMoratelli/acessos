//go:build linux || windows

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
	"acessos-go/internal/vnc"

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

// backoffSchedule espelha o ESPERAS do app original (acessos.py).
var backoffSchedule = []time.Duration{
	3 * time.Second, 5 * time.Second, 8 * time.Second,
	13 * time.Second, 21 * time.Second, 30 * time.Second,
}

// vncView descreve como o framebuffer remoto está mapeado na área da aba
// (escala + letterbox) — mesma lógica de cmd/vncview.
type vncView struct {
	scale      float32
	offX, offY float32
}

func computeVNCView(size image.Point, fw, fh int, modo int32) vncView {
	if fw == 0 || fh == 0 {
		return vncView{scale: 1}
	}
	sx := float32(size.X) / float32(fw)
	sy := float32(size.Y) / float32(fh)
	scale := sx
	if sy < scale {
		scale = sy
	}
	// nunca AMPLIA: esticar o framebuffer de um PDV borra o texto, que é
	// justamente o que se vai ler. E no modo 1:1 não escala de jeito
	// nenhum, mesmo que sobre espaço.
	if scale > 1 || modo == modo1x1 {
		scale = 1
	}
	ow, oh := float32(fw)*scale, float32(fh)*scale
	return vncView{
		scale: scale,
		offX:  (float32(size.X) - ow) / 2,
		offY:  (float32(size.Y) - oh) / 2,
	}
}

type vncTab struct {
	w         *app.Window
	title     string
	host      string
	port      int
	sess      atomic.Pointer[vnc.Session]
	stop      chan struct{}
	religar   chan struct{}
	closeOnce sync.Once
	viewMu    sync.Mutex
	view      vncView
	clip      clipboardSync

	// estado mostrado e controlado pela barra de sessão
	auto   atomic.Bool // reconectar sozinho ao cair
	clipOn atomic.Bool // sincronizar área de transferência
	modo   atomic.Int32
	caiu   atomic.Bool
	fw, fh atomic.Int32
	// nomeConexao é a seção do .ini (vazio quando a conexão veio da linha
	// de comando: aí não há onde gravar preferência).
	nomeConexao string
	ronly       atomic.Bool
	btnOlho     widget.Clickable
	btnRec      widget.Clickable
	btnTeclas   widget.Clickable
	btnAuto     widget.Clickable
	btnClip     widget.Clickable
	btnModo     [2]widget.Clickable
}

// modos da tela remota. "encaixar" reduz só quando não cabe (nunca
// amplia — ampliar borra o texto do PDV); "1:1" mostra pixel a pixel,
// centralizado, cortando o que não couber.
const (
	modoEncaixar = 0
	modo1x1      = 1
)

var rotulosModo = []string{"Encaixar", "1:1"}

func newVNCTab(w *app.Window, spec map[string]string) *vncTab {
	host := spec["host"]
	port := specInt(spec, "port", 5900)
	t := &vncTab{
		w:       w,
		title:   rotuloAba(spec, "VNC", host),
		host:    host,
		port:    port,
		stop:    make(chan struct{}),
		religar: make(chan struct{}, 1),
	}
	t.auto.Store(true)
	t.clipOn.Store(true)
	t.nomeConexao = spec["rotulo"]
	t.ronly.Store(spec["ronly"] == "1")
	if spec["modo"] == "1x1" {
		t.modo.Store(modo1x1)
	}
	if spec["auto"] == "0" {
		t.auto.Store(false)
	}
	go t.manageSession(spec["user"], spec["pass"])
	return t
}

func (t *vncTab) Title() string { return t.title }
func (t *vncTab) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	e := estiloDe(conexoes.VNC)
	return e.ic, e.cor(), e.fundo()
}

// EhTelaRemota marca esta aba como tela remota (ver tab.go).
func (t *vncTab) EhTelaRemota() {}

func (t *vncTab) Pinned() bool  { return false }
func (t *vncTab) SoIcone() bool { return false }

func (t *vncTab) Close() {
	t.closeOnce.Do(func() { close(t.stop) })
}

func (t *vncTab) manageSession(user, pass string) {
	attempt := 0

	for {
		sess := vnc.New()
		sess.SetCredentials(user, pass)
		sess.OnUpdate = func(x, y, w, h int) { t.w.Invalidate() }
		sess.OnResize = func(w, h int) { t.w.Invalidate() }
		sess.OnCutText = func(text string) {
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
			ce := err.(*vnc.ConnectError)
			fmt.Fprintf(os.Stderr, "[%s] falha: %s (auth=%v precisa_usuario=%v recusado=%v)\n",
				t.title, ce.Message, ce.AuthFailed, ce.NeedsUsername, ce.Rejected)
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
			// Reconectar a mão: derruba a sessão e recomeça JÁ, sem
			// espera — quem clicou está olhando a tela.
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
			continue
		}

		if !t.auto.Load() {
			// Reconexão automática desligada: fica parada até alguém
			// clicar em Reconectar.
			select {
			case <-t.religar:
				attempt = 0
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
	}
}

// OnLocalClipboard implementa clipboardReceiver: o clipboard do sistema
// mudou, manda pro servidor VNC se for a aba ativa (main.go só chama isto
// pra aba ativa no momento).
func (t *vncTab) OnLocalClipboard(text string) {
	if !t.clipOn.Load() {
		return
	}
	if !t.clip.checkAndSet(text) {
		return
	}
	if sess := t.sess.Load(); sess != nil {
		sess.SendCutText(text)
	}
}

func (t *vncTab) Layout(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	if depurarLayout {
		fmt.Printf("[dbg vnc] max=%v min=%v\n", gtx.Constraints.Max, gtx.Constraints.Min)
	}
	sess := t.sess.Load()

	var buf []byte
	var fw, fh int
	if sess != nil {
		buf, fw, fh = sess.Framebuffer()
	}
	t.fw.Store(int32(fw))
	t.fh.Store(int32(fh))
	v := computeVNCView(size, fw, fh, t.modo.Load())
	t.viewMu.Lock()
	t.view = v
	t.viewMu.Unlock()

	// Clipa à própria área: sem isto, o preenchimento preto de fundo
	// (sem clip nenhum) pinta por cima de tudo que já foi desenhado no
	// frame, inclusive a barra de abas logo acima.
	area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
	defer area.Pop()

	paint.ColorOp{Color: color.NRGBA{A: 255}}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)

	if len(buf) > 0 {
		img := bgrxToNRGBA(buf, fw, fh)
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

func (t *vncTab) HandlePointer(ev pointer.Event, _ image.Point) {
	sess := t.sess.Load()
	// Somente leitura: nada de ponteiro nem de teclado sai daqui. É o
	// modo de acompanhar o operador da loja sem esbarrar no que ele está
	// fazendo — e o clique acidental num PDV em venda custa caro.
	if sess == nil || t.ronly.Load() {
		return
	}
	t.viewMu.Lock()
	v := t.view
	t.viewMu.Unlock()
	if v.scale == 0 {
		return
	}
	x := (ev.Position.X - v.offX) / v.scale
	y := (ev.Position.Y - v.offY) / v.scale

	buttons := 0
	if ev.Buttons.Contain(pointer.ButtonPrimary) {
		buttons |= 1 << 0
	}
	if ev.Buttons.Contain(pointer.ButtonSecondary) {
		buttons |= 1 << 2
	}
	if ev.Buttons.Contain(pointer.ButtonTertiary) {
		buttons |= 1 << 1
	}
	sess.PointerEvent(int(x), int(y), buttons)
}

func (t *vncTab) HandleKey(keysym, _ uint32, pressed bool) {
	if t.ronly.Load() {
		return
	}
	if sess := t.sess.Load(); sess != nil {
		sess.KeyEvent(keysym, pressed)
	}
}

// bgrxToNRGBA converte o framebuffer do shim (32bpp, bytes B,G,R,X) para
// image.NRGBA, que é o que paint.NewImageOp espera.
func bgrxToNRGBA(buf []byte, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		b := buf[i*4+0]
		g := buf[i*4+1]
		r := buf[i*4+2]
		img.Pix[i*4+0] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = 255
	}
	return img
}

// ------------------------------------------------ barra de sessão

func (t *vncTab) EstadoSessao() estadoSessao {
	e := estadoSessao{
		Chip: "AGUARDE", Tipo: "neutro",
		Texto: fmt.Sprintf("%s:%d", t.host, t.port),
	}
	switch {
	case t.sess.Load() != nil && t.ronly.Load():
		e.Chip, e.Tipo = "SÓ VER", "atencao"
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

func (t *vncTab) ControlesSessao(gtx layout.Context, th *material.Theme) layout.Dimensions {
	for i := range t.btnModo {
		if t.btnModo[i].Clicked(gtx) {
			t.modo.Store(int32(i))
			gravarPreferencia(t.nomeConexao, "modo", []string{"encaixar", "1x1"}[i])
		}
	}
	if t.btnTeclas.Clicked(gtx) {
		menuTeclas(ultimaPosPonteiro(), t.ronly.Load(), func(ks uint32, pressionada bool) {
			if sess := t.sess.Load(); sess != nil {
				sess.KeyEvent(ks, pressionada)
			}
		})
	}
	if t.btnOlho.Clicked(gtx) {
		t.ronly.Store(!t.ronly.Load())
		gravarPreferencia(t.nomeConexao, "ronly", simNao(t.ronly.Load()))
	}
	if t.btnRec.Clicked(gtx) {
		t.Reconectar()
	}
	if t.btnAuto.Clicked(gtx) {
		t.auto.Store(!t.auto.Load())
		gravarPreferencia(t.nomeConexao, "auto", simNao(t.auto.Load()))
	}
	if t.btnClip.Clicked(gtx) {
		t.clipOn.Store(!t.clipOn.Load())
	}

	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return segmentado(gtx, th,
				[]*widget.Clickable{&t.btnModo[0], &t.btnModo[1]},
				rotulosModo, int(t.modo.Load()))
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// olho: fechado = somente leitura. Vermelho não, porque não
			// destrói nada; é estado, e estado usa a cor do assunto.
			ic := icons.ActionVisibility
			cor := tema.Verde
			if t.ronly.Load() {
				ic, cor = icons.ActionVisibilityOff, tema.AtencaoFg
			}
			return toggleSessao(gtx, th, &t.btnOlho, ic, true, cor)
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
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

// Reconectar derruba a sessão viva e recomeça na hora. Sem efeito se a aba
// já está fechando.
func (t *vncTab) Reconectar() {
	select {
	case t.religar <- struct{}{}:
	default: // já há um pedido na fila
	}
}
