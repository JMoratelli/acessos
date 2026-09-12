package main

import (
	"testing"

	"acessos-go/internal/conexoes"
)

func TestInterpretarAlvo(t *testing.T) {
	casos := []struct {
		entrada string
		proto   conexoes.Protocolo
		host    string
		porta   int
		usuario string
	}{
		{"10.1.1.99", conexoes.VNC, "10.1.1.99", 5900, ""},
		{"10.1.1.99:22", conexoes.SSH, "10.1.1.99", 22, ""},
		{"10.1.1.99:3389", conexoes.RDP, "10.1.1.99", 3389, ""},
		{"zanthus@10.1.1.99", conexoes.SSH, "10.1.1.99", 22, "zanthus"},
		{"rdp serv-ad", conexoes.RDP, "serv-ad", 3389, ""},
		{"tela 10.1.1.5:5901", conexoes.VNC, "10.1.1.5", 5901, ""},
	}
	for _, c := range casos {
		cx, p, ok := interpretarAlvo(c.entrada)
		if !ok || p != c.proto || cx.Host != c.host {
			t.Errorf("%q -> proto %q host %q (queria %q / %q)", c.entrada, p, cx.Host, c.proto, c.host)
			continue
		}
		var porta int
		var usuario string
		switch p {
		case conexoes.VNC:
			porta, usuario = cx.VNC.Porta, cx.VNC.Usuario
		case conexoes.SSH:
			porta, usuario = cx.SSH.Porta, cx.SSH.Usuario
		case conexoes.RDP:
			porta, usuario = cx.RDP.Porta, cx.RDP.Usuario
		}
		if porta != c.porta || usuario != c.usuario {
			t.Errorf("%q -> porta %d usuário %q (queria %d / %q)", c.entrada, porta, usuario, c.porta, c.usuario)
		}
	}
	if _, _, ok := interpretarAlvo("   "); ok {
		t.Error("texto vazio não pode virar conexão")
	}
}
