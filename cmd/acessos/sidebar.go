package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"

	"acessos-go/internal/conexoes"

	"gio.tools/icons"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	lateralLarga = unit.Dp(212)
	// UMA calha só pra toda a coluna: marca, busca, itens e o botão de
	// recolher começam na mesma vertical, e os ícones (lupa, item, chevron)
	// caem todos na coluna padColuna+padItem. Antes cada elemento tinha o
	// próprio recuo (12, 8, 5+10) e nada batia com nada.
	padColuna       = unit.Dp(10)
	padItem         = unit.Dp(10)
	lateralTrilho   = unit.Dp(60) // recolhida vira TRILHO de ícones, nunca some
	itemLateralRaio = unit.Dp(7)
)

// itemLateral é uma entrada do menu. sid identifica a seção; o clique abre
// (ou foca) a aba correspondente — padrão B: o item do menu ABRE uma aba
// que permanece aberta e fechável.
type itemLateral struct {
	sid    string
	rotulo string
	ic     *widget.Icon
	btn    widget.Clickable
}

// estadoLateral: larga -> trilho de ícones -> oculta. A skill diz pra
// nunca sumir por completo; o terceiro estado entra porque foi pedido
// explicitamente (tela cheia de operação, onde cada pixel conta). O menu
// da barra de topo continua trazendo a lateral de volta, então não há
// beco sem saída.
type estadoLateral int

const (
	lateralAberta estadoLateral = iota
	lateralRecolhida
	lateralOculta
)

// sidebar é o menu lateral, SEMPRE vertical (quando visível). O botão de
// recolher mora na própria lateral, no rodapé — nunca na barra de abas,
// que rola (gtk.md, padrão B).
type sidebar struct {
	estado      estadoLateral
	itens       []*itemLateral
	collapseBtn widget.Clickable

	// A lista de máquinas mora aqui, como no app original: grupo ->
	// subgrupo -> host, e o clique no host abre a conexão no protocolo
	// escolhido na tira de baixo (Tela/Shell/RDP/Arquivos).
	painel   *dashTab
	lista    widget.List
	expandir map[string]bool
	cliques  map[string]*widget.Clickable
	proto    conexoes.Protocolo
	btnProto [4]widget.Clickable

	// menu de contexto e clique do meio: mesma técnica do Painel — uma
	// área só, por cima, com PassOp, e quem está sob o cursor sai do
	// hover (ver dashtab.rastrearPonteiro).
	tagPonteiro *int
	ultimaPos   image.Point
	sobCursor   *conexoes.Conexao
	grupoSob    *conexoes.Grupo
	aoMenuHost  func(cx conexoes.Conexao, pos image.Point)
	aoMenuGrupo func(g *conexoes.Grupo, pos image.Point)
}

func newSidebar() *sidebar {
	s := &sidebar{
		itens: []*itemLateral{
			{sid: "painel", rotulo: "Painel", ic: icons.ActionDashboard},
		},
		expandir:    map[string]bool{},
		cliques:     map[string]*widget.Clickable{},
		proto:       conexoes.VNC,
		tagPonteiro: new(int),
	}
	s.lista.Axis = layout.Vertical
	// Nasce como estava quando o app foi fechado: quem trabalha com a
	// lateral escondida não quer reabri-la toda manhã. Mesma chave (e
	// mesmo significado) do app original: [geral] lateral = 0 | 1.
	if lateralInicialOculta {
		s.estado = lateralOculta
	}
	return s
}

// lateralInicialOculta é lido do .ini no start (ver main.go).
var lateralInicialOculta bool

