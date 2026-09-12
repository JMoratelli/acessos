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
