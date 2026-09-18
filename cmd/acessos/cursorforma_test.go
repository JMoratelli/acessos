package main

import (
	"strings"
	"testing"

	"gioui.org/io/pointer"
)

// As silhuetas abaixo são desenhadas à mão, com as PROPORÇÕES dos cursores
// de verdade (Windows e X11) — não com os bitmaps, que não cabem num
// teste. É o que o classificador vê: 1 byte por pixel, opaco ou não.
//
// Quem mexer nos limiares de cursorforma.go passa por aqui primeiro: é
// mais barato descobrir que a seta virou mão neste arquivo do que numa
// sessão remota.

// mascara converte arte ASCII em máscara: '#' é opaco, o resto é vazio.
// Todas as linhas precisam ter o mesmo comprimento.
func mascara(t *testing.T, arte string) (w, h int, m []byte) {
	t.Helper()
	linhas := strings.Split(strings.Trim(arte, "\n"), "\n")
	h = len(linhas)
	w = len(linhas[0])
	m = make([]byte, w*h)
	for y, l := range linhas {
		if len(l) != w {
			t.Fatalf("linha %d tem %d colunas, esperado %d", y, len(l), w)
		}
		for x, c := range l {
			if c == '#' {
				m[y*w+x] = 255
			}
		}
	}
	return w, h, m
}

const arteSeta = `
#...........
##..........
###.........
####........
#####.......
######......
#######.....
########....
#########...
##########..
###########.
############
########....
#####.###...
####..###...
##.....###..
#.......###.
.........##.
`

const arteMao = `
....##.......
....##.......
....##.......
....##.......
....##.##....
....######.##
.##.#########
############.
############.
.###########.
.###########.
.###########.
..##########.
..##########.
...########..
...########..
`

const arteTexto = `
#######
...#...
...#...
...#...
...#...
...#...
...#...
...#...
...#...
...#...
...#...
...#...
...#...
#######
`

const arteSetaNS = `
.....#.....
....###....
...#####...
..#######..
.#########.
.....#.....
.....#.....
.....#.....
.....#.....
.....#.....
.....#.....
.....#.....
.....#.....
.#########.
..#######..
...#####...
....###....
.....#.....
`

const arteAmpulheta = `
#########
#########
.#######.
..#####..
...###...
....#....
...###...
..#####..
.#######.
#########
#########
`

const arteMover = `
....#....
...###...
..#####..
....#....
.#..#..#.
##..#..##
#########
##..#..##
.#..#..#.
....#....
..#####..
...###...
....#....
`

const arteCruz = `
....#....
....#....
....#....
....#....
#########
....#....
....#....
....#....
....#....
`

// arteOcupadoSeta é o "trabalhando em segundo plano": a seta com o
// indicador de ocupado ao lado. O que o denuncia é a caixa ficar larga.
const arteOcupadoSeta = `
#...........########
##..........########
###..........######.
####..........####..
#####..........##...
######........####..
#######......######.
########....########
#########...########
##########..........
###########.........
############........
########............
#####.###...........
####..###...........
##.....###..........
`

// transposta devolve a arte espelhada em torno da diagonal principal —
// é assim que a seta ↕ vira ↔ e que a ↘↖ vira ela mesma.
func transposta(w, h int, m []byte) (int, int, []byte) {
	n := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n[x*h+y] = m[y*w+x]
		}
	}
	return h, w, n
}

// setaDiagonal monta uma seta dupla na diagonal, do tamanho pedido. É
// gerada e não desenhada de propósito: o que o classificador olha nela é
// a SIMETRIA em torno da diagonal, e desenhá-la à mão arriscaria testar
// um erro de desenho em vez da regra.
func setaDiagonal(n int, anti bool) (int, int, []byte) {
	m := make([]byte, n*n)
	por := func(x, y int) {
		if x < 0 || y < 0 || x >= n || y >= n {
			return
		}
		if anti {
			x = n - 1 - x
		}
		m[y*n+x] = 255
	}
	// o corpo: a diagonal, com 2px de espessura
	for i := 0; i < n; i++ {
		por(i, i)
		por(i+1, i)
	}
	// as duas cabeças: um triângulo em cada ponta da diagonal
	k := n / 3
	for i := 0; i < k; i++ {
		for j := 0; j+i < k; j++ {
			por(j, i)
			por(n-1-j, n-1-i)
		}
	}
	return n, n, m
}

