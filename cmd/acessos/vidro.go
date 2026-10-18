package main

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// VIDRO FOSCO
//
// O Gio não tem blur. Translucidez sem blur deixa o fundo ATRAVESSAR o
// cartão com forma e tudo — a faixa diagonal cortando o card, a palavra
// "REMOTO" do canto aparecendo atrás do nome da máquina —, e por isso o
// card tinha de ser quase opaco (Vidro2 0,78) para continuar legível.
//
// O fundo da janela, porém, não depende do conteúdo: é função só do
// tamanho, do tema e da escala (gradientes, faixas e os blocos de texto
// do canto, ver fundoJanela). Então dá para refazê-lo na CPU, uma vez,
// em baixa resolução, desfocar, e pintar essa imagem atrás de cada
// cartão. O que o patch do fork (paint.ImageOp.Tela) acrescenta é a
// âncora: a imagem fica presa à JANELA e não ao cartão, então cada
// cartão mostra exatamente o pedaço do fundo que está atrás dele — com
// rolagem, com hover, sem o cartão saber onde está na tela.
//
// Vale só para superfícies que ficam DIRETO sobre o fundo da janela
// (cards do Painel). Diálogo e menu ficam por cima de outros cartões,
// que esta imagem não tem — neles o vidro mostraria o fundo "por trás"
// dos cards, como raio X.

const (
	// foscoEscala: cada texel cobre foscoEscala×foscoEscala pixels da
	// janela. O desfoque apaga o detalhe de qualquer jeito; em 1/8 a
	// imagem de uma janela 1920×1080 tem 33 mil texels e sai em poucos
	// milissegundos, o bastante para refazer a cada passo de um resize.
	foscoEscala = 8
	// foscoSigma é o raio do desfoque, em pixels da janela.
	foscoSigma = 22
)

var fosco struct {
	tam    image.Point
	escuro bool
	pxSp   float32
	img    paint.ImageOp
	ok     bool
}

// atualizarFosco refaz a imagem desfocada quando o fundo muda. Chamada
// por fundoJanela, então só a janela principal tem fosco — e é só nela
// que há cards sobre o fundo.
func atualizarFosco(gtx layout.Context, size image.Point) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	px := gtx.Metric.PxPerSp
	if fosco.ok && fosco.tam == size && fosco.escuro == tema.Escuro && fosco.pxSp == px {
		return
	}
	img := renderFosco(size, px)
	fosco.img = paint.NewImageOp(img)
	fosco.img.Filter = paint.FilterLinear
	fosco.img.Tela = foscoEscala
	fosco.tam, fosco.escuro, fosco.pxSp, fosco.ok = size, tema.Escuro, px, true
}

// superficieVidro: vidro fosco. Por baixo, o fundo da janela desfocado;
// por cima, a tinta (fina — VidroTinta), a mesma luz de superficieRelevo
// e a borda. Com borda transparente, a borda é de LUZ: clara em cima,
// apagando para baixo, como a quina de um vidro iluminado de cima — que
// é de onde a sombra (sombra.go) diz que a luz vem.
func superficieVidro(gtx layout.Context, size image.Point, tinta, borda color.NRGBA, raioDp unit.Dp) {
	var fundo *paint.ImageOp
	if fosco.ok {
		fundo = &fosco.img
	}
	pintarVidro(gtx, size, tinta, borda, raioDp, fundo)
}

// vidroSobreDesktop: o mesmo vidro para uma janela que flutua sobre o
// DESKTOP (a busca do atalho global). Quem desfoca o que está atrás é o
// compositor (app.Window.DesfocarFundo); aqui só vão tinta, luz e borda.
// Não usa o fosco da janela principal — é outra janela, outra goroutine,
// e o fundo dela é o desktop, não o gradiente do app.
func vidroSobreDesktop(gtx layout.Context, size image.Point, tinta color.NRGBA, raioDp unit.Dp) {
	pintarVidro(gtx, size, tinta, transparente, raioDp, nil)
}

func pintarVidro(gtx layout.Context, size image.Point, tinta, borda color.NRGBA, raioDp unit.Dp, fundo *paint.ImageOp) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	rr := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(raioDp))
	molde := rr.Push(gtx.Ops)
	if fundo != nil {
		fundo.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
	}
	paint.ColorOp{Color: tinta}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	molde.Pop()
	brilhoRelevo(gtx, size, raioDp)

	cima, baixo := rgba(0xffffff, 0.85), rgba(0x11161a, 0.10)
	if tema.Escuro {
		cima, baixo = rgba(0xffffff, 0.11), rgba(0xffffff, 0.03)
	}
	bordaDeLuz(gtx, rr, size, cima, baixo, borda)
}

