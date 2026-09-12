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
// enquanto se digita um destino que não está cadastrado.
//
// Diferença para interpretarAlvo: aqui os quatro protocolos nascem
// ligados, porque o card existe justamente para o operador ESCOLHER pelo
// ícone — o palpite do texto ("10.1.1.9:22" é shell) vira só a porta
// daquele protocolo, não uma decisão tomada por ele.
func alvoRascunho(texto string) (conexoes.Conexao, bool) {
	cx, proto, ok := interpretarAlvo(texto)
	if !ok || !ehDestinoPlausivel(texto) {
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

// ehDestinoPlausivel evita o card aparecer a cada letra de uma busca
// comum: só um texto com cara de endereço (IP, host:porta, usuário@host)
// ou com prefixo de protocolo explícito vira destino. "caixa" é busca;
// "10.1.1.9", "serv.local" e "rdp serv-ad" são destino.
func ehDestinoPlausivel(texto string) bool {
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
	return false
}
