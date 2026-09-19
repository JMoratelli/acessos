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

// lembrarGeral grava uma chave do [geral] — tema, fonte, lateral: as
// preferências DA PESSOA, não da sessão. Grava NA HORA do clique e não ao
// sair, pelo mesmo motivo do gravarPreferencia acima: fechar pelo botão
// da janela (ou uma queda) não pode custar a preferência.
//
// Existe para não repetir "confere caminhoINI, chama SalvarGeral, cospe o
// erro no stderr" em cada botão que lembra alguma coisa — foi assim que o
// TEMA ficou de fora: o A+ e a lateral gravavam, o tema só trocava a
// variável em memória e o app reabria sempre no claro (relatado no
// Windows, mas valia para os dois sistemas).
func lembrarGeral(chave, valor string) {
	if caminhoINI == "" {
		return
	}
	if err := conexoes.SalvarGeral(caminhoINI, map[string]string{chave: valor}); err != nil {
		fmt.Fprintf(os.Stderr, "gravar [geral] %s: %v\n", chave, err)
	}
}
