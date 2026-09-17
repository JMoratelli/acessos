package main

import (
	"strconv"
	"strings"

	"acessos-go/internal/conexoes"
)

// Conexão efêmera: conectar num destino DIGITADO, sem cadastrar. É o caso
// de "me passaram um IP no chat" — cadastrar antes de acessar é burocracia
// que ninguém faz, e o resultado é gente usando outro programa.
//
// Formas aceitas (mesma precedência do app original):
//
//	10.1.1.99            -> VNC 5900     (o padrão)
//	10.1.1.99:22         -> SSH          (a porta decide)
//	zanthus@10.1.1.99    -> SSH          (usuário@ implica shell)
//	rdp serv-ad          -> RDP          (prefixo explícito manda)
//
// Precedência: prefixo > porta > usuario@ > VNC.
//
// Nada disso é gravado: a conexão vive enquanto a aba existir.
func interpretarAlvo(texto string) (conexoes.Conexao, conexoes.Protocolo, bool) {
	t := strings.TrimSpace(texto)
	if t == "" {
		return conexoes.Conexao{}, "", false
	}

	proto := conexoes.Protocolo("")
	// 1) prefixo explícito
	if campos := strings.Fields(t); len(campos) == 2 {
		switch strings.ToLower(campos[0]) {
		case "vnc", "tela":
			proto, t = conexoes.VNC, campos[1]
		case "ssh", "shell":
			proto, t = conexoes.SSH, campos[1]
		case "rdp":
			proto, t = conexoes.RDP, campos[1]
		case "sftp", "arquivos":
			proto, t = conexoes.SFTP, campos[1]
		}
	}

	usuario := ""
	if i := strings.LastIndex(t, "@"); i > 0 {
		usuario, t = t[:i], t[i+1:]
	}

	host, porta := t, 0
	if i := strings.LastIndex(t, ":"); i > 0 {
		if p, err := strconv.Atoi(t[i+1:]); err == nil {
			host, porta = t[:i], p
		}
	}
	if host == "" {
		return conexoes.Conexao{}, "", false
	}

	// 2) a porta decide, quando não houve prefixo
	if proto == "" && porta > 0 {
		switch porta {
		case 22:
			proto = conexoes.SSH
		case 3389:
			proto = conexoes.RDP
		default:
			proto = conexoes.VNC
		}
	}
	// 3) usuario@ implica shell
	if proto == "" && usuario != "" {
		proto = conexoes.SSH
	}
	// 4) padrão
	if proto == "" {
		proto = conexoes.VNC
	}

	cx := conexoes.Conexao{Nome: host, Host: host, Grupo: []string{"temporária"}}
	switch proto {
	case conexoes.VNC:
		if porta == 0 {
			porta = 5900
		}
		cx.VNC = conexoes.AcessoVNC{Ligado: true, Porta: porta, Usuario: usuario, Modo: "encaixar"}
	case conexoes.SSH, conexoes.SFTP:
		if porta == 0 {
			porta = 22
		}
		cx.SSH = conexoes.AcessoSSH{Ligado: true, Porta: porta, Usuario: usuario}
	case conexoes.RDP:
		if porta == 0 {
			porta = 3389
		}
		cx.RDP = conexoes.AcessoRDP{Ligado: true, Porta: porta, Usuario: usuario, Tela: "dinamico"}
	}
	return cx, proto, true
}

