package main

import (
	"image"
	"testing"

	"acessos-go/internal/conexoes"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

func painelDeTeste(t *testing.T) (*dashTab, func(...any)) {
	t.Helper()
	tema = temaClaro
	th := material.NewTheme()
	th.Shaper = shaperDoApp()
	temaApp = th

	arq := &conexoes.Arquivo{Conexoes: []conexoes.Conexao{
		{Nome: "CAIXA101", Host: "10.0.0.1", Grupo: []string{"Loja 01", "Caixas"},
			VNC: conexoes.AcessoVNC{Porta: 5900, Ligado: true},
			SSH: conexoes.AcessoSSH{Porta: 22, Usuario: "zanthus", Ligado: true}},
	}}
	d := newDashTab(th, arq, "teste.ini", func(conexoes.Conexao, conexoes.Protocolo) {}, func() int { return 0 })
	d.expandirTudo()
	t.Logf("grupos na árvore: %d", len(d.arvore))
	for _, g := range d.arvore {
		t.Logf("  grupo %v com %d conexões e %d filhos", g.Caminho, len(g.Conexoes), len(g.Filhos))
	}
	return d, func(...any) {}
}

func TestPainelDesenhaCards(t *testing.T) {
	d, _ := painelDeTeste(t)
	var r input.Router
	var ops op.Ops
	quadro := func(evs ...pointer.Event) {
		ops.Reset()
		gtx := layout.Context{
			Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(900, 700)), Source: r.Source(),
		}
		d.Layout(gtx)
		r.Frame(gtx.Ops)
		for _, e := range evs {
			r.Queue(e)
		}
	}
	var pediu bool
	d.aoMenuCard = func(cx conexoes.Conexao, pos image.Point) { pediu = true }
	// O botão direito NÃO pode abrir conexão: só o menu.
	var abriu []conexoes.Protocolo
	d.abrir = func(_ conexoes.Conexao, p conexoes.Protocolo) { abriu = append(abriu, p) }

	quadro()
	for y := 60; y < 700 && !pediu; y += 15 {
		for x := 20; x < 900 && !pediu; x += 15 {
			pos := f32.Pt(float32(x), float32(y))
			quadro(pointer.Event{Kind: pointer.Move, Position: pos, Source: pointer.Mouse})
			quadro()
			if d.sobCursor != nil {
				t.Logf("hover em (%d,%d) -> %s", x, y, d.sobCursor.Nome)
				quadro(pointer.Event{Kind: pointer.Press, Position: pos, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary})
				quadro()
			}
		}
	}
	if !pediu {
		t.Fatal("botão direito sobre o card não pediu o menu")
	}
	if len(abriu) > 0 {
		t.Fatalf("botão direito ABRIU conexão(ões) %v — devia só abrir o menu", abriu)
	}
}

// Clicar no ícone de um protocolo abre SÓ aquele protocolo. O Clickable
// do card cobre os quatro ícones, então os dois disparam no mesmo clique
// — sem a marca de "foi num ícone", clicar em shell abria shell E tela.
func TestCliqueNoIconeAbreSoAquele(t *testing.T) {
	d, _ := painelDeTeste(t)
	var r input.Router
	var ops op.Ops
	quadro := func(evs ...pointer.Event) {
		ops.Reset()
		gtx := layout.Context{
			Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(900, 700)), Source: r.Source(),
		}
		d.Layout(gtx)
		r.Frame(gtx.Ops)
		for _, e := range evs {
			r.Queue(e)
		}
	}
	var abertos []conexoes.Protocolo
	d.abrir = func(_ conexoes.Conexao, p conexoes.Protocolo) { abertos = append(abertos, p) }

	quadro()
	// procura o ícone do SSH: varre a faixa de baixo dos cards
	for y := 60; y < 700 && len(abertos) == 0; y += 6 {
		for x := 20; x < 900 && len(abertos) == 0; x += 6 {
			pos := f32.Pt(float32(x), float32(y))
			quadro(pointer.Event{Kind: pointer.Move, Position: pos, Source: pointer.Mouse})
			quadro()
			if d.protoSobCursor != conexoes.SSH {
				continue
			}
			quadro(pointer.Event{Kind: pointer.Press, Position: pos, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary})
			quadro(pointer.Event{Kind: pointer.Release, Position: pos, Source: pointer.Mouse})
			quadro()
		}
	}
	if len(abertos) == 0 {
		t.Fatal("não achei o ícone do SSH para clicar")
	}
	if len(abertos) != 1 || abertos[0] != conexoes.SSH {
		t.Fatalf("clique no ícone do SSH abriu %v — devia abrir só [ssh]", abertos)
	}
}

// O card temporário (destino digitado que não está no inventário) não
// abre direto: passa pelo pedido de credenciais, senão a sessão nasce sem
// senha e morre com "autenticação recusada".
func TestCardRascunhoPedeCredenciais(t *testing.T) {
	d, _ := painelDeTeste(t)
	d.filtro.SetText("10.9.9.9")
	var r input.Router
	var ops op.Ops
	quadro := func(evs ...pointer.Event) {
		ops.Reset()
		gtx := layout.Context{
			Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(900, 700)), Source: r.Source(),
		}
		d.Layout(gtx)
		r.Frame(gtx.Ops)
		for _, e := range evs {
			r.Queue(e)
		}
	}
	var abertos []conexoes.Protocolo
	var pedidos []conexoes.Protocolo
	d.abrir = func(_ conexoes.Conexao, p conexoes.Protocolo) { abertos = append(abertos, p) }
	d.aoRascunho = func(_ conexoes.Conexao, p conexoes.Protocolo) { pedidos = append(pedidos, p) }

	quadro()
	for y := 60; y < 700 && len(pedidos) == 0; y += 6 {
		for x := 20; x < 900 && len(pedidos) == 0; x += 6 {
			pos := f32.Pt(float32(x), float32(y))
			quadro(pointer.Event{Kind: pointer.Move, Position: pos, Source: pointer.Mouse})
			quadro()
			if d.protoSobCursor != conexoes.RDP {
				continue
			}
			quadro(pointer.Event{Kind: pointer.Press, Position: pos, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary})
			quadro(pointer.Event{Kind: pointer.Release, Position: pos, Source: pointer.Mouse})
			quadro()
		}
	}
	if len(pedidos) == 0 {
		t.Fatal("clicar no protocolo do card temporário não pediu credenciais")
	}
	if pedidos[0] != conexoes.RDP {
		t.Fatalf("pediu credenciais para %v, esperado rdp", pedidos[0])
	}
	if len(abertos) > 0 {
		t.Fatalf("abriu %v sem passar pelas credenciais", abertos)
	}
}
