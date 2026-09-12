package conexoes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const amostra = `[geral]
tema = claro

[cofre]
versao = 1
salt = AAAA
verificador = BBBB

[CAIXA1]
grupo = Loja 01;Caixas
host = 10.0.0.1
# comentário que precisa sobreviver
vnc_senha = enc:v1:XXXX
ssh = 1

[CAIXA2]
host = 10.0.0.2
`

func arquivoTemp(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "conexoes.ini")
	if err := os.WriteFile(p, []byte(amostra), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func conteudo(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSalvarPreservaComentarioEAlteraNoLugar(t *testing.T) {
	p := arquivoTemp(t)
	if err := Salvar(p, "CAIXA1", "", map[string]string{"host": "10.0.0.9", "rdp": "1"}); err != nil {
		t.Fatal(err)
	}
	got := conteudo(t, p)
	if !strings.Contains(got, "host = 10.0.0.9") {
		t.Errorf("host não foi trocado:\n%s", got)
	}
	if !strings.Contains(got, "# comentário que precisa sobreviver") {
		t.Errorf("comentário sumiu:\n%s", got)
	}
	if !strings.Contains(got, "rdp = 1") {
		t.Errorf("chave nova não entrou:\n%s", got)
	}
	if !strings.Contains(got, "[CAIXA2]\nhost = 10.0.0.2") {
		t.Errorf("outra seção foi mexida:\n%s", got)
	}
}

func TestRenomearDuplicarRemover(t *testing.T) {
	p := arquivoTemp(t)
	if err := Salvar(p, "CAIXA1", "CAIXA1-NOVO", nil); err != nil {
		t.Fatal(err)
	}
	if got := conteudo(t, p); !strings.Contains(got, "[CAIXA1-NOVO]") {
		t.Fatalf("não renomeou:\n%s", got)
	}
	if err := Duplicar(p, "CAIXA1-NOVO", "CAIXA3"); err != nil {
		t.Fatal(err)
	}
	got := conteudo(t, p)
	if strings.Count(got, "enc:v1:XXXX") != 2 {
		t.Errorf("duplicata não copiou o segredo como estava:\n%s", got)
	}
	if err := Duplicar(p, "CAIXA3", "CAIXA2"); err == nil {
		t.Error("duplicar sobre nome existente devia falhar")
	}
	if err := Remover(p, "CAIXA3"); err != nil {
		t.Fatal(err)
	}
	if got := conteudo(t, p); strings.Contains(got, "[CAIXA3]") {
		t.Errorf("não removeu:\n%s", got)
	}
}

func TestTrocarSenhaMestraRecifraTudo(t *testing.T) {
	p := arquivoTemp(t)
	err := TrocarSenhaMestra(p,
		func(v string) (string, error) { return strings.TrimPrefix(v, "enc:v1:"), nil },
		func(v string) (string, error) { return "enc:v1:NOVO-" + v, nil },
		map[string]string{"salt": "CCCC", "verificador": "DDDD"})
	if err != nil {
		t.Fatal(err)
	}
	got := conteudo(t, p)
	if !strings.Contains(got, "vnc_senha = enc:v1:NOVO-XXXX") {
		t.Errorf("segredo não foi recifrado:\n%s", got)
	}
	if !strings.Contains(got, "salt = CCCC") || !strings.Contains(got, "verificador = DDDD") {
		t.Errorf("[cofre] não foi atualizado:\n%s", got)
	}
}

func TestGravacaoGuardaCopiaNoHistorico(t *testing.T) {
	p := arquivoTemp(t)
	if err := Salvar(p, "CAIXA1", "", map[string]string{"host": "10.0.0.9"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(p), "historico")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("histórico não foi criado: %v", err)
	}
	var copias, indices int
	for _, e := range ents {
		switch {
		case strings.HasPrefix(e.Name(), "conexoes."):
			copias++
		case e.Name() == "index.txt":
			indices++
		}
	}
	if copias != 1 || indices != 1 {
		t.Errorf("esperava 1 cópia e 1 índice, veio %d e %d", copias, indices)
	}
	// a cópia tem o conteúdo ANTERIOR à edição
	b, _ := os.ReadFile(filepath.Join(dir, ents[0].Name()))
	if strings.Contains(string(b), "10.0.0.9") {
		t.Error("a cópia devia ser do estado ANTES da gravação")
	}
}

// Alias do chaveiro é REFERÊNCIA, não segredo: gravar cifrado transforma
// "!nome" num segredo cujo conteúdo é o texto do alias, e a conexão
// seguinte manda isso como senha. Foi assim que o SSH quebrou uma vez.
func TestAliasSobreviveAGravacao(t *testing.T) {
	p := arquivoTemp(t)
	if err := Salvar(p, "CAIXA1", "", map[string]string{"ssh_senha": "!suporte"}); err != nil {
		t.Fatal(err)
	}
	arq, err := Carregar(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, cx := range arq.Conexoes {
		if cx.Nome != "CAIXA1" {
			continue
		}
		if cx.SSH.SenhaBruta != "!suporte" {
			t.Fatalf("alias virou %q", cx.SSH.SenhaBruta)
		}
		return
	}
	t.Fatal("CAIXA1 sumiu do arquivo")
}