// bordaDeLuz: traço de 1px em degradê (cima → baixo), com uma borda
// chapada opcional por cima — é assim que o hover do card ACRESCENTA a
// Borda2 aos poucos sem a borda de luz sumir de uma vez.
func bordaDeLuz(gtx layout.Context, rr clip.RRect, size image.Point, cima, baixo, borda color.NRGBA) {
	traco := clip.Stroke{Path: rr.Path(gtx.Ops), Width: 1}.Op().Push(gtx.Ops)
	paint.LinearGradientOp{
		Stop1: f32.Pt(0, 0), Color1: cima,
		Stop2: f32.Pt(0, float32(size.Y)), Color2: baixo,
	}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	if borda.A > 0 {
		paint.ColorOp{Color: borda}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
	}
	traco.Pop()
}

// superficieCartao: superfície OPACA (diálogo, menu, dica, splash) com a
// mesma linguagem do vidro — brilho do topo e borda de luz —, mas sem
// fundo desfocado: ela fica por cima de outros cards, e o fundo fosco
// mostraria a janela "por trás" deles. No claro a borda é a Borda que
// escurece para baixo (branco sobre branco não teria quina); no escuro,
// luz no topo apagando.
func superficieCartao(gtx layout.Context, size image.Point, raioDp unit.Dp) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	rr := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(raioDp))
	paint.FillShape(gtx.Ops, tema.Cartao, rr.Op(gtx.Ops))
	brilhoRelevo(gtx, size, raioDp)
	cima, baixo := comAlfa(tema.Borda, 0.55), tema.Borda2
	if tema.Escuro {
		cima, baixo = rgba(0xffffff, 0.14), rgba(0xffffff, 0.05)
	}
	bordaDeLuz(gtx, rr, size, cima, baixo, transparente)
}

// faixaFosca: barra retangular translúcida (lateral, rodapé, barra da
// sessão) com o fundo desfocado por baixo. Só na janela PRINCIPAL — é a
// única que tem o fundo fosco, e ela é reconhecida pelo Theme (um por
// janela; temaApp é o dela). A barra da sessão também é desenhada na
// janela destacada, sobre a cor sólida dela: lá vai só a tinta de sempre.
func faixaFosca(gtx layout.Context, th *material.Theme, size image.Point) {
	r := clip.Rect{Max: size}.Push(gtx.Ops)
	defer r.Pop()
	if th == temaApp && fosco.ok {
		fosco.img.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		paint.ColorOp{Color: tema.BarraFosca}.Add(gtx.Ops)
	} else {
		paint.ColorOp{Color: tema.Barra}.Add(gtx.Ops)
	}
	paint.PaintOp{}.Add(gtx.Ops)
}

// ------------------------------------------------------------ CPU

// corLin é cor PRÉ-MULTIPLICADA em espaço linear — o mesmo espaço em que
// o Gio compõe. Refazer o fundo em sRGB daria um tom diferente do da
// janela, e o cartão viraria uma mancha de outra cor.
type corLin struct{ r, g, b, a float32 }

func paraLin(c color.NRGBA) corLin {
	a := float32(c.A) / 255
	return corLin{srgbLin(c.R) * a, srgbLin(c.G) * a, srgbLin(c.B) * a, a}
}

var tabSrgbLin = func() (t [256]float32) {
	for i := range t {
		c := float64(i) / 255
		if c <= 0.04045 {
			t[i] = float32(c / 12.92)
		} else {
			t[i] = float32(math.Pow((c+0.055)/1.055, 2.4))
		}
	}
	return
}()

func srgbLin(v uint8) float32 { return tabSrgbLin[v] }

// tabLinSrgb: a volta, por tabela — math.Pow por canal era metade do
// tempo de renderFosco. 16 mil degraus bastam: perto do preto, onde a
// curva do sRGB é mais íngreme, um degrau vale menos de um nível de 8 bits.
const degrausLin = 1 << 14

var tabLinSrgb = func() (t [degrausLin + 1]uint8) {
	for i := range t {
		c := float64(i) / degrausLin
		if c < 0.0031308 {
			c *= 12.92
		} else {
			c = 1.055*math.Pow(c, 1/2.4) - 0.055
		}
		t[i] = uint8(math.Round(c * 255))
	}
	return
}()

func linSrgb(c float32) uint8 {
	if c <= 0 {
		return 0
	} else if c >= 1 {
		return 255
	}
	return tabLinSrgb[int(c*degrausLin+0.5)]
}

// sobre compõe s por cima de d (Porter-Duff "over", pré-multiplicado).
func sobre(d, s corLin) corLin {
	k := 1 - s.a
	return corLin{s.r + d.r*k, s.g + d.g*k, s.b + d.b*k, s.a + d.a*k}
}

func (c corLin) mul(k float32) corLin { return corLin{c.r * k, c.g * k, c.b * k, c.a * k} }

