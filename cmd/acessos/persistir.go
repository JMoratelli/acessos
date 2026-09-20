package main

import (
	"fmt"
	"os"
	"strconv"

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

// aplicarGeral põe de pé as preferências DA PESSOA que vêm do [geral]:
// tema e escala da interface. É o par de leitura do lembrarGeral acima.
//
// Existe como função única porque são DOIS processos que abrem janela e
// precisam das duas: o app (main.go) e o serviço do atalho global
// (servico_linux.go), que é um processo à parte e monta a caixa de busca
// sozinho. O serviço tinha ficado para trás — aplicava o tema e ignorava a
// fonte —, então quem usava o app com A+ abria a caixa e ela vinha em
// tamanho base, justamente para quem aumentou a letra por precisar dela.
// É a armadilha de arquivo irmão fora de sincronia que o CLAUDE.md
// descreve, e a saída é a mesma de sempre: um lugar só.
//
// A lateral NÃO entra aqui de propósito: é preferência do app, e o serviço
// não tem uma.
// Simétrica de propósito: "claro" VOLTA para o claro. No app isso nunca
// pesou (ele nasce claro e lê o .ini uma vez), mas o serviço relê a cada
// abertura da caixa — e sem o outro lado do if, trocar de escuro para
// claro no app não chegaria nunca na busca, enquanto o caminho contrário
// funcionava. Chave AUSENTE continua não mexendo em nada: quem não tem a
// preferência gravada fica com o que já está de pé.
func aplicarGeral(g map[string]string) {
	switch g["tema"] {
	case "escuro":
		tema = temaEscuro
	case "claro":
		tema = temaClaro
	}
	if n, err := strconv.Atoi(g["fonte"]); err == nil {
		nivelFonte = n
	}
}
