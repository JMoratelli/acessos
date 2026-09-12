package main

import (
	"fmt"
	"sort"
	"strings"

	"acessos-go/internal/massa/model"
	"acessos-go/internal/massa/snippets"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgSnippets é a biblioteca de comandos: o mesmo snippets.ini que o app
// original usa, e o mesmo formato do comandos.ini do executor em massa —
// um arquivo só, lido pelos três lugares que precisam dele (aqui, a aba de
// massa e o terminal SSH).
//
// Quando aberto de dentro de um terminal, ganha o botão "inserir": o
// comando vai para a sessão em vez de ser só editado.
type dlgSnippets struct {
	w       *app.Window
	caminho string
	snips   []model.Snippet
	sel     int
	inserir func(string) // != nil quando veio de um terminal SSH

	lista      widget.List
	btnLinha   []widget.Clickable
	btnNovo    widget.Clickable
	btnRemove  widget.Clickable
	btnSalvar  widget.Clickable
	btnInseri  widget.Clickable
	btnFechar  widget.Clickable
	descricao  widget.Editor
	comando    widget.Editor
	root       widget.Clickable
	usaRoot    bool
	btnPlat    [3]widget.Clickable
	plataforma int // 0 linux, 1 windows, 2 ambos
	erro       string
}

func abrirSnippets(w *app.Window, caminho string, inserir func(string)) {
	d := &dlgSnippets{w: w, caminho: caminho, sel: -1, inserir: inserir}
	d.lista.Axis = layout.Vertical
	d.descricao.SingleLine = true
	d.recarregar()
	abrirDialogo(d)
}

func (d *dlgSnippets) Titulo() string   { return "Snippets" }
func (d *dlgSnippets) Largura() unit.Dp { return 820 }

func (d *dlgSnippets) recarregar() {
	s, err := snippets.CarregarSnippets(d.caminho)
	if err != nil {
		d.erro = err.Error()
		return
	}
	sort.Slice(s, func(i, j int) bool { return s[i].Descricao < s[j].Descricao })
	d.snips = s
	d.btnLinha = make([]widget.Clickable, len(s))
	if d.sel >= len(s) {
		d.sel = -1
	}
}

func (d *dlgSnippets) escolher(i int) {
	d.sel = i
	d.descricao.SetText(d.snips[i].Descricao)
	d.comando.SetText(d.snips[i].Comando)
	d.usaRoot = d.snips[i].Root
	d.plataforma = indicePlataforma(d.snips[i].Plataforma)
}

func indicePlataforma(p model.Plataforma) int {
	switch p {
	case model.Windows:
		return 1
	case model.Ambos:
		return 2
	}
	return 0
}

func plataformaDoIndice(i int) model.Plataforma {
	switch i {
	case 1:
		return model.Windows
	case 2:
		return model.Ambos
	}
	return model.Linux
}

func (d *dlgSnippets) salvar() {
	desc := strings.TrimSpace(d.descricao.Text())
	cmd := strings.TrimSpace(d.comando.Text())
	if desc == "" || cmd == "" {
		d.erro = "descrição e comando são obrigatórios"
		return
	}
	novo := model.Snippet{
		Descricao: desc, Comando: cmd, Root: d.usaRoot,
		Plataforma: plataformaDoIndice(d.plataforma),
	}
	if d.sel >= 0 && d.sel < len(d.snips) {
		novo.ID = d.snips[d.sel].ID
		d.snips[d.sel] = novo
	} else {
		// ID derivado da descrição, como faz o app original: o arquivo é
		// editável à mão, e [id] legível vale mais que um número.
		novo.ID = snippets.IDValido(desc)
		d.snips = append(d.snips, novo)
	}
	if err := snippets.SalvarSnippets(d.caminho, d.snips); err != nil {
		d.erro = err.Error()
		return
	}
	d.erro = ""
	d.recarregar()
}

func (d *dlgSnippets) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	for i := range d.btnLinha {
		if d.btnLinha[i].Clicked(gtx) {
			d.escolher(i)
		}
	}
	if d.btnNovo.Clicked(gtx) {
		d.sel = -1
		d.descricao.SetText("")
		d.comando.SetText("")
		d.usaRoot = false
	}
	if d.btnSalvar.Clicked(gtx) {
		d.salvar()
	}
	if d.root.Clicked(gtx) {
		d.usaRoot = !d.usaRoot
	}
	for i := range d.btnPlat {
		if d.btnPlat[i].Clicked(gtx) {
			d.plataforma = i
		}
	}
	if d.btnFechar.Clicked(gtx) {
		fecharDialogo()
	}
	if d.btnInseri.Clicked(gtx) && d.inserir != nil {
		d.inserir(d.comando.Text())
		fecharDialogo()
	}
	if d.btnRemove.Clicked(gtx) && d.sel >= 0 && d.sel < len(d.snips) {
		alvo := d.snips[d.sel]
		confirmarDestrutivo(d.w, "Remover snippet", []string{alvo.Descricao},
			"Remover definitivamente", func() {
				resto := append([]model.Snippet{}, d.snips[:d.sel]...)
				resto = append(resto, d.snips[d.sel+1:]...)
				if err := snippets.SalvarSnippets(d.caminho, resto); err != nil {
					fmt.Println(err)
					return
				}
				d.sel = -1
				d.recarregar()
				abrirDialogo(d) // volta para a biblioteca
			})
	}

	alturaMax := gtx.Constraints.Max.Y / 2
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = alturaMax
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					larg := gtx.Dp(300)
					gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
					// A lista ganha fundo próprio: branco sobre branco com
					// texto cinza não separa nada, e era isso que deixava a
					// tela "opaca". Aqui a coluna tem superfície e borda.
					return layout.Background{}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							superficie(gtx, gtx.Constraints.Min, tema.Vidro1, tema.Borda, 8)
							return layout.Dimensions{Size: gtx.Constraints.Min}
						},
						func(gtx layout.Context) layout.Dimensions {
							return layout.UniformInset(4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return d.listaSnips(gtx, th)
							})
						},
					)
				}),
				layout.Rigid(layout.Spacer{Width: 10}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return d.editor(gtx, th)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if d.erro == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 6}.Layout(gtx, rotulo(th, fonteMono, spSecundario, d.erro, tema.ErroFg))
		}),
		espaco(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoSutil(gtx, th, &d.btnNovo, "novo")
				}),
				layout.Rigid(layout.Spacer{Width: 6}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// remover é destrutivo: vermelho sempre
					return botaoPerigo(gtx, th, &d.btnRemove, "remover")
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnFechar, "Fechar")
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if d.inserir == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return botaoPrimario(gtx, th, &d.btnInseri, "Inserir no terminal")
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoPrimario(gtx, th, &d.btnSalvar, "Salvar")
				}),
			)
		}),
	)
}

