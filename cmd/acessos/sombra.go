package main

import (
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// SOMBRA NATURAL
//
// A sombra antiga empilhava cinco retângulos arredondados cada vez
// maiores: de longe passava, de perto eram anéis, e o degrau entre um e
// outro é o que deixava o cartão com cara de recortado em vez de pousado.
//
// Esta é a sombra de verdade: o retângulo arredondado DESFOCADO por uma
// gaussiana, calculado analiticamente (a integral da gaussiana num eixo
// é fechada — erf — e o outro eixo é amostrado; receita de Evan Wallace
// para sombras de caixa arredondada). Duas camadas, como num objeto de
// verdade sob luz de cima: uma de CONTATO, curta e firme, colada na
// base; outra AMBIENTE, larga e fraca, caindo para baixo. O "spread"
// negativo da ambiente encolhe a forma antes de desfocar — é o que deixa
// a sombra cair para baixo sem vazar para os lados, onde ela escureceria
// o cartão vizinho (cards da mesma fileira estão na mesma altura, não
// fazem sombra um no outro).
//
// Calcular por pixel a cada quadro seria caro; a sombra é calculada UMA
// vez por elevação/raio/escala num gabarito pequeno e cortada em nove
// pedaços (cantos inteiros, bordas esticadas). Todos os cards do Painel
// usam o mesmo gabarito, então são oito texturas no app inteiro.

// camadaSombra é uma camada no espírito do box-shadow do CSS.
type camadaSombra struct {
	dy, sigma, spread unit.Dp
	alfa, alfaEscuro  float32
}

type elevacao [2]camadaSombra

var (
	// elevCard: cards, menus, dicas, listas suspensas.
	elevCard = elevacao{
		{dy: 1, sigma: 1.5, alfa: 0.12, alfaEscuro: 0.45},
		{dy: 5, sigma: 6, spread: -3, alfa: 0.20, alfaEscuro: 0.55},
	}
	// elevDialogo: diálogos e o cartão do splash, mais altos que tudo.
	elevDialogo = elevacao{
		{dy: 2, sigma: 2, alfa: 0.12, alfaEscuro: 0.45},
		{dy: 14, sigma: 16, spread: -6, alfa: 0.26, alfaEscuro: 0.60},
	}
	// elevBusca: a caixa do atalho global vive numa janela própria com
	// margem transparente de 14dp (buscaMargem); a sombra tem de caber
	// nela, senão é cortada reta na borda da janela. Alcance para baixo
	// = dy + 3σ + spread = 3 + 10,5 - 2 = 11,5dp.
	elevBusca = elevacao{
		{dy: 1, sigma: 1.5, alfa: 0.12, alfaEscuro: 0.45},
		{dy: 3, sigma: 3.5, spread: -2, alfa: 0.20, alfaEscuro: 0.55},
	}
)

// sombra: sombra de card sob um retângulo arredondado de tamanho size.
func sombra(gtx layout.Context, size image.Point, raioDp unit.Dp) {
	sombraElev(gtx, size, raioDp, &elevCard)
}

// sombraVazada é a sombra só do lado de FORA da superfície. Precisa
// dela quem é translúcido (a caixa do atalho global, sobre o desktop):
// a sombra corre um pouco por dentro da borda, e através do vidro isso
// aparecia como uma moldura escura com um retângulo mais claro no meio
// — o miolo que o corte em nove não pinta. Superfície opaca não precisa:
// ela mesma cobre o que está embaixo.
func sombraVazada(gtx layout.Context, size image.Point, raioDp unit.Dp, el *elevacao) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	// Contorno de fora num sentido e a caixa arredondada no sentido
	// oposto: com a regra non-zero do Gio, a caixa vira um furo.
	folga := float32(gtx.Dp(48))
	w, h := float32(size.X), float32(size.Y)
	r := min(float32(gtx.Dp(raioDp)), w/2, h/2)
	const k = 0.5523 // controle da cúbica que aproxima um quarto de círculo
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(-folga, -folga))
	p.LineTo(f32.Pt(w+folga, -folga))
	p.LineTo(f32.Pt(w+folga, h+folga))
	p.LineTo(f32.Pt(-folga, h+folga))
	p.Close()
	p.MoveTo(f32.Pt(w-r, 0))
	p.LineTo(f32.Pt(r, 0))
	p.CubeTo(f32.Pt(r-r*k, 0), f32.Pt(0, r-r*k), f32.Pt(0, r))
	p.LineTo(f32.Pt(0, h-r))
	p.CubeTo(f32.Pt(0, h-r+r*k), f32.Pt(r-r*k, h), f32.Pt(r, h))
	p.LineTo(f32.Pt(w-r, h))
	p.CubeTo(f32.Pt(w-r+r*k, h), f32.Pt(w, h-r+r*k), f32.Pt(w, h-r))
	p.LineTo(f32.Pt(w, r))
	p.CubeTo(f32.Pt(w, r-r*k), f32.Pt(w-r+r*k, 0), f32.Pt(w-r, 0))
	p.Close()
	defer clip.Outline{Path: p.End()}.Op().Push(gtx.Ops).Pop()
	sombraElev(gtx, size, raioDp, el)
}

