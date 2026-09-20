package conexoes

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func copiasNoHistorico(t *testing.T, ini string) []string {
	t.Helper()
	ents, err := os.ReadDir(dirHistorico(ini))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var copias []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "conexoes.") {
			copias = append(copias, e.Name())
		}
	}
	return copias
}

// Uma OPERAÇÃO, uma cópia — mesmo gravando várias conexões.
//
// Este teste existe por um estrago concreto: detectar a plataforma de uma
// loja gravava máquina por máquina, cada gravação guardando a sua cópia.
// Numa loja de 54 máquinas eram 54 cópias de uma vez e, como a rotação
// mantém só as 20 últimas, a detecção APAGAVA todas as cópias de edição de
// verdade — exatamente o que o histórico existe para proteger.
func TestLoteGuardaUmaCopiaSoParaAOperacaoInteira(t *testing.T) {
	ini := arquivoTemp(t)

	lote := NovoLote(ini, "detectou a plataforma de 2 máquina(s)")
	if err := lote.Salvar("CAIXA1", "", map[string]string{"windows": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := lote.Salvar("CAIXA2", "", map[string]string{"windows": "0"}); err != nil {
		t.Fatal(err)
	}

	if c := copiasNoHistorico(t, ini); len(c) != 1 {
		t.Fatalf("queria 1 cópia para a operação inteira, vieram %d: %v", len(c), c)
	}
	// e as duas gravações aconteceram de verdade
	txt := conteudo(t, ini)
	if !strings.Contains(txt, "windows = 1") || !strings.Contains(txt, "windows = 0") {
		t.Fatalf("as seções não foram gravadas:\n%s", txt)
	}
	// o índice recebe o motivo da operação, uma linha só
	b, err := os.ReadFile(filepath.Join(dirHistorico(ini), "index.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(string(b)), "\n"); n != 0 {
		t.Fatalf("queria uma linha só no índice, veio:\n%s", b)
	}
	if !strings.Contains(string(b), "detectou a plataforma") {
		t.Fatalf("o motivo da operação não foi anotado:\n%s", b)
	}
}

// Salvar avulso continua guardando a sua cópia: o lote é para operação em
// bloco, não para desligar o histórico.
func TestSalvarAvulsoContinuaGuardandoCopia(t *testing.T) {
	ini := arquivoTemp(t)
	if err := Salvar(ini, "CAIXA1", "", map[string]string{"host": "10.0.0.9"}); err != nil {
		t.Fatal(err)
	}
	if err := Salvar(ini, "CAIXA2", "", map[string]string{"host": "10.0.0.8"}); err != nil {
		t.Fatal(err)
	}
	if c := copiasNoHistorico(t, ini); len(c) != 2 {
		t.Fatalf("queria 2 cópias (uma por edição), vieram %d", len(c))
	}
}

// O lote é usado por várias sondas ao mesmo tempo, e o .ini é regravado
// INTEIRO a cada seção: sem a trava, duas gravações terminando juntas se
// sobrescreveriam e uma das máquinas perderia o valor.
func TestLoteGravaDeVariasGoroutinesSemPerderNada(t *testing.T) {
	ini := arquivoTemp(t)
	lote := NovoLote(ini, "detectou a plataforma de 2 máquina(s)")

	var wg sync.WaitGroup
	for nome, valor := range map[string]string{"CAIXA1": "1", "CAIXA2": "0"} {
		wg.Add(1)
		go func(nome, valor string) {
			defer wg.Done()
			if err := lote.Salvar(nome, "", map[string]string{"windows": valor}); err != nil {
				t.Errorf("%s: %v", nome, err)
			}
		}(nome, valor)
	}
	wg.Wait()

	txt := conteudo(t, ini)
	if !strings.Contains(txt, "windows = 1") || !strings.Contains(txt, "windows = 0") {
		t.Fatalf("uma das gravações se perdeu:\n%s", txt)
	}
	if c := copiasNoHistorico(t, ini); len(c) != 1 {
		t.Fatalf("queria 1 cópia, vieram %d", len(c))
	}
	// o comentário do CAIXA1 é a prova de que a edição continua linha a
	// linha, e não uma regravação a partir do modelo
	if !strings.Contains(txt, "# comentário que precisa sobreviver") {
		t.Fatalf("a edição comeu o comentário:\n%s", txt)
	}
}
