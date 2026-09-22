package main

import (
	"image"
	"testing"

	"acessos-go/internal/conexoes"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// O diálogo dá o foco ao primeiro campo quando abre — e PARA POR AÍ.
//
// Pedir o foco a cada quadro, que é o jeito ingênuo de fazer isso, não se
// manifesta como problema de foco: o segundo campo simplesmente não aceita
// clique nem Tab. O clique dá o foco, o quadro seguinte toma de volta, e
// quem está digitando conclui que o campo está quebrado. Foi assim que
// "Criar cofre" ficou sem como repetir a senha — e, no mesmo diálogo, sem
// como chegar aos botões pelo teclado.
//
// O teste vale para os três diálogos que pedem foco inicial; cada um tem o
// seu caso abaixo, porque a correção de um não conserta o outro sozinho
// (foi exatamente o que aconteceu: o efêmero tratou, o cofre não).

// quadros desenha n quadros do corpo e devolve o último contexto, já
// entregue ao roteador. antes roda DENTRO do quadro indicado, antes do
// layout — é onde o teste simula "o operador clicou neste campo".
func quadros(t *testing.T, r *input.Router, corpo func(layout.Context, *material.Theme) layout.Dimensions,
	n int, antes map[int]func(layout.Context)) layout.Context {
	t.Helper()
	th := material.NewTheme()
	th.Shaper = shaperDoApp()
	var ops op.Ops
	var gtx layout.Context
	for i := range n {
		ops.Reset()
		gtx = layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(380, 400)),
			Source:      r.Source(),
		}
		if f, ok := antes[i]; ok {
			f(gtx)
		}
		corpo(gtx, th)
		r.Frame(gtx.Ops)
	}
	return gtx
}

func TestCriarCofreNaoRoubaOFocoDoCampoDeBaixo(t *testing.T) {
	var r input.Router
	d := &dlgCofre{arq: &conexoes.Arquivo{}, criando: true}

	// Quadro 0 e 1: o diálogo abre e leva o foco ao primeiro campo, que é
	// o comportamento desejado e não pode se perder no conserto.
	gtx := quadros(t, &r, d.Corpo, 2, nil)
	if !gtx.Focused(&d.nova) {
		t.Fatal("ao abrir, o foco tinha de estar na senha nova")
	}

	// Quadro 2: o operador clica no campo de baixo (aqui, o efeito do
	// clique: o foco vai para ele). Quadro 3: o foco tem de continuar lá.
	gtx = quadros(t, &r, d.Corpo, 2, map[int]func(layout.Context){
		0: func(gtx layout.Context) {
			gtx.Execute(key.FocusCmd{Tag: &d.confirma})
		},
	})
	if !gtx.Focused(&d.confirma) {
		t.Fatal("o diálogo tomou o foco de volta: é o defeito em que " +
			"'repita a senha' não aceita clique nem Tab")
	}
}

// Trocar a senha mestra mostra três campos e tem o mesmo risco, por outro
// caminho do mesmo Corpo.
func TestTrocarSenhaMestraNaoRoubaOFoco(t *testing.T) {
	var r input.Router
	d := &dlgCofre{arq: &conexoes.Arquivo{}, trocando: true}

	gtx := quadros(t, &r, d.Corpo, 2, nil)
	if !gtx.Focused(&d.senha) {
		t.Fatal("ao abrir, o foco tinha de estar na senha atual")
	}
	gtx = quadros(t, &r, d.Corpo, 2, map[int]func(layout.Context){
		0: func(gtx layout.Context) {
			gtx.Execute(key.FocusCmd{Tag: &d.confirma})
		},
	})
	if !gtx.Focused(&d.confirma) {
		t.Fatal("o diálogo tomou o foco de volta ao trocar a senha mestra")
	}
}

// O diálogo da conexão efêmera já tinha uma guarda, mas ela só protegia os
// CAMPOS: sair deles pelo Tab (para os botões) devolvia o foco ao campo,
// então não havia como chegar em "Conectar" pelo teclado.
func TestEfemeraDeixaOFocoSairDosCampos(t *testing.T) {
	var r input.Router
	d := &dlgEfemera{proto: conexoes.VNC}

	gtx := quadros(t, &r, d.Corpo, 2, nil)
	if !gtx.Focused(&d.senha) {
		t.Fatal("ao abrir, o foco tinha de estar na senha")
	}
	gtx = quadros(t, &r, d.Corpo, 2, map[int]func(layout.Context){
		0: func(gtx layout.Context) {
			gtx.Execute(key.FocusCmd{Tag: &d.btnOk})
		},
	})
	if !gtx.Focused(&d.btnOk) {
		t.Fatal("o diálogo tomou o foco de volta: não há como alcançar " +
			"os botões pelo teclado")
	}
}
