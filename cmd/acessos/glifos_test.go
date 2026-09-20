package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gioui.org/font/opentype"
)

// Todo caractere que o app DESENHA tem que existir nas fontes embutidas.
//
// Por que isto é um teste e não um cuidado: no Linux, um glifo que falta
// na IBM Plex cai numa fonte do sistema (fontconfig) e ninguém percebe.
// No Windows não há em quem cair — o que falta vira o QUADRADINHO VAZIO.
// Foi assim que "▸"/"▾" (as setas dos blocos do editor de conexão),
// "⟳" (recarregar do SFTP) e "⌁" (botão de teclas da barra de sessão)
// chegaram ao usuário como ícones quebrados, e nenhum deles aparecia
// errado na máquina de quem escreveu o código.
//
// A regra é dura de propósito: SÍMBOLO EM TEXTO SÓ ENTRA SE A PLEX TIVER.
// Quando não tiver, há duas saídas boas — um ícone vetorial do
// gio.tools/icons (ver setaExpansor em tema.go), que não depende de fonte
// nenhuma, ou um caractere equivalente que exista (foi o caso do "↻",
// que substituiu o "⟳").
func TestFontesEmbutidasCobremOsGlifosUsados(t *testing.T) {
	col := colecaoFontes()
	if len(col) == 0 {
		t.Fatal("nenhuma fonte embutida carregou")
	}

	faltando := map[rune][]string{} // rune -> fontes que não têm
	for r, ondes := range runasDesenhadas(t) {
		if soMono[r] {
			continue
		}
		for _, ff := range col {
			face, ok := ff.Face.(opentype.Face)
			if !ok {
				t.Fatalf("face inesperada para %v", ff.Font.Typeface)
			}
			if _, tem := face.Face().NominalGlyph(r); !tem {
				faltando[r] = append(faltando[r],
					string(ff.Font.Typeface)+" "+ff.Font.Weight.String())
			}
		}
		if _, ruim := faltando[r]; ruim {
			sort.Strings(ondes)
			t.Errorf("U+%04X %q não existe em %d das %d fontes embutidas — "+
				"no Windows sai quadradinho vazio. Usado em: %s",
				r, string(r), len(faltando[r]), len(col), strings.Join(ondes, ", "))
		}
	}
}

// soMono são os caracteres que só a IBM Plex Mono tem e que o app só
// desenha COM ELA. Cada entrada precisa dizer onde, porque a conferência
// é manual: o teste não sabe com que fonte cada string é desenhada.
var soMono = map[rune]bool{
	// "── comando 1 · exit 0 ──", cabeçalho da saída do massa
	// (massatab.go, textoCompleto) — mostrada no dlgSaida, que é
	// fonteMono (saidadlg.go).
	'─': true,
}

// runasDesenhadas devolve os caracteres não-ASCII que aparecem em
// literais de produção, com o arquivo:linha de cada um. Só os arquivos de
// produção: o que um teste escreve vai para o terminal, não para a tela.
//
// Varre o pacote da interface E o internal/: TEXTO DE TELA TAMBÉM NASCE
// LÁ. O ErrSemMemoria de internal/telaproc, por exemplo, é escrito lá
// dentro e desenhado aqui, na aba que não abriu — se um símbolo novo
// entrar por esse caminho, o quadradinho vazio aparece igual. Ficar só no
// diretório do pacote era um furo: ele nunca veria essa metade.
//
// Consequência de propósito: um símbolo usado só em mensagem de TERMINAL
// (stderr de um pacote do internal/) também é acusado. Nesse caso a saída
// é a mesma dos outros — trocar por um caractere que as fontes tenham —,
// porque a fonte do terminal também varia e não custa nada obedecer.
func runasDesenhadas(t *testing.T) map[rune][]string {
	t.Helper()
	arqs, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// ../../internal a partir de cmd/acessos, que é onde o teste roda.
	raizInternal := filepath.Join("..", "..", "internal")
	err = filepath.WalkDir(raizInternal, func(caminho string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(caminho, ".go") {
			arqs = append(arqs, caminho)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("varrendo %s: %v", raizInternal, err)
	}
	fset := token.NewFileSet()
	uso := map[rune]map[string]bool{}
	for _, arq := range arqs {
		if strings.HasSuffix(arq, "_test.go") {
			continue
		}
		// ParseFile lê TODOS os arquivos, inclusive os de outra
		// plataforma: o glifo que falta tem que aparecer aqui mesmo
		// quando o teste roda no Linux.
		f, err := parser.ParseFile(fset, arq, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", arq, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || (lit.Kind != token.STRING && lit.Kind != token.CHAR) {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, r := range s {
				if r < 128 {
					continue
				}
				if uso[r] == nil {
					uso[r] = map[string]bool{}
				}
				p := fset.Position(lit.Pos())
				// nome curto para o do próprio pacote, caminho para o
				// resto: "sftptab.go:955" x "internal/telaproc/vigia.go:82"
				onde := filepath.Base(p.Filename)
				if strings.Contains(p.Filename, string(filepath.Separator)) {
					onde = filepath.ToSlash(strings.TrimPrefix(p.Filename, "../../")) +
						":" + strconv.Itoa(p.Line)
				} else {
					onde += ":" + strconv.Itoa(p.Line)
				}
				uso[r][onde] = true
			}
			return true
		})
	}
	if len(uso) == 0 {
		t.Fatal("nenhum literal lido — o teste não está no diretório do pacote?")
	}
	fora := map[rune][]string{}
	for r, ondes := range uso {
		for onde := range ondes {
			fora[r] = append(fora[r], onde)
		}
	}
	return fora
}

// Guarda de sanidade do teste acima: se a leitura dos literais parar de
// funcionar (glob errado, diretório errado), o teste de cobertura passa
// vazio e ninguém nota. Este aqui quebra nesse caso.
func TestRunasDesenhadasAchaOsAcentos(t *testing.T) {
	uso := runasDesenhadas(t)
	for _, r := range []rune{'á', 'ç', '—'} {
		if len(uso[r]) == 0 {
			t.Errorf("%q não apareceu em literal nenhum do pacote", r)
		}
	}
	if _, err := os.Stat("fontes"); err != nil {
		t.Fatalf("diretório das fontes embutidas sumiu: %v", err)
	}
}
