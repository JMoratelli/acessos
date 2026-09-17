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

// TestSequenciaMouse cobre a montagem de bytes pros quatro protocolos que
// importam (legado com/sem SGR, roda, motion) sem precisar de sessão
// nenhuma — é conta pura.
func TestSequenciaMouse(t *testing.T) {
	casos := []struct {
		nome  string
		modo  vt10x.ModeFlag
		tipo  tipoEventoMouse
		botao int
		mods  modificadores
		x, y  int
		quer  []byte
	}{
		{
			nome: "sem modo nenhum: nada a mandar",
			modo: 0, tipo: mousePress, botao: 0, x: 0, y: 0,
			quer: nil,
		},
		{
			nome: "SGR: botao esquerdo pressionado em (0,0) -> 1;1",
			modo: vt10x.ModeMouseButton | vt10x.ModeMouseSgr,
			tipo: mousePress, botao: 0, x: 0, y: 0,
			quer: []byte("\x1b[<0;1;1M"),
		},
		{
			nome: "SGR: solta o mesmo botao -> minusculo m",
			modo: vt10x.ModeMouseButton | vt10x.ModeMouseSgr,
			tipo: mouseRelease, botao: 0, x: 0, y: 0,
			quer: []byte("\x1b[<0;1;1m"),
		},
		{
			nome: "SGR: roda pra cima -> codigo 64",
			modo: vt10x.ModeMouseButton | vt10x.ModeMouseSgr,
			tipo: mouseScrollUp, x: 4, y: 9,
			quer: []byte("\x1b[<64;5;10M"),
		},
		{
			nome: "SGR: Shift soma 4 ao codigo",
			modo: vt10x.ModeMouseButton | vt10x.ModeMouseSgr,
			tipo: mousePress, botao: 0, mods: modificadores{shift: true}, x: 0, y: 0,
			quer: []byte("\x1b[<4;1;1M"),
		},
		{
			nome: "legado sem SGR: botao esquerdo em (2,3)",
			modo: vt10x.ModeMouseButton,
			tipo: mousePress, botao: 0, x: 2, y: 3,
			quer: []byte{0x1b, '[', 'M', 0 + 32, 2 + 1 + 32, 3 + 1 + 32},
		},
		{
			nome: "legado sem SGR: release e sempre codigo 3",
			modo: vt10x.ModeMouseButton,
			tipo: mouseRelease, botao: 0, x: 0, y: 0,
			quer: []byte{0x1b, '[', 'M', 3 + 32, 1 + 32, 1 + 32},
		},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := sequenciaMouse(c.modo, c.tipo, c.botao, c.mods, c.x, c.y)
			if string(got) != string(c.quer) {
				t.Fatalf("got %q (%v), quer %q (%v)", got, got, c.quer, c.quer)
			}
		})
	}
}

