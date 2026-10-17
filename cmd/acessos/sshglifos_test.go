package main

import "testing"

// O que o terminal SSH mostra todo dia tem de ficar DENTRO do trecho (o
// shaper avança certo), e o que a Plex Mono não tem ou é largo tem de
// sair sozinho. Os caracteres de borda são o caso que mais importa: toda
// tela de ncurses é feita deles.
func TestGlifoNaGrade(t *testing.T) {
	naGrade := []rune("abc ç ã é ─│┌┐└┘├┤┬┴┼ ▒█ ≤≥ ° · ±")
	for _, r := range naGrade {
		if !glifoNaGrade(r) {
			t.Errorf("%q (U+%04X) devia caber no trecho", r, r)
		}
	}
	fora := []rune("日本語한국✔🙂Ａ")
	for _, r := range fora {
		if glifoNaGrade(r) {
			t.Errorf("%q (U+%04X) devia sair sozinho", r, r)
		}
	}
	if glifoNaGrade('\x1b') || glifoNaGrade(0) {
		t.Error("controle não é glifo")
	}
}
