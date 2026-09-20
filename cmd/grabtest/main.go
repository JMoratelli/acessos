//go:build linux

// grabtest é verificação DE CAMPO do ciclo de vida da captura, contra o
// Wayland de verdade. Roda à mão: abre duas janelinhas, faz o que tem de
// fazer e sai com código 0 se sobreviveu.
//
// Existe porque o teste unitário de internal/grab não alcança nada disto —
// não há wl_display num `go test`, e os dois caminhos abaixo só quebram
// com um de verdade. O do Liberar(), em especial, era use-after-free e
// derrubava o PROCESSO INTEIRO, não só a janela: descobrir isso em
// produção custaria as sessões abertas de quem estivesse trabalhando.
//
// Exercita exatamente os dois caminhos novos:
//
//	A) duas capturas SIMULTÂNEAS, uma por janela, cada uma com os
//	   próprios callbacks. Era impossível antes: o pacote guardava os
//	   callbacks em variáveis globais e a segunda janela roubava a
//	   primeira em silêncio.
//	B) o desmonte. Stop() com o display VIVO (quem fecha por decisão
//	   própria) e Liberar() depois do DestroyEvent (quem só descobriu
//	   depois — o display daquela janela já caiu).
package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"acessos-go/internal/grab"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
)

type janela struct {
	nome     string
	w        *app.Window
	g        atomic.Pointer[grab.Handle]
	teclas   atomic.Int64
	quadros  int
	pararCom string // "stop" (display vivo) ou "liberar" (depois do destroy)
	pronto   chan struct{}
}

func (j *janela) rodar(falhas *atomic.Int64) {
	defer close(j.pronto)
	var ops op.Ops
	for {
		e := j.w.Event()
		switch e := e.(type) {
		case app.DestroyEvent:
			if j.pararCom == "liberar" {
				// A conexão desta janela JÁ CAIU aqui. Liberar não fala
				// Wayland; se ele tocasse em wl_proxy, seria
				// use-after-free e o processo morreria agora.
				j.g.Load().Liberar()
				fmt.Printf("  [%s] Liberar() depois do DestroyEvent: sobreviveu\n", j.nome)
			}
			return

		case app.ViewEvent:
			ev, ok := e.(app.WaylandViewEvent)
			if !ok || !ev.Valid() || j.g.Load() != nil {
				continue
			}
			h := grab.Start(ev.Display, ev.Surface,
				func(ks, kc uint32, pressed bool) { j.teclas.Add(1) },
				func(string) {})
			if h == nil {
				fmt.Printf("  [%s] grab.Start devolveu nil\n", j.nome)
				falhas.Add(1)
				continue
			}
			j.g.Store(h)
			fmt.Printf("  [%s] captura armada\n", j.nome)

		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			j.quadros++
			if j.quadros == 3 {
				if g := j.g.Load(); g != nil {
					g.Inibir(true)
					fmt.Printf("  [%s] Inibir(true): ok (modificadores=%d)\n",
						j.nome, g.Modificadores())
				}
			}
			if j.quadros == 8 {
				if j.pararCom == "stop" {
					g := j.g.Load()
					g.Inibir(false)
					g.Stop()
					g.Stop() // idempotente
					fmt.Printf("  [%s] Inibir(false)+Stop()x2 com display vivo: sobreviveu\n", j.nome)
				}
				go func() { j.w.Perform(system.ActionClose) }()
			}
			e.Frame(gtx.Ops)
			j.w.Invalidate()
		}
	}
}

func main() {
	var falhas atomic.Int64
	go func() {
		defer func() {
			if falhas.Load() == 0 {
				fmt.Println("RESULTADO: OK")
			} else {
				fmt.Printf("RESULTADO: %d FALHA(S)\n", falhas.Load())
			}
			os.Exit(int(falhas.Load()))
		}()

		fmt.Println(">> duas janelas com captura SIMULTÂNEA")
		a := &janela{nome: "A", pararCom: "stop", pronto: make(chan struct{})}
		b := &janela{nome: "B", pararCom: "liberar", pronto: make(chan struct{})}
		for _, j := range []*janela{a, b} {
			j.w = new(app.Window)
			j.w.Option(app.Title("grabtest "+j.nome), app.Size(320, 200))
			go j.rodar(&falhas)
			time.Sleep(400 * time.Millisecond)
		}
		<-a.pronto
		<-b.pronto
		fmt.Println(">> as duas janelas fecharam sem derrubar o processo")
	}()
	app.Main()
}
