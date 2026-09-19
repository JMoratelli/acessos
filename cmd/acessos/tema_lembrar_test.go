package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// O tema tem que voltar como ficou. A chave é a mesma do app original —
// [geral] tema = claro | escuro — e é ela que main.go lê na abertura.
//
// Este teste existe por um defeito real: o botão da barra de topo
// trocava a variável em memória e mais nada, então trocar para o escuro
// durava até fechar o app. Foi relatado no Windows e valia nos dois
// sistemas. O auxiliar ler() é o do sidebar_lembrar_test.go, que faz a
// mesma conferência para a lateral.
func TestAlternarTemaGravaNoIni(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "conexoes.ini")
	if err := os.WriteFile(ini, []byte("[geral]\ntema = claro\nfonte = 0\n\n[X]\nhost = 10.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	anterior, temaAnterior := caminhoINI, tema
	caminhoINI, tema = ini, temaClaro
	defer func() { caminhoINI, tema = anterior, temaAnterior }()

	alternarTema()
	if !tema.Escuro {
		t.Fatal("o primeiro clique não trocou para o escuro")
	}
	if txt := ler(t, ini); !strings.Contains(txt, "tema = escuro") {
		t.Fatalf("não gravou tema = escuro:\n%s", txt)
	}

	alternarTema()
	txt := ler(t, ini)
	if tema.Escuro {
		t.Fatal("o segundo clique não voltou para o claro")
	}
	if !strings.Contains(txt, "tema = claro") {
		t.Fatalf("não gravou tema = claro:\n%s", txt)
	}
	// a chave é SUBSTITUÍDA, não duplicada a cada clique
	if strings.Count(txt, "tema =") != 1 {
		t.Fatalf("chave duplicada:\n%s", txt)
	}
	// e o resto do arquivo continua de pé
	if !strings.Contains(txt, "fonte = 0") || !strings.Contains(txt, "[X]") {
		t.Fatalf("mexeu no resto do arquivo:\n%s", txt)
	}
}

// Sem .ini (o app aberto antes de escolher inventário) gravar é
// impossível, e não pode ser motivo de queda: lembrarGeral só sai de
// fininho.
func TestLembrarGeralSemIniNaoQuebra(t *testing.T) {
	anterior := caminhoINI
	caminhoINI = ""
	defer func() { caminhoINI = anterior }()
	lembrarGeral("tema", "escuro")
}