func misturar(a, b corLin, t float32) corLin {
	return corLin{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t, a.a + (b.a-a.a)*t}
}

// gradLin é gradCSS fora do Gio: mesma convenção de ângulo, mesmas
// paradas, mesma interpolação (linear, pré-multiplicada, travada nas
// pontas — é o que o shader de gradiente do Gio faz).
type gradLin struct {
	p1x, p1y, dx, dy, inv float32
	c1, c2                corLin
}

func novoGradLin(w, h float32, anguloGraus float64, c1 color.NRGBA, pos1 float64, c2 color.NRGBA, pos2 float64) gradLin {
	rad := anguloGraus * math.Pi / 180
	sx, sy := math.Sin(rad), -math.Cos(rad)
	comp := math.Abs(float64(w)*sx) + math.Abs(float64(h)*sy)
	cx, cy := float64(w)/2, float64(h)/2
	p1x, p1y := cx+sx*(pos1-0.5)*comp, cy+sy*(pos1-0.5)*comp
	p2x, p2y := cx+sx*(pos2-0.5)*comp, cy+sy*(pos2-0.5)*comp
	dx, dy := p2x-p1x, p2y-p1y
	n := dx*dx + dy*dy
	if n == 0 {
		n = 1
	}
	return gradLin{float32(p1x), float32(p1y), float32(dx), float32(dy), float32(1 / n), paraLin(c1), paraLin(c2)}
}

func (g gradLin) em(x, y float32) corLin {
	t := ((x-g.p1x)*g.dx + (y-g.p1y)*g.dy) * g.inv
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return misturar(g.c1, g.c2, t)
}

// dentroQuad: ponto dentro do quadrilátero convexo (qualquer sentido).
func dentroQuad(p [4]f32.Point, x, y float32) bool {
	pos, neg := false, false
	for i := range 4 {
		a, b := p[i], p[(i+1)%4]
		c := (b.X-a.X)*(y-a.Y) - (b.Y-a.Y)*(x-a.X)
		if c > 0 {
			pos = true
		} else if c < 0 {
			neg = true
		}
	}
	return !(pos && neg)
}

// renderFosco refaz fundoJanela em baixa resolução e desfoca. Pontos,
// linha lateral e nós ficam de fora: são finos demais para sobrar
// qualquer coisa depois do desfoque. O texto do canto entra como uma
// mancha do tamanho das letras com a tinta média delas — é o que o
// desfoque deixaria dele.
func renderFosco(size image.Point, pxPorSp float32) *image.RGBA {
	const e = foscoEscala
	w, h := float32(size.X), float32(size.Y)
	lw, lh := size.X/e+2, size.Y/e+2

	base := novoGradLin(w, h, 170, tema.Fundo1, 0, tema.Fundo3, 1)
	luz1 := novoGradLin(w, h, 150, tema.Luz1, 0, transparente, 0.55)
	luz2 := novoGradLin(w, h, 15, tema.Luz2, 0, transparente, 0.45)
	faixas := faixasCanto(w, h)
	var corFaixa [4]corLin
	for i, f := range faixas {
		corFaixa[i] = paraLin(f.cor)
	}

	// Manchas de texto e barras do canto, em pixels da janela. Métrica da
	// IBM Plex Mono: avanço 0,6 em, altura de maiúscula 0,7 em, linha de
	// base em ~1,0 em; letra maiúscula cobre ~22% da própria caixa.
	type mancha struct {
		r   image.Rectangle
		cor corLin
		g   *gradLin
	}
	var manchas []mancha
	tinta := paraLin(comAlfa(tema.Sec, cantoTxtAlfa*0.22))
	em := cantoTamFonte * pxPorSp
	barra := novoGradLin(cantoLargBarra, cantoAltBarra, 90, tema.Roxo, 0, tema.Verde, 1)
	for _, b := range blocosCanto {
		x, y := w*cantoX, h*b.y
		for i, s := range b.linhas {
			topo := y + float32(i*cantoAltLinha)
			manchas = append(manchas, mancha{r: image.Rect(
				int(x), int(topo+0.30*em), int(x+0.6*em*float32(len([]rune(s)))), int(topo+1.0*em)),
				cor: tinta})
		}
		by := y + float32(len(b.linhas))*cantoAltLinha + cantoBarraDy
		g := barra
		manchas = append(manchas, mancha{r: image.Rect(int(x), int(by), int(x)+cantoLargBarra, int(by)+cantoAltBarra), g: &g})
	}

	// Caixa de cada faixa, para descartar rápido o texel que nem chega
	// perto dela (a maioria).
	var caixas [4]image.Rectangle
	for k, f := range faixas {
		r := image.Rectangle{Min: image.Pt(math.MaxInt32, math.MaxInt32), Max: image.Pt(math.MinInt32, math.MinInt32)}
		for _, p := range f.pts {
			r.Min.X, r.Min.Y = min(r.Min.X, int(p.X)-1), min(r.Min.Y, int(p.Y)-1)
			r.Max.X, r.Max.Y = max(r.Max.X, int(p.X)+1), max(r.Max.Y, int(p.Y)+1)
		}
		caixas[k] = r
	}

	buf := make([]corLin, lw*lh)
	const area = 1.0 / (e * e)
	for j := range lh {
		for i := range lw {
			// Os gradientes são lisos: o centro do texel basta. Só as
			// bordas (faixas, manchas) precisam de cobertura.
			cx, cy := (float32(i)+0.5)*e, (float32(j)+0.5)*e
			c := base.em(cx, cy)
			c = sobre(c, luz1.em(cx, cy))
			c = sobre(c, luz2.em(cx, cy))
			tex := image.Rect(i*e, j*e, (i+1)*e, (j+1)*e)
			for k, f := range faixas {
				if !tex.Overlaps(caixas[k]) {
					continue
				}
				n := 0
				for _, d := range [4][2]float32{{0.25, 0.25}, {0.75, 0.25}, {0.25, 0.75}, {0.75, 0.75}} {
					if dentroQuad(f.pts, (float32(i)+d[0])*e, (float32(j)+d[1])*e) {
						n++
					}
				}
				if n > 0 {
					c = sobre(c, corFaixa[k].mul(float32(n)/4))
				}
			}
			for _, m := range manchas {
				in := tex.Intersect(m.r)
				if in.Empty() {
					continue
				}
				cor := m.cor
				if m.g != nil {
					cor = m.g.em(cx-float32(m.r.Min.X), 0)
				}
				c = sobre(c, cor.mul(float32(in.Dx()*in.Dy())*area))
			}
			buf[j*lw+i] = c
		}
	}

	desfocar(buf, lw, lh, foscoSigma/float64(e))

	img := image.NewRGBA(image.Rect(0, 0, lw, lh))
	for j := range lh {
		for i := range lw {
			c := buf[j*lw+i]
			o := img.PixOffset(i, j)
			img.Pix[o+0] = linSrgb(c.r)
			img.Pix[o+1] = linSrgb(c.g)
			img.Pix[o+2] = linSrgb(c.b)
			img.Pix[o+3] = 255
		}
	}
	return img
}

