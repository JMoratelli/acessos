package main

import (
	"image"
	"testing"

	"acessos-go/internal/conexoes"

	"gioui.org/f32"
	"image/color"

	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// O menu de contexto do Painel já falhou duas vezes por causa do
// roteamento de ponteiro do Gio: uma área registrada POR BAIXO dos widgets
// nunca recebe evento, porque ao encontrar uma área sem PassOp o hit-test
// salta para o nó pai e ignora as irmãs anteriores.
//
// Este teste trava o mecanismo que ficou: UMA área, por cima de tudo, com
// PassOp — ela recebe o movimento (âncora do menu) e o clique com o botão
// direito, sem tirar o clique normal de quem está embaixo.
func TestAreaDeRastreioRecebeMoveESecundario(t *testing.T) {
	d := &dashTab{tagPainel: new(int)}
	var r input.Router
	var ops op.Ops

	quadro := func(evs ...pointer.Event) {
		ops.Reset()
		gtx := layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(400, 400)),
			Source:      r.Source(),
		}
		d.rastrearPonteiro(gtx)
		r.Frame(gtx.Ops)
		for _, e := range evs {
			r.Queue(e)
		}
	}

	quadro()
	quadro(pointer.Event{Kind: pointer.Move, Position: f32.Pt(120, 130), Source: pointer.Mouse})
	quadro()
	if d.ultimaPos != image.Pt(120, 130) {
		t.Fatalf("posição do ponteiro não foi registrada: %v", d.ultimaPos)
	}

	var pediuMenu bool
	cx := conexoes.Conexao{Nome: "CAIXA101"}
	d.sobCursor = &cx
	d.aoMenuCard = func(_ conexoes.Conexao, pos image.Point) {
		pediuMenu = true
		if pos != image.Pt(200, 210) {
			t.Errorf("menu ancorado em %v, esperava (200,210)", pos)
		}
	}
	quadro(pointer.Event{
		Kind: pointer.Press, Position: f32.Pt(200, 210), Source: pointer.Mouse,
		Buttons: pointer.ButtonSecondary,
	})
	quadro()
	if !pediuMenu {
		t.Fatal("clique com o botão direito não pediu o menu de contexto")
	}
}

// Roda de alta definição manda dezenas de deltas por volta. Uma rodada
// rápida tem de andar UMA aba, não a tira inteira.
func TestRolagemDeAltaResolucaoAndaUmaAba(t *testing.T) {
	bar := newTabBar([]Tab{&abaFalsa{}, &abaFalsa{}, &abaFalsa{}, &abaFalsa{}, &abaFalsa{}})
	var r input.Router
	var ops op.Ops
	th := material.NewTheme()
	th.Shaper = shaperDoApp()
	tema = temaClaro

	quadro := func(evs ...pointer.Event) {
		ops.Reset()
		gtx := layout.Context{
			Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(800, 40)), Source: r.Source(),
		}
		bar.layout(gtx, th)
		r.Frame(gtx.Ops)
		for _, e := range evs {
			r.Queue(e)
		}
	}

	quadro()
	// 30 deltas pequenos, como um mouse de alta resolução manda numa volta
	for i := 0; i < 30; i++ {
		quadro(pointer.Event{
			Kind: pointer.Scroll, Position: f32.Pt(100, 20),
			Source: pointer.Mouse, Scroll: f32.Pt(0, 5),
		})
	}
	quadro()
	if bar.idx != 1 {
		t.Fatalf("uma rodada andou %d abas — devia andar 1", bar.idx)
	}
}

// abaFalsa é o mínimo para a tira desenhar no teste.
type abaFalsa struct{}

func (a *abaFalsa) Title() string { return "aba" }
func (a *abaFalsa) SoIcone() bool { return false }
func (a *abaFalsa) Pinned() bool  { return false }
func (a *abaFalsa) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	return nil, color.NRGBA{}, color.NRGBA{}
}
func (a *abaFalsa) Layout(gtx layout.Context) layout.Dimensions  { return layout.Dimensions{} }
func (a *abaFalsa) HandleKey(_, _ uint32, _ bool)                {}
func (a *abaFalsa) HandlePointer(_ pointer.Event, _ image.Point) {}
func (a *abaFalsa) Close()                                       {}
