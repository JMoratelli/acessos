package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// A caixa de busca NÃO pode desenhar com o Theme da janela principal.
//
// Ela é uma segunda janela, com laço em goroutine própria, e o text.Shaper
// que mora dentro do Theme é um cache sem trava: o Gio avisa em
// text/shaper.go que duas goroutines nele dão panic, e panic de mapa não
// devolve erro — derruba o processo inteiro, com as sessões remotas
// abertas junto. O defeito só aparece com a caixa na tela por cima de uma
// aba recebendo dados, que é o uso normal do atalho global, e some em
// qualquer teste de janela única.
//
// Por isso a guarda é ESTÁTICA: a chamada tem de passar um Theme que não
// seja o da janela principal (`th` ou o global `temaApp`). Quem escrever
// `abrirJanelaBusca(th, ...)` de novo quebra o build, não a máquina de
// quem estiver usando.
func TestBuscaNaoDesenhaComOThemeDaJanelaPrincipal(t *testing.T) {
	proibidos := map[string]bool{"th": true, "temaApp": true}

	arqs, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	chamadas := 0
	for _, arq := range arqs {
		// Os arquivos de outra plataforma entram também: servico_linux.go
		// é um deles, e é justamente quem mais abre a caixa.
		f, err := parser.ParseFile(fset, arq, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", arq, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			ch, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := ch.Fun.(*ast.Ident)
			if !ok || id.Name != "abrirJanelaBusca" || len(ch.Args) == 0 {
				return true
			}
			chamadas++
			arg, ok := ch.Args[0].(*ast.Ident)
			if !ok {
				return true // s.th e afins: campo, não o Theme da principal
			}
			if proibidos[arg.Name] {
				p := fset.Position(ch.Pos())
				t.Errorf("%s:%d: abrirJanelaBusca(%s, ...) — esse é o Theme da "+
					"janela principal, e o shaper dele não aguenta duas goroutines; "+
					"use o temaBusca (ver tema.go)",
					filepath.Base(p.Filename), p.Line, arg.Name)
			}
			return true
		})
	}
	if chamadas == 0 {
		t.Fatal("nenhuma chamada a abrirJanelaBusca encontrada — o teste parou de " +
			"vigiar alguma coisa (renomearam a função?)")
	}
	t.Logf("%d chamada(s) conferida(s)", chamadas)
}