type chaveSombra struct {
	el     elevacao
	raio   int
	pxDp   float32
	escuro bool
	tam    image.Point // só no gabarito de tamanho exato
}

// gabaritoSombra: a sombra de um retângulo de referência, já cortada.
// p é quanto a sombra passa para fora do retângulo; k, quanto ela entra
// (até onde canto e borda ainda mexem no valor).
type gabaritoSombra struct {
	p, k  int
	pecas [9]paint.ImageOp
	unica paint.ImageOp // gabarito de tamanho exato (retângulo pequeno)
}

var (
	gabaritos       = map[chaveSombra]*gabaritoSombra{}
	gabaritosExatos = map[chaveSombra]*gabaritoSombra{}
)

func sombraElev(gtx layout.Context, size image.Point, raioDp unit.Dp, el *elevacao) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	ch := chaveSombra{el: *el, raio: gtx.Dp(raioDp), pxDp: gtx.Metric.PxPerDp, escuro: tema.Escuro}
	g := gabaritos[ch]
	if g == nil {
		g = novoGabarito(ch, image.Point{})
		gabaritos[ch] = g
	}
	if size.X < 2*g.k || size.Y < 2*g.k {
		// Pequeno demais para o corte em nove (dica, item de lista):
		// sombra calculada no tamanho exato. Guarda poucas — tamanhos
		// pequenos variam com o texto e não vale acumular.
		ch.tam = size
		ge := gabaritosExatos[ch]
		if ge == nil {
			if len(gabaritosExatos) >= 24 {
				clear(gabaritosExatos)
			}
			ge = novoGabarito(ch, size)
			gabaritosExatos[ch] = ge
		}
		desenharPeca(gtx, ge.unica, -ge.p, -ge.p, 1, 1)
		return
	}

	p, k := g.p, g.k
	mx := float32(size.X-2*k) / 2 // os pedaços do meio têm 2px
	my := float32(size.Y-2*k) / 2
	xs := [3]int{-p, k, size.X - k}
	ys := [3]int{-p, k, size.Y - k}
	esc := [3]float32{1, 0, 1}
	for lin := range 3 {
		for col := range 3 {
			if lin == 1 && col == 1 {
				// O miolo fica todo debaixo do cartão: pintá-lo só
				// escureceria superfície translúcida.
				continue
			}
			sx, sy := esc[col], esc[lin]
			if col == 1 {
				sx = mx
			}
			if lin == 1 {
				sy = my
			}
			if sx == 0 || sy == 0 {
				continue
			}
			desenharPeca(gtx, g.pecas[lin*3+col], xs[col], ys[lin], sx, sy)
		}
	}
}

func desenharPeca(gtx layout.Context, img paint.ImageOp, x, y int, sx, sy float32) {
	t := op.Affine(f32.AffineId().Scale(f32.Point{}, f32.Pt(sx, sy)).Offset(f32.Pt(float32(x), float32(y)))).Push(gtx.Ops)
	img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	t.Pop()
}

