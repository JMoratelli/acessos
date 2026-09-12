package main

import (
	"fmt"
	"os"

	"acessos-go/internal/conexoes"
)

// Os botões de estado da barra de sessão (olho, ↻, modo de tela) mexem no
// .ini, como no app original: a preferência é DA MÁQUINA, não da janela.
// Quem marca "somente leitura" numa caixa de loja espera achar assim na
// próxima vez, inclusive abrindo pelo app Python.
//
// A gravação é a cirúrgica de internal/conexoes (uma linha, comentários
// preservados); erro só vai para o stderr — perder a preferência não pode
// derrubar a sessão que está na tela.
func gravarPreferencia(nomeConexao, chave, valor string) {
	if caminhoINI == "" || nomeConexao == "" {
		return
	}
	err := conexoes.Salvar(caminhoINI, nomeConexao, "", map[string]string{chave: valor})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gravar %s de %s: %v\n", chave, nomeConexao, err)
	}
}

func simNao(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