var _ = icons.ActionCode

// listaSnips: uma linha por snippet, com a PLATAFORMA visível. Um comando
// de Windows rodando num PDV Linux (e vice-versa) é erro caro, e o arquivo
// guarda essa informação desde sempre — ela só não estava aparecendo.
func (d *dlgSnippets) listaSnips(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return material.List(th, &d.lista).Layout(gtx, len(d.snips), func(gtx layout.Context, i int) layout.Dimensions {
		s := d.snips[i]
		return d.btnLinha[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					fundo, borda := tema.Cartao, transparente
					switch {
					case i == d.sel:
						fundo, borda = tema.AzulFraco, tema.Azul
					case d.btnLinha[i].Hovered():
						fundo, borda = tema.Hover, tema.Borda2
					}
					superficie(gtx, gtx.Constraints.Min, fundo, borda, 6)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				},
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 6, Bottom: 6, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return selinhoPlataforma(gtx, th, s.Plataforma)
									}),
									layout.Rigid(layout.Spacer{Width: 6}.Layout),
									layout.Flexed(1, rotulo(th, fonteSans, spCorpo, s.Descricao, tema.Texto)),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										if !s.Root {
											return layout.Dimensions{}
										}
										return rotulo(th, fonteMono, spCardMeta, "root", tema.AtencaoFg)(gtx)
									}),
								)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: 34, Top: 2}.Layout(gtx,
									rotuloLinha(th, fonteMono, spCardMeta, primeiraLinha(s.Comando), tema.Sec))
							}),
						)
					})
				},
			)
		})
	})
}

// selinhoPlataforma: linux / windows / ambos, com a mesma cor em todo o
// app (ambos = neutro, porque vale para os dois).
func selinhoPlataforma(gtx layout.Context, th *material.Theme, p model.Plataforma) layout.Dimensions {
	rot, fundo, cor := "univ", tema.Vidro2, tema.Sec
	switch p {
	case model.Linux:
		rot, fundo, cor = "linux", tema.VerdeFraco, tema.Verde
	case model.Windows:
		rot, fundo, cor = "win", tema.AzulFraco, tema.Azul
	}
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			superficie(gtx, gtx.Constraints.Min, fundo, transparente, 4)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 1, Bottom: 1, Left: 5, Right: 5}.Layout(gtx,
				negrito(txt(th, fonteMono, spCardMeta, rot, cor)).Layout)
		},
	)
}

// editor é a coluna direita: descrição, comando e as opções do snippet.
func (d *dlgSnippets) editor(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			superficie(gtx, gtx.Constraints.Min, tema.Cartao, tema.Borda, 8)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.UniformInset(10).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return caixaEditor(gtx, th, &d.descricao, "descrição", 0)
					}),
					espaco(6),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						alt := unit.Dp(float32(gtx.Constraints.Max.Y) / gtx.Metric.PxPerDp)
						return caixaEditor(gtx, th, &d.comando, "comando", alt)
					}),
					espaco(8),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return caixaMarcar(gtx, th, &d.root, d.usaRoot, "sugerir root")
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Dimensions{Size: gtx.Constraints.Min}
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return segmentado(gtx, th,
									[]*widget.Clickable{&d.btnPlat[0], &d.btnPlat[1], &d.btnPlat[2]},
									[]string{"linux", "windows", "ambos"}, d.plataforma)
							}),
						)
					}),
				)
			})
		},
	)
}
