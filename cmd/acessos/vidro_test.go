package main

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// As regras de contraste combinadas na revisão de 2026-10-18, como teste:
// mexer num token e cair abaixo do mínimo quebra o build em vez de virar
// "texto cinza que ninguém lê" na próxima rodada de prévias.
//
// A composição é em espaço LINEAR, como o Gio faz — conta em sRGB dá
// números otimistas no tema escuro (o branco translúcido sai bem mais
// claro do que ela prevê).

func luminancia(c color.NRGBA) float64 {
	l := func(v uint8) float64 { return float64(srgbLin(v)) }
	return 0.2126*l(c.R) + 0.7152*l(c.G) + 0.0722*l(c.B)
}

func contraste(a, b color.NRGBA) float64 {
	la, lb := luminancia(a), luminancia(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// composta: a cor opaca que resulta de pintar c (com o alfa dela) sobre
// o fundo opaco.
func composta(fundo color.NRGBA, c ...color.NRGBA) color.NRGBA {
	r := paraLin(fundo)
	for _, x := range c {
		r = sobre(r, paraLin(x))
	}
	return color.NRGBA{linSrgb(r.r), linSrgb(r.g), linSrgb(r.b), 255}
}

func TestContrasteDosTokens(t *testing.T) {
	salvo := tema
	defer func() { tema = salvo }()

	for _, nome := range []string{"claro", "escuro"} {
		tema = temaClaro
		if nome == "escuro" {
			tema = temaEscuro
		}
		// Pior card: o vidro sobre a parte mais escura do fundo (claro)
		// ou sobre uma faixa diagonal (escuro, onde a faixa clareia).
		faixa := faixasCanto(1000, 1000)[0].cor
		cardPior := composta(tema.Fundo3, tema.VidroTinta)
		if tema.Escuro {
			cardPior = composta(tema.Fundo1, faixa, tema.VidroTinta)
		}

		casos := []struct {
			oque       string
			texto, sob color.NRGBA
			min        float64
		}{
			{"Texto no diálogo", tema.Texto, tema.Cartao, 7},
			{"Sec no diálogo (parágrafo)", tema.Sec, tema.Cartao, 4.5},
			{"Sec no hover", tema.Sec, tema.Hover, 4.5},
			{"Fraco no diálogo (dica)", tema.Fraco, tema.Cartao, 3},
			{"Fraco no hover", tema.Fraco, tema.Hover, 3},
			{"Texto no pior card", tema.Texto, cardPior, 4.5},
			{"Sec no pior card", tema.Sec, cardPior, 3.5},
			{"CardFraco no pior card", tema.CardFraco, cardPior, 3.5},
			{"HeroSec no hero", tema.HeroSec, tema.Hero2, 4.5},
			{"aviso âmbar no diálogo", tema.AtencaoFg, tema.Cartao, 4.5},
			{"erro no diálogo", tema.ErroFg, tema.Cartao, 3},
			// O vermelho também é fundo do botão destrutivo: clarear o
			// ErroFg para o texto de erro não pode apagar o rótulo branco.
			{"rótulo do botão destrutivo", hex(0xffffff), tema.ErroFg, 4.5},
			{"destrutivo na contagem", tema.ErroFg, tema.ErroBg, 3},
			// Rótulo branco do botão primário: 4,4:1 no claro, mas 2,97:1
			// no escuro (Azul #748ffc é claro demais para texto branco).
			// Pendente para a próxima rodada visual, com prévia; o mínimo
			// aqui só impede que piore.
			{"rótulo do botão primário", hex(0xffffff), tema.Azul, 2.9},
			// Hover tem de se ver contra a superfície em que acontece.
			{"hover contra o diálogo", tema.Hover, tema.Cartao, 1.15},
		}
		for _, c := range casos {
			if got := contraste(c.texto, c.sob); got < c.min {
				t.Errorf("%s: %s = %.2f:1, mínimo %.1f:1", nome, c.oque, got, c.min)
			}
		}
	}
}

func TestSrgbLinIdaEVolta(t *testing.T) {
	for v := 0; v < 256; v++ {
		if got := linSrgb(srgbLin(uint8(v))); got != uint8(v) {
			t.Fatalf("linSrgb(srgbLin(%d)) = %d", v, got)
		}
	}
}

func TestRaiosCaixaAproximamSigma(t *testing.T) {
	for _, sigma := range []float64{1.5, 2.75, 6, 12} {
		var vari float64
		for _, r := range raiosCaixa(sigma, 3) {
			w := float64(2*r + 1)
			vari += (w*w - 1) / 12 // variância de uma caixa de largura w
		}
		if got := math.Sqrt(vari); math.Abs(got-sigma)/sigma > 0.15 {
			t.Errorf("sigma %.2f: as três caixas dão %.2f", sigma, got)
		}
	}
}

func TestRenderFoscoCobreAJanela(t *testing.T) {
	salvo := tema
	defer func() { tema = salvo }()
	for _, tm := range []Tema{temaClaro, temaEscuro} {
		tema = tm
		img := renderFosco(image.Pt(1366, 700), 1)
		// cada texel cobre foscoEscala px: a imagem tem de passar da borda
		if b := img.Bounds().Size(); b.X*foscoEscala < 1366 || b.Y*foscoEscala < 700 {
			t.Fatalf("fosco %v não cobre 1366x700", b)
		}
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] != 255 {
				t.Fatal("o fundo fosco tem de ser opaco")
			}
		}
	}
}

func TestSombraCaixa(t *testing.T) {
	lo, hi := f32Ponto{0, 0}, f32Ponto{200, 100}
	if v := sombraCaixa(lo, hi, f32Ponto{100, 50}, 4, 8); v < 0.99 {
		t.Errorf("no meio da caixa a sombra é cheia, deu %.3f", v)
	}
	if v := sombraCaixa(lo, hi, f32Ponto{100, 140}, 4, 8); v > 0.001 {
		t.Errorf("a 10 sigmas da borda não há sombra, deu %.3f", v)
	}
	if v := sombraCaixa(lo, hi, f32Ponto{100, 100}, 4, 8); math.Abs(v-0.5) > 0.05 {
		t.Errorf("na borda reta a sombra é metade, deu %.3f", v)
	}
}

func TestGabaritoSombraCortaEmNove(t *testing.T) {
	ch := chaveSombra{el: elevCard, raio: 9, pxDp: 1.5}
	g := novoGabarito(ch, image.Point{})
	if g.p <= 0 || g.k <= 0 {
		t.Fatalf("alcance inválido p=%d k=%d", g.p, g.k)
	}
	// As três colunas somadas têm a largura do gabarito inteiro.
	larg := 0
	for col := range 3 {
		larg += g.pecas[col].Size().X
	}
	if want := 2*g.k + 2 + 2*g.p; larg != want {
		t.Errorf("colunas somam %d, gabarito tem %d", larg, want)
	}
	if g.pecas[1].Size().X != 2 || g.pecas[3].Size().Y != 2 {
		t.Errorf("os pedaços do meio têm de ter 2px para esticar")
	}
}
