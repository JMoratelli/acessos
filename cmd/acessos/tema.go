package main

import (
	"image"
	"image/color"
	"math"
	"os"
	"sync/atomic"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Tema é o dicionário de tokens — mesma ideia do TEMAS do tema.py: cor
// NUNCA aparece literal num componente, só o token. Trocar de tema é
// trocar o dicionário inteiro, sem folha duplicada.
//
// Os valores são os MESMOS do tema.py (calibrados em tela real, não
// derivados no papel).
type Tema struct {
	Fundo, Cartao, Borda, Borda2                color.NRGBA
	Texto, Sec, Fraco                           color.NRGBA
	// CardFraco é o Fraco QUANDO o texto está por cima do preenchimento
	// translúcido do card (Vidro2/Vidro3), não do fundo da janela — ver
	// comentário no valor de cada tema.
	CardFraco color.NRGBA
	Fundo1, Fundo2, Fundo3                      color.NRGBA
	Luz1, Luz2                                  color.NRGBA
	Vidro1, Vidro2, Vidro3                      color.NRGBA
	LuzB                                        color.NRGBA
	Barra                                       color.NRGBA
	HeroLuz1, HeroLuz2                          color.NRGBA
	Topo1, Topo2, Titulo1, Titulo2, TituloBorda color.NRGBA
	Abas1, Abas2                                color.NRGBA
	TopoTxt, TopoSec                            color.NRGBA
	Vidro, VidroH, VidroB                       color.NRGBA
	Palco, TermBg, TermFg                       color.NRGBA
	Hover, Campo                                color.NRGBA
	OkBg, OkFg                                  color.NRGBA
	ErroBg, ErroFg, ErroH                       color.NRGBA
	AtencaoBg, AtencaoFg                        color.NRGBA
	Azul, AzulH, Verde, Roxo                    color.NRGBA
	AzulFraco, VerdeFraco                       color.NRGBA
	RoxoFraco                                   color.NRGBA
	Hero1, Hero2, HeroTxt                       color.NRGBA
	HeroSec                                     color.NRGBA
	// MarcaFundo é o ícone-marca-d'água do fundo da janela (fundoMarca);
	// LinhaFundo são os traços de circuito (fundoCircuito). Baixíssima
	// opacidade de propósito — é decoração, não conteúdo.
	MarcaFundo, LinhaFundo color.NRGBA
	// SombraBase/SombraPasso calibram sombra(): preto puro em baixa alfa,
	// que basta pra "levantar" um cartão claro do fundo claro, é
	// invisível sobre um fundo já quase preto — o escuro precisa de bem
	// mais alfa pra registrar a mesma diferença perceptível.
	SombraBase, SombraPasso uint8
}

var temaClaro = Tema{
	Fundo: hex(0xd9e2ee), Cartao: hex(0xffffff), Borda: hex(0xc7cfd8), Borda2: hex(0xacb6c2),
	Texto: hex(0x1b232b), Sec: hex(0x5b6976), Fraco: hex(0x94a1ad),
	// Sem relato de baixo contraste no card claro — mesmo valor do Fraco.
	CardFraco: hex(0x94a1ad),
	Fundo1: hex(0xe7edf6), Fundo2: hex(0xd9e2ee), Fundo3: hex(0xc7d3e3),
	Luz1: rgba(0x4c6ef5, 0.10), Luz2: rgba(0x0ca678, 0.07),
	Vidro1: rgba(0xffffff, 0.70), Vidro2: rgba(0xffffff, 0.86), Vidro3: rgba(0xffffff, 1.00),
	// Borda do card em repouso: era 11%, quase se perdia contra o fundo —
	// ver o relato de bordas "dissolvendo" no tema claro.
	LuzB:     rgba(0x11161a, 0.17),
	Barra:    rgba(0xe8edf3, 0.86),
	HeroLuz1: rgba(0x748ffc, 0.30), HeroLuz2: rgba(0x38d9a9, 0.20),
	Topo1: hex(0xf2f5f9), Topo2: hex(0xe6ecf3),
	// A faixa de janela (minimizar/maximizar/fechar) é MAIS ESCURA que a
	// tira de abas de propósito: são duas faixas coladas, e sem diferença
	// de tom elas viram um bloco só com um degrau de cor no meio sem
	// explicação. Aqui a hierarquia fica: janela (mais escura) > abas >
	// conteúdo.
	Titulo1: hex(0xdbe3ee), Titulo2: hex(0xcbd6e4), TituloBorda: rgba(0x11161a, 0.22),
	Abas1: hex(0xf5f8fc), Abas2: hex(0xeaf0f7),
	TopoTxt: hex(0x11161a), TopoSec: hex(0x5b6976),
	// Sobre cromo CLARO quem modula é PRETO translúcido — branco sobre
	// barra clara resolve para a própria cor da barra e some (tema.py).
	Vidro: rgba(0x11161a, 0.05), VidroH: rgba(0x11161a, 0.11), VidroB: rgba(0x11161a, 0.12),
	Palco: hex(0x0b0f14), TermBg: hex(0x0d1117), TermFg: hex(0xd7dee6),
	Hover: hex(0xeef2f5), Campo: hex(0xffffff),
	OkBg: hex(0xdcf5ec), OkFg: hex(0x0b7a63),
	ErroBg: hex(0xfde4e4), ErroFg: hex(0xb02a37), ErroH: hex(0x8d1f2a),
	AtencaoBg: hex(0xfdf1d8), AtencaoFg: hex(0x8a5a00),
	Azul: hex(0x4c6ef5), AzulH: hex(0x3b5bdb), Verde: hex(0x0ca678), Roxo: hex(0x6b40d0),
	AzulFraco: rgba(0x4c6ef5, 0.13), VerdeFraco: rgba(0x0ca678, 0.13), RoxoFraco: rgba(0x6b40d0, 0.13),
	Hero1: hex(0x171c22), Hero2: hex(0x2b3a4a), HeroTxt: hex(0xffffff),
	// O hero é escuro NOS DOIS temas (é ele que ancora a página), então o
	// texto secundário dele tem que vir da paleta ESCURA. Usar o topo_sec
	// do tema claro (#5b6976) sobre um hero quase preto é o mesmo erro da
	// "faixa preta acidental" que a skill descreve, só que invertido.
	HeroSec: hex(0x9aa6b2),
	// Não é linear-por-baixo-do-branco aqui (o overlay é ESCURO sobre
	// fundo CLARO — o caso "normal" que a conta em sRGB já acerta), por
	// isso o alfa pode ser mais alto que no escuro sem medo de exagerar.
	MarcaFundo: rgba(0x11161a, 0.09), LinhaFundo: rgba(0x11161a, 0.16),
	SombraBase: 6, SombraPasso: 3,
}

var temaEscuro = Tema{
	Fundo: hex(0x10141a), Cartao: hex(0x171b21), Borda: hex(0x242a32), Borda2: hex(0x333b45),
	Texto: hex(0xd6dde5), Sec: hex(0x94a1ae), Fraco: hex(0x68757f),
	// Fraco foi calibrado contra Fundo (quase preto). O preenchimento
	// translúcido do card (Vidro2/Vidro3) sai mais claro que a conta em
	// sRGB prevê — o Gio compõe em linear, mesmo aviso do Luz1/Luz2 mais
	// abaixo — e Fraco quase desaparecia em cima dele, pior ainda no
	// hover/selecionado (Vidro3, translúcido a mais ainda). CardFraco é
	// um degrau mais claro, só pra texto que fica sobre o card.
	CardFraco: hex(0x8a97a3),
	Fundo1: hex(0x1a212b), Fundo2: hex(0x10141a), Fundo3: hex(0x080b0f),
	// As luzes vão bem abaixo do alfa do tema.py (0.16/0.10 lá). O Gio
	// compõe em espaço LINEAR, o GTK/cairo em sRGB: a mesma rgba() sobre
	// um fundo quase preto sai bem mais forte aqui. Uma primeira redução
	// (~55% do original) ainda deixava o verde tomar a metade da tela
	// num teste real — cortado bem mais forte agora.
	Luz1: rgba(0x748ffc, 0.045), Luz2: rgba(0x38d9a9, 0.02),
	// Preenchimento do card: DE VOLTA ao valor original. Subir o alfa
	// aqui pareceu seguro pela conta em sRGB, mas o Gio compõe em linear
	// (mesmo aviso do Luz1/Luz2 acima) — o resultado real saiu bem mais
	// claro que a conta previa, um cinza lavado que atropelava até o
	// contraste do texto secundário em cima do card.
	Vidro1: rgba(0xffffff, 0.045), Vidro2: rgba(0xffffff, 0.075), Vidro3: rgba(0xffffff, 0.13),
	// A borda do card em repouso era branco puro translúcido — sobre fundo
	// quase preto isso lê como BRILHO, não como borda de material. Um
	// azul-acinzentado neutro em vez de branco resolve isso sem mudar o
	// matiz frio do resto da paleta escura.
	LuzB: rgba(0x8fa0b8, 0.16),
	Barra:    rgba(0x10141a, 0.86),
	HeroLuz1: rgba(0x748ffc, 0.13), HeroLuz2: rgba(0x38d9a9, 0.08),
	Topo1: hex(0x080a0d), Topo2: hex(0x12171d),
	Titulo1: hex(0x0e131b), Titulo2: hex(0x03050a), TituloBorda: rgba(0x000000, 0.75),
	Abas1: hex(0x18202b), Abas2: hex(0x1e2733),
	TopoTxt: hex(0xffffff), TopoSec: hex(0x9aa6b2),
	Vidro: rgba(0xffffff, 0.06), VidroH: rgba(0xffffff, 0.13), VidroB: rgba(0xffffff, 0.10),
	Palco: hex(0x06080a), TermBg: hex(0x0d1117), TermFg: hex(0xd7dee6),
	Hover: hex(0x1e242b), Campo: hex(0x171b21),
	OkBg: hex(0x0d2f28), OkFg: hex(0x4fd1b0),
	ErroBg: hex(0x341a1e), ErroFg: hex(0xc9414d), ErroH: hex(0xe05561),
	AtencaoBg: hex(0x332810), AtencaoFg: hex(0xe0b458),
	Azul: hex(0x748ffc), AzulH: hex(0x91a7ff), Verde: hex(0x38d9a9), Roxo: hex(0xb197fc),
	AzulFraco: rgba(0x748ffc, 0.16), VerdeFraco: rgba(0x38d9a9, 0.16), RoxoFraco: rgba(0xb197fc, 0.16),
	Hero1: hex(0x0b0e12), Hero2: hex(0x1c2733), HeroTxt: hex(0xffffff),
	HeroSec: hex(0x9aa6b2),
	// Branco em vez de escuro (o fundo já é quase preto) — mas com o
	// MESMO cuidado do LuzB/Vidro logo acima: o Gio compõe em linear, e
	// um branco translúcido sobre fundo escuro sai mais forte do que a
	// conta prevê. Alfa bem baixo de propósito; testar no app antes de
	// considerar calibrado.
	MarcaFundo: rgba(0xffffff, 0.045), LinhaFundo: rgba(0xffffff, 0.07),
	// ~4x o do tema claro: preto de baixa alfa não registra sobre um
	// fundo que já está perto do preto — sem isso a sombra existia só no
	// código, o cartão escuro nunca teve pista de elevação nenhuma.
	SombraBase: 26, SombraPasso: 12,
}

// Foco em campo de texto.
//
// O teclado do Wayland é roteado direto para a aba remota (ver
// internal/grab): sem isto, digitar na busca da lateral com uma sessão
// VNC aberta mandava as letras para a máquina remota e o campo ficava
// vazio. Todo campo de texto do app passa pelos helpers caixaEditor /
// caixaBusca, então marcar ali cobre a interface inteira.
var focoEmCampo atomic.Bool

// marcarFoco é chamado pelos helpers de campo a cada quadro.
func marcarFoco(focado bool) {
	if focado {
		focoEmCampo.Store(true)
	}
}

// temaApp é o material.Theme do app (fonte/shaper), preenchido no main.
var temaApp *material.Theme

// tema é o tema em uso. Trocar aqui troca o app inteiro.
var tema = temaClaro

// Tipografia — mesmas famílias e tamanhos do tema.py. As famílias IBM Plex
// não estão instaladas nesta máquina, então o original já cai nos mesmos
// substitutos que pedimos aqui (o Gio consulta as fontes do sistema).
// CANTARELL FICA DE FORA DE PROPÓSITO. Ela está instalada (é a fonte que o
// app GTK original acaba usando aqui), mas o shaper do Gio não consegue
// carregá-la: devolve run de largura ZERO, e — pior — um nome assim no
// meio da lista zera a lista INTEIRA em vez de cair pro próximo. O sintoma
// era todo texto em sans sumir da tela, com o layout se fechando em volta
// do nada. Fira Sans é o substituto humanista mais próximo que carrega.
var (
	fonteMono = font.Font{Typeface: "IBM Plex Mono, DejaVu Sans Mono, monospace"}
	fonteSans = font.Font{Typeface: "IBM Plex Sans, Inter, Fira Sans, DejaVu Sans, sans-serif"}
	fonteCond = font.Font{Typeface: "IBM Plex Sans Condensed, Fira Sans Condensed, Fira Sans, DejaVu Sans, sans-serif"}
)

// Tamanhos, um por classe do CSS original — assim o que muda aqui muda no
// lugar certo, em vez de números soltos no meio do layout.
const (
	spHeroTitulo  = unit.Sp(32) // .hero-titulo
	spHeroNum     = unit.Sp(27) // .hero-num
	spHeroEyebrow = unit.Sp(10) // .hero-eyebrow
	spHeroSub     = unit.Sp(10) // .hero-sub
	spHeroCap     = unit.Sp(9)  // .hero-cap
	spGrupoTitulo = unit.Sp(18) // .grupo-titulo
	spSubgrupo    = unit.Sp(15) // .subgrupo-titulo
	spGrupoCont   = unit.Sp(10) // .grupo-cont
	spCardNome    = unit.Sp(17) // .card-nome
	spCardHost    = unit.Sp(12) // .card-host
	spCardMeta    = unit.Sp(9)  // .card-meta
	spAbaNome     = unit.Sp(11) // .aba-nome
	spBtnTopo     = unit.Sp(10) // .btn-topo
	spRodape      = unit.Sp(11) // .rodape-info
	spMarcaTopo   = unit.Sp(13) // .marca-topo
	spMarcaSub    = unit.Sp(10) // .marca-sub
	spSecundario  = unit.Sp(11) // .secundario
	spCorpo       = unit.Sp(12) // .mono / corpo
)

func hex(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

func rgba(v uint32, alpha float64) color.NRGBA {
	c := hex(v)
	c.A = uint8(math.Round(alpha * 255))
	return c
}

// ------------------------------------------------------------- texto

func txt(th *material.Theme, f font.Font, tam unit.Sp, s string, cor color.NRGBA) material.LabelStyle {
	l := material.Label(th, tam, s)
	l.Font = f
	l.Color = cor
	l.MaxLines = 1
	return l
}

// paragrafo é texto que PODE quebrar em várias linhas: explicação de
// diálogo, não rótulo de lista. O txt() padrão corta em UMA linha com
// reticências, que é o certo numa tabela e errado numa frase — a frase
// simplesmente sumia pela direita.
func paragrafo(th *material.Theme, f font.Font, tam unit.Sp, s string, cor color.NRGBA) layout.Widget {
	l := material.Label(th, tam, s)
	l.Font = f
	l.Color = cor
	return l.Layout
}

// rotuloLinha é rótulo de UMA linha só, cortado com reticências. Comando é
// linear: quebrar um comando longo em duas linhas muda o que a pessoa lê e
// desalinha a lista inteira.
func rotuloLinha(th *material.Theme, f font.Font, tam unit.Sp, s string, cor color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := txt(th, f, tam, s, cor)
		l.MaxLines = 1
		return l.Layout(gtx)
	}
}

func rotulo(th *material.Theme, f font.Font, tam unit.Sp, s string, cor color.NRGBA) layout.Widget {
	return txt(th, f, tam, s, cor).Layout
}

func negrito(l material.LabelStyle) material.LabelStyle {
	l.Font.Weight = font.SemiBold
	return l
}

// ---------------------------------------------------- gradientes (CSS)

// gradCSS pinta um gradiente linear com a MESMA convenção de ângulo do
// CSS (0° = para cima, 90° = para a direita), já que os ângulos vieram
// copiados do tema.py. O Gio só faz gradiente de duas paradas; onde o CSS
// tinha três, a do meio fica bem perto da interpolação linear das pontas
// (diferença de ~3/255 por canal no fundo), então não vale o custo de
// emular com clip rotacionado.
func gradCSS(gtx layout.Context, size image.Point, anguloGraus float64,
	c1 color.NRGBA, pos1 float64, c2 color.NRGBA, pos2 float64) {

	if size.X <= 0 || size.Y <= 0 {
		return
	}
	rad := anguloGraus * math.Pi / 180
	dx, dy := math.Sin(rad), -math.Cos(rad)
	w, h := float64(size.X), float64(size.Y)
	comp := math.Abs(w*dx) + math.Abs(h*dy) // comprimento da linha do gradiente
	cx, cy := w/2, h/2

	ponto := func(p float64) f32.Point {
		d := (p - 0.5) * comp
		return f32.Pt(float32(cx+dx*d), float32(cy+dy*d))
	}

	paint.LinearGradientOp{
		Stop1: ponto(pos1), Color1: c1,
		Stop2: ponto(pos2), Color2: c2,
	}.Add(gtx.Ops)
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.PaintOp{}.Add(gtx.Ops)
}

var transparente = color.NRGBA{}

// fundoJanela: as três camadas do `window` do tema.py. Sem fundo variável
// não existe vidro — translucidez sobre cor chapada resolve para outra cor
// chapada, e o efeito some.
func fundoJanela(gtx layout.Context, size image.Point) {
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	defer area.Pop()

	// base (a de baixo, no CSS a última da lista)
	gradCSS(gtx, size, 170, tema.Fundo1, 0, tema.Fundo3, 1)
	// duas luzes de acento, ângulos opostos
	gradCSS(gtx, size, 150, tema.Luz1, 0, transparente, 0.55)
	gradCSS(gtx, size, 15, tema.Luz2, 0, transparente, 0.45)
	fundoMarca(gtx, size)
	fundoCircuito(gtx, size)
}

// fundoMarca desenha o próprio ícone do app (as duas telas sobrepostas,
// sem a seta — em silhueta ela só confundiria com o conteúdo por cima)
// gigante e quase invisível, ancorado no canto inferior direito. É
// decoração pura: baixa o suficiente pra nunca competir com um card ou
// texto de verdade por cima. Aprovado por prévia antes de entrar aqui.
func fundoMarca(gtx layout.Context, size image.Point) {
	// ~50% da altura da janela — dá identidade no canto sem tomar meia
	// tela (0.85 ocupava demais e não deixava espaço pros traços do
	// fundoCircuito não cruzarem por cima dela).
	esc := float32(size.Y) * 0.5 / 64
	if esc <= 0 {
		return
	}
	tr := op.Affine(f32.Affine2D{}.
		Scale(f32.Point{}, f32.Point{X: esc, Y: esc}).
		Offset(f32.Point{X: float32(size.X) - 58*esc, Y: float32(size.Y) - 58*esc}),
	).Push(gtx.Ops)
	defer tr.Pop()

	// raio em unidades do PRÓPRIO ícone (mesmo rx=7 do viewBox 64x64 de
	// icones/acessos.svg), não Dp — a escala acima já cuida de converter
	// para pixel de tela junto com o resto da forma.
	const raioIcone = 7
	rr1 := clip.UniformRRect(image.Rect(6, 8, 44, 38), raioIcone)
	paint.FillShape(gtx.Ops, tema.MarcaFundo, rr1.Op(gtx.Ops))
	rr2 := clip.UniformRRect(image.Rect(20, 24, 58, 56), raioIcone)
	paint.FillShape(gtx.Ops, tema.MarcaFundo, rr2.Op(gtx.Ops))
}

// fundoCircuito desenha alguns traços em ângulo reto — a mesma ideia de
// "placa de circuito" do banner (icones/banner-*.svg), bem mais discreta
// aqui porque fica atrás de conteúdo de verdade, não numa imagem estática.
// Coordenadas em FRAÇÃO da janela, não unidade do ícone: assim o desenho
// acompanha a proporção da tela em vez de vazar pra fora em janelas
// muito largas ou muito estreitas.
func fundoCircuito(gtx layout.Context, size image.Point) {
	w, h := float32(size.X), float32(size.Y)
	if w <= 0 || h <= 0 {
		return
	}

	linha := func(pts ...f32.Point) {
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(pts[0])
		for _, pt := range pts[1:] {
			p.LineTo(pt)
		}
		paint.FillShape(gtx.Ops, tema.LinhaFundo, clip.Stroke{Path: p.End(), Width: 1.2}.Op())
	}
	ponto := func(p f32.Point) {
		r := 2.4
		rr := clip.Ellipse{
			Min: image.Pt(int(p.X-float32(r)), int(p.Y-float32(r))),
			Max: image.Pt(int(p.X+float32(r)), int(p.Y+float32(r))),
		}
		paint.FillShape(gtx.Ops, tema.LinhaFundo, rr.Op(gtx.Ops))
	}

	// Só ângulo reto (horizontal + vertical, nunca diagonal), sempre
	// NASCENDO na borda da janela — um traço solto no meio do nada não
	// lê como circuito, lê como risco perdido (relatado em teste real).
	// E nunca cruzando por cima do canto onde a marca-d'água mora
	// (bottom-right, grosso modo x>0.7w e y>0.55h).
	dobraA := f32.Pt(w*0.07, h*0.14)
	linha(f32.Pt(0, h*0.14), dobraA, f32.Pt(w*0.07, h*0.24))
	ponto(dobraA)

	dobraB := f32.Pt(w*0.30, h*0.10)
	linha(f32.Pt(w*0.30, 0), dobraB, f32.Pt(w*0.38, h*0.10))
	ponto(dobraB)

	dobraC := f32.Pt(w*0.09, h*0.55)
	linha(f32.Pt(0, h*0.55), dobraC, f32.Pt(w*0.09, h*0.66))
	ponto(dobraC)

	dobraD := f32.Pt(w*0.05, h*0.90)
	linha(f32.Pt(w*0.05, h), dobraD, f32.Pt(w*0.13, h*0.90))
	ponto(dobraD)

	dobraE := f32.Pt(w*0.50, h*0.90)
	linha(f32.Pt(w*0.50, h), dobraE, f32.Pt(w*0.58, h*0.90))
	ponto(dobraE)

	// os dois pedidos por último: mesma posição das marcas vermelhas do
	// teste, só que em ângulo reto em vez de diagonal.
	dobraF := f32.Pt(w*0.25, h*0.10)
	linha(f32.Pt(w*0.25, 0), dobraF, f32.Pt(w*0.33, h*0.10))
	ponto(dobraF)

	dobraG1, dobraG2 := f32.Pt(w*0.90, h*0.10), f32.Pt(w*0.80, h*0.10)
	linha(f32.Pt(w*0.90, 0), dobraG1, dobraG2, f32.Pt(w*0.80, h*0.26))
	ponto(dobraG1)
	ponto(dobraG2)
}

// fundoHero: faixa escura que ancora a página (.hero).
func fundoHero(gtx layout.Context, size image.Point) {
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	defer area.Pop()

	gradCSS(gtx, size, 115, tema.Hero1, 0, tema.Hero2, 1)
	gradCSS(gtx, size, 160, tema.HeroLuz1, 0, transparente, 0.60)
	gradCSS(gtx, size, 20, tema.HeroLuz2, 0, transparente, 0.50)
}

// fundoAbas: a tira de abas tem COR PRÓPRIA, sólida, e não o vidro
// translúcido que ela usava antes. Translúcida ela pegava o tom do que
// estivesse atrás e encostava tanto na faixa da janela quanto no conteúdo;
// com as três faixas em tons distintos (janela mais escura, abas no meio,
// conteúdo) dá pra ver onde uma acaba e a outra começa sem procurar.
func fundoAbas(gtx layout.Context, size image.Point) {
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	defer area.Pop()
	gradCSS(gtx, size, 180, tema.Abas1, 0, tema.Abas2, 1)
}

// fundoCromo: lateral e outros cromos (180°, topo1 -> topo2).
func fundoCromo(gtx layout.Context, size image.Point) {
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	defer area.Pop()
	gradCSS(gtx, size, 180, tema.Topo1, 0, tema.Topo2, 1)
}

// fundoTitulo: a faixa da janela (menu, marca, botões de janela).
//
// O gradiente vai do claro pro ESCURO de cima pra baixo, ao contrário do
// cromo. Na primeira tentativa ele terminava claro justamente na emenda
// com a tira de abas, e as duas faixas se fundiam — no tema claro dava pra
// ver a diferença, no escuro não dava nenhuma. Terminando no tom mais
// escuro, a emenda fica evidente nos dois temas.
func fundoTitulo(gtx layout.Context, size image.Point) {
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	defer area.Pop()
	gradCSS(gtx, size, 180, tema.Titulo1, 0, tema.Titulo2, 1)
}

// ------------------------------------------------------------ formas

// superficie pinta um retângulo arredondado com borda de 1px. A borda é
// desenhada por cima, como traço: a caixa tem o MESMO tamanho com e sem
// borda, então estado nunca muda dimensão (regra dura da skill).
func superficie(gtx layout.Context, size image.Point, fundo, borda color.NRGBA, raioDp unit.Dp) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	rr := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(raioDp))
	paint.FillShape(gtx.Ops, fundo, rr.Op(gtx.Ops))
	if borda.A > 0 {
		paint.FillShape(gtx.Ops, borda, clip.Stroke{Path: rr.Path(gtx.Ops), Width: 1}.Op())
	}
}