// novoGabarito calcula a sombra. Com tam zero, de um quadrado de
// referência grande o bastante para ter borda reta no meio, e corta em
// nove; com tam, do retângulo exato, numa imagem só.
func novoGabarito(ch chaveSombra, tam image.Point) *gabaritoSombra {
	type cam struct{ dy, sigma, spread, alfa float64 }
	var cams [2]cam
	p, k := 0, 0
	r := float64(ch.raio)
	for i, c := range ch.el {
		px := float64(ch.pxDp)
		cm := cam{
			dy:     float64(c.dy) * px,
			sigma:  math.Max(float64(c.sigma)*px, 0.5),
			spread: float64(c.spread) * px,
			alfa:   float64(c.alfa),
		}
		if ch.escuro {
			cm.alfa = float64(c.alfaEscuro)
		}
		cams[i] = cm
		alcance := 3 * cm.sigma
		fora := int(math.Ceil(alcance+math.Max(cm.spread, 0)+math.Abs(cm.dy))) + 1
		dentro := int(math.Ceil(-math.Min(cm.spread, 0)+math.Abs(cm.dy)+math.Max(0, r+cm.spread)+alcance)) + 1
		p, k = max(p, fora), max(k, dentro)
	}

	w, h := tam.X, tam.Y
	if tam == (image.Point{}) {
		w, h = 2*k+2, 2*k+2
	}
	cw, chh := w+2*p, h+2*p
	img := image.NewRGBA(image.Rect(0, 0, cw, chh))
	for y := range chh {
		for x := range cw {
			// centro do pixel, em coordenadas do retângulo
			px, py := float64(x-p)+0.5, float64(y-p)+0.5
			livre := 1.0
			for _, c := range cams {
				lo := f32Ponto{-c.spread, -c.spread + c.dy}
				hi := f32Ponto{float64(w) + c.spread, float64(h) + c.spread + c.dy}
				v := sombraCaixa(lo, hi, f32Ponto{px, py}, c.sigma, math.Max(0, r+c.spread))
				livre *= 1 - v*c.alfa
			}
			a := 1 - livre
			img.Pix[img.PixOffset(x, y)+3] = uint8(math.Round(a * 255))
		}
	}

	g := &gabaritoSombra{p: p, k: k}
	if tam != (image.Point{}) {
		g.unica = paint.NewImageOp(img)
		return g
	}
	a := p + k // fim do canto, a partir da borda do gabarito
	cortes := [3][2]int{{0, a}, {a, a + 2}, {a + 2, cw}}
	for lin := range 3 {
		for col := range 3 {
			ret := image.Rect(cortes[col][0], cortes[lin][0], cortes[col][1], cortes[lin][1])
			peca := image.NewRGBA(image.Rectangle{Max: ret.Size()})
			for y := range ret.Dy() {
				copy(peca.Pix[y*peca.Stride:], img.Pix[img.PixOffset(ret.Min.X, ret.Min.Y+y):][:ret.Dx()*4])
			}
			g.pecas[lin*3+col] = paint.NewImageOp(peca)
		}
	}
	return g
}

type f32Ponto struct{ x, y float64 }

// sombraCaixa: cobertura (0..1) da sombra de um retângulo arredondado
// de lo a hi, desfocado por uma gaussiana de desvio sigma, no ponto pt.
func sombraCaixa(lo, hi, pt f32Ponto, sigma, raio float64) float64 {
	cx, cy := (lo.x+hi.x)/2, (lo.y+hi.y)/2
	mx, my := (hi.x-lo.x)/2, (hi.y-lo.y)/2
	if mx <= 0 || my <= 0 {
		return 0
	}
	raio = math.Min(raio, math.Min(mx, my))
	x, y := pt.x-cx, pt.y-cy
	baixo, alto := y-my, y+my
	ini := clampF(-3*sigma, baixo, alto)
	fim := clampF(3*sigma, baixo, alto)
	const amostras = 6
	passo := (fim - ini) / amostras
	yy := ini + passo/2
	v := 0.0
	for range amostras {
		v += sombraX(x, y-yy, sigma, raio, mx, my) * gauss(yy, sigma) * passo
		yy += passo
	}
	return math.Min(math.Max(v, 0), 1)
}

func sombraX(x, y, sigma, raio, mx, my float64) float64 {
	delta := math.Min(my-raio-math.Abs(y), 0)
	curva := mx - raio + math.Sqrt(math.Max(0, raio*raio-delta*delta))
	k := math.Sqrt(0.5) / sigma
	a := 0.5 + 0.5*math.Erf((x-curva)*k)
	b := 0.5 + 0.5*math.Erf((x+curva)*k)
	return b - a
}

func gauss(x, sigma float64) float64 {
	return math.Exp(-(x*x)/(2*sigma*sigma)) / (math.Sqrt(2*math.Pi) * sigma)
}

func clampF(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(v, hi))
}