// lembrarLateral grava o estado no [geral] do inventário. Grava na hora
// do clique, e não ao sair: fechar o app pelo botão da janela — ou uma
// queda — não pode custar a preferência.
func lembrarLateral(oculta bool) {
	if caminhoINI == "" {
		return
	}
	v := "1"
	if oculta {
		v = "0"
	}
	if err := conexoes.SalvarGeral(caminhoINI, map[string]string{"lateral": v}); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func (s *sidebar) clique(chave string) *widget.Clickable {
	c, ok := s.cliques[chave]
	if !ok {
		c = &widget.Clickable{}
		s.cliques[chave] = c
	}
	return c
}

// ciclar alterna aberta <-> oculta. É UM clique: a lateral aqui não é um
// menu de seções (o app tem uma seção só, o Painel) e sim a LISTA das
// máquinas — um trilho de ícones no meio do caminho não mostraria nada
// útil e só atrasaria quem quer a tela cheia.
func (s *sidebar) ciclar() {
	if s.estado == lateralAberta {
		s.estado = lateralOculta
	} else {
		s.estado = lateralAberta
	}
	lembrarLateral(s.estado == lateralOculta)
}

func (s *sidebar) recolhida() bool { return s.estado == lateralRecolhida }
func (s *sidebar) oculta() bool    { return s.estado == lateralOculta }

func (s *sidebar) largura(gtx layout.Context) int {
	switch s.estado {
	case lateralOculta:
		return 0
	case lateralRecolhida:
		return gtx.Dp(lateralTrilho)
	}
	return gtx.Dp(lateralLarga)
}

// layout desenha a lateral. aoAbrir recebe o sid do item clicado.
func (s *sidebar) layout(gtx layout.Context, th *material.Theme, ativo string,
	aoAbrir func(sid string), busca layout.Widget) layout.Dimensions {

	if s.oculta() {
		return layout.Dimensions{}
	}
	for _, it := range s.itens {
		if it.btn.Clicked(gtx) {
			aoAbrir(it.sid)
		}
	}
	for s.collapseBtn.Clicked(gtx) {
		s.ciclar()
	}

	s.sobCursor, s.grupoSob = nil, nil
	defer s.rastrear(gtx)

	w := s.largura(gtx)
	gtx.Constraints.Min.X = w
	gtx.Constraints.Max.X = w
	alt := gtx.Constraints.Max.Y

	// translúcida sobre o gradiente da janela (.barra do tema.py), não o
	// cromo opaco: é o que dá o vidro.
	paint.FillShape(gtx.Ops, tema.Barra, clip.Rect{Max: image.Pt(w, alt)}.Op())
	// fio de separação à direita, como a borda do cromo no original
	paint.FillShape(gtx.Ops, tema.Borda2,
		clip.Rect{Min: image.Pt(w-1, 0), Max: image.Pt(w, alt)}.Op())

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return s.marca(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// A busca dos hosts mora AQUI quando há largura pra ela; no
			// modo trilho ela volta pro painel (ver dashtab), pra nunca
			// ficar sem forma de procurar.
			if s.recolhida() || busca == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 6, Bottom: 2, Left: padColuna, Right: padColuna}.Layout(gtx, busca)
		}),
		layout.Rigid(layout.Spacer{Height: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var filhos []layout.FlexChild
			for _, it := range s.itens {
				it := it
				filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return it.btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return s.item(gtx, th, it, it.sid == ativo, it.btn.Hovered())
					})
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return s.arvore(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return s.tiraProtocolo(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return s.botaoRecolher(gtx, th) }),
	)
}

// marca: eyebrow + nome do app, escondidos no modo trilho (só o ícone
// sobrevive ali — não há largura para texto).
func (s *sidebar) marca(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if s.recolhida() {
		return layout.Inset{Top: 10, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return icone(gtx, icons.ActionHome, tema.TopoTxt, 20)
			})
		})
	}
	return layout.Inset{Top: 10, Bottom: 4, Left: padColuna + padItem, Right: padColuna}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(txt(th, fonteMono, spMarcaSub, "MACHADÃO CORP", tema.TopoSec).Layout),
			layout.Rigid(negrito(txt(th, fonteCond, spMarcaTopo, "Acessos", tema.TopoTxt)).Layout),
		)
	})
}

