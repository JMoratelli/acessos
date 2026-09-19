package main

// O que a caixa de busca escolheu, aberto na janela grande.
//
// Tudo aqui roda NO LAÇO da janela principal (chegou por
// naJanelaPrincipal — ver filajanela.go): abrirConexao mexe na tira de
// abas e cria processo filho, e o caminho dos diálogos de credencial lê
// estado que o laço escreve a cada quadro.

import (
	"fmt"
	"os"
	"strings"

	"acessos-go/internal/conexoes"

	"gioui.org/app"
	"gioui.org/io/system"
)

// abrirEscolhaDaBusca abre a máquina escolhida e traz a janela para a
// frente.
//
// A escolha entra pelos MESMOS caminhos do Painel, e isso é o ponto:
// antes a busca chamava abrirConexao direto e pulava as duas perguntas
// que o Painel faz — credencial de destino avulso e senha mestra de cofre
// trancado. Uma máquina sem senha disponível simplesmente falhava com
// "autenticação recusada", sem dizer o que fazer, e o VNC era onde mais
// doía. Agora:
//
//   - destino AVULSO (digitado, sem cadastro) -> pedirCredenciaisEfemeras,
//     igual ao card de destino avulso do Painel;
//   - máquina CADASTRADA -> abrirConexao, que já sabe pedir a senha mestra
//     quando a senha está no cofre e o cofre está trancado.
func abrirEscolhaDaBusca(w *app.Window, bar *tabBar, painel *dashTab,
	cx conexoes.Conexao, p conexoes.Protocolo, avulso bool, token string) {

	arq := painel.arq
	defer trazerParaFrente(w, token)

	// A máquina cadastrada viaja só pelo NOME (ver alvoAbrir): quem tem a
	// versão boa dos dados — usuário, porta, senha, cofre — é o
	// inventário DESTE processo, não o de quem mandou.
	if !avulso {
		if c, ok := porNome(arq, cx.Nome); ok {
			abrirConexao(w, bar, arq, c, p)
			w.Invalidate()
			return
		}
		// Sumiu do inventário entre a busca e a escolha (alguém editou o
		// .ini no meio). Tratar como avulso é melhor que não abrir nada:
		// o host ainda é o que a pessoa escolheu, só falta credencial.
		if cx.Host == "" {
			fmt.Fprintf(os.Stderr, "busca: %q não está mais no inventário\n", cx.Nome)
			w.Invalidate()
			return
		}
	}

	pedirCredenciaisEfemeras(w, cx, p, func(c conexoes.Conexao) {
		abrirConexao(w, bar, arq, c, p)
	})
	w.Invalidate()
}

// aplicarPedidoDeAbrir trata o que chegou pelo socket: ou uma escolha da
// caixa de busca, ou as conexões da linha de comando de uma SEGUNDA
// invocação do app (`acessos -conn ...` com uma janela já aberta).
func aplicarPedidoDeAbrir(w *app.Window, bar *tabBar, painel *dashTab, m mensagem) {
	// Só trazer para a frente: é a segunda metade da escolha na caixa de
	// busca, que chega depois da abertura porque o token de ativação
	// demora o quanto o compositor quiser (ver buscapop.go).
	if m.Tipo == msgAtivar {
		trazerParaFrente(w, m.Token)
		return
	}
	if a := m.Alvo; a != nil {
		cx := conexoes.Conexao{Nome: a.Nome}
		if a.Conexao != nil {
			cx = *a.Conexao
		}
		abrirEscolhaDaBusca(w, bar, painel, cx, conexoes.Protocolo(a.Protocolo), a.Avulso, m.Token)
		return
	}
	for _, spec := range m.Specs {
		t, err := newTab(w, spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "conexão inválida (%v): %v\n", spec, err)
			continue
		}
		bar.append(t)
	}
	// Sem specs e sem alvo é a segunda invocação pedindo só atenção
	// ("abriram o Acessos de novo"): trazer a janela para a frente é
	// exatamente a resposta certa.
	trazerParaFrente(w, m.Token)
	w.Invalidate()
}

// trazerParaFrente usa o token de ativação quando há um: no Wayland é a
// ÚNICA forma de uma janela se pôr à frente, e ele foi pedido pela caixa
// de busca enquanto ela ainda tinha o foco.
//
// As duas saídas passam por foraDoQuadro porque AtivarCom/Perform vão
// parar no Window.Run, que no Windows reentra no windowProc — mesmo
// congelamento do botão de minimizar; ver acaojanela.go. Continua
// valendo mesmo agora que o drenarFilaJanela() roda no topo do laço:
// quem chama isto também pode ser um quadro (o Painel, por exemplo).
func trazerParaFrente(w *app.Window, token string) {
	if token != "" {
		foraDoQuadro(func() {
			if err := w.AtivarCom(token); err != nil {
				fmt.Fprintf(os.Stderr, "busca: %v\n", err)
			}
		})
		return
	}
	foraDoQuadro(func() { w.Perform(system.ActionRaise) })
}

func porNome(arq *conexoes.Arquivo, nome string) (conexoes.Conexao, bool) {
	if arq == nil {
		return conexoes.Conexao{}, false
	}
	for _, c := range arq.Conexoes {
		if strings.EqualFold(c.Nome, nome) {
			return c, true
		}
	}
	return conexoes.Conexao{}, false
}
