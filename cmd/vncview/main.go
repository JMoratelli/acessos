// vncview é uma janela mínima que mostra uma sessão VNC ao vivo, usando o
// binding internal/vnc (cgo sobre libvncclient) e Gio para desenho/entrada.
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
	"acessos-go/internal/vnc"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// backoffSchedule espelha o ESPERAS do app original (acessos.py): tempos de
// espera entre tentativas automáticas de reconexão, crescente até estabilizar.
var backoffSchedule = []time.Duration{
	3 * time.Second, 5 * time.Second, 8 * time.Second,
	13 * time.Second, 21 * time.Second, 30 * time.Second,
}

// currentSession é a sessão VNC em uso agora — trocada a cada reconexão.
// nil enquanto não há nenhuma (conectando ou entre tentativas).
var currentSession atomic.Pointer[vnc.Session]

// currentGrab é a captura Wayland ativa (teclado + clipboard + atalhos) —
// nil se ainda não chegou o WaylandViewEvent ou se o compositor não é
// Wayland. Lido pelo callback OnCutText (goroutine de rede da sessão).
var currentGrab atomic.Pointer[grab.Handle]

func main() {
	host := flag.String("host", "", "endereço do servidor VNC")
	port := flag.Int("port", 5900, "porta")
	user := flag.String("user", "", "usuário (se o servidor exigir)")
	pass := flag.String("pass", "", "senha")
	flag.Parse()

	if *host == "" {
		fmt.Fprintln(os.Stderr, "uso: vncview -host <ip> [-port 5900] [-user u] -pass <senha>")
		os.Exit(2)
	}

	w := new(app.Window)
	w.Option(app.Title("acessos-go — " + *host))

	stop := make(chan struct{})
	go manageSession(w, stop, *host, *port, *user, *pass)

	go func() {
		if err := loop(w); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		close(stop)
		os.Exit(0)
	}()

	app.Main()
}

// manageSession mantém a conexão viva: conecta, roda até cair, e — se a
// queda aconteceu DEPOIS de conectar (não numa tentativa inicial recusada
// ou de credencial errada, que não adianta repetir) — agenda uma nova
// tentativa com backoff crescente, do jeito que o app original já fazia
// para aguentar redes de loja instáveis.
func manageSession(w *app.Window, stop <-chan struct{}, host string, port int, user, pass string) {
	attempt := 0
	firstResize := true

	for {
		sess := vnc.New()
		sess.SetCredentials(user, pass)
		sess.OnUpdate = func(x, y, wid, h int) { w.Invalidate() }
		sess.OnResize = func(wid, h int) { w.Invalidate() }
		sess.OnCutText = func(text string) {
			clipSync.fromRemote(text, currentGrab.Load())
		}

		fmt.Printf("conectando a %s:%d...\n", host, port)
		if err := sess.Connect(host, port); err != nil {
			ce := err.(*vnc.ConnectError)
			fmt.Fprintf(os.Stderr, "falha: %s (auth=%v precisa_usuario=%v recusado=%v)\n",
				ce.Message, ce.AuthFailed, ce.NeedsUsername, ce.Rejected)
			sess.Close()
			// Recusa (bloqueio tipo UltraVNC) ou credencial errada: repetir
			// sozinho só piora (aumenta o bloqueio) ou nunca vai dar certo.
			return
		}
		fmt.Println("conectado.")
		attempt = 0

		if firstResize {
			if _, fw, fh := sess.Framebuffer(); fw > 0 && fh > 0 {
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

// bgrxToImage converte o framebuffer do shim (32bpp, bytes B,G,R,X) para
// image.NRGBA, que é o que paint.NewImageOp espera.
func bgrxToImage(buf []byte, w, h int) *image.NRGBA {
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

var inputTag = new(int)

// view descreve como o framebuffer remoto (fw x fh) está mapeado dentro da
// janela atual (escala + letterbox), para converter coordenadas de mouse de
// volta ao espaço remoto.
type view struct {
	scale      float32
	offX, offY float32
	fw, fh     int
}

func computeView(winSize image.Point, fw, fh int) view {
	if fw == 0 || fh == 0 {
		return view{scale: 1, fw: fw, fh: fh}
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
		fw:    fw, fh: fh,
	}
}

func loop(w *app.Window) error {
	var ops op.Ops

	for {
		e := w.Event()
		switch e := e.(type) {
		case app.DestroyEvent:
			// NÃO chamar kbGrab.Stop() aqui: a esta altura o Gio já pode
			// estar desmontando a própria conexão Wayland, e soltar nosso
			// inibidor/wl_keyboard contra um wl_display já encerrado
			// derrubava o processo (visto na prática). O processo sai
			// logo em seguida de qualquer forma — o compositor libera
			// tudo sozinho quando o socket fecha.
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
					func(keysym, _ uint32, pressed bool) {
						if sess := currentSession.Load(); sess != nil {
							sess.KeyEvent(keysym, pressed)
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
			var fw, fh int
			if sess != nil {
				buf, fw, fh = sess.Framebuffer()
			}
			v := computeView(e.Size, fw, fh)

			// Teclado e clipboard não passam por aqui: vêm direto do
			// Wayland (ver internal/grab) — o Gio não é confiável nesta
			// pilha nem pra Modifiers de tecla nem pros mime types de
			// clipboard que o KDE oferece (ver comentários no pacote).
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
				img := bgrxToImage(buf, fw, fh)
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

func handlePointer(sess *vnc.Session, ev pointer.Event, v view) {
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