// item: ícone sempre visível; rótulo some no modo trilho. O trilho de
// seleção (3px) existe SEMPRE, transparente quando inativo — se ele
// aparecesse só no item ativo, o texto deslocaria a cada clique.
func (s *sidebar) item(gtx layout.Context, th *material.Theme, it *itemLateral, ativo, hover bool) layout.Dimensions {
	fundo := transparente
	cor := tema.TopoSec
	switch {
	case ativo:
		fundo, cor = tema.VidroH, tema.TopoTxt
	case hover:
		fundo, cor = tema.Vidro, tema.TopoTxt
	}
	trilho := transparente
	if ativo {
		trilho = tema.Azul
	}

	return layout.Inset{Top: 1, Bottom: 1, Left: padColuna, Right: padColuna}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				superficie(gtx, size, fundo, transparente, itemLateralRaio)
				lw := gtx.Dp(3)
				rr := clip.UniformRRect(image.Rect(0, 0, lw, size.Y), gtx.Dp(2))
				paint.FillShape(gtx.Ops, trilho, rr.Op(gtx.Ops))
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				if s.recolhida() {
					return layout.Inset{Top: 7, Bottom: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return icone(gtx, it.ic, cor, 18)
						})
					})
				}
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: 7, Bottom: 7, Left: padItem, Right: padItem}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return icone(gtx, it.ic, cor, 18)
						}),
						layout.Rigid(layout.Spacer{Width: 10}.Layout),
						layout.Rigid(txt(th, fonteSans, spCorpo, it.rotulo, cor).Layout),
					)
				})
			}),
		)
	})
}

func (s *sidebar) botaoRecolher(gtx layout.Context, th *material.Theme) layout.Dimensions {
	ic := icons.NavigationChevronLeft
	rot := "Recolher  Ctrl+B"
	if s.recolhida() {
		ic = icons.NavigationChevronRight
		rot = ""
	}
	cor := tema.TopoSec
	if s.collapseBtn.Hovered() {
		cor = tema.TopoTxt
	}

	return layout.Inset{Top: 4, Bottom: 8, Left: padColuna, Right: padColuna}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return s.collapseBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					size := gtx.Constraints.Min
					fundo := transparente
					if s.collapseBtn.Hovered() {
						fundo = tema.Vidro
					}
					superficie(gtx, size, fundo, transparente, itemLateralRaio)
					return layout.Dimensions{Size: size}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					if s.recolhida() {
						return layout.Inset{Top: 6, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return icone(gtx, ic, cor, 18)
							})
						})
					}
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 6, Bottom: 6, Left: padItem, Right: padItem}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return icone(gtx, ic, cor, 18)
							}),
							layout.Rigid(layout.Spacer{Width: 10}.Layout),
							layout.Rigid(txt(th, fonteMono, spBtnTopo, rot, cor).Layout),
						)
					})
				}),
			)
		})
	})
}

// garante que color continua importado mesmo se o corpo mudar
var _ color.NRGBA

// ------------------------------------------------ lista de máquinas

const (
	linhaLateralAlt = unit.Dp(13) // recuo por nível da árvore
)

// arvore desenha grupo -> subgrupo -> host, rolável, filtrada pelo mesmo
// termo do campo de busca (o do painel e o da lateral são espelhados).
func (s *sidebar) arvore(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if s.painel == nil || s.recolhida() {
		return layout.Dimensions{Size: gtx.Constraints.Min}
	}
	termo := strings.ToLower(strings.TrimSpace(s.painel.ultimoTermo))
	raizes := s.painel.arvore
	return material.List(th, &s.lista).Layout(gtx, len(raizes), func(gtx layout.Context, i int) layout.Dimensions {
		return s.ramo(gtx, th, raizes[i], 0, termo)
	})
}

