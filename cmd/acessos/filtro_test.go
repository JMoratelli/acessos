package main

import (
	"testing"

	"acessos-go/internal/conexoes"
)

// algumCasa e filtrar TÊM de concordar sempre: o primeiro existe só para
// responder "tem alguma?" sem montar a lista, e se os dois divergirem a
// busca passa a mostrar um card de destino avulso enquanto ainda há
// máquina casando (ou o contrário).
func TestAlgumCasaConcordaComFiltrar(t *testing.T) {
	lista := []conexoes.Conexao{
		{Nome: "CAIXA01", Host: "10.0.0.1", Grupo: []string{"Loja 01", "Caixas"}},
		{Nome: "SERV-AD", Host: "10.0.0.250", Grupo: []string{"Servidores"}},
		{Nome: "fc52002-lj06", Host: "10.6.0.2", Grupo: []string{"Loja 06"}},
	}
	termos := []string{
		"", "caixa", "CAIXA", "10.0.0.", "loja 01", "servidores",
		"lj06", "naoexiste", "10.9", " ", "-",
	}
	for _, termo := range termos {
		quer := len(filtrar(lista, termo)) > 0
		if got := algumCasa(lista, termo); got != quer {
			t.Errorf("termo %q: algumCasa=%v, filtrar dá %d resultado(s)",
				termo, got, len(filtrar(lista, termo)))
		}
	}
	// lista vazia: os dois dizem "não tem", inclusive com termo vazio
	for _, termo := range []string{"", "caixa"} {
		if algumCasa(nil, termo) {
			t.Errorf("lista vazia com termo %q devia dar false", termo)
		}
	}
}
