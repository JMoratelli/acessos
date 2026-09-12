package main

import (
	"image"
	"image/color"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const topBarAltura = unit.Dp(30)

// topBar é a NOSSA titlebar. A decoração do sistema fica desligada
// (app.Decorated(false)) pra não gastar uma faixa inteira de altura só com
// o nome da janela: aqui a mesma faixa leva identidade, menu e os botões
// de janela. É o que o app original faz com a headerbar integrada.
type topBar struct {
	menu widget.Clickable
	// sem botão "início" aqui: a aba fixa da casinha, logo abaixo, JÁ é
	// esse botão. Dois controles iguais a 30px um do outro só fazem o
	// operador perguntar qual é a diferença.
	recarrega  widget.Clickable
	novaConex  widget.Clickable
	snippets   widget.Clickable
	ajustes    widget.Clickable
	cofre      widget.Clickable
	chaveiro   widget.Clickable
	tema       widget.Clickable
	minimizar  widget.Clickable
	maximizar  widget.Clickable
	fechar     widget.Clickable
	maximizada bool
}

type acoesTopo struct {
	menu       func()
	recarregar func()
	nova       func()
	snippets   func()
	ajustes    func()
	abrirCofre func()
	chaveiro   func()
	trocarTema func()
}

func (t *topBar) layout(gtx layout.Context, w *app.Window, th *material.Theme, colunaLateral int, a acoesTopo) layout.Dimensions {
	for t.menu.Clicked(gtx) {
		a.menu()
	}
	for t.recarrega.Clicked(gtx) {
		a.recarregar()
	}
	for t.cofre.Clicked(gtx) {
		a.abrirCofre()
	}
	for t.chaveiro.Clicked(gtx) {
		a.chaveiro()
	}
	for t.novaConex.Clicked(gtx) {
		a.nova()
	}
	for t.snippets.Clicked(gtx) {
		a.snippets()
	}
	for t.ajustes.Clicked(gtx) {
		a.ajustes()
	}
	for t.tema.Clicked(gtx) {
		a.trocarTema()
	}
	for t.minimizar.Clicked(gtx) {
		w.Perform(system.ActionMinimize)
	}
	for t.maximizar.Clicked(gtx) {
		if t.maximizada {
			w.Perform(system.ActionUnmaximize)
		} else {
			w.Perform(system.ActionMaximize)
		}
		t.maximizada = !t.maximizada
	}
	for t.fechar.Clicked(gtx) {
		w.Perform(system.ActionClose)
	}

	h := gtx.Dp(topBarAltura)
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h
	largura := gtx.Constraints.Max.X

	fundoTitulo(gtx, image.Pt(largura, h))
	// Fio de sombra na emenda: tem que ser MAIS ESCURO que as duas faixas.
	// Com tema.Borda (que no escuro é mais claro que a barra) ele virava
	// um realce e colava ainda mais as duas.
	paint.FillShape(gtx.Ops, tema.TituloBorda,
		clip.Rect{Min: image.Pt(0, h-1), Max: image.Pt(largura, h)}.Op())

	// arrastar a janela pela barra: sem decoração do sistema, é daqui que
	// o compositor recebe o pedido de mover.
	area := clip.Rect{Max: image.Pt(largura, h)}.Push(gtx.Ops)
	system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
	area.Pop()

	// Inset 1 à esquerda para o ≡ cair na MESMA coluna da casinha da tira
	// de abas: os dois são caixas de 24px (ícone de 14 + 5 de respiro de
	// cada lado) e a primeira aba também começa a 1px da borda, então os
	// dois glifos ficam com o mesmo centro. Quem se alinha à coluna da
	// lateral é o texto da marca, logo depois do ≡.
	return layout.Inset{Left: 1, Right: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// Altura mínima ZERO: com Min.Y = altura da barra, cada filho
		// esticava até o topo e a base e o Alignment: Middle não tinha o
		// que centralizar — texto, pílulas e botões de janela acabavam em
		// linhas de base diferentes. Solto, cada um mede o próprio tamanho
		// e o Middle alinha todos pelo meio da faixa.
		gtx.Constraints.Min.Y = 0
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			// O bloco da marca ocupa EXATAMENTE a coluna da lateral: o
			// "Acessos" da barra cai na mesma vertical do cabeçalho da
			// lateral, e o subtítulo começa junto com a tira de abas.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				d := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return botaoIcone(gtx, &t.menu, icons.NavigationMenu, tema.TopoSec, tema.TopoTxt)
					}),
					layout.Rigid(layout.Spacer{Width: 4}.Layout),
					layout.Rigid(negrito(txt(th, fonteCond, spMarcaTopo, "Acessos", tema.TopoTxt)).Layout),
				)
				// -4 do inset esquerdo da barra: a conta vai até a BORDA da
				// lateral, não até onde o texto começa.
				if col := colunaLateral - gtx.Dp(1); col > d.Size.X {
					d.Size.X = col
				} else {
					// lateral oculta: sem a coluna pra alinhar, ainda assim
					// o subtítulo não pode encostar na marca.
					d.Size.X += gtx.Dp(12)
				}
				return d
			}),
			layout.Rigid(txt(th, fonteMono, spMarcaSub, "VNC · SSH · RDP", tema.TopoSec).Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return pilula(gtx, th, &t.novaConex, icons.ContentAdd, "nova")
			}),
			layout.Rigid(layout.Spacer{Width: 4}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return pilula(gtx, th, &t.snippets, icons.ActionCode, "snippets")
			}),
			layout.Rigid(layout.Spacer{Width: 4}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return pilula(gtx, th, &t.recarrega, icons.NavigationRefresh, "INI")
			}),
			layout.Rigid(layout.Spacer{Width: 4}.Layout),
			// O cadeado é indicador E botão: aberto = cofre destrancado
			// nesta sessão, fechado = as senhas cifradas ainda não abrem.
			// chaveiro: credenciais nomeadas. Fica ao lado do cadeado
			// porque as duas coisas são o mesmo assunto — o cofre é a
			// chave, o chaveiro é o que ela abre.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoIcone(gtx, &t.chaveiro, icons.CommunicationVPNKey, tema.TopoSec, tema.TopoTxt)
			}),
			// Só o cadeado: o estado (fechado/aberto) já é o próprio
			// glifo, e a palavra ao lado não acrescentava nada.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				ic, cor := icons.ActionLock, tema.TopoSec
				if cofreAberto != nil {
					ic, cor = icons.ActionLockOpen, tema.Verde
				}
				return botaoIcone(gtx, &t.cofre, ic, cor, tema.TopoTxt)
			}),
			layout.Rigid(layout.Spacer{Width: 4}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoIcone(gtx, &t.ajustes, icons.ActionSettings, tema.TopoSec, tema.TopoTxt)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return pilula(gtx, th, &t.tema, icons.ImageBrightness6, "tema")
			}),
			layout.Rigid(layout.Spacer{Width: 10}.Layout),
			// Botões de janela com as MESMAS métricas das pílulas — assim a
			// área de hover deles e a das pílulas ficam na mesma linha
			// (tema.py faz questão disso).
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoIcone(gtx, &t.minimizar, icons.ContentRemove, tema.TopoSec, tema.TopoTxt)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				ic := icons.ActionOpenInNew
				if t.maximizada {
					ic = icons.ActionFlipToFront
				}
				return botaoIcone(gtx, &t.maximizar, ic, tema.TopoSec, tema.TopoTxt)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoFechar(gtx, &t.fechar)
			}),
		)
	})
}

