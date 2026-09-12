package main

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Menu de contexto (botão direito). Um por vez, como o modal: a janela é
// uma só e dois menus abertos ao mesmo tempo não significariam nada.
type itemMenu struct {
	rotulo string
	perigo bool // pinta de erro: só para ação que destrói
	acao   func()
	btn    widget.Clickable
}

type menuContexto struct {
	pos   image.Point
	itens []*itemMenu
}

var menuAtual *menuContexto

func abrirMenu(pos image.Point, itens []*itemMenu) { menuAtual = &menuContexto{pos: pos, itens: itens} }
func fecharMenu()                                  { menuAtual = nil }
func temMenu() bool                                { return menuAtual != nil }

var tagMenuFora = new(int)

const menuLargura = unit.Dp(190)

// layoutMenu desenha o menu ancorado na posição do clique. Clique fora
// fecha — menu que só fecha escolhendo algo prende quem abriu por engano.
func layoutMenu(gtx layout.Context, th *material.Theme) layout.Dimensions {
	m := menuAtual
	if m == nil {
		return layout.Dimensions{}
	}
	tela := gtx.Constraints.Max

	// captura de clique fora, ANTES do menu (fica por baixo dele)
	area := clip.Rect{Max: tela}.Push(gtx.Ops)
	event.Op(gtx.Ops, tagMenuFora)
	area.Pop()
	for {
		ev, ok := gtx.Source.Event(pointer.Filter{Target: tagMenuFora, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, isP := ev.(pointer.Event); isP {
			fecharMenu()
			return layout.Dimensions{}
		}
	}

	for _, it := range m.itens {
		if it.btn.Clicked(gtx) {
			acao := it.acao
			fecharMenu()
			if acao != nil {
				acao()
			}
			return layout.Dimensions{}
		}
	}

	larg := gtx.Dp(menuLargura)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
	gtx.Constraints.Min.Y = 0

	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		var filhos []layout.FlexChild
		for _, it := range m.itens {
			it := it
			filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return it.btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Background{}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							if it.btn.Hovered() {
								superficie(gtx, gtx.Constraints.Min, tema.Hover, transparente, 0)
							}
							return layout.Dimensions{Size: gtx.Constraints.Min}
						},
						func(gtx layout.Context) layout.Dimensions {
							cor := tema.Texto
							if it.perigo {
								cor = tema.ErroFg
							}
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return layout.Inset{Top: 7, Bottom: 7, Left: 12, Right: 12}.Layout(gtx,
								txt(th, fonteSans, spCorpo, it.rotulo, cor).Layout)
						},
					)
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
	})
	conteudo := macro.Stop()

	// 4px de folga: nascendo exatamente sob o cursor, o primeiro item
	// ficava no ponto do clique e um segundo clique acidental o
	// acionava — e o primeiro item costuma ser "abrir alguma coisa".
	x, y := m.pos.X+4, m.pos.Y+4
	if x+dims.Size.X > tela.X {
		x = tela.X - dims.Size.X
	}
	if y+dims.Size.Y > tela.Y {
		y = tela.Y - dims.Size.Y
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	defer op.Offset(image.Pt(x, y)).Push(gtx.Ops).Pop()
	sombra(gtx, dims.Size, 10)
	superficie(gtx, dims.Size, tema.Cartao, tema.Borda, 10)
	conteudo.Add(gtx.Ops)
	return dims
}
