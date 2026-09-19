package main

import (
	"os"
	"path/filepath"
	"testing"
)

func iniTemporario(t *testing.T, conteudo string) string {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "conexoes.ini")
	if err := os.WriteFile(caminho, []byte(conteudo), 0o600); err != nil {
		t.Fatal(err)
	}
	return caminho
}

// O padrão é LIGADO, e é o caso que mais importa: instalação nova, chave
// ausente, arquivo ilegível — em nenhum deles o atalho pode sumir sozinho.
func TestAtalhoGlobalPadraoEhLigado(t *testing.T) {
	casos := []struct {
		nome     string
		conteudo string
	}{
		{"arquivo vazio", ""},
		{"sem a chave", "[geral]\ntema = escuro\n"},
		{"chave vazia", "[geral]\natalho_global =\n"},
		{"valor estranho", "[geral]\natalho_global = talvez\n"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if !atalhoGlobalLigado(iniTemporario(t, c.conteudo)) {
				t.Fatal("deveria estar ligado")
			}
		})
	}
}

func TestAtalhoGlobalIniInexistenteNaoDesliga(t *testing.T) {
	if !atalhoGlobalLigado(filepath.Join(t.TempDir(), "nao-existe.ini")) {
		t.Fatal("arquivo ausente tem de valer como ligado")
	}
}

func TestAtalhoGlobalSoDesligaComZero(t *testing.T) {
	if atalhoGlobalLigado(iniTemporario(t, "[geral]\natalho_global = 0\n")) {
		t.Fatal("0 tem de desligar")
	}
	if !atalhoGlobalLigado(iniTemporario(t, "[geral]\natalho_global = 1\n")) {
		t.Fatal("1 tem de ligar")
	}
}

// Ida e volta pelo disco: é assim que o serviço enxerga o que os Ajustes
// gravaram — ele relê o arquivo, não recebe o valor pelo socket.
func TestSalvarAtalhoGlobalIdaEVolta(t *testing.T) {
	ini := iniTemporario(t, "[geral]\ntema = escuro\n")

	if err := salvarAtalhoGlobalLigado(ini, false); err != nil {
		t.Fatal(err)
	}
	if atalhoGlobalLigado(ini) {
		t.Fatal("gravou desligado, leu ligado")
	}

	if err := salvarAtalhoGlobalLigado(ini, true); err != nil {
		t.Fatal(err)
	}
	if !atalhoGlobalLigado(ini) {
		t.Fatal("gravou ligado, leu desligado")
	}

	// A gravação não pode levar junto o que já estava na seção.
	b, err := os.ReadFile(ini)
	if err != nil {
		t.Fatal(err)
	}
	if !contemLinha(string(b), "tema = escuro") {
		t.Fatalf("perdeu outra chave de [geral]:\n%s", b)
	}
}

func contemLinha(texto, alvo string) bool {
	for _, l := range splitLinhas(texto) {
		if l == alvo {
			return true
		}
	}
	return false
}

func splitLinhas(s string) []string {
	var out []string
	inicio := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, trimCR(s[inicio:i]))
			inicio = i + 1
		}
	}
	return append(out, trimCR(s[inicio:]))
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}
