package main

import (
	"testing"

	"acessos-go/internal/conexoes"
)

// Inventário novo (o exemplo que o app escreve na primeira execução) não
// tem seção [cofre]: sem verificador, QUALQUER senha erra, e o cadeado
// ficava fechado para sempre. Nesse caso o diálogo precisa nascer no modo
// de criação.
func TestCofreNovoEntraEmModoCriacao(t *testing.T) {
	chaveiroAtual = nil
	vazio := &conexoes.Arquivo{Cofre: map[string]string{}}
	if temCofre(vazio) {
		t.Fatal("arquivo sem [cofre] não tem cofre")
	}
	comCofre := &conexoes.Arquivo{Cofre: map[string]string{
		"kdf": "pbkdf2", "salt": "c2Fs", "verificador": "dmVy",
	}}
	if !temCofre(comCofre) {
		t.Fatal("arquivo com verificador tem cofre")
	}
}