// alvoRascunho monta a conexão TEMPORÁRIA que o Painel mostra como card
// enquanto se digita um destino que não está cadastrado. semResultados
// diz se a busca em curso não casou com nenhuma máquina do inventário —
// ver ehDestinoPlausivel.
//
// Diferença para interpretarAlvo: aqui os quatro protocolos nascem
// ligados, porque o card existe justamente para o operador ESCOLHER pelo
// ícone — o palpite do texto ("10.1.1.9:22" é shell) vira só a porta
// daquele protocolo, não uma decisão tomada por ele.
func alvoRascunho(texto string, semResultados bool) (conexoes.Conexao, bool) {
	cx, proto, ok := interpretarAlvo(texto)
	if !ok || !ehDestinoPlausivel(texto, semResultados) {
		return conexoes.Conexao{}, false
	}
	usuario := ""
	switch proto {
	case conexoes.VNC:
		usuario = cx.VNC.Usuario
	case conexoes.SSH, conexoes.SFTP:
		usuario = cx.SSH.Usuario
	case conexoes.RDP:
		usuario = cx.RDP.Usuario
	}
	porta := func(p conexoes.Protocolo, padrao int) int {
		if proto != p {
			return padrao
		}
		switch p {
		case conexoes.VNC:
			return cx.VNC.Porta
		case conexoes.SSH:
			return cx.SSH.Porta
		case conexoes.RDP:
			return cx.RDP.Porta
		}
		return padrao
	}
	cx.VNC = conexoes.AcessoVNC{Ligado: true, Porta: porta(conexoes.VNC, 5900),
		Usuario: usuario, Modo: "encaixar"}
	cx.SSH = conexoes.AcessoSSH{Ligado: true, Porta: porta(conexoes.SSH, 22), Usuario: usuario}
	cx.RDP = conexoes.AcessoRDP{Ligado: true, Porta: porta(conexoes.RDP, 3389),
		Usuario: usuario, Tela: "dinamico"}
	return cx, true
}

// ehDestinoPlausivel decide se o texto digitado vira card de "conectar
// sem cadastrar". São duas perguntas diferentes, e por isso dois casos:
//
//   - forma inequívoca de endereço (IP, host:porta, usuario@host, nome
//     com domínio, prefixo explícito "rdp serv-ad"): vira destino
//     SEMPRE, mesmo com a busca ainda casando com máquinas do
//     inventário. Quem digita "10.1.1.99" já disse aonde quer ir.
//   - nome cru, sem ponto, porta ou usuário ("fc52002-lj06"): só vira
//     destino quando a busca NÃO casou com nada. Enquanto houver
//     máquina cadastrada na tela o texto é filtro; quando a lista
//     esvazia não há mais o que filtrar, e oferecer a conexão avulsa é
//     a única saída útil que resta.
//
// Era esse segundo caso que faltava, e ele não é exceção nenhuma:
// hostname sem domínio é o formato NORMAL da rede interna. A regra
// antiga exigia ".", "@" ou ":" e mandava todo nome de máquina de verdade
// para o lado "isto é busca" — a conexão avulsa por nome só existia pelo
// Enter, que é o atalho invisível que o card veio justamente resolver.
//
// O contador de resultados é o que segura o card quieto durante uma busca
// comum: "caixa" casa com máquinas, então não vira destino; "caixa" numa
// instalação sem nenhuma "caixa" vira — e aí é isso mesmo que se quer.
func ehDestinoPlausivel(texto string, semResultados bool) bool {
	t := strings.TrimSpace(strings.ToLower(texto))
	if t == "" {
		return false
	}
	if campos := strings.Fields(t); len(campos) == 2 {
		switch campos[0] {
		case "vnc", "tela", "ssh", "shell", "rdp", "sftp", "arquivos":
			return true
		}
		return false
	}
	if strings.ContainsAny(t, "@:") {
		return true
	}
	// IP ou nome com domínio: ponto entre duas partes não vazias
	if i := strings.Index(t, "."); i > 0 && i < len(t)-1 {
		return true
	}
	return semResultados && ehNomeDeMaquina(t)
}

// ehNomeDeMaquina: o texto tem forma de hostname (RFC 1123, já em
// minúsculas). Serve para uma busca sem resultado que seja frase ou
// tenha pontuação ("impressora nova", "cadê a 5?") continuar sendo busca
// vazia, e não virar um destino que ninguém vai conseguir resolver.
func ehNomeDeMaquina(t string) bool {
	if t == "" || len(t) > 63 {
		return false
	}
	if strings.HasPrefix(t, "-") || strings.HasSuffix(t, "-") {
		return false
	}
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
