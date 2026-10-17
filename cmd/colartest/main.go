//go:build linux

// colartest: lê o clipboard pelo MESMO caminho que o widget.Editor usa ao
// colar (clipboard.ReadCmd → transfer.DataEvent), com o grab do app
// ligado na mesma janela, e imprime o que chega. Ferramenta de campo,
// como grabtest: não entra no app.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"acessos-go/internal/grab"

	"gioui.org/app"
	"gioui.org/io/clipboard"
	"gioui.org/io/transfer"
	"gioui.org/op"
	"gioui.org/unit"
)

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("teste de colar"), app.Size(unit.Dp(300), unit.Dp(120)))
		var ops op.Ops
		tag := new(int)
		inicio := time.Now()
		ultimo := time.Time{}
		var g *grab.Handle
		for {
			switch e := w.Event().(type) {
			case app.DestroyEvent:
				os.Exit(0)
			case app.ViewEvent:
				ev, ok := e.(app.WaylandViewEvent)
				if ok && ev.Valid() && g == nil {
					g = grab.Start(ev.Display, ev.Surface,
						func(ks, kc uint32, pressed bool) {},
						func(s string) { fmt.Printf("%5.1fs [grab] clipboard mudou: %q\n", time.Since(inicio).Seconds(), s) })
					fmt.Printf("%5.1fs grab armado=%v\n", time.Since(inicio).Seconds(), g != nil)
				}
			case app.FrameEvent:
				gtx := app.NewContext(&ops, e)
				for {
					ev, ok := gtx.Event(transfer.TargetFilter{Target: tag, Type: "application/text"})
					if !ok {
						break
					}
					if de, ok := ev.(transfer.DataEvent); ok {
						r := de.Open()
						b, _ := io.ReadAll(r)
						r.Close()
						fmt.Printf("%5.1fs [gio] ReadCmd entregou: %q\n", time.Since(inicio).Seconds(), string(b))
					}
				}
				if time.Since(ultimo) > 1500*time.Millisecond {
					ultimo = time.Now()
					fmt.Printf("%5.1fs [gio] pedindo clipboard\n", time.Since(inicio).Seconds())
					gtx.Execute(clipboard.ReadCmd{Tag: tag})
				}
				if time.Since(inicio) > 28*time.Second {
					os.Exit(0)
				}
				gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(300 * time.Millisecond)})
				e.Frame(gtx.Ops)
			}
		}
	}()
	app.Main()
}
