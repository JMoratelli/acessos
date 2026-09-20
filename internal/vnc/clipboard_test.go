package vnc

import "testing"

// O clipboard do RFB é Latin-1. Estes dois sentidos viajavam crus e eram
// lidos como UTF-8 na outra ponta: "conferência" vinda de um PDV chegava ao
// clipboard do sistema com um 0xEA solto (UTF-8 inválido, anunciado como
// charset=utf-8), e "ç" colado saía no servidor como "Ã§". Nenhum teste com
// texto ASCII pega isso.
func TestClipboardLatin1IdaEVolta(t *testing.T) {
	casos := []struct {
		nome   string
		latin1 []byte
		texto  string
	}{
		{"ascii", []byte("cupom 123"), "cupom 123"},
		{"cedilha", []byte{0x63, 0x6F, 0x6E, 0x66, 0x65, 0x72, 0xEA, 0x6E, 0x63, 0x69, 0x61}, "conferência"},
		{"acentos", []byte{0xE7, 0xE3, 0xF5, 0xC1}, "çãõÁ"},
		{"vazio", []byte{}, ""},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := latin1ParaUTF8(c.latin1); got != c.texto {
				t.Errorf("latin1ParaUTF8(% x) = %q; queria %q", c.latin1, got, c.texto)
			}
			got := utf8ParaLatin1(c.texto)
			if string(got) != string(c.latin1) {
				t.Errorf("utf8ParaLatin1(%q) = % x; queria % x", c.texto, got, c.latin1)
			}
		})
	}
}

// O que não cabe em Latin-1 vira '?' — um caractere visível é melhor que um
// byte truncado que o servidor mostra como outra letra.
func TestClipboardForaDoLatin1ViraInterrogacao(t *testing.T) {
	if got := string(utf8ParaLatin1("preço €10 ✓")); got != string([]byte{
		0x70, 0x72, 0x65, 0xE7, 0x6F, 0x20, '?', 0x31, 0x30, 0x20, '?'}) {
		t.Errorf("veio % x", got)
	}
}

// Todo byte Latin-1 tem de sobreviver à ida e volta — inclusive os que não
// formam UTF-8 sozinhos, que são justamente os que quebravam.
func TestClipboardTodosOsBytesSobrevivem(t *testing.T) {
	todos := make([]byte, 256)
	for i := range todos {
		todos[i] = byte(i)
	}
	if volta := utf8ParaLatin1(latin1ParaUTF8(todos)); string(volta) != string(todos) {
		for i := range todos {
			if i >= len(volta) || volta[i] != todos[i] {
				t.Fatalf("byte %d (0x%02X) não sobreviveu à ida e volta", i, i)
			}
		}
	}
}
