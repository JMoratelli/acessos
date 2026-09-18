package main

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
)

// TestCliqueNoIconeTambemEhCliqueNaLinha fixa o comportamento do Gio em que
// a correção de buscapop.go se apoia: um Clickable DENTRO de outro faz os
// DOIS dispararem no mesmo quadro.
//
// É por isso que a caixa de busca lê os ícones de protocolo antes da linha
// e descarta o clique da linha quando um ícone pegou. Sem isso, clicar no
// ícone do SSH abria o protocolo preferido (a linha), marcava a escolha
// como em andamento e a vez do ícone caía na guarda — que fechava a
// janela e levava junto a abertura pendente. Na tela: clicar em qualquer
// ícone fechava a caixa sem abrir nada.
//
// Se um dia uma versão nova do Gio parar de entregar o evento às duas
// camadas, é este teste que avisa — e aí a ordem de leitura pode voltar a
// ser a natural.
func TestCliqueNoIconeTambemEhCliqueNaLinha(t *testing.T) {
	var (
		r     input.Router
		ops   op.Ops
		linha widget.Clickable
		icone widget.Clickable
	)
	naLinha, naIcone := false, false

	// Mesmo vaivém dos outros testes de ponteiro daqui: desenha o quadro,
	// entrega ao roteador e SÓ ENTÃO enfileira o evento, que será lido no
	// quadro seguinte.
	quadro := func(evs ...pointer.Event) {
		ops.Reset()
		gtx := layout.Context{
			Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(200, 40)), Source: r.Source(),
		}
		// Os cliques são lidos ANTES do layout, como em buscapop.go: o
		// Layout do Clickable chama update() e DESCARTA o que achar, então
		// um Clicked() depois dele nunca vê nada.
		for icone.Clicked(gtx) {
			naIcone = true
		}
		for linha.Clicked(gtx) {
			naLinha = true
		}
		// linha de 200x40 com um ícone de 40x40 no fim, como a linha da
		// busca: nome à esquerda, fileira de protocolos à direita.
		linha.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			defer op.Offset(image.Pt(160, 0)).Push(gtx.Ops).Pop()
			gtx.Constraints = layout.Exact(image.Pt(40, 40))
			icone.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			})
			return layout.Dimensions{Size: image.Pt(200, 40)}
		})
		r.Frame(gtx.Ops)
		for _, e := range evs {
			r.Queue(e)
		}
	}

	no := f32.Pt(180, 20) // bem no meio do ícone
	quadro()
	// O Move antes do Press não é enfeite: é ele que põe o ponteiro
	// dentro das áreas (o Enter de cada Clickable) antes do clique.
	quadro(pointer.Event{Kind: pointer.Move, Position: no, Source: pointer.Mouse})
	quadro()
	quadro(pointer.Event{Kind: pointer.Press, Position: no,
		Source: pointer.Mouse, Buttons: pointer.ButtonPrimary})
	quadro(pointer.Event{Kind: pointer.Release, Position: no, Source: pointer.Mouse})
	quadro()

	if !naIcone {
		t.Fatal("o ícone não recebeu o próprio clique")
	}
	if !naLinha {
		t.Skip("este Gio não entrega mais o clique à linha que contém o ícone; " +
			"a ordem de leitura em buscapop.go pode ser simplificada")
	}
}