// TestHandlePointerRelataQuandoRemotoPediu é o fim-a-fim sem sessão de
// verdade: liga o modo de mouse via uma sequência ANSI de verdade (como o
// htop manda ao abrir), clica, arrasta e solta — confere que os bytes
// certos saem por t.entrada em vez de virar seleção local.
func TestHandlePointerRelataQuandoRemotoPediu(t *testing.T) {
	tema = temaClaro
	th := material.NewTheme()
	th.Shaper = shaperDoApp()

	buf := &bufFechavel{}
	tab := &sshTab{th: th, cols: 80, rows: 24, corpo: 13, entrada: buf}
	tab.term = vt10x.New(vt10x.WithSize(80, 24))

	var r input.Router
	var ops op.Ops
	quadro := func() {
		ops.Reset()
		gtx := layout.Context{
			Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(900, 500)), Source: r.Source(),
		}
		tab.Layout(gtx)
		r.Frame(gtx.Ops)
	}
	quadro() // mede a grade, igual TestSelecaoPorArrastoNoTerminal

	tab.mu.Lock()
	margem, avanco, ch := tab.margem, tab.avanco, tab.alturaCel
	tab.mu.Unlock()
	emCelula := func(col int) f32.Point {
		return f32.Pt(float32(float64(margem)+avanco*(float64(col)+0.5)),
			float32(margem+ch/2))
	}

	// htop liga botao+motion+SGR ao abrir — é a sequência de verdade, não
	// uma constante inventada.
	tab.term.Write([]byte("\x1b[?1000h\x1b[?1002h\x1b[?1006h"))

	tab.HandlePointer(pointer.Event{Kind: pointer.Press, Buttons: pointer.ButtonPrimary,
		Position: emCelula(2), Source: pointer.Mouse}, image.Pt(900, 500))
	if _, ok := tab.textoSelecionado(); ok {
		t.Fatal("com modo de mouse ligado, o clique não devia virar seleção local")
	}
	if got := buf.String(); got != "\x1b[<0;3;1M" {
		t.Fatalf("press mandou %q, esperado \\x1b[<0;3;1M", got)
	}
	buf.Reset()

	tab.HandlePointer(pointer.Event{Kind: pointer.Drag,
		Position: emCelula(5), Source: pointer.Mouse}, image.Pt(900, 500))
	if got := buf.String(); got != "\x1b[<32;6;1M" {
		t.Fatalf("drag (motion) mandou %q, esperado \\x1b[<32;6;1M", got)
	}
	buf.Reset()

	tab.HandlePointer(pointer.Event{Kind: pointer.Release,
		Position: emCelula(5), Source: pointer.Mouse}, image.Pt(900, 500))
	if got := buf.String(); got != "\x1b[<0;6;1m" {
		t.Fatalf("release mandou %q, esperado \\x1b[<0;6;1m", got)
	}
}

// TestHandlePointerShiftForcaSelecaoLocal confere a válvula de escape:
// mesmo com o remoto pedindo mouse, Shift continua selecionando texto
// local — sem isto, copiar um erro de dentro do htop não teria como.
func TestHandlePointerShiftForcaSelecaoLocal(t *testing.T) {
	tema = temaClaro
	th := material.NewTheme()
	th.Shaper = shaperDoApp()

	buf := &bufFechavel{}
	tab := &sshTab{th: th, cols: 80, rows: 24, corpo: 13, entrada: buf}
	tab.term = vt10x.New(vt10x.WithSize(80, 24))
	tab.term.Write([]byte("erro: disco cheio"))

	var r input.Router
	var ops op.Ops
	gtx := layout.Context{
		Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(900, 500)), Source: r.Source(),
	}
	tab.Layout(gtx)
	r.Frame(gtx.Ops)

	tab.mu.Lock()
	margem, avanco, ch := tab.margem, tab.avanco, tab.alturaCel
	tab.mu.Unlock()
	emCelula := func(col int) f32.Point {
		return f32.Pt(float32(float64(margem)+avanco*(float64(col)+0.5)),
			float32(margem+ch/2))
	}

	tab.term.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	tab.mods.shift = true

	tab.HandlePointer(pointer.Event{Kind: pointer.Press, Buttons: pointer.ButtonPrimary,
		Position: emCelula(0), Source: pointer.Mouse}, image.Pt(900, 500))
	tab.HandlePointer(pointer.Event{Kind: pointer.Drag,
		Position: emCelula(3), Source: pointer.Mouse}, image.Pt(900, 500))
	tab.HandlePointer(pointer.Event{Kind: pointer.Release,
		Position: emCelula(3), Source: pointer.Mouse}, image.Pt(900, 500))

	if buf.Len() != 0 {
		t.Fatalf("com Shift não devia mandar nada pro remoto, mandou %q", buf.String())
	}
	texto, ok := tab.textoSelecionado()
	if !ok || texto != "erro" {
		t.Fatalf("Shift devia selecionar localmente; textoSelecionado()=%q,%v", texto, ok)
	}
}
