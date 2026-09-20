package main

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// A barra de sessão é a faixa logo abaixo das abas, presente só nas abas
// de conexão: à esquerda o estado (chip + destino), à direita os controles
// da sessão e a geometria. É a mesma barra do app original (a `self.barra`
// de AbaBase em acessos.py): o operador precisa ver, sem abrir menu
// nenhum, se a sessão está viva, para onde ela aponta e em que resolução.
const barraSessaoAltura = unit.Dp(26)

// estadoSessao é o que a barra mostra à esquerda e à direita. Tipo é uma
// das chaves de corChip.
type estadoSessao struct {
	Chip  string // "ATIVO", "AGUARDE", "CAIU"
	Tipo  string // ok | atencao | erro | neutro
	Texto string // destino: 192.168.8.132:5900
	Geo   string // "1920x1080 1:1", "80x24"…
}

// abaSessao é implementada pelas abas que têm sessão remota. As que não
// têm (Painel) simplesmente não ganham a barra.
type abaSessao interface {
	EstadoSessao() estadoSessao
	ControlesSessao(gtx layout.Context, th *material.Theme) layout.Dimensions
}

func corChip(tipo string) (fundo, texto color.NRGBA) {
	switch tipo {
	case "ok":
		return tema.OkBg, tema.OkFg
	case "erro":
		return tema.ErroBg, tema.ErroFg
	case "atencao":
		return tema.AtencaoBg, tema.AtencaoFg
	}
	return tema.Vidro2, tema.Sec
}

// layoutBarraSessao desenha a faixa. `extras` são botões de JANELA (tela
// cheia, devolver a aba), e não da sessão: vão no extremo direito, depois
// da geometria, separados dos controles da sessão de propósito — um age
// sobre a máquina remota, o outro sobre a janela que a mostra.
//
// A MESMA barra serve a janela principal e a janela destacada. Ela já era
// "a barrinha fina do topo" pedida: 26dp, com chip de estado, destino e
// geometria. Duplicá-la para a tela cheia seria criar a segunda cópia de
// algo que já existe.
func layoutBarraSessao(gtx layout.Context, th *material.Theme, a abaSessao,
	extras ...layout.Widget) layout.Dimensions {
	h := gtx.Dp(barraSessaoAltura)
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h
	larg := gtx.Constraints.Max.X

	paint.FillShape(gtx.Ops, tema.Abas2, clip.Rect{Max: image.Pt(larg, h)}.Op())
	paint.FillShape(gtx.Ops, tema.Borda, clip.Rect{Min: image.Pt(0, h-1), Max: image.Pt(larg, h)}.Op())

	e := a.EstadoSessao()
	return layout.Inset{Left: 8, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = 0
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return chipEstado(gtx, th, e.Chip, e.Tipo)
			}),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			// O DESTINO é quem cede quando falta largura, e por isso é
			// Flexed e não Rigid: ele absorve a folga quando sobra espaço
			// (fazendo o resto encostar à direita, como antes) e encolhe
			// quando falta, em vez de empurrar os controles e os botões de
			// janela para fora da barra.
			//
			// Era Rigid com um espaçador flexível ao lado, e aí um nome de
			// máquina longo numa janela estreita zerava a largura de tudo
			// o que vinha depois — inclusive o ✕, que é o canto que o
			// operador procura para fechar. Ver barrasessao_test.go.
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return txt(th, fonteMono, spSecundario, e.Texto, tema.Sec).Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.ControlesSessao(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Width: 10}.Layout),
			layout.Rigid(txt(th, fonteMono, spSecundario, e.Geo, tema.Fraco).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if len(extras) == 0 {
					return layout.Dimensions{}
				}
				filhos := make([]layout.FlexChild, 0, len(extras)*2)
				for _, x := range extras {
					filhos = append(filhos,
						layout.Rigid(layout.Spacer{Width: 6}.Layout),
						layout.Rigid(x))
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.
					Layout(gtx, filhos...)
			}),
		)
	})
}

// chipEstado: caixinha de estado. Cor semântica, nunca decorativa — erro
// só aparece quando a sessão realmente caiu.
func chipEstado(gtx layout.Context, th *material.Theme, texto, tipo string) layout.Dimensions {
	fundo, cor := corChip(tipo)
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			superficie(gtx, gtx.Constraints.Min, fundo, transparente, 5)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 2, Bottom: 2, Left: 6, Right: 6}.Layout(gtx,
				negrito(txt(th, fonteMono, spBtnTopo, texto, cor)).Layout)
		},
	)
}

// botaoSessao é o botão de texto da barra ("Reconectar").
func botaoSessao(gtx layout.Context, th *material.Theme, btn *widget.Clickable, rot string) layout.Dimensions {
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				fundo, borda := tema.Vidro2, tema.LuzB
				if btn.Hovered() {
					fundo, borda = tema.Vidro3, tema.Borda2
				}
				superficie(gtx, gtx.Constraints.Min, fundo, borda, 6)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			},
			func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 3, Bottom: 3, Left: 8, Right: 8}.Layout(gtx,
					txt(th, fonteMono, spBtnTopo, rot, tema.Sec).Layout)
			},
		)
	})
}

// toggleSessao é indicador E botão no mesmo controle (gtk.md §6): ligado
// ganha a cor do assunto, desligado fica apagado. A caixa é a MESMA nos
// dois estados — estado muda só cor, nunca dimensão.
func toggleSessao(gtx layout.Context, th *material.Theme, btn *widget.Clickable,
	ic *widget.Icon, ligado bool, corLigado color.NRGBA) layout.Dimensions {

	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lado := gtx.Dp(20)
		gtx.Constraints.Min = image.Pt(lado, lado)
		gtx.Constraints.Max = image.Pt(lado, lado)

		fundo, borda, cor := transparente, tema.LuzB, tema.Fraco
		if ligado {
			fundo, borda, cor = tema.Vidro2, tema.Borda2, corLigado
		}
		if btn.Hovered() {
			fundo = tema.Vidro3
		}
		superficie(gtx, image.Pt(lado, lado), fundo, borda, 6)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return icone(gtx, ic, cor, 13)
		})
	})
}

// segmentado é o grupo de opções exclusivas (Encaixar | 1:1 | Dinâmico),
// no lugar de um combo: um combo GTK ignorava o tema no app original, e
// aqui o motivo é o mesmo — três opções cabem na barra e dizem em que
// estado a sessão está sem precisar abrir nada.
func segmentado(gtx layout.Context, th *material.Theme, btns []*widget.Clickable,
	rotulos []string, escolhido int) layout.Dimensions {

	var filhos []layout.FlexChild
	for i := range rotulos {
		i := i
		filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return btns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Background{}.Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						fundo, borda := transparente, transparente
						switch {
						case i == escolhido:
							fundo = tema.Vidro3
							borda = tema.Borda2
						case btns[i].Hovered():
							fundo = tema.Vidro2
						}
						superficie(gtx, gtx.Constraints.Min, fundo, borda, 5)
						return layout.Dimensions{Size: gtx.Constraints.Min}
					},
					func(gtx layout.Context) layout.Dimensions {
						cor := tema.Sec
						if i == escolhido {
							cor = tema.Texto
						}
						return layout.Inset{Top: 3, Bottom: 3, Left: 7, Right: 7}.Layout(gtx,
							txt(th, fonteMono, spBtnTopo, rotulos[i], cor).Layout)
					},
				)
			})
		}))
	}
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			superficie(gtx, gtx.Constraints.Min, tema.Vidro1, tema.LuzB, 6)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, filhos...)
		},
	)
}