func TestClassificarCursores(t *testing.T) {
	var c classificadorCursor

	casos := []struct {
		nome       string
		arte       string
		xhot, yhot int
		quer       pointer.Cursor
	}{
		{"seta", arteSeta, 0, 0, pointer.CursorDefault},
		{"mão", arteMao, 5, 0, pointer.CursorPointer},
		{"texto", arteTexto, 3, 7, pointer.CursorText},
		{"redimensionar ↕", arteSetaNS, 5, 9, pointer.CursorNorthSouthResize},
		{"ocupado", arteAmpulheta, 4, 5, pointer.CursorWait},
		{"mover", arteMover, 4, 6, pointer.CursorAllScroll},
		{"cruz", arteCruz, 4, 4, pointer.CursorCrosshair},
		{"seta + ocupado", arteOcupadoSeta, 0, 0, pointer.CursorProgress},
	}
	for _, caso := range casos {
		w, h, m := mascara(t, caso.arte)
		if got := c.classificar(caso.xhot, caso.yhot, w, h, m); got != caso.quer {
			t.Errorf("%s: classificou como %v, esperado %v", caso.nome, got, caso.quer)
		}
	}

	// ↔ é a ↕ deitada; o ponto quente acompanha a rotação.
	w, h, m := mascara(t, arteSetaNS)
	w, h, m = transposta(w, h, m)
	if got := c.classificar(9, 5, w, h, m); got != pointer.CursorEastWestResize {
		t.Errorf("redimensionar ↔: classificou como %v, esperado %v",
			got, pointer.CursorEastWestResize)
	}

	w, h, m = setaDiagonal(24, false)
	if got := c.classificar(12, 12, w, h, m); got != pointer.CursorNorthWestSouthEastResize {
		t.Errorf("redimensionar ↘↖: classificou como %v, esperado %v",
			got, pointer.CursorNorthWestSouthEastResize)
	}
	w, h, m = setaDiagonal(24, true)
	if got := c.classificar(12, 12, w, h, m); got != pointer.CursorNorthEastSouthWestResize {
		t.Errorf("redimensionar ↗↙: classificou como %v, esperado %v",
			got, pointer.CursorNorthEastSouthWestResize)
	}
}

// A queixa que motivou a reescrita: a seta comum virava mão e ficava
// presa assim até a sessão cair, porque a linha de base tinha sido
// tomada de um cursor que não era a seta. Sem linha de base, a ordem em
// que os cursores chegam não pode mudar o resultado de nenhum deles.
func TestOrdemNaoContaminaClassificacao(t *testing.T) {
	var c classificadorCursor
	wS, hS, mS := mascara(t, arteSeta)
	wA, hA, mA := mascara(t, arteAmpulheta)

	// ampulheta primeiro (sessão que abre numa tela ocupada), seta depois
	if got := c.classificar(4, 5, wA, hA, mA); got != pointer.CursorWait {
		t.Fatalf("ampulheta: %v", got)
	}
	for i := 0; i < 3; i++ {
		if got := c.classificar(0, 0, wS, hS, mS); got != pointer.CursorDefault {
			t.Fatalf("seta depois da ampulheta (volta %d): %v", i, got)
		}
	}
}

// Sombra do cursor do Windows: alfa baixo numa mancha bem maior que o
// desenho. Contá-la como silhueta (o que o corte antigo, "!= 0", fazia)
// deformava caixa e preenchimento de todo cursor.
func TestSombraNaoEntraNaSilhueta(t *testing.T) {
	w, h, m := mascara(t, arteSeta)
	comSombra := make([]byte, len(m))
	copy(comSombra, m)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if comSombra[y*w+x] == 0 {
				comSombra[y*w+x] = 40 // véu de sombra por cima de tudo
			}
		}
	}
	var c classificadorCursor
	if got := c.classificar(0, 0, w, h, comSombra); got != pointer.CursorDefault {
		t.Errorf("seta com sombra: %v, esperado CursorDefault", got)
	}
}

// Decodificador que devolve a máscara TODA opaca (cursor monocromático
// sem canal alfa) não pode virar "ocupado" em cima de tudo: sem silhueta,
// o certo é a seta comum.
func TestMascaraTodaOpacaVoltaAoPadrao(t *testing.T) {
	var c classificadorCursor
	m := make([]byte, 32*32)
	for i := range m {
		m[i] = 255
	}
	if got := c.classificar(0, 0, 32, 32, m); got != pointer.CursorDefault {
		t.Errorf("máscara toda opaca: %v, esperado CursorDefault", got)
	}
}

func TestMascaraVaziaVoltaAoPadrao(t *testing.T) {
	var c classificadorCursor
	if got := c.classificar(0, 0, 0, 0, nil); got != pointer.CursorDefault {
		t.Errorf("máscara vazia: %v", got)
	}
	if got := c.classificar(0, 0, 8, 8, make([]byte, 64)); got != pointer.CursorDefault {
		t.Errorf("máscara toda transparente: %v", got)
	}
	// máscara menor que w*h: servidor mentiu no tamanho, não pode ler fora
	if got := c.classificar(0, 0, 8, 8, make([]byte, 10)); got != pointer.CursorDefault {
		t.Errorf("máscara curta: %v", got)
	}
}
