package main

import (
	"path/filepath"

	"acessos-go/internal/massa/model"
	"acessos-go/internal/massa/snippets"
)

// snippetsDoArquivo lê o snippets.ini. O formato é o mesmo do comandos.ini
// do Mass SSH Executer (descricao/plataforma/root/ignorar_exit/comando),
// então o leitor portado serve para os dois arquivos sem tradução.
func snippetsDoArquivo(caminho string) ([]model.Snippet, error) {
	return snippets.CarregarSnippets(caminho)
}

// caminhoSnippets acha o snippets.ini ao lado do conexoes.ini — é onde o
// app original guarda, e os dois têm que ler o MESMO arquivo.
func caminhoSnippets(iniConexoes string) string {
	if iniConexoes == "" {
		return "snippets.ini"
	}
	return filepath.Join(filepath.Dir(iniConexoes), "snippets.ini")
}
