//go:build linux || windows

package main

import (
	"image"
	"os"
	"testing"

	"acessos-go/internal/telaproc"
)

// O binário de teste faz de processo-filho de verdade — é a MESMA
// modoWorker() que o app usa, com a libfreerdp junto. Sem isto, o caminho
// mais frágil da aba (subir o filho e falar com ele) só seria exercitado
// com um servidor RDP ao vivo por perto.
func TestMain(m *testing.M) {
	if modoWorker() {
		return
	}
	os.Exit(m.Run())
}

// ------------------------------------------------------------ dano

func TestDanoTelaJuntaERecorta(t *testing.T) {
	var d danoTela
	if _, _, _, _, ok := d.tomar(); ok {
		t.Fatal("dano virgem se diz sujo")
	}
	d.juntar(10, 10, 5, 5)
	d.juntar(3, 20, 2, 2)
	x, y, w, h, ok := d.tomar()
	if !ok || x != 3 || y != 10 || w != 12 || h != 12 {
		t.Fatalf("caixa (%d,%d %dx%d) ok=%v, esperava (3,10 12x12)", x, y, w, h, ok)
	}
	if _, _, _, _, ok := d.tomar(); ok {
		t.Fatal("tomar não zerou o dano")
	}
	// Retângulo vazio não suja nada: a libfreerdp manda 0x0 em situações
	// de canto, e aceitá-los faria a bomba mandar quadro à toa.
	d.juntar(0, 0, 0, 0)
	if _, _, _, _, ok := d.tomar(); ok {
		t.Fatal("retângulo vazio sujou a tela")
	}
}

// ------------------------------------------------------------ montagem

func TestAplicarQuadroColaNoLugarCerto(t *testing.T) {
	pix := []byte{9, 9, 9, 9, 8, 8, 8, 8} // 2x1
	img := aplicarQuadro(nil, telaproc.Quadro{X: 1, Y: 1, W: 2, H: 1, TotalW: 4, TotalH: 3}, pix)
	if img.Rect != image.Rect(0, 0, 4, 3) {
		t.Fatalf("tela ficou %v", img.Rect)
	}
	if got := img.NRGBAAt(1, 1); got.R != 9 {
		t.Fatalf("pixel (1,1) = %+v", got)
	}
	if got := img.NRGBAAt(2, 1); got.R != 8 {
		t.Fatalf("pixel (2,1) = %+v", got)
	}
	if got := img.NRGBAAt(0, 0); got.R != 0 {
		t.Fatalf("pixel (0,0) foi tocado: %+v", got)
	}

	// Resolução nova (resize do servidor) troca a imagem inteira em vez
	// de escrever fora dos limites da antiga.
	maior := aplicarQuadro(img, telaproc.Quadro{X: 0, Y: 0, W: 1, H: 1, TotalW: 8, TotalH: 6}, []byte{1, 2, 3, 4})
	if maior.Rect != image.Rect(0, 0, 8, 6) {
		t.Fatalf("resize não trocou a tela: %v", maior.Rect)
	}

	// Retângulo que não cabe é descartado, não estoura o slice: é a
	// defesa contra um cabeçalho estragado do outro lado do socket.
	antes := maior.Rect
	depois := aplicarQuadro(maior, telaproc.Quadro{X: 7, Y: 5, W: 4, H: 4, TotalW: 8, TotalH: 6}, make([]byte, 64))
	if depois.Rect != antes {
		t.Fatalf("quadro fora dos limites mexeu na tela")
	}
}

func TestRecortarBGRXparaNRGBARespeitaStride(t *testing.T) {
	// tela 3x2 com stride 16 (4 pixels de largura alocada, 3 usados)
	const stride = 16
	buf := make([]byte, stride*2)
	// pixel (1,1) em BGRX = B,G,R,X
	copy(buf[stride+4:], []byte{0x11, 0x22, 0x33, 0xff})
	out := recortarBGRXparaNRGBA(buf, stride, 1, 1, 1, 1)
	quer := []byte{0x33, 0x22, 0x11, 255} // R,G,B,A
	if string(out) != string(quer) {
		t.Fatalf("saiu %v, esperava %v", out, quer)
	}
}
