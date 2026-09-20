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
	if got := img.RGBAAt(1, 1); got.R != 9 {
		t.Fatalf("pixel (1,1) = %+v", got)
	}
	if got := img.RGBAAt(2, 1); got.R != 8 {
		t.Fatalf("pixel (2,1) = %+v", got)
	}
	if got := img.RGBAAt(0, 0); got.R != 0 {
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

func TestRecortarBGRXparaRGBARespeitaStride(t *testing.T) {
	// tela 3x2 com stride 16 (4 pixels de largura alocada, 3 usados)
	const stride = 16
	buf := make([]byte, stride*2)
	// pixel (1,1) em BGRX = B,G,R,X
	copy(buf[stride+4:], []byte{0x11, 0x22, 0x33, 0xff})
	out := recortarBGRXparaRGBA(nil, buf, stride, 1, 1, 1, 1)
	quer := []byte{0x33, 0x22, 0x11, 255} // R,G,B,A
	if string(out) != string(quer) {
		t.Fatalf("saiu %v, esperava %v", out, quer)
	}
}

// ------------------------------------------------------------ bomba de quadros

// TestEnviarQuadroTomaDanoAntesDeCapturar prova a ORDEM que corrige o
// bug de remendo/glitch visual encontrado em auditoria: dano.tomar() tem
// que já ter consumido o retângulo antes de capturar() tirar o
// snapshot — senão uma pintura chegando bem nesse meio-tempo entra no
// retângulo tomado (fica marcada "enviada") sem que seus pixels tenham
// sido capturados, e aquele pedaço da tela fica com conteúdo velho até
// alguma dano futura por acaso cobrir a mesma área de novo.
func TestEnviarQuadroTomaDanoAntesDeCapturar(t *testing.T) {
	danoJaConsumidoQuandoCapturou := false
	b := &bombaTela{}
	b.capturar = func() ([]byte, int, int, int) {
		// Se dano.tomar() já rodou (a ordem certa), o que sobrou aqui
		// dentro é "nada sujo" — um tomar() feito agora devolve
		// ok=false. Na ordem errada (capturar antes de tomar), o
		// retângulo original ainda estaria pendente aqui dentro.
		_, _, _, _, ok := b.dano.tomar()
		danoJaConsumidoQuandoCapturou = !ok
		return nil, 0, 0, 0 // sem framebuffer: enviarQuadro() não chega a usar b.c
	}
	b.dano.juntar(0, 0, 4, 4)
	b.enviarQuadro()

	if !danoJaConsumidoQuandoCapturou {
		t.Fatal("capturar() rodou antes de dano.tomar() consumir o retângulo — voltou a ordem que causava o glitch (ver o comentário em enviarQuadro)")
	}
}

// TestEnviarQuadroRedanificaSeCapturaFalhaDepoisDeTomar cobre a borda
// que a troca de ordem acima abriu: se dano.tomar() já consumiu o
// retângulo mas capturar() não tem framebuffer nenhum ainda (sessão
// recém-aberta), o retângulo não pode ser descartado — senão aquela
// área nunca mais seria reenviada assim que o framebuffer chegasse.
func TestEnviarQuadroRedanificaSeCapturaFalhaDepoisDeTomar(t *testing.T) {
	b := &bombaTela{
		capturar: func() ([]byte, int, int, int) { return nil, 0, 0, 0 },
	}
	b.dano.juntar(5, 5, 10, 10)

	if b.enviarQuadro() {
		t.Fatal("enviarQuadro() disse que mandou sem framebuffer nenhum")
	}
	x, y, w, h, ok := b.dano.tomar()
	if !ok {
		t.Fatal("o retângulo se perdeu quando a captura falhou depois do tomar()")
	}
	if x != 5 || y != 5 || w != 10 || h != 10 {
		t.Fatalf("retângulo devolvido (%d,%d %dx%d), esperava (5,5 10x10)", x, y, w, h)
	}
}

// O buffer de saída é reaproveitado entre quadros. Duas coisas têm de
// valer sempre: a fatia devolvida tem len EXATO (quem recebe confere o
// tamanho contra a geometria do cabeçalho e recusa o quadro se não bater),
// e um quadro menor não pode deixar aparecer pixel do quadro anterior.
func TestRecortarReaproveitaOBufferSemVazarQuadroAnterior(t *testing.T) {
	// framebuffer 4x2 em BGRX, todo 0xAA
	const w, h = 4, 2
	stride := w * 4
	grande := make([]byte, stride*h)
	for i := range grande {
		grande[i] = 0xAA
	}

	primeiro := recortarBGRXparaRGBA(nil, grande, stride, 0, 0, w, h)
	if len(primeiro) != w*h*4 {
		t.Fatalf("len = %d, queria %d", len(primeiro), w*h*4)
	}

	// agora um recorte MENOR, reusando o buffer do anterior
	pequeno := make([]byte, stride*h)
	for i := range pequeno {
		pequeno[i] = 0x11
	}
	segundo := recortarBGRXparaRGBA(primeiro, pequeno, stride, 0, 0, 1, 1)
	if len(segundo) != 1*1*4 {
		t.Fatalf("len do recorte menor = %d, queria 4 — o outro lado recusa o quadro", len(segundo))
	}
	if &segundo[0] != &primeiro[0] {
		t.Error("o buffer não foi reaproveitado (alocou outro)")
	}
	// só os bytes do recorte novo podem ser lidos, e eles vêm do
	// framebuffer NOVO — nada de 0xAA sobrando
	for i, b := range segundo {
		if b == 0xAA {
			t.Fatalf("byte %d ainda é do quadro anterior", i)
		}
	}
	if segundo[3] != 255 {
		t.Errorf("alfa = %d, queria 255 (é o que permite o tipo RGBA)", segundo[3])
	}

	// e um recorte MAIOR que o buffer guardado cresce sozinho
	maior := recortarBGRXparaRGBA(segundo, grande, stride, 0, 0, w, h)
	if len(maior) != w*h*4 {
		t.Fatalf("len ao crescer = %d, queria %d", len(maior), w*h*4)
	}
}
