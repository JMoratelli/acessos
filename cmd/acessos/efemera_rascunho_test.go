package main

import "testing"

// O card temporário só pode aparecer para texto com cara de destino:
// aparecer a cada letra de uma busca comum seria ruído bem no caminho do
// olho de quem só queria filtrar.
func TestEhDestinoPlausivel(t *testing.T) {
	sim := []string{"10.1.1.99", "serv.local", "caixa02:5900", "zanthus@caixa02", "rdp serv-ad"}
	nao := []string{"", "caixa", "loja 06", "   "}
	for _, s := range sim {
		if !ehDestinoPlausivel(s) {
			t.Errorf("%q devia virar destino", s)
		}
	}
	for _, s := range nao {
		if ehDestinoPlausivel(s) {
			t.Errorf("%q é busca, não destino", s)
		}
	}
}

// No card os QUATRO protocolos ficam disponíveis — quem escolhe é o
// clique, não o palpite do texto. O palpite vira só a porta.
func TestAlvoRascunhoLigaTodosOsProtocolos(t *testing.T) {
	cx, ok := alvoRascunho("zanthus@10.1.1.99:22")
	if !ok {
		t.Fatal("devia produzir um destino")
	}
	if cx.Host != "10.1.1.99" {
		t.Fatalf("host = %q", cx.Host)
	}
	if !cx.VNC.Ligado || !cx.SSH.Ligado || !cx.RDP.Ligado {
		t.Fatal("os três protocolos deviam estar disponíveis no card")
	}
	if cx.SSH.Porta != 22 || cx.VNC.Porta != 5900 || cx.RDP.Porta != 3389 {
		t.Fatalf("portas: ssh=%d vnc=%d rdp=%d", cx.SSH.Porta, cx.VNC.Porta, cx.RDP.Porta)
	}
	if cx.SSH.Usuario != "zanthus" {
		t.Fatalf("usuário = %q", cx.SSH.Usuario)
	}
}
