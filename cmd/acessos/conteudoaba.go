package main

// O desenho da área de conteúdo de uma aba, com o roteamento de ponteiro
// que vai junto.
//
// Mora num lugar só porque as DUAS janelas precisam dele: a principal, com
// a aba selecionada na tira, e a janela destacada, com a única aba que ela
// hospeda. O trecho tem detalhe demais para virar duas cópias — o
// ScrollRange abaixo é o exemplo: sem ele o Gio entrega delta zero e nada
// rola, e é o tipo de coisa que se corrige numa cópia e não na outra.

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
)

// rotearPonteiroEDesenhar entrega os eventos de ponteiro da área a t e
// depois desenha t. size é o tamanho da área, que as abas que escalam
// (VNC/RDP) usam para converter a coordenada.
func rotearPonteiroEDesenhar(gtx layout.Context, t Tab, tag event.Tag,
	size image.Point) layout.Dimensions {

	area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
	event.Op(gtx.Ops, tag)
	area.Pop()

	for {
		ev, ok := gtx.Source.Event(
			pointer.Filter{
				Target: tag,
				Kinds:  pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Scroll,
				// Sem ScrollX/ScrollY o Gio devolve e.Scroll SEMPRE
				// (0,0) — a faixa aqui não é um filtro de "quanto
				// aceitar", é o que HABILITA o valor de verdade chegar
				// (ver clampScroll no Gio). Sem isto, rolarHistorico
				// (SSH) e a roda de RDP/VNC (rdptab.go/vnctab.go) nunca
				// recebiam delta nenhum, mesmo checando ev.Scroll
				// certinho.
				ScrollX: pointer.ScrollRange{Min: -1000, Max: 1000},
				ScrollY: pointer.ScrollRange{Min: -1000, Max: 1000},
			},
		)
		if !ok {
			break
		}
		if pe, ok := ev.(pointer.Event); ok && t != nil {
			t.HandlePointer(pe, size)
		}
	}

	if t == nil {
		return layout.Dimensions{Size: size}
	}
	return t.Layout(gtx)
}
