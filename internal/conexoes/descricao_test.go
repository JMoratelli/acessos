package conexoes

import (
	"strings"
	"testing"
)

func TestLimitarDescricaoCortaPorRuna(t *testing.T) {
	// Acentuado de propósito: cortar por BYTE partiria o "é" ao meio e
	// devolveria string inválida — e a conta do limite sairia menor do
	// que o prometido para quem escreve em português.
	d := LimitarDescricao(strings.Repeat("é", LimiteDescricao+50))
	if n := len([]rune(d)); n != LimiteDescricao {
		t.Fatalf("cortou em %d runas, queria %d", n, LimiteDescricao)
	}
	if !strings.HasSuffix(d, "é") {
		t.Fatalf("corte deixou runa quebrada: %q", d[len(d)-4:])
	}
}

func TestLimitarDescricaoMataQuebraDeLinha(t *testing.T) {
	// O .ini é lido linha a linha: um "\n" no meio da descrição partiria
	// a seção, e o resto do texto viraria uma linha solta no arquivo.
	d := LimitarDescricao("caixa 3\nimpressora\r\nfiscal\tnova")
	if strings.ContainsAny(d, "\n\r\t") {
		t.Fatalf("sobrou quebra de linha: %q", d)
	}
	if d != "caixa 3 impressora  fiscal nova" {
		t.Fatalf("texto saiu %q", d)
	}
}

func TestDescricaoVaiEVoltaNoArquivo(t *testing.T) {
	p := arquivoTemp(t)

	if err := Salvar(p, "CAIXA1", "", map[string]string{
		"descricao": "caixa 3, impressora fiscal",
	}); err != nil {
		t.Fatal(err)
	}
	if txt := conteudo(t, p); !strings.Contains(txt, "descricao = caixa 3, impressora fiscal") {
		t.Fatalf("descrição não foi gravada:\n%s", txt)
	}

	arq, err := Carregar(p)
	if err != nil {
		t.Fatal(err)
	}
	if cx := acharCx(t, arq, "CAIXA1"); cx.Descricao != "caixa 3, impressora fiscal" {
		t.Fatalf("leu %q", cx.Descricao)
	}
	// O comentário do meio da seção é o canário de sempre: a descrição
	// entra pelo editor de linhas, que não pode reescrever o arquivo.
	if txt := conteudo(t, p); !strings.Contains(txt, "# comentário que precisa sobreviver") {
		t.Fatal("o comentário da seção sumiu")
	}
}

func TestDescricaoVaziaNaoVaiParaOArquivo(t *testing.T) {
	p := arquivoTemp(t)

	// Nunca preenchida: não pode nascer uma chave morta no .ini.
	if err := Salvar(p, "CAIXA2", "", map[string]string{"descricao": ""}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conteudo(t, p), "descricao") {
		t.Fatalf("gravou chave vazia:\n%s", conteudo(t, p))
	}

	// Preenchida e depois apagada: a chave tem de SAIR, não virar
	// "descricao = ".
	if err := Salvar(p, "CAIXA2", "", map[string]string{"descricao": "algo"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conteudo(t, p), "descricao = algo") {
		t.Fatal("não gravou a descrição para depois apagar")
	}
	if err := Salvar(p, "CAIXA2", "", map[string]string{"descricao": ""}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conteudo(t, p), "descricao") {
		t.Fatalf("a chave ficou para trás:\n%s", conteudo(t, p))
	}
}

func TestDescricaoDoArquivoTambemERecortada(t *testing.T) {
	// Quem edita o .ini à mão passa longe do campo da tela; o limite vale
	// na LEITURA também, senão o balão de hover recebe texto sem teto.
	cx := montar("X", map[string]string{"descricao": strings.Repeat("a", LimiteDescricao+10)})
	if n := len([]rune(cx.Descricao)); n != LimiteDescricao {
		t.Fatalf("montar deixou passar %d runas", n)
	}
}

func acharCx(t *testing.T, arq *Arquivo, nome string) Conexao {
	t.Helper()
	for _, cx := range arq.Conexoes {
		if cx.Nome == nome {
			return cx
		}
	}
	t.Fatalf("conexão %q não está no arquivo", nome)
	return Conexao{}
}
