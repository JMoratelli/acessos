package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"gioui.org/io/pointer"
)

// Máscaras capturadas de sessões DE VERDADE, ao contrário das silhuetas
// desenhadas à mão de cursorforma_test.go. Foram colhidas em 2026-09-20
// com cmd/cursorcap: um Windows por RDP (tela de logon) e uma área de
// trabalho Windows por VNC.
//
// POR QUE OS DOIS PROTOCOLOS IMPORTAM AQUI: eles entregam coisas
// diferentes, e o classificador vê os dois pelo mesmo corte.
//
//   - RDP entrega ALFA de verdade, 0..255. Os cursores do Windows vêm com
//     borda suavizada e, no tema padrão, SOMBRA — uma mancha de alfa
//     baixo bem maior que o desenho. É a razão de existir alfaOpaco=128,
//     e arte ASCII 0/255 nunca exercitava esse corte.
//   - VNC entrega máscara DURA, 0/255 depois da expansão feita no shim.
//     Ela chega da libvncclient como 0/1 (HandleCursorShape preenche bit
//     a bit com `>> b & 1`) e é vncshim.c que abre para 0/255. Enquanto
//     repassava o valor cru, todo pixel ficava abaixo de alfaOpaco, a
//     silhueta saía vazia e o cursor remoto do VNC NUNCA aparecia.
//
// Foi este arquivo de teste que pegou aquilo: o defeito não tinha sintoma
// no log nem erro no caminho, só o cursor padrão para sempre.

type casoReal struct {
	arquivo  string
	esperado pointer.Cursor
	// nota diz o que a amostra é e por que ela está aqui. Entra na
	// mensagem de falha: sem ela, "esperado Wait, veio AllScroll" não
	// lembra a ninguém QUAL cursor era.
	nota string
}

var casosReais = []casoReal{
	{"rdp-seta.pgm", pointer.CursorDefault,
		"seta do Windows, com sombra: a caixa só fica certa porque o corte de alfa descarta o halo"},
	{"rdp-ocupado-a.pgm", pointer.CursorWait, "anel de ocupado, quadro 1 de 4 (32x32)"},
	{"rdp-ocupado-b.pgm", pointer.CursorWait, "anel de ocupado, quadro 2 de 4 (41x39, com sombra)"},
	{"rdp-ocupado-c.pgm", pointer.CursorWait, "anel de ocupado, quadro 3 de 4 (32x32)"},
	{"rdp-ocupado-d.pgm", pointer.CursorWait, "anel de ocupado, quadro 4 de 4 (41x39, com sombra)"},
	{"vnc-seta.pgm", pointer.CursorDefault, "seta pelo VNC, máscara dura, hotspot (0,0)"},
	{"vnc-mao.pgm", pointer.CursorPointer, "mão pelo VNC — o par da 'mão presa' que o classificador tem de separar da seta"},

	// A seta dupla ↕ é a amostra que guarda o limiar do CURSOR DE TEXTO
	// pelo lado de fora. Em familiaCentrada() as duas caem no mesmo ramo
	// (simétricas e em pé, aspecto <= 0.66) e quem as separa é só
	// larguraTopo >= 0.6: o I-beam abre com a serifa ocupando a caixa
	// inteira, a seta dupla abre com o bico. Não há amostra real do
	// I-beam aqui — nenhuma das telas usadas em 20/09 tinha campo de
	// texto sob o passeio —, mas esta prende o falso positivo, que é o
	// erro que apareceria na tela: seta dupla virando cursor de texto.
	{"vnc-seta-dupla-vertical.pgm", pointer.CursorNorthSouthResize,
		"seta dupla ↕ de divisor de painel; o não-I-beam que segura larguraTopo"},

	// Seta + anel ("abrindo programa"): é da família SETA, não da
	// centrada, e sai por aspecto >= 0.9 em familiaSeta(). Os dois
	// quadros têm de dar o mesmo resultado — cursor animado chega como
	// várias máscaras distintas e oscilar entre dois cursores locais
	// seria pior que errar os dois.
	{"vnc-seta-anel-a.pgm", pointer.CursorProgress, "seta + anel, quadro 1 de 2"},
	{"vnc-seta-anel-b.pgm", pointer.CursorProgress, "seta + anel, quadro 2 de 2"},
}

