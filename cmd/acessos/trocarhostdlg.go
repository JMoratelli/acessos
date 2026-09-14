package main

import (
	"image"
	"sort"
	"strings"

	"acessos-go/internal/conexoes"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgTrocarHost troca a máquina de uma sessão SFTP sem fechar a aba. Era
// um menu que despejava TODAS as conexões SSH cadastradas, sem busca nem
// rolagem — inviável com centenas de máquinas cadastradas. Vira um
// popover com campo de busca e lista rolável, sempre em ordem
// alfabética, ancorado ONDE o botão "Trocar máquina" foi clicado — do
// mesmo jeito que o menu de contexto (menu.go), não um diálogo
// centralizado: aqui o "onde eu cliquei" importa mais que num diálogo
// comum, então clicar fora fecha (como o menu) em vez de exigir Fechar.
type dlgTrocarHost struct {
	pos        image.Point
	nomes      []string
	porNome    map[string]conexoes.Conexao
	aoEscolher func(conexoes.Conexao)

	// trava o clique-fora por UM quadro: o próprio clique que abriu o
	// popover ainda está no ar quando este quadro desenha a área de
	// "fora" pela primeira vez, e sem a trava ele se fechava sozinho no
	// instante de abrir (mesmo problema que dashTab.travaAbrir resolve
	// para o menu de contexto do card).
	travaFechar int

	filtro    widget.Editor
	lista     widget.List
	btns      []widget.Clickable
	btnFechar widget.Clickable
}

var trocarHostAtual *dlgTrocarHost

const trocarHostLargura = unit.Dp(380)

func abrirTrocarHost(w *app.Window, pos image.Point, opcoes []conexoes.Conexao, aoEscolher func(conexoes.Conexao)) {
	d := &dlgTrocarHost{
		pos:         pos,
		porNome:     map[string]conexoes.Conexao{},
		aoEscolher:  aoEscolher,
		travaFechar: 1,
	}
	d.filtro.SingleLine = true
	d.lista.Axis = layout.Vertical
	for _, cx := range opcoes {
		d.nomes = append(d.nomes, cx.Nome)
		d.porNome[cx.Nome] = cx
	}
	sort.Strings(d.nomes)
	d.btns = make([]widget.Clickable, len(d.nomes))
	trocarHostAtual = d
	w.Invalidate()
}

func fecharTrocarHost() { trocarHostAtual = nil }

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

var tagTrocarHostFora = new(int)

// layoutTrocarHost desenha o popover ancorado na posição do clique que o
// abriu, igual ao menu de contexto (menu.go): clique fora fecha.
func layoutTrocarHost(gtx layout.Context, th *material.Theme) layout.Dimensions {
	d := trocarHostAtual
	if d == nil {
		return layout.Dimensions{}
	}
	tela := gtx.Constraints.Max

	area := clip.Rect{Max: tela}.Push(gtx.Ops)
	event.Op(gtx.Ops, tagTrocarHostFora)
	area.Pop()
	for {
		ev, ok := gtx.Source.Event(pointer.Filter{Target: tagTrocarHostFora, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, isP := ev.(pointer.Event); isP {
			if d.travaFechar > 0 {
				continue
			}
			fecharTrocarHost()
			return layout.Dimensions{}
		}
	}
	if d.travaFechar > 0 {
		d.travaFechar--
	}

	if d.btnFechar.Clicked(gtx) {
		fecharTrocarHost()
		return layout.Dimensions{}
	}

	idxs := d.filtrados()
	larg := gtx.Dp(trocarHostLargura)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
	gtx.Constraints.Min.Y = 0

	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, negrito(txt(th, fonteCond, spSubgrupo, "Trocar máquina", tema.Texto)).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.btnFechar.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							cor := tema.Fraco
							if d.btnFechar.Hovered() {
								cor = tema.Texto
							}
							return layout.UniformInset(2).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return icone(gtx, icons.NavigationClose, cor, 16)
							})
						})
					}),
				)
			}),
			espaco(10),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return caixaEditor(gtx, th, &d.filtro, "buscar máquina...", 0)
			}),
			espaco(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// Altura fixa: é o que faz a lista ROLAR em vez de estourar
				// pra fora da tela com centenas de máquinas.
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
						aoEscolher := d.aoEscolher
						fecharTrocarHost()
						aoEscolher(cx)
						return layout.Dimensions{}
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
	})
	conteudo := macro.Stop()

	x, y := d.pos.X, d.pos.Y
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
	sombra(gtx, dims.Size, 12)
	superficie(gtx, dims.Size, tema.Cartao, tema.Borda, 12)
	conteudo.Add(gtx.Ops)
	return dims
}
