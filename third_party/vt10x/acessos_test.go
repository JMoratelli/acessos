package vt10x

import (
	"bytes"
	"strings"
	"testing"
)

// Testes do patch acessos (ver PATCH.md): cada caso aqui é uma sequência
// que um programa de tela cheia manda de verdade e que, sem o patch,
// deixava a tela fora do lugar.

func novoTeste(cols, rows int) (*State, *bytes.Buffer) {
	var resp bytes.Buffer
	term := newTerminal(TerminalInfo{w: &resp, cols: cols, rows: rows})
	return term.State, &resp
}

func escrever(t *State, s string) {
	for _, c := range s {
		t.put(c)
	}
}

func linha(t *State, y int) string {
	var sb strings.Builder
	for x := 0; x < t.cols; x++ {
		sb.WriteRune(t.lines[y][x].Char)
	}
	return strings.TrimRight(sb.String(), " ")
}

func TestREPRepeteUltimoCaractere(t *testing.T) {
	st, _ := novoTeste(20, 3)
	escrever(st, "a\x1b[4bX")
	if got := linha(st, 0); got != "aaaaaX" {
		t.Fatalf("REP: %q", got)
	}
	// com desenho de linha ativo, repete o caractere já traduzido
	escrever(st, "\r\x1b[K\x1b(0q\x1b[2b\x1b(B|")
	if got := linha(st, 0); got != "───|" {
		t.Fatalf("REP com gfx: %q", got)
	}
}

func TestModoInsercaoEmpurraLinha(t *testing.T) {
	st, _ := novoTeste(10, 2)
	escrever(st, "abcd\x1b[1;2H\x1b[4hXY\x1b[4l")
	if got := linha(st, 0); got != "aXYbcd" {
		t.Fatalf("IRM: %q", got)
	}
}

func TestSOSIEscolhemG1(t *testing.T) {
	st, _ := novoTeste(10, 2)
	escrever(st, "\x1b)0q\x0eq\x0fq")
	if got := linha(st, 0); got != "q─q" {
		t.Fatalf("SO/SI: %q", got)
	}
	// DECSC/DECRC devolvem o conjunto junto com o cursor
	escrever(st, "\x1b7\x0e\x1b8q")
	if got := linha(st, 0); got != "q─qq" {
		t.Fatalf("DECRC não restaurou G0: %q", got)
	}
}

func TestPrefixoMaiorNaoViraSGR(t *testing.T) {
	st, _ := novoTeste(10, 2)
	escrever(st, "\x1b[31m\x1b[>4;2ma")
	if st.lines[0][0].FG != Color(1) {
		t.Fatalf("CSI >4;2m zerou a cor: FG=%v", st.lines[0][0].FG)
	}
}

func TestDARespondido(t *testing.T) {
	st, resp := novoTeste(10, 2)
	escrever(st, "\x1b[c\x1b[>c")
	if got := resp.String(); got != "\x1b[?62;22c\x1b[>0;0;0c" {
		t.Fatalf("DA: %q", got)
	}
}

func TestRISLimpaTelaInteira(t *testing.T) {
	st, _ := novoTeste(40, 4)
	escrever(st, strings.Repeat("x", 40)+"\x1bc")
	if got := linha(st, 0); got != "" {
		t.Fatalf("RIS deixou sobra: %q", got)
	}
}

func TestTabulacaoDepoisDeAlargar(t *testing.T) {
	st, _ := novoTeste(20, 2)
	st.resize(40, 2)
	escrever(st, "\x1b[1;30H\tx")
	if st.lines[0][32].Char != 'x' {
		t.Fatalf("tab depois de alargar não parou na coluna 32: %q", linha(st, 0))
	}
}

func TestED1LimpaLinhaZero(t *testing.T) {
	st, _ := novoTeste(10, 3)
	escrever(st, "aaaa\r\nbbbb\x1b[1J")
	if got := linha(st, 0); got != "" {
		t.Fatalf("ED 1 deixou a linha 0: %q", got)
	}
}

func TestLargoOcupaDuasColunas(t *testing.T) {
	st, _ := novoTeste(6, 2)
	escrever(st, "a日b")
	if st.lines[0][1].Char != '日' || st.lines[0][1].Mode&attrWide == 0 ||
		st.lines[0][2].Mode&attrWideDummy == 0 || st.lines[0][3].Char != 'b' {
		t.Fatalf("largo: %q", linha(st, 0))
	}
	// não cabe na última coluna: quebra antes
	escrever(st, "\x1b[1;6H日")
	if st.lines[1][0].Char != '日' {
		t.Fatalf("largo na borda não quebrou: %q / %q", linha(st, 0), linha(st, 1))
	}
}

func TestReversoEmAreaLimpa(t *testing.T) {
	st, _ := novoTeste(6, 1)
	escrever(st, "\x1b[7m\x1b[K")
	g := st.lines[0][0]
	if g.FG != DefaultBG || g.BG != DefaultFG {
		t.Fatalf("clear em reverso não trocou as cores: FG=%v BG=%v", g.FG, g.BG)
	}
}

func TestDECSCUSR(t *testing.T) {
	st, _ := novoTeste(6, 1)
	escrever(st, "\x1b[5 q")
	if st.CursorStyle() != CursorBar {
		t.Fatalf("estilo: %d", st.CursorStyle())
	}
	escrever(st, "\x1b[2 q")
	if st.CursorStyle() != CursorBlock {
		t.Fatalf("bloco: %d", st.CursorStyle())
	}
	escrever(st, "\x1b[0 q")
	if st.CursorStyle() != CursorDefault {
		t.Fatalf("estilo padrão: %d", st.CursorStyle())
	}
}
