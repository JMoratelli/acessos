package main

import "testing"

// O card temporário só pode aparecer para texto com cara de destino:
// aparecer a cada letra de uma busca comum seria ruído bem no caminho do
// olho de quem só queria filtrar.
func TestEhDestinoPlausivel(t *testing.T) {
	// Forma de endereço: vira destino mesmo com a busca casando com
	// máquinas do inventário.
	sim := []string{"10.1.1.99", "serv.local", "caixa02:5900", "zanthus@caixa02", "rdp serv-ad"}
	nao := []string{"", "caixa", "loja 06", "   "}
	for _, s := range sim {
		if !ehDestinoPlausivel(s, false) {
			t.Errorf("%q devia virar destino", s)
		}
	}
	for _, s := range nao {
		if ehDestinoPlausivel(s, false) {
			t.Errorf("%q é busca, não destino", s)
		}
	}
}

// Nome cru de máquina — o formato normal da rede interna — vira destino
// quando, e só quando, a busca não sobrou nada para filtrar.
func TestNomeCruViraDestinoSoSemResultados(t *testing.T) {
	nomes := []string{"fc52002-lj06", "serv-ad", "caixa02", "pdv_01"}
	for _, s := range nomes {
		if ehDestinoPlausivel(s, false) {
			t.Errorf("%q com resultados na busca ainda é filtro", s)
		}
		if !ehDestinoPlausivel(s, true) {
			t.Errorf("%q sem nenhum resultado devia virar destino", s)
		}
	}
	// Busca vazia que não tem forma de hostname continua sendo busca
	// vazia: não adianta oferecer conexão para o que ninguém resolve.
	for _, s := range []string{"impressora nova", "cadê a 5?", "-lj06", "loja/06"} {
		if ehDestinoPlausivel(s, true) {
			t.Errorf("%q não tem forma de nome de máquina", s)
		}
	}
}

// No card os QUATRO protocolos ficam disponíveis — quem escolhe é o
// clique, não o palpite do texto. O palpite vira só a porta.
func TestAlvoRascunhoLigaTodosOsProtocolos(t *testing.T) {
	cx, ok := alvoRascunho("zanthus@10.1.1.99:22", false)
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
