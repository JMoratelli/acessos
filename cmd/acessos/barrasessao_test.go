package main

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

type sessaoFalsa struct{}

func (sessaoFalsa) EstadoSessao() estadoSessao {
	return estadoSessao{Chip: "ATIVO", Tipo: "ok",
		Texto: "jurandir@192.168.8.1:3389", Geo: "1920x1080"}
}
func (sessaoFalsa) ControlesSessao(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Dimensions{Size: image.Pt(gtx.Dp(120), gtx.Dp(18))}
}

func gtxDeTeste(larg int) layout.Context {
	return layout.Context{
		Ops:         new(op.Ops),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(larg, 26)),
	}
}

// Os botões de JANELA da barrinha (minimizar, maximizar, fechar) não
// apareciam na tela. Este teste mede cada extra: um widget que é chamado
// mas devolve tamanho zero é invisível, e era isso que precisava ser
// distinguido de "nem foi chamado".
func TestExtrasDaBarraRecebemEspaco(t *testing.T) {
	th := material.NewTheme()
	th.Shaper = shaperDoApp()

	type medida struct {
		chamado bool
		maxX    int
		dims    layout.Dimensions
	}
	medidas := make([]medida, 3)
	extras := make([]layout.Widget, 3)
	for i := range extras {
		i := i
		extras[i] = func(gtx layout.Context) layout.Dimensions {
			medidas[i].chamado = true
			medidas[i].maxX = gtx.Constraints.Max.X
			d := layout.Dimensions{Size: image.Pt(24, 24)}
			medidas[i].dims = d
			return d
		}
	}

	layoutBarraSessao(gtxDeTeste(1920), th, sessaoFalsa{}, extras...)

	for i, m := range medidas {
		if !m.chamado {
			t.Errorf("extra %d não foi desenhado", i)
			continue
		}
		if m.maxX <= 0 {
			t.Errorf("extra %d foi desenhado com largura máxima %d — invisível", i, m.maxX)
		}
	}
}

// Numa barra ESTREITA o texto do destino não pode empurrar os botões para
// fora: eles são o canto que o operador procura para fechar.
func TestExtrasSobrevivemABarraEstreita(t *testing.T) {
	th := material.NewTheme()
	th.Shaper = shaperDoApp()

	var maxX int
	extra := func(gtx layout.Context) layout.Dimensions {
		maxX = gtx.Constraints.Max.X
		return layout.Dimensions{Size: image.Pt(24, 24)}
	}
	layoutBarraSessao(gtxDeTeste(300), th, sessaoFalsa{}, extra)
	if maxX <= 0 {
		t.Errorf("com a barra em 300px o extra ficou com largura %d", maxX)
	}
}
