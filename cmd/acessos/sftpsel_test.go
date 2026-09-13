package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

func sftpDeTeste(t *testing.T, dir string) *sftpTab {
	t.Helper()
	tema = temaClaro
	th := material.NewTheme()
	th.Shaper = shaperDoApp()
	ab := &sftpTab{th: th}
	ab.local.caminho.SetText(dir)
	ab.local.sel = map[string]bool{}
	ab.remoto.remoto = true
	ab.remoto.sel = map[string]bool{}
	ab.listarLocal()
	return ab
}

func quadroSFTP(ab *sftpTab, r *input.Router, ops *op.Ops, evs ...pointer.Event) {
	ops.Reset()
	gtx := layout.Context{
		Ops: ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(1000, 600)), Source: r.Source(),
		Now: time.Now(),
	}
	ab.Layout(gtx)
	r.Frame(gtx.Ops)
	for _, e := range evs {
		r.Queue(e)
	}
}

// Clicar num arquivo tem que marcá-lo — antes o clique simples não fazia
// nada e a tela não dava retorno nenhum.
func TestCliqueMarcaArquivoNoSFTP(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ab := sftpDeTeste(t, dir)
	var r input.Router
	var ops op.Ops
	quadroSFTP(ab, &r, &ops)

	marcou := false
	for y := 40; y < 600 && !marcou; y += 4 {
		for x := 20; x < 480 && !marcou; x += 40 {
			pos := f32.Pt(float32(x), float32(y))
			quadroSFTP(ab, &r, &ops,
				pointer.Event{Kind: pointer.Press, Position: pos, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary},
				pointer.Event{Kind: pointer.Release, Position: pos, Source: pointer.Mouse})
			quadroSFTP(ab, &r, &ops)
			marcou = len(selecionados(&ab.local)) > 0
		}
	}
	if !marcou {
		t.Fatal("clicar num arquivo não marcou nada")
	}
	if n := len(selecionados(&ab.local)); n != 1 {
		t.Fatalf("clique simples marcou %d itens, esperado 1", n)
	}
}

// Entrar numa pasta com MENOS arquivos que a atual derrubava o app: a
// lista de botões era trocada no meio do laço que a estava percorrendo.
func TestNavegarParaPastaMenorNaoDerruba(t *testing.T) {
	dir := t.TempDir()
	dentro := filepath.Join(dir, "sub")
	if err := os.Mkdir(dentro, 0o755); err != nil {
		t.Fatal(err)
	}
	// a pasta de cima tem 20 arquivos, a de dentro tem 1
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(dir, string(rune('a'+i))+".txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dentro, "unico.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ab := sftpDeTeste(t, dir)
	var r input.Router
	var ops op.Ops
	quadroSFTP(ab, &r, &ops)
	if len(ab.local.btnItem) != 21 {
		t.Fatalf("esperava 21 entradas, veio %d", len(ab.local.btnItem))
	}
	// navega direto (é o que o duplo clique faz) e desenha de novo: sem a
	// correção, o quadro seguinte panica ao percorrer a lista antiga.
	ab.navegar(&ab.local, "sub")
	quadroSFTP(ab, &r, &ops)
	quadroSFTP(ab, &r, &ops)
	if len(ab.local.itens) != 1 {
		t.Fatalf("esperava 1 entrada dentro de sub, veio %d", len(ab.local.itens))
	}
}