func TestClassificarMascarasReais(t *testing.T) {
	var c classificadorCursor
	for _, caso := range casosReais {
		t.Run(caso.arquivo, func(t *testing.T) {
			xhot, yhot, w, h, mask := lerPGM(t, caso.arquivo)
			got := c.classificar(xhot, yhot, w, h, mask)
			if got != caso.esperado {
				f, ok := medirForma(xhot, yhot, w, h, mask)
				t.Errorf("%s (%s):\n  esperado %v, veio %v\n  %s",
					caso.arquivo, caso.nota, caso.esperado, got, descrever(f, ok))
			}
		})
	}
}

// TestMascarasReaisTemSilhueta guarda a invariante que o defeito do VNC
// violava: máscara que chega de servidor PRECISA ter pixel acima de
// alfaOpaco. Sem isto medirForma devolve falso e classificar cai em
// CursorDefault — calado, para qualquer cursor.
func TestMascarasReaisTemSilhueta(t *testing.T) {
	for _, caso := range casosReais {
		t.Run(caso.arquivo, func(t *testing.T) {
			_, _, _, _, mask := lerPGM(t, caso.arquivo)
			var opacos int
			for _, v := range mask {
				if v >= alfaOpaco {
					opacos++
				}
			}
			if opacos == 0 {
				t.Fatalf("%s: nenhum pixel atinge alfaOpaco=%d — silhueta vazia, "+
					"classificar devolveria CursorDefault para tudo", caso.arquivo, alfaOpaco)
			}
		})
	}
}

// TestMascarasRDPTemAlfaParcial documenta POR QUE alfaOpaco existe, e
// falha se as amostras do RDP forem trocadas por outras sem borda
// suavizada: elas são a única prova, neste repositório, de que o corte
// tem o que separar. As do VNC são o contrário — duras de propósito.
func TestMascarasRDPTemAlfaParcial(t *testing.T) {
	for _, caso := range casosReais {
		if !bytes.HasPrefix([]byte(caso.arquivo), []byte("rdp-")) {
			continue
		}
		t.Run(caso.arquivo, func(t *testing.T) {
			_, _, _, _, mask := lerPGM(t, caso.arquivo)
			var parciais int
			for _, v := range mask {
				if v != 0 && v != 255 {
					parciais++
				}
			}
			if parciais == 0 {
				t.Errorf("%s: só 0/255 — amostra sem borda suavizada não exercita alfaOpaco",
					caso.arquivo)
			}
		})
	}
}

// TestRetratoDasMascarasReais não afirma nada: imprime a medida de cada
// amostra (`go test -run Retrato -v ./cmd/acessos`). É o que evita
// calibrar limiar no chute — quem for mexer num corte de familiaSeta()
// ou de familiaCentrada() vê primeiro onde as amostras reais caem.
func TestRetratoDasMascarasReais(t *testing.T) {
	var c classificadorCursor
	for _, caso := range casosReais {
		xhot, yhot, w, h, mask := lerPGM(t, caso.arquivo)
		f, ok := medirForma(xhot, yhot, w, h, mask)
		t.Logf("%s -> %v\n  %s\n  (%s)",
			caso.arquivo, c.classificar(xhot, yhot, w, h, mask),
			descrever(f, ok), caso.nota)
	}
}

