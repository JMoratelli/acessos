// rdpview é uma janela mínima que mostra uma sessão RDP ao vivo, usando o
// binding internal/rdp (cgo sobre libfreerdp3), Gio para desenho, e
// internal/grab para teclado e captura de atalhos no Wayland — mesma
// arquitetura do cmd/vncview.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"sync/atomic"
	"time"

	"acessos-go/internal/grab"
	"acessos-go/internal/rdp"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// backoffSchedule espelha o ESPERAS do app original (acessos.py).
var backoffSchedule = []time.Duration{
	3 * time.Second, 5 * time.Second, 8 * time.Second,
	13 * time.Second, 21 * time.Second, 30 * time.Second,
}

// currentSession é a sessão RDP em uso agora — trocada a cada reconexão.
var currentSession atomic.Pointer[rdp.Session]

// currentGrab é a captura Wayland ativa (teclado + clipboard + atalhos).
var currentGrab atomic.Pointer[grab.Handle]

func main() {
	host := flag.String("host", "", "endereço do servidor RDP")
	port := flag.Int("port", 3389, "porta")
	user := flag.String("user", "", "usuário")
	pass := flag.String("pass", "", "senha")
	domain := flag.String("domain", "", "domínio (opcional)")
	flag.Parse()

	if *host == "" {
		fmt.Fprintln(os.Stderr, "uso: rdpview -host <ip> [-port 3389] -user <u> -pass <senha> [-domain <d>]")
		os.Exit(2)
	}

	w := new(app.Window)
	w.Option(app.Title("acessos-go — " + *host))

	stop := make(chan struct{})
	go manageSession(w, stop, *host, *port, *user, *pass, *domain)

	go func() {
		if err := loop(w); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		close(stop)
		os.Exit(0)
	}()

	app.Main()
}

// manageSession mantém a conexão viva com o mesmo backoff do vncview:
// reconecta sozinho só depois de uma queda pós-conexão, nunca insiste
// sozinho numa falha de conexão inicial (credencial errada ou host fora
// não se resolvem tentando de novo sem intervenção).
func manageSession(w *app.Window, stop <-chan struct{}, host string, port int, user, pass, domain string) {
	attempt := 0
	firstResize := true

	for {
		sess := rdp.New()
		sess.SetCredentials(user, pass, domain)
		sess.OnUpdate = func(x, y, wid, h int) { w.Invalidate() }
		sess.OnResize = func(wid, h int) { w.Invalidate() }
		sess.OnClipboardText = func(text string) {
			clipSync.fromRemote(text, currentGrab.Load())
		}

		fmt.Printf("conectando a %s:%d...\n", host, port)
		if err := sess.Connect(host, port); err != nil {
			ce := err.(*rdp.ConnectError)
			fmt.Fprintf(os.Stderr, "falha: %s (auth=%v)\n", ce.Message, ce.AuthFailed)
			sess.Close()
			return
		}
		fmt.Println("conectado.")
		attempt = 0

		if firstResize {
			if _, fw, fh, _ := sess.Framebuffer(); fw > 0 && fh > 0 {
				w.Option(app.Size(unit.Dp(fw), unit.Dp(fh)))
			}
			firstResize = false
		}

		currentSession.Store(sess)
		runStop := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- sess.Run(runStop) }()

		select {
		case <-stop:
			close(runStop)
			<-done
			sess.Close()
			return
		case err := <-done:
			currentSession.Store(nil)
			sess.Close()
			fmt.Fprintf(os.Stderr, "sessão caiu: %v\n", err)
			w.Invalidate()
		}

		wait := backoffSchedule[min(attempt, len(backoffSchedule)-1)]
		attempt++
		fmt.Printf("reconectando em %s (tentativa #%d)\n", wait, attempt)
		select {
		case <-time.After(wait):
		case <-stop:
			return
		}
	}
}