func (s *sidebar) ramo(gtx layout.Context, th *material.Theme, g *conexoes.Grupo, nivel int, termo string) layout.Dimensions {
	chave := strings.Join(g.Caminho, ";")
	visiveis := filtrar(g.Conexoes, termo)

	aberto := s.expandir[chave]
	if termo != "" {
		if len(visiveis) == 0 && !temDescendente(g, termo) {
			return layout.Dimensions{}
		}
		aberto = true
	}

	btn := s.clique("g:" + chave)
	if btn.Clicked(gtx) {
		s.expandir[chave] = !s.expandir[chave]
	}
	if btn.Hovered() {
		s.grupoSob = g
	}

	filhos := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return s.linhaGrupo(gtx, th, g, nivel, aberto, btn.Hovered())
			})
		}),
	}
	if aberto {
		for _, f := range g.Filhos {
			f := f
			filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.ramo(gtx, th, f, nivel+1, termo)
			}))
		}
		for _, cx := range visiveis {
			cx := cx
			filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.linhaHost(gtx, th, cx, nivel+1)
			}))
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

func (s *sidebar) linhaGrupo(gtx layout.Context, th *material.Theme, g *conexoes.Grupo, nivel int, aberto, hover bool) layout.Dimensions {
	seta := icons.NavigationChevronRight
	if aberto {
		seta = icons.NavigationExpandMore
	}
	cor := tema.TopoSec
	if hover {
		cor = tema.TopoTxt
	}
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			if hover {
				superficie(gtx, gtx.Constraints.Min, tema.Vidro1, transparente, 5)
			}
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{
				Top: 3, Bottom: 3, Right: padColuna,
				Left: padColuna + unit.Dp(float32(nivel))*linhaLateralAlt,
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return icone(gtx, seta, cor, 14)
					}),
					layout.Rigid(layout.Spacer{Width: 4}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						w, h := gtx.Dp(3), gtx.Dp(13)
						rr := clip.UniformRRect(image.Rectangle{Max: image.Pt(w, h)}, gtx.Dp(2))
						paint.FillShape(gtx.Ops, corCategoria(g.Caminho[0]), rr.Op(gtx.Ops))
						return layout.Dimensions{Size: image.Pt(w, h)}
					}),
					layout.Rigid(layout.Spacer{Width: 6}.Layout),
					layout.Flexed(1, rotulo(th, fonteSans, spCorpo, g.Nome, cor)),
					layout.Rigid(layout.Spacer{Width: 4}.Layout),
					layout.Rigid(rotulo(th, fonteMono, spGrupoCont, fmt.Sprint(g.Total()), tema.Fraco)),
				)
			})
		}),
	)
}

// linhaHost: clicar abre a conexão no protocolo escolhido na tira de
// baixo. Se a máquina não tiver aquele protocolo, cai no primeiro que ela
// tiver — melhor abrir o que dá do que não responder ao clique.
func (s *sidebar) linhaHost(gtx layout.Context, th *material.Theme, cx conexoes.Conexao, nivel int) layout.Dimensions {
	btn := s.clique("h:" + cx.GrupoStr() + "|" + cx.Nome)
	if btn.Clicked(gtx) && s.painel != nil {
		p := s.proto
		if !cx.Tem(p) {
			p = conexoes.Protocolo("")
			for _, e := range protocolos {
				if cx.Tem(e.p) {
					p = e.p
					break
				}
			}
		}
		if p != "" {
			s.painel.abrir(cx, p)
		}
	}
	if btn.Hovered() {
		cx := cx
		s.sobCursor = &cx
	}
	e := estiloDe(s.proto)
	corIco := e.cor()
	if !cx.Tem(s.proto) {
		corIco = tema.Fraco
	}
	cor := tema.TopoSec
	if btn.Hovered() {
		cor = tema.TopoTxt
	}

	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				if btn.Hovered() {
					superficie(gtx, gtx.Constraints.Min, tema.Vidro, transparente, 5)
				}
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{
					Top: 2, Bottom: 2, Right: padColuna,
					Left: padColuna + unit.Dp(float32(nivel))*linhaLateralAlt + 18,
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return icone(gtx, e.ic, corIco, 13)
						}),
						layout.Rigid(layout.Spacer{Width: 6}.Layout),
						layout.Flexed(1, rotulo(th, fonteSans, spCorpo, cx.Nome, cor)),
					)
				})
			}),
		)
	})
}

