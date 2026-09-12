package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A lateral tem que voltar como ficou: quem trabalha com ela escondida
// não quer reabri-la toda manhã. A chave é a mesma do app original —
// [geral] lateral = 0 | 1 — para os dois lerem o mesmo arquivo.
func TestLembrarLateralGravaNoIni(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "conexoes.ini")
	if err := os.WriteFile(ini, []byte("[geral]\ntema = claro\n\n[X]\nhost = 10.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	anterior := caminhoINI
	caminhoINI = ini
	defer func() { caminhoINI = anterior }()

	lembrarLateral(true)
	if txt := ler(t, ini); !strings.Contains(txt, "lateral = 0") {
		t.Fatalf("não gravou lateral = 0:\n%s", txt)
	}
	lembrarLateral(false)
	txt := ler(t, ini)
	if !strings.Contains(txt, "lateral = 1") {
		t.Fatalf("não gravou lateral = 1:\n%s", txt)
	}
	// a chave é SUBSTITUÍDA, não duplicada a cada clique
	if strings.Count(txt, "lateral") != 1 {
		t.Fatalf("chave duplicada:\n%s", txt)
	}
	// e o resto do arquivo continua de pé
	if !strings.Contains(txt, "tema = claro") || !strings.Contains(txt, "[X]") {
		t.Fatalf("mexeu no resto do arquivo:\n%s", txt)
	}
}

func ler(t *testing.T, caminho string) string {
	t.Helper()
	b, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