// descrever monta o retrato da forma para a mensagem de falha. Sem ele,
// "esperado Wait, veio AllScroll" não diz QUAL medida saiu do lugar, e
// calibrar limiar vira tentativa e erro.
func descrever(f formaCursor, ok bool) string {
	if !ok {
		return "medirForma falhou: nenhum pixel opaco"
	}
	hx, hy, temHot := f.hotRel()
	return fmt.Sprintf(
		"bitmap %dx%d, caixa %dx%d, opacos=%d\n  aspecto=%.3f preenchimento=%.3f "+
			"topoCentro=%.3f larguraTopo=%.3f\n  simH=%.3f simV=%.3f simDiag=%.3f "+
			"simAnti=%.3f correlacao=%.3f\n  hotRel=(%.3f,%.3f) dentroDaCaixa=%v",
		f.w, f.h, f.bw(), f.bh(), f.opacos,
		f.aspecto(), f.preenchimento(), f.topoCentro, f.larguraTopo,
		f.simH(), f.simV(), f.simDiag(), f.simAnti(), f.correlacao(),
		hx, hy, temHot)
}

// lerPGM lê um PGM binário (P5) de testdata/cursores. O ponto quente vem
// num comentário `# hot X Y` porque o formato não tem campo para ele —
// e sem o hotspot o classificador perde o sinal mais forte que tem. Ver
// gravarPGM, em cmd/cursorcap.
func lerPGM(t *testing.T, nome string) (xhot, yhot, w, h int, mask []byte) {
	t.Helper()
	bruto, err := os.ReadFile(filepath.Join("testdata", "cursores", nome))
	if err != nil {
		t.Fatalf("lendo %s: %v", nome, err)
	}

	temHot := false
	var campos []string
	i := 0
	for len(campos) < 4 && i < len(bruto) {
		switch {
		case bruto[i] == '#':
			fim := bytes.IndexByte(bruto[i:], '\n')
			if fim < 0 {
				t.Fatalf("%s: comentário sem fim de linha", nome)
			}
			linha := string(bruto[i+1 : i+fim])
			if x, y, ok := lerHot(linha); ok {
				xhot, yhot, temHot = x, y, true
			}
			i += fim + 1
		case bruto[i] == ' ' || bruto[i] == '\n' || bruto[i] == '\t' || bruto[i] == '\r':
			i++
		default:
			j := i
			for j < len(bruto) && !ehBranco(bruto[j]) {
				j++
			}
			campos = append(campos, string(bruto[i:j]))
			i = j
		}
	}
	if len(campos) < 4 {
		t.Fatalf("%s: cabeçalho PGM incompleto (%v)", nome, campos)
	}
	if campos[0] != "P5" {
		t.Fatalf("%s: esperado PGM binário (P5), veio %q", nome, campos[0])
	}
	if !temHot {
		t.Fatalf("%s: sem o comentário `# hot X Y` — o ponto quente é "+
			"indispensável para classificar", nome)
	}
	w = inteiro(t, nome, campos[1])
	h = inteiro(t, nome, campos[2])
	if maxval := inteiro(t, nome, campos[3]); maxval != 255 {
		t.Fatalf("%s: maxval %d, esperado 255", nome, maxval)
	}

	// Um (e só um) byte branco separa o cabeçalho dos pixels.
	i++
	if i+w*h > len(bruto) {
		t.Fatalf("%s: %d bytes de pixel para %dx%d", nome, len(bruto)-i, w, h)
	}
	return xhot, yhot, w, h, bruto[i : i+w*h]
}

func ehBranco(b byte) bool { return b == ' ' || b == '\n' || b == '\t' || b == '\r' }

func lerHot(linha string) (x, y int, ok bool) {
	var campos []string
	inicio := -1
	for i := 0; i <= len(linha); i++ {
		if i == len(linha) || ehBranco(linha[i]) {
			if inicio >= 0 {
				campos = append(campos, linha[inicio:i])
				inicio = -1
			}
			continue
		}
		if inicio < 0 {
			inicio = i
		}
	}
	if len(campos) != 3 || campos[0] != "hot" {
		return 0, 0, false
	}
	x, err1 := strconv.Atoi(campos[1])
	y, err2 := strconv.Atoi(campos[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return x, y, true
}

func inteiro(t *testing.T, nome, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("%s: campo %q não é número: %v", nome, s, err)
	}
	return n
}
