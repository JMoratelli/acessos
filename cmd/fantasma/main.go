//go:build linux

// Reprodução isolada da "janela fantasma": abre e fecha uma segunda janela
// várias vezes e conta os descritores do processo entre uma e outra.
//
// Cada janela do Gio abre a PRÓPRIA conexão Wayland (newWLWindow chama
// newWLDisplay), ou seja um socket. Se o número não voltar ao que era
// depois de fechar, a janela não morreu — e é ela que fica na barra de
// tarefas sem ninguém desenhando.
//
// -grab liga o mesmo ciclo de captura que a janela de sessão faz, para
// separar "o Gio não fecha" de "a captura segura".
package main

import (
	"flag"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"acessos-go/internal/grab"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
)

func fds() int {
	f, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(f)
}

// alvos lista para onde cada descritor aponta, para saber QUEM vazou.
func alvos() map[string]int {
	m := map[string]int{}
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return m
	}
	for _, e := range ents {
		alvo, err := os.Readlink("/proc/self/fd/" + e.Name())
		if err != nil {
			continue
		}
		m[alvo]++
	}
	return m
}

func main() {
	comGrab := flag.Bool("grab", false, "arma e solta a captura, como a janela de sessão")
	voltas := flag.Int("voltas", 4, "quantas vezes abrir e fechar")
	flag.Parse()

	go func() {
		// janela "principal", que fica de pé o tempo todo
		principal := new(app.Window)
		principal.Option(app.Title("fantasma-principal"), app.Size(400, 300))
		vivo := make(chan struct{})
		go func() {
			var ops op.Ops
			for {
				switch e := principal.Event().(type) {
				case app.DestroyEvent:
					close(vivo)
					return
				case app.FrameEvent:
					gtx := app.NewContext(&ops, e)
					e.Frame(gtx.Ops)
				}
			}
		}()
		time.Sleep(800 * time.Millisecond)

		base := fds()
		baseAlvos := alvos()
		fmt.Printf("descritores com só a principal: %d\n", base)

		for v := 1; v <= *voltas; v++ {
			w := new(app.Window)
			w.Option(app.Title(fmt.Sprintf("fantasma-%d", v)), app.Size(320, 240))
			fim := make(chan struct{})
			var g atomic.Pointer[grab.Handle]
			go func() {
				var ops op.Ops
				quadros := 0
				for {
					e := w.Event()
					switch e := e.(type) {
					case app.DestroyEvent:
						if *comGrab {
							g.Load().Liberar()
						}
						close(fim)
						return
					case app.ViewEvent:
						ev, ok := e.(app.WaylandViewEvent)
						if *comGrab && ok && ev.Valid() && g.Load() == nil {
							if h := grab.Start(ev.Display, ev.Surface, nil, nil); h != nil {
								g.Store(h)
							}
						}
					case app.FrameEvent:
						gtx := app.NewContext(&ops, e)
						quadros++
						if quadros == 5 {
							if *comGrab {
								if h := g.Load(); h != nil {
									h.Inibir(false)
									h.Stop()
									g.Store(nil)
								}
							}
							w.Perform(system.ActionClose)
						}
						e.Frame(gtx.Ops)
						w.Invalidate()
					}
				}
			}()
			<-fim
			time.Sleep(500 * time.Millisecond)
			fmt.Printf("volta %d: descritores=%d (base %d, sobra %+d)\n",
				v, fds(), base, fds()-base)
			if v == *voltas {
				fmt.Println("  o que sobrou a mais que no começo:")
				for alvo, n := range alvos() {
					if b := baseAlvos[alvo]; n > b {
						fmt.Printf("    %-50s %d (era %d)\n", alvo, n, b)
					}
				}
			}
		}

		principal.Perform(system.ActionClose)
		<-vivo
		fmt.Println("fim")
		os.Exit(0)
	}()
	app.Main()
}
