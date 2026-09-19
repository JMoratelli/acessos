//go:build linux || windows

package main

import (
	"testing"

	"gioui.org/f32"
)

// A roda do mouse nunca foi encaminhada pra sessão remota — HandlePointer
// só tratava Move e botão, em RDP e VNC. Estes testes cobrem a tradução
// pro protocolo de cada um; a falta delas foi o que deixou o bug passar
// despercebido.

func TestPassosDaRodaRDP(t *testing.T) {
	casos := []struct {
		delta float32
		quer  int
	}{
		{-1, 1},  // roda pra cima -> passo positivo (WHEEL_DELTA do Windows)
		{-40, 1}, // sinal importa, não a magnitude (ver o comentário na função)
		{1, -1},  // roda pra baixo -> passo negativo
		{40, -1},
		{0, 0}, // sem scroll, sem passo
	}
	for _, c := range casos {
		if got := passosDaRoda(c.delta); got != c.quer {
			t.Errorf("passosDaRoda(%v) = %d, quer %d", c.delta, got, c.quer)
		}
	}
}

func TestMascaraDaRodaVNC(t *testing.T) {
	casos := []struct {
		nome   string
		scroll f32.Point
		quer   int
	}{
		{"cima", f32.Point{Y: -1}, 1 << 3},
		{"baixo", f32.Point{Y: 1}, 1 << 4},
		{"esquerda", f32.Point{X: -1}, 1 << 5},
		{"direita", f32.Point{X: 1}, 1 << 6},
		{"parado", f32.Point{}, 0},
		// vertical vence quando os dois vêm juntos (não deveria acontecer
		// com roda de mouse de verdade, mas o comportamento tem que ser
		// determinístico mesmo assim).
		{"os dois juntos, vertical vence", f32.Point{X: 1, Y: -1}, 1 << 3},
	}
	for _, c := range casos {
		if got := mascaraDaRoda(c.scroll); got != c.quer {
			t.Errorf("%s: mascaraDaRoda(%v) = %#x, quer %#x", c.nome, c.scroll, got, c.quer)
		}
	}
}
