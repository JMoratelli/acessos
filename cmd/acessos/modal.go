package main

import (
	"image"
	"sync"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Um modal por vez, guardado num global porque a janela é uma só. Enquanto
// há modal aberto, o teclado cru do Wayland NÃO vai para a aba (senão o que
// se digita na senha mestra cairia dentro da sessão VNC/RDP) — ver main.go.
//
// O acesso é protegido por mutex porque o runner da execução em massa
// abre diálogo DE OUTRA GOROUTINE (ele bloqueia esperando a resposta do
// operador); sem isso haveria corrida com o layout, que roda na goroutine
// da janela.
var (
	modalMu    sync.Mutex
	modalAtual dialogo
)

// dialogo é o que um modal precisa saber fazer. O corpo é desenhado dentro
// de um cartão OPACO e centralizado: vidro em modal deixa o conteúdo da
// janela aparecer atrás do texto que se precisa ler (gtk.md §8).
type dialogo interface {
	Titulo() string
	Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions
	Largura() unit.Dp
}

func abrirDialogo(d dialogo) {
	modalMu.Lock()
	modalAtual = d
	modalMu.Unlock()
}

func fecharDialogo() {
	modalMu.Lock()
	modalAtual = nil
	modalMu.Unlock()
}

func dialogoAtual() dialogo {
	modalMu.Lock()
	defer modalMu.Unlock()
	return modalAtual
}

func temDialogo() bool { return dialogoAtual() != nil }

var tagModal = new(int)

// layoutModal desenha o véu e o cartão por cima de TUDO. O véu também
// engole os eventos de ponteiro: sem isso dá pra clicar num card do painel
// com o diálogo aberto na frente.
func layoutModal(gtx layout.Context, th *material.Theme) layout.Dimensions {
	d := dialogoAtual()
	if d == nil {
		return layout.Dimensions{}
	}
	size := gtx.Constraints.Max

	veu := tema.Palco
	veu.A = 150
	paint.FillShape(gtx.Ops, veu, clip.Rect{Max: size}.Op())

	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, tagModal)
	area.Pop()
	for {
		ev, ok := gtx.Source.Event(pointer.Filter{
			Target: tagModal,
			Kinds:  pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Scroll,
		})
		if !ok || ev == nil {
			break
		}
	}

	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		larg := gtx.Dp(d.Largura())
		if larg > size.X-gtx.Dp(24) {
			larg = size.X - gtx.Dp(24)
		}
		gtx.Constraints.Min.X = larg
		gtx.Constraints.Max.X = larg
		gtx.Constraints.Min.Y = 0

		macro := op.Record(gtx.Ops)
		dims := layout.Inset{Top: 14, Bottom: 14, Left: 16, Right: 16}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(negrito(txt(th, fonteCond, spSubgrupo, d.Titulo(), tema.Texto)).Layout),
					layout.Rigid(layout.Spacer{Height: 10}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.Corpo(gtx, th)
					}),
				)
			})
		conteudo := macro.Stop()

		// peso vem da SOMBRA, não de vidro: o cartão é opaco.
		sombra(gtx, dims.Size, 12)
		superficie(gtx, dims.Size, tema.Cartao, tema.Borda, 12)
		conteudo.Add(gtx.Ops)
		return dims
	})
}

// campoSenha é o editor mascarado dos diálogos de senha.
func campoSenha(gtx layout.Context, th *material.Theme, ed *widget.Editor, dica string) layout.Dimensions {
	marcarFoco(gtx.Focused(ed))
	ed.SingleLine = true
	ed.Submit = true
	ed.Mask = '•'
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			superficie(gtx, gtx.Constraints.Min, tema.Campo, tema.Borda2, 8)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 8, Bottom: 8, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				e := material.Editor(th, ed, dica)
				e.Font = fonteMono
				e.TextSize = spCorpo
				e.Color = tema.Texto
				e.HintColor = tema.Fraco
				return e.Layout(gtx)
			})
		},
	)
}

// espaco devolve um separador vertical simples.
func espaco(h unit.Dp) layout.FlexChild {
	return layout.Rigid(layout.Spacer{Height: h}.Layout)
}

var _ = image.Point{}
