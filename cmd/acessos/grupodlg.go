package main

import (
	"fmt"

	"acessos-go/internal/conexoes"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgGrupo abre o grupo inteiro de uma vez. Duas coisas o tornam seguro:
// ele DIZ quantas máquinas tem cada protocolo antes de abrir, e nasce com
// "em segundo plano" marcado — abrir 40 abas trocando de aba 40 vezes
// deixa a interface inútil enquanto isso acontece.
type dlgGrupo struct {
	w      *app.Window
	painel *dashTab
	grupo  *conexoes.Grupo
	lista  []conexoes.Conexao

	marca   [4]widget.Clickable
	ligados [4]bool
	fundo   widget.Clickable
	emFundo bool
	btnOk   widget.Clickable
	btnCanc widget.Clickable
}

func abrirGrupo(w *app.Window, painel *dashTab, g *conexoes.Grupo) {
	d := &dlgGrupo{w: w, painel: painel, grupo: g, lista: todasDoGrupo(g), emFundo: true}
	d.ligados[0] = true // tela vem marcada, como no original
	abrirDialogo(d)
}

func (d *dlgGrupo) Titulo() string   { return "Abrir grupo " + d.grupo.Nome }
func (d *dlgGrupo) Largura() unit.Dp { return 440 }

func (d *dlgGrupo) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	for i := range d.marca {
		if d.marca[i].Clicked(gtx) {
			d.ligados[i] = !d.ligados[i]
		}
	}
	if d.fundo.Clicked(gtx) {
		d.emFundo = !d.emFundo
	}
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}
	if d.btnOk.Clicked(gtx) {
		fecharDialogo()
		go d.abrir()
	}

	filhos := []layout.FlexChild{
		layout.Rigid(rotulo(th, fonteMono, spSecundario,
			fmt.Sprintf("%d máquina(s), subgrupos inclusos", len(d.lista)), tema.Sec)),
		espaco(10),
	}
	for i, e := range protocolos {
		i, e := i, e
		n := 0
		for _, cx := range d.lista {
			if cx.Tem(e.p) {
				n++
			}
		}
		filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return caixaMarcar(gtx, th, &d.marca[i], d.ligados[i],
					fmt.Sprintf("%s — %d máquina(s)", e.rotulo, n))
			})
		}))
	}
	filhos = append(filhos,
		espaco(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return caixaMarcar(gtx, th, &d.fundo, d.emFundo, "abrir em segundo plano")
		}),
		espaco(14),
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
					return botaoPrimario(gtx, th, &d.btnOk, "Abrir")
				}),
			)
		}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

func (d *dlgGrupo) abrir() {
	for _, cx := range d.lista {
		for i, e := range protocolos {
			if !d.ligados[i] || !cx.Tem(e.p) {
				continue
			}
			d.painel.abrirEm(cx, e.p, !d.emFundo)
		}
	}
	d.w.Invalidate()
}