// sombra desenha uma sombra suave sob um retângulo arredondado. O Gio não
// tem primitiva de sombra nem blur, então empilhamos algumas camadas
// arredondadas cada vez maiores e mais fracas — de perto é degradê, de
// longe é sombra, e é o suficiente pra levantar o cartão do fundo.
func sombra(gtx layout.Context, size image.Point, raioDp unit.Dp) {
	raio := gtx.Dp(raioDp)
	for i := 3; i >= 1; i-- {
		d := gtx.Dp(unit.Dp(float32(i)))
		r := image.Rect(-d/2, 0, size.X+d/2, size.Y+d)
		rr := clip.UniformRRect(r, raio+d)
		alfa := int(tema.SombraBase) + int(tema.SombraPasso)*(3-i)
		if alfa > 255 {
			alfa = 255
		}
		paint.FillShape(gtx.Ops, color.NRGBA{A: uint8(alfa)}, rr.Op(gtx.Ops))
	}
}

// icone desenha um ícone vetorial Material. Ícone de verdade, não glifo de
// fonte: a fonte não tem emoji e desenha "tofu" (□).
func icone(gtx layout.Context, ic *widget.Icon, cor color.NRGBA, tam unit.Dp) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	gtx.Constraints.Max = image.Pt(gtx.Dp(tam), gtx.Dp(tam))
	return ic.Layout(gtx, cor)
}

