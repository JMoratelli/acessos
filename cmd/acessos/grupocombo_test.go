package main

import (
	"reflect"
	"strings"
	"testing"

	"acessos-go/internal/conexoes"
)

func arqDeTeste() *conexoes.Arquivo {
	return &conexoes.Arquivo{Conexoes: []conexoes.Conexao{
		{Nome: "CX1", Grupo: []string{"Loja 06", "Caixas"}},
		{Nome: "CX2", Grupo: []string{"Loja 06", "Caixas"}},
		{Nome: "SRV", Grupo: []string{"Loja 06", "Servidores"}},
		{Nome: "SOLTA", Grupo: []string{"Sem grupo"}},
		{Nome: "ADM", Grupo: []string{"Administrativo"}},
	}}
}

func TestGruposDoArquivoIncluiAncestrais(t *testing.T) {
	// "Loja 06" não é o grupo de NENHUMA máquina — só aparece como pai.
	// Ele ainda é um destino válido, e sem os ancestrais a sugestão não o
	// ofereceria nunca.
	quer := []string{"Administrativo", "Loja 06", "Loja 06;Caixas", "Loja 06;Servidores"}
	if got := gruposDoArquivo(arqDeTeste()); !reflect.DeepEqual(got, quer) {
		t.Fatalf("saiu %q, queria %q", got, quer)
	}
}

func TestGruposDoArquivoIgnoraSemGrupo(t *testing.T) {
	// "Sem grupo" é o rótulo que caminhoGrupo inventa para quem não tem
	// grupo — sugeri-lo faria o operador gravar a string literal no .ini
	// e criar um grupo de verdade com esse nome.
	for _, g := range gruposDoArquivo(arqDeTeste()) {
		if g == "Sem grupo" {
			t.Fatal("ofereceu 'Sem grupo' como destino")
		}
	}
}

func TestGruposDoArquivoSemArquivo(t *testing.T) {
	if got := gruposDoArquivo(nil); got != nil {
		t.Fatalf("queria nada, saiu %q", got)
	}
}

func TestFiltrarGruposCasaNoMeio(t *testing.T) {
	todos := gruposDoArquivo(arqDeTeste())
	got := filtrarGrupos(todos, "caixa")
	// O termo útil é o último galho, não o prefixo do caminho.
	if len(got) != 1 || got[0] != "Loja 06;Caixas" {
		t.Fatalf("saiu %q", got)
	}
	if n := len(filtrarGrupos(todos, "LOJA")); n != 3 {
		t.Fatalf("ignorou maiúsculas: %d achados", n)
	}
}

func TestFiltrarGruposNaoSugereOQueJaEstaEscrito(t *testing.T) {
	todos := gruposDoArquivo(arqDeTeste())
	for _, g := range filtrarGrupos(todos, "Loja 06;Caixas") {
		if strings.EqualFold(g, "Loja 06;Caixas") {
			t.Fatal("ofereceu exatamente o que já está no campo")
		}
	}
}

func TestCasaComTermoUsaDescricao(t *testing.T) {
	cx := conexoes.Conexao{
		Nome:      "CX1",
		Host:      "10.0.0.1",
		Grupo:     []string{"Loja 06", "Caixas"},
		Descricao: "impressora fiscal",
	}
	if !casaComTermo(cx, "fiscal") {
		t.Fatal("a descrição ficou de fora do casamento")
	}
	if casaComTermo(cx, "inexistente") {
		t.Fatal("casou com termo que não está em lugar nenhum")
	}
	// Sem descrição, nada muda para quem já usava a busca.
	cx.Descricao = ""
	if casaComTermo(cx, "fiscal") {
		t.Fatal("casou com descrição vazia")
	}
}
