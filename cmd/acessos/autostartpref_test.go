package main

import (
	"os"
	"path/filepath"
	"testing"
)

// "Não perguntado" é o padrão em tudo que não seja um 1 ou um 0 legível —
// e é o estado que ainda leva à pergunta. O contrário (assumir "recusado"
// quando o arquivo está estranho) calaria o portal para sempre a partir
// de um ini mal salvo, e ninguém descobriria por quê.
func TestAutostartPadraoEhNaoPerguntado(t *testing.T) {
	casos := []struct {
		nome     string
		conteudo string
	}{
		{"arquivo vazio", ""},
		{"sem a chave", "[geral]\ntema = escuro\n"},
		{"chave vazia", "[geral]\natalho_autostart =\n"},
		{"valor estranho", "[geral]\natalho_autostart = talvez\n"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := lerAutostart(iniTemporario(t, c.conteudo)); got != autostartNaoPerguntado {
				t.Fatalf("esperado não perguntado, veio %v", got)
			}
		})
	}
}

func TestAutostartIniInexistenteEhNaoPerguntado(t *testing.T) {
	if got := lerAutostart(filepath.Join(t.TempDir(), "nao-existe.ini")); got != autostartNaoPerguntado {
		t.Fatalf("esperado não perguntado, veio %v", got)
	}
}

func TestAutostartLeOsDoisValores(t *testing.T) {
	if got := lerAutostart(iniTemporario(t, "[geral]\natalho_autostart = 1\n")); got != autostartLigado {
		t.Fatalf("1 tem de ser ligado, veio %v", got)
	}
	if got := lerAutostart(iniTemporario(t, "[geral]\natalho_autostart = 0\n")); got != autostartRecusado {
		t.Fatalf("0 tem de ser recusado, veio %v", got)
	}
}

// Ida e volta pelo disco: a caixa dos Ajustes grava, e quem lê depois é o
// serviço no próximo start — processo diferente, só o arquivo entre eles.
func TestSalvarAutostartIdaEVolta(t *testing.T) {
	ini := iniTemporario(t, "[geral]\ntema = escuro\n")

	if err := salvarAutostart(ini, true); err != nil {
		t.Fatal(err)
	}
	if got := lerAutostart(ini); got != autostartLigado {
		t.Fatalf("gravou ligado, leu %v", got)
	}

	if err := salvarAutostart(ini, false); err != nil {
		t.Fatal(err)
	}
	if got := lerAutostart(ini); got != autostartRecusado {
		t.Fatalf("gravou recusado, leu %v", got)
	}

	// Depois de gravado, o estado NUNCA mais volta a "não perguntado" —
	// é o que impede o portal de reabrir o diálogo a cada login.
	if lerAutostart(ini) == autostartNaoPerguntado {
		t.Fatal("gravar não pode deixar o estado como não perguntado")
	}

	b, err := os.ReadFile(ini)
	if err != nil {
		t.Fatal(err)
	}
	if !contemLinha(string(b), "tema = escuro") {
		t.Fatalf("perdeu outra chave de [geral]:\n%s", b)
	}
}

// As duas chaves moram na mesma seção e são independentes: desligar o
// atalho não pode apagar a resposta do autostart, nem o contrário.
func TestAutostartEAtalhoNaoSePisam(t *testing.T) {
	ini := iniTemporario(t, "[geral]\n")
	if err := salvarAutostart(ini, true); err != nil {
		t.Fatal(err)
	}
	if err := salvarAtalhoGlobalLigado(ini, false); err != nil {
		t.Fatal(err)
	}
	if got := lerAutostart(ini); got != autostartLigado {
		t.Fatalf("salvar o atalho mexeu no autostart: %v", got)
	}
	if atalhoGlobalLigado(ini) {
		t.Fatal("o atalho devia estar desligado")
	}
}
