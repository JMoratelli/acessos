package main

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgSaida mostra o retorno COMPLETO de uma máquina. A lista de execução
// só cabe um resumo de uma linha; quando algo dá errado, o que resolve é a
// saída inteira, em monoespaçada (senão `df -h` sai desalinhado).
type dlgSaida struct {
	titulo   string
	texto    string
	lista    widget.List
	rolagemH widget.List
	fechar   widget.Clickable
	sel      widget.Selectable
}

func (d *dlgSaida) Titulo() string   { return d.titulo }
func (d *dlgSaida) Largura() unit.Dp { return 760 }

func (d *dlgSaida) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if d.fechar.Clicked(gtx) {
		fecharDialogo()
	}
	d.lista.Axis = layout.Vertical

	// teto de altura: o diálogo não pode crescer além da janela quando a
	// saída tem centenas de linhas.
	max := gtx.Constraints.Max.Y * 6 / 10
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = max
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					superficie(gtx, gtx.Constraints.Min, tema.TermBg, tema.Borda2, 8)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				},
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 8, Bottom: 8, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						// Duas rolagens: vertical pelas linhas e HORIZONTAL
						// pelo comprimento. Saída de comando não pode ser
						// quebrada — um `df -h` quebrado deixa de estar em
						// colunas e vira texto embaralhado.
						d.rolagemH.Axis = layout.Horizontal
						return material.List(th, &d.lista).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
							return material.List(th, &d.rolagemH).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
								gtx.Constraints.Max.X = 1 << 20
								gtx.Constraints.Min.X = 0
								// texto SELECIONÁVEL: copiar o erro para
								// colar num chamado é metade do uso desta tela.
								l := material.Label(th, spCorpo, d.texto)
								l.Font = fonteMono
								l.Color = tema.TermFg
								l.State = &d.sel
								return l.Layout(gtx)
							})
						})
					})
				},
			)
		}),
		espaco(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.fechar, "Fechar")
				}),
			)
		}),
	)
}
