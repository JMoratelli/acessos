package main

// Preferência do AUTOSTART do serviço, pela chave `[geral]
// atalho_autostart` do conexoes.ini:
//
//	(vazio)  nunca foi perguntado — o serviço pergunta uma vez e grava
//	1        o sistema sobe o serviço no login
//	0        recusado; não pergunta de novo
//
// Não confundir com `atalho_global` (ver atalhopref.go): aquela diz se o
// atalho EXISTE; esta diz se ele existe numa sessão em que ninguém abriu
// o app ainda.
//
// ----------------------------------------------------------------------
// POR QUE A CHAVE SOZINHA NÃO BASTA
// ----------------------------------------------------------------------
//
// Quem de fato sobe o serviço no login é o portal Background, não esta
// chave: ela só registra a resposta para não repetir a pergunta. Mudar o
// valor aqui e parar por aí seria mentir para quem clicou — o sistema
// continuaria subindo o serviço com a caixa desmarcada. Por isso quem
// troca a preferência tem de falar com o portal também
// (definirAutostartNoSistema), e só gravar depois que ele responder.

import "acessos-go/internal/conexoes"

const chaveAtalhoAutostart = "atalho_autostart"

// estadoAutostart é o que a chave guarda. O terceiro valor existe porque
// "nunca perguntado" e "recusado" levam a caminhos diferentes: o primeiro
// ainda vai abrir o diálogo do portal no próximo start do serviço, o
// segundo não abre nunca mais sozinho.
type estadoAutostart int

const (
	autostartNaoPerguntado estadoAutostart = iota
	autostartLigado
	autostartRecusado
)

// lerAutostart lê a preferência. Problema de leitura devolve "não
// perguntado", que é o estado que ainda leva à pergunta — melhor
// perguntar de novo que afirmar um "não" que ninguém disse.
func lerAutostart(caminhoINI string) estadoAutostart {
	arq, err := conexoes.Carregar(caminhoINI)
	if err != nil {
		return autostartNaoPerguntado
	}
	switch arq.Geral[chaveAtalhoAutostart] {
	case "1":
		return autostartLigado
	case "0":
		return autostartRecusado
	}
	return autostartNaoPerguntado
}

// salvarAutostart grava só "1" ou "0": voltar ao estado "nunca
// perguntado" não é oferecido de propósito, porque ele não significa
// nada para quem clica — significa "vou te perguntar de novo depois", e
// quem está nos Ajustes já está respondendo agora.
func salvarAutostart(caminhoINI string, ligado bool) error {
	valor := "0"
	if ligado {
		valor = "1"
	}
	return conexoes.SalvarGeral(caminhoINI, map[string]string{chaveAtalhoAutostart: valor})
}

// definirAutostartNoSistema pede ou revoga o autostart no sistema e
// devolve o que o sistema decidiu de fato (que pode ser diferente do
// pedido: o diálogo é do desktop, não nosso).
//
// Nil onde a ideia não existe — o autostart do serviço é coisa do portal
// Background, e fora do Linux não há serviço à parte para subir (ver
// atalhoglobal_windows.go). Quem desenha os Ajustes usa o nil para não
// oferecer uma caixa que não faria nada.
var definirAutostartNoSistema func(quer bool) (bool, error)
