package main

import (
	"sort"
	"strings"

	"acessos-go/internal/conexoes"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgTrocarHost troca a máquina de uma sessão SFTP sem fechar a aba. Era
// um menu que despejava TODAS as conexões SSH sem busca nem rolagem —
// inviável com centenas de máquinas cadastradas. Vira diálogo comum, com
// campo de busca e lista rolável, sempre em ordem alfabética.
type dlgTrocarHost struct {
	nomes      []string
	porNome    map[string]conexoes.Conexao
	aoEscolher func(conexoes.Conexao)

	filtro widget.Editor
	lista  widget.List
	btns   []widget.Clickable
}

func abrirTrocarHost(w *app.Window, opcoes []conexoes.Conexao, aoEscolher func(conexoes.Conexao)) {
	d := &dlgTrocarHost{
		porNome:    map[string]conexoes.Conexao{},
		aoEscolher: aoEscolher,
	}
	d.filtro.SingleLine = true
	d.lista.Axis = layout.Vertical
	for _, cx := range opcoes {
		d.nomes = append(d.nomes, cx.Nome)
		d.porNome[cx.Nome] = cx
	}
	sort.Strings(d.nomes)
	d.btns = make([]widget.Clickable, len(d.nomes))
	abrirDialogo(d)
	w.Invalidate()
}

func (d *dlgTrocarHost) Titulo() string   { return "Trocar máquina" }
func (d *dlgTrocarHost) Largura() unit.Dp { return 380 }

// filtrados devolve os ÍNDICES (em d.nomes/d.btns) que casam com a busca,
// já em ordem alfabética porque d.nomes já está ordenado.
func (d *dlgTrocarHost) filtrados() []int {
	q := strings.ToLower(strings.TrimSpace(d.filtro.Text()))
	var idxs []int
	for i, nome := range d.nomes {
		if q == "" {
			idxs = append(idxs, i)
			continue
		}
		cx := d.porNome[nome]
		if strings.Contains(strings.ToLower(nome), q) || strings.Contains(strings.ToLower(cx.Host), q) {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

func (d *dlgTrocarHost) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	idxs := d.filtrados()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return caixaEditor(gtx, th, &d.filtro, "buscar máquina...", 0)
		}),
		espaco(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// Altura fixa: é o que faz a lista ROLAR em vez de estourar o
			// diálogo pra fora da tela com centenas de máquinas.
			gtx.Constraints.Max.Y = gtx.Dp(320)
			gtx.Constraints.Min.Y = 0
			if len(idxs) == 0 {
				return layout.Center.Layout(gtx, rotulo(th, fonteMono, spCardMeta,
					"nenhuma máquina encontrada", tema.Fraco))
			}
			return material.List(th, &d.lista).Layout(gtx, len(idxs), func(gtx layout.Context, pos int) layout.Dimensions {
				i := idxs[pos]
				nome := d.nomes[i]
				cx := d.porNome[nome]
				if d.btns[i].Clicked(gtx) {
					fecharDialogo()
					d.aoEscolher(cx)
				}
				return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Background{}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							fundo, borda := tema.Cartao, transparente
							if d.btns[i].Hovered() {
								fundo, borda = tema.Hover, tema.Borda2
							}
							superficie(gtx, gtx.Constraints.Min, fundo, borda, 6)
							return layout.Dimensions{Size: gtx.Constraints.Min}
						},
						func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return d.btns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: 6, Bottom: 6, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(rotuloLinha(th, fonteSans, spCorpo, nome, tema.Texto)),
										layout.Rigid(rotuloLinha(th, fonteMono, spCardMeta, cx.Host, tema.Fraco)),
									)
								})
							})
						},
					)
				})
			})
		}),
	)
}
