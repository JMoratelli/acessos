package main

import (
	"fmt"
	"strings"

	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgConflito: arquivos que já existem no destino. As três saídas são as
// do app original — sobrescrever, pular os existentes (os demais vão
// assim mesmo, que é o "merge") ou cancelar tudo. Nenhuma delas é
// destrutiva a ponto de pedir contagem, mas sobrescrever é a arriscada e
// por isso é a que não fica como primária.
type dlgConflito struct {
	w            *app.Window
	nomes        []string
	sobrescrever func()
	pular        func()

	lista     widget.List
	btnSobre  widget.Clickable
	btnPular  widget.Clickable
	btnCancel widget.Clickable
}

func (d *dlgConflito) Titulo() string   { return "Já existe no destino" }
func (d *dlgConflito) Largura() unit.Dp { return 520 }

func (d *dlgConflito) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	d.lista.Axis = layout.Vertical
	switch {
	case d.btnCancel.Clicked(gtx):
		fecharDialogo()
	case d.btnPular.Clicked(gtx):
		fecharDialogo()
		d.pular()
	case d.btnSobre.Clicked(gtx):
		fecharDialogo()
		d.sobrescrever()
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(rotulo(th, fonteMono, spSecundario,
			fmt.Sprintf("%d arquivo(s) já existem no destino:", len(d.nomes)), tema.Sec)),
		espaco(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = gtx.Constraints.Max.Y / 3
			return material.List(th, &d.lista).Layout(gtx, len(d.nomes), func(gtx layout.Context, i int) layout.Dimensions {
				return rotuloLinha(th, fonteMono, spCorpo, "• "+d.nomes[i], tema.Texto)(gtx)
			})
		}),
		espaco(14),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnCancel, "Cancelar")
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnSobre, "Sobrescrever")
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoPrimario(gtx, th, &d.btnPular, "Pular os existentes")
				}),
			)
		}),
	)
}

// dlgTexto é o "um campo, duas saídas" do dialogo_ui: validação INLINE sob
// o campo, nunca um segundo diálogo empilhado em cima do primeiro.
type dlgTexto struct {
	w      *app.Window
	titulo string
	dica   string
	valor  widget.Editor
	ok     func(string) error
	erro   string

	btnOk   widget.Clickable
	btnCanc widget.Clickable
}

func (d *dlgTexto) Titulo() string   { return d.titulo }
func (d *dlgTexto) Largura() unit.Dp { return 420 }

func (d *dlgTexto) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	d.valor.SingleLine = true
	d.valor.Submit = true
	confirmar := d.btnOk.Clicked(gtx)
	for {
		ev, ok := d.valor.Update(gtx)
		if !ok {
			break
		}
		if _, sub := ev.(widget.SubmitEvent); sub {
			confirmar = true
		}
	}
	if confirmar {
		if err := d.ok(strings.TrimSpace(d.valor.Text())); err != nil {
			d.erro = err.Error()
		} else {
			fecharDialogo()
		}
	}
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}

	filhos := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return caixaEditor(gtx, th, &d.valor, d.dica, 0)
		}),
	}
	if d.erro != "" {
		filhos = append(filhos, espaco(6),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, d.erro, tema.ErroFg)))
	}
	filhos = append(filhos, espaco(6),
		layout.Rigid(rotulo(th, fonteMono, spCardMeta, "Enter confirma · Esc cancela", tema.Fraco)),
		espaco(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnCanc, "Cancelar")
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoPrimario(gtx, th, &d.btnOk, "Criar")
				}),
			)
		}))
	gtx.Execute(key.FocusCmd{Tag: &d.valor})
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}