// bgrxToImage converte o framebuffer do shim (32bpp BGRX, PIXEL_FORMAT_BGRX32)
// para image.NRGBA. Ao contrário do VNC, aqui o gdi pode alinhar cada linha
// além de w*4 bytes — por isso stride precisa ser respeitado por linha, em
// vez de tratar o buffer como um bloco contíguo w*h*4.
func bgrxToImage(buf []byte, w, h, stride int) *image.NRGBA {
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

var inputTag = new(int)

// view descreve como o framebuffer remoto (fw x fh) está mapeado dentro da
// janela atual (escala + letterbox), para converter coordenadas de mouse.
type view struct {
	scale      float32
	offX, offY float32
}

func computeView(winSize image.Point, fw, fh int) view {
	if fw == 0 || fh == 0 {
		return view{scale: 1}
	}
	sx := float32(winSize.X) / float32(fw)
	sy := float32(winSize.Y) / float32(fh)
	scale := sx
	if sy < scale {
		scale = sy
	}
	ow := float32(fw) * scale
	oh := float32(fh) * scale
	return view{
		scale: scale,
		offX:  (float32(winSize.X) - ow) / 2,
		offY:  (float32(winSize.Y) - oh) / 2,
	}
}

func loop(w *app.Window) error {
	var ops op.Ops
	var lastSizeRequested image.Point

	for {
		e := w.Event()
		switch e := e.(type) {
		case app.DestroyEvent:
			// NÃO chamar kbGrab.Stop() aqui: mesma razão do vncview — a
			// esta altura o Gio já pode estar desmontando a própria
			// conexão Wayland, e soltar nosso inibidor/wl_keyboard contra
			// um wl_display já encerrado derrubava o processo (visto na
			// prática). O processo sai logo em seguida de qualquer forma.
			return e.Err

		case app.WaylandViewEvent:
			// SÓ cria o grab uma vez, na primeira view válida — nunca
			// recria nem para (Stop) num evento posterior: o Gio manda um
			// WaylandViewEvent{} (zero-value) durante o fechamento da
			// janela, e mesmo uma segunda view VÁLIDA pode aparecer em
			// operação normal (ex.: chamadas a app.Size()) — parar/
			// recriar o grab nesses casos corre contra a leitura Wayland
			// que o próprio Gio faz internamente e derrubava o processo.
			if e.Valid() && currentGrab.Load() == nil {
				gh := grab.Start(e.Display, e.Surface,
					func(_, keycodeX11 uint32, pressed bool) {
						if sess := currentSession.Load(); sess != nil {
							sess.KeyEvent(keycodeX11, pressed)
						}
					},
					func(text string) {
						clipSync.fromLocal(text, currentSession.Load())
					},
				)
				currentGrab.Store(gh)
				switch {
				case gh != nil:
					fmt.Println("teclado + clipboard via wayland direto + captura de atalhos do compositor ativa")
				default:
					fmt.Println("wl_seat indisponível — sem teclado, clipboard nem captura de atalhos")
				}
			}

		case app.FrameEvent:
			ops.Reset()
			sess := currentSession.Load()

			area := clip.Rect(image.Rectangle{Max: e.Size}).Push(&ops)
			event.Op(&ops, inputTag)
			area.Pop()

			var buf []byte
			var fw, fh, stride int
			if sess != nil {
				buf, fw, fh, stride = sess.Framebuffer()
			}
			v := computeView(e.Size, fw, fh)

			// Resolução dinâmica: pede pro servidor redimensionar a área
			// remota para acompanhar o tamanho da janela local (canal
			// Display Control). Só reenvia quando o tamanho muda — sem
			// isto, um redimensionamento arrastado (que gera muitos
			// FrameEvent com tamanhos diferentes) inundaria o canal.
			if sess != nil && e.Size != lastSizeRequested {
				sess.RequestResize(e.Size.X, e.Size.Y)
				lastSizeRequested = e.Size
			}

			// Teclado e clipboard não passam por aqui: vêm direto do
			// Wayland (ver internal/grab) — mesmo motivo do vncview.
			for {
				ev, ok := e.Source.Event(
					pointer.Filter{Target: inputTag, Kinds: pointer.Press | pointer.Release | pointer.Move | pointer.Drag},
				)
				if !ok {
					break
				}
				if sess == nil {
					continue
				}
				if ev, ok := ev.(pointer.Event); ok {
					handlePointer(sess, ev, v)
				}
			}

			// Fundo preto: sobra letterbox quando a proporção da janela
			// não bate com a da tela remota.
			paint.ColorOp{Color: color.NRGBA{A: 255}}.Add(&ops)
			paint.PaintOp{}.Add(&ops)

			if len(buf) > 0 {
				img := bgrxToImage(buf, fw, fh, stride)
				t := op.Affine(f32.Affine2D{}.
					Scale(f32.Point{}, f32.Point{X: v.scale, Y: v.scale}).
					Offset(f32.Point{X: v.offX, Y: v.offY}),
				).Push(&ops)
				imgOp := paint.NewImageOp(img)
				imgOp.Add(&ops)
				paint.PaintOp{}.Add(&ops)
				t.Pop()
			}

			e.Frame(&ops)
		}
	}
}

// lastButtons rastreia o estado anterior para descobrir qual botão mudou —
// a API do RDP pede press/release POR BOTÃO, diferente do bitmask único
// que o VNC aceitava de uma vez.
var lastButtons pointer.Buttons

func handlePointer(sess *rdp.Session, ev pointer.Event, v view) {
	if v.scale == 0 {
		return
	}
	x := int((ev.Position.X - v.offX) / v.scale)
	y := int((ev.Position.Y - v.offY) / v.scale)

	sess.PointerMove(x, y)

	changed := ev.Buttons ^ lastButtons
	if changed&pointer.ButtonPrimary != 0 {
		sess.PointerButton(x, y, 1, ev.Buttons&pointer.ButtonPrimary != 0)
	}
	if changed&pointer.ButtonTertiary != 0 {
		sess.PointerButton(x, y, 2, ev.Buttons&pointer.ButtonTertiary != 0)
	}
	if changed&pointer.ButtonSecondary != 0 {
		sess.PointerButton(x, y, 3, ev.Buttons&pointer.ButtonSecondary != 0)
	}
	lastButtons = ev.Buttons
}