// botaoIcone é o botão quadrado só-ícone da barra de topo.
func botaoIcone(gtx layout.Context, btn *widget.Clickable, ic *widget.Icon, cor, corHover color.NRGBA) layout.Dimensions {
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				if btn.Hovered() {
					superficie(gtx, size, tema.VidroH, transparente, 5)
				}
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				c := cor
				if btn.Hovered() {
					c = corHover
				}
				return layout.UniformInset(5).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return icone(gtx, ic, c, 14)
				})
			}),
		)
	})
}

// botaoFechar: o único com hover vermelho. Vermelho marca a ação que
// DESTRÓI — aqui, descartar as sessões abertas.
func botaoFechar(gtx layout.Context, btn *widget.Clickable) layout.Dimensions {
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				if btn.Hovered() {
					superficie(gtx, size, hex(0xd13438), transparente, 5)
				}
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				c := tema.TopoSec
				if btn.Hovered() {
					c = hex(0xffffff)
				}
				return layout.UniformInset(5).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return icone(gtx, icons.NavigationClose, c, 14)
				})
			}),
		)
	})
}

// pilula: ícone + rótulo curto em mono, fundo de vidro, hover fixo da
// paleta. Altura própria, menor que a barra (.btn-topo no tema.py).
func pilula(gtx layout.Context, th *material.Theme, btn *widget.Clickable,
	ic *widget.Icon, rot string) layout.Dimensions {

	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				fundo, borda := tema.Vidro, tema.VidroB
				if btn.Hovered() {
					fundo = tema.VidroH
				}
				superficie(gtx, size, fundo, borda, 5)
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				cor := tema.TopoSec
				if btn.Hovered() {
					cor = tema.TopoTxt
				}
				return layout.Inset{Top: 3, Bottom: 3, Left: 5, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return icone(gtx, ic, cor, 13)
						}),
						layout.Rigid(layout.Spacer{Width: 4}.Layout),
						layout.Rigid(txt(th, fonteMono, spBtnTopo, rot, cor).Layout),
					)
				})
			}),
		)
	})
}

// botaoBarra é o botão de texto das ações da barra de filtro ("recolher
// tudo", "expandir tudo") — mesma regra: geometria fixa, estado só muda cor.
func botaoBarra(gtx layout.Context, th *material.Theme, btn *widget.Clickable, rot string) layout.Dimensions {
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				fundo := tema.Vidro2
				borda := tema.LuzB
				if btn.Hovered() {
					fundo, borda = tema.Vidro3, tema.Borda2
				}
				superficie(gtx, size, fundo, borda, 7)
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10}.Layout(gtx,
					txt(th, fonteMono, spSecundario, rot, tema.Sec).Layout)
			}),
		)
	})
}