// ------------------------------------------------------ régua de alinhamento

// janelaRaio é o arredondamento dos cantos da janela quando ela NÃO está
// maximizada e a decoração é nossa (CSD).
const janelaRaio = unit.Dp(10)

// reguaLigada mostra a régua virtual (-regua na linha de comando, Ctrl+G
// em tempo de execução). É uma FERRAMENTA DE AJUSTE, não parte da
// interface: linhas a cada 8dp, mais fortes a cada 32dp, e uma guia forte
// na borda da lateral — que é a coluna com a qual o resto tem que alinhar.
var reguaLigada bool

// depurarLayout imprime as restrições de cada aba (ACESSOS_DBG_LAYOUT=1).
var depurarLayout = os.Getenv("ACESSOS_DBG_LAYOUT") == "1"

const reguaPasso = unit.Dp(8)

func regua(gtx layout.Context, colunaLateral int) {
	if !reguaLigada {
		return
	}
	size := gtx.Constraints.Max
	passo := gtx.Dp(reguaPasso)
	if passo <= 0 {
		return
	}
	fina := color.NRGBA{R: 0xff, G: 0x00, B: 0x88, A: 0x22}
	grossa := color.NRGBA{R: 0xff, G: 0x00, B: 0x88, A: 0x55}
	guia := color.NRGBA{R: 0x00, G: 0xc8, B: 0xff, A: 0xaa}

	linhaV := func(x int, c color.NRGBA) {
		paint.FillShape(gtx.Ops, c, clip.Rect{Min: image.Pt(x, 0), Max: image.Pt(x+1, size.Y)}.Op())
	}
	linhaH := func(y int, c color.NRGBA) {
		paint.FillShape(gtx.Ops, c, clip.Rect{Min: image.Pt(0, y), Max: image.Pt(size.X, y+1)}.Op())
	}
	for i, x := 0, 0; x < size.X; i, x = i+1, x+passo {
		c := fina
		if i%4 == 0 {
			c = grossa
		}
		linhaV(x, c)
	}
	for i, y := 0, 0; y < size.Y; i, y = i+1, y+passo {
		c := fina
		if i%4 == 0 {
			c = grossa
		}
		linhaH(y, c)
	}
	// as guias que importam: a coluna da lateral e o meio da janela.
	if colunaLateral > 0 {
		linhaV(colunaLateral, guia)
	}
	linhaV(size.X/2, guia)
	linhaH(size.Y/2, guia)
}

// clarear devolve a mesma cor um pouco mais clara — só para o hover dos
// botões cheios, onde a paleta não tem um token próprio. Fora daí, hover
// usa cor FIXA da paleta, nunca calculada (regra dura da skill).
func clarear(c color.NRGBA) color.NRGBA {
	sobe := func(v uint8) uint8 {
		n := int(v) + 22
		if n > 255 {
			n = 255
		}
		return uint8(n)
	}
	return color.NRGBA{R: sobe(c.R), G: sobe(c.G), B: sobe(c.B), A: c.A}
}