// tiraProtocolo escolhe COM QUE protocolo o clique na lista abre — é o
// mesmo controle do rodapé da lista no app original (Tela/Shell/RDP).
// Slots sempre presentes; só a cor muda entre escolhido e não escolhido.
func (s *sidebar) tiraProtocolo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if s.recolhida() || s.painel == nil {
		return layout.Dimensions{}
	}
	var filhos []layout.FlexChild
	for i, e := range protocolos {
		i, e := i, e
		if s.btnProto[i].Clicked(gtx) {
			s.proto = e.p
		}
		escolhido := s.proto == e.p
		filhos = append(filhos,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return s.btnProto[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Stack{}.Layout(gtx,
						layout.Expanded(func(gtx layout.Context) layout.Dimensions {
							size := gtx.Constraints.Min
							fundo, borda := tema.Vidro, tema.LuzB
							if escolhido {
								fundo, borda = e.fundo(), transparente
							} else if s.btnProto[i].Hovered() {
								fundo = tema.VidroH
							}
							superficie(gtx, size, fundo, borda, itemLateralRaio)
							return layout.Dimensions{Size: size}
						}),
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							cor := tema.TopoSec
							if escolhido {
								cor = e.cor()
							}
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								// Só o rótulo: com quatro fatias em 212dp, ícone
								// + texto não cabia e o texto saía cortado
								// ("she…", "arq…"). O ícone do protocolo já
								// está na linha de cada máquina, logo acima.
								return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return txt(th, fonteMono, spBtnTopo, e.rotulo, cor).Layout(gtx)
								})
							})
						}),
					)
				})
			}),
			layout.Rigid(layout.Spacer{Width: 3}.Layout),
		)
	}
	return layout.Inset{Top: 4, Bottom: 2, Left: padColuna, Right: padColuna}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, filhos[:len(filhos)-1]...)
		})
}

// rastrear registra a área por cima da lateral (PassOp) para saber onde
// está o ponteiro e detectar os cliques do meio e direito.
func (s *sidebar) rastrear(gtx layout.Context) {
	for {
		ev, ok := gtx.Source.Event(pointer.Filter{
			Target: s.tagPonteiro,
			Kinds:  pointer.Move | pointer.Press | pointer.Drag,
		})
		if !ok {
			break
		}
		pe, isP := ev.(pointer.Event)
		if !isP {
			continue
		}
		s.ultimaPos = image.Pt(int(pe.Position.X), int(pe.Position.Y))
		if pe.Kind != pointer.Press {
			continue
		}
		meio := pe.Buttons.Contain(pointer.ButtonTertiary)
		dir := pe.Buttons.Contain(pointer.ButtonSecondary)
		if !meio && !dir {
			continue
		}
		pos := s.ultimaPos.Add(image.Pt(0, alturaTopoAtual))
		switch {
		case s.sobCursor != nil && dir && s.aoMenuHost != nil:
			s.aoMenuHost(*s.sobCursor, pos)
		case s.sobCursor != nil && meio:
			// clique do meio na máquina: abre o primeiro protocolo em
			// segundo plano, como no app original.
			if p, ok := protocoloPadrao(*s.sobCursor); ok && s.painel != nil {
				s.painel.abrirEm(*s.sobCursor, p, false)
			}
		case s.grupoSob != nil && (meio || dir) && s.aoMenuGrupo != nil:
			s.aoMenuGrupo(s.grupoSob, pos)
		}
	}
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: image.Pt(s.largura(gtx), gtx.Constraints.Max.Y)}.Push(gtx.Ops)
	event.Op(gtx.Ops, s.tagPonteiro)
	area.Pop()
	pass.Pop()
}