// desfocar aplica um desfoque gaussiano aproximado por três passadas de
// média móvel em cada eixo (a soma de três caixas já é indistinguível de
// uma gaussiana a olho), com a borda replicada.
func desfocar(buf []corLin, w, h int, sigma float64) {
	if sigma <= 0 {
		return
	}
	tmp := make([]corLin, len(buf))
	for _, r := range raiosCaixa(sigma, 3) {
		caixa(buf, tmp, w, r, 1, w) // horizontal: linhas de w, vizinho ao lado
		caixa(tmp, buf, h, r, w, 1) // vertical: colunas de h, vizinho embaixo
	}
}

// caixa: média móvel de raio r ao longo de cada linha de comprimento m;
// passo é a distância entre vizinhos na linha, salto entre linhas.
func caixa(src, dst []corLin, m, r, passo, salto int) {
	linhas := len(src) / m
	inv := 1 / float32(2*r+1)
	for l := range linhas {
		ini := l * salto
		at := func(i int) corLin {
			if i < 0 {
				i = 0
			} else if i >= m {
				i = m - 1
			}
			return src[ini+i*passo]
		}
		var s corLin
		for i := -r; i <= r; i++ {
			c := at(i)
			s = corLin{s.r + c.r, s.g + c.g, s.b + c.b, s.a + c.a}
		}
		for i := range m {
			dst[ini+i*passo] = s.mul(inv)
			a, b := at(i+r+1), at(i-r)
			s = corLin{s.r + a.r - b.r, s.g + a.g - b.g, s.b + a.b - b.b, s.a + a.a - b.a}
		}
	}
}

// raiosCaixa: raios de n caixas cuja soma aproxima uma gaussiana de
// desvio sigma (receita de Kovesi).
func raiosCaixa(sigma float64, n int) []int {
	wIdeal := math.Sqrt(12*sigma*sigma/float64(n) + 1)
	wl := int(math.Floor(wIdeal))
	if wl%2 == 0 {
		wl--
	}
	wu := wl + 2
	mIdeal := (12*sigma*sigma - float64(n*wl*wl) - 4*float64(n*wl) - 3*float64(n)) / (-4*float64(wl) - 4)
	m := int(math.Round(mIdeal))
	r := make([]int, n)
	for i := range r {
		if i < m {
			r[i] = (wl - 1) / 2
		} else {
			r[i] = (wu - 1) / 2
		}
	}
	return r
}
