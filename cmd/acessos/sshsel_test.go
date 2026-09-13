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
	"gioui.org/widget/material"

	"github.com/hinshun/vt10x"
)

// Arrastar o mouse sobre o terminal tem que selecionar o texto daquele
// trecho — é como se copia um erro de um log para colar num chamado.
func TestSelecaoPorArrastoNoTerminal(t *testing.T) {
	tema = temaClaro
	th := material.NewTheme()
	th.Shaper = shaperDoApp()

	ab := &sshTab{th: th, cols: 80, rows: 24, corpo: 13}
	ab.term = vt10x.New(vt10x.WithSize(80, 24))
	ab.term.Write([]byte("erro: falha ao montar /dev/sdb1"))

	var r input.Router
	var ops op.Ops
	quadro := func() {
		ops.Reset()
		gtx := layout.Context{
			Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(900, 500)), Source: r.Source(),
		}
		ab.Layout(gtx)
		r.Frame(gtx.Ops)
	}
	quadro() // mede a célula e guarda a geometria

	ab.mu.Lock()
	margem, avanco, ch := ab.margem, ab.avanco, ab.alturaCel
	ab.mu.Unlock()
	if avanco <= 0 || ch <= 0 {
		t.Fatal("geometria da grade não foi medida")
	}
	// centro da célula (col, 0)
	emCelula := func(col int) f32.Point {
		return f32.Pt(float32(float64(margem)+avanco*(float64(col)+0.5)),
			float32(margem+ch/2))
	}

	ab.HandlePointer(pointer.Event{Kind: pointer.Press, Buttons: pointer.ButtonPrimary,
		Position: emCelula(0), Source: pointer.Mouse}, image.Pt(900, 500))
	ab.HandlePointer(pointer.Event{Kind: pointer.Drag,
		Position: emCelula(3), Source: pointer.Mouse}, image.Pt(900, 500))
	ab.HandlePointer(pointer.Event{Kind: pointer.Release,
		Position: emCelula(3), Source: pointer.Mouse}, image.Pt(900, 500))

	texto, ok := ab.textoSelecionado()
	if !ok {
		t.Fatal("o arrasto não produziu seleção")
	}
	if texto != "erro" {
		t.Fatalf("selecionou %q, esperado %q", texto, "erro")
	}

	// clique simples desmarca
	ab.HandlePointer(pointer.Event{Kind: pointer.Press, Buttons: pointer.ButtonPrimary,
		Position: emCelula(10), Source: pointer.Mouse}, image.Pt(900, 500))
	ab.HandlePointer(pointer.Event{Kind: pointer.Release,
		Position: emCelula(10), Source: pointer.Mouse}, image.Pt(900, 500))
	if _, ok := ab.textoSelecionado(); ok {
		t.Fatal("clique simples devia limpar a seleção")
	}
}
