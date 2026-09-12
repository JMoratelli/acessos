package main

import (
	"image"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	tabStripHeight = unit.Dp(28)
	tabRaio        = unit.Dp(7) // notebook tab border-radius
	tabTipoLado    = unit.Dp(18)
)

// tabBar é a tira de abas-documento (padrão B): barra com o gradiente do
// cromo, aba em repouso `vidro`, hover `vidro_h`, ativa `vidro_h` + trilho
// de acento por dentro.
type tabBar struct {
	// tagRolagem/acumulado: trocar de aba com a rodinha. Mouse de alta
	// resolução manda dezenas de eventos fracionários por volta do
	// dente — somamos até completar um "click" inteiro e só então
	// andamos UMA aba, senão uma rodada varre a tira toda.
	tagRolagem    *int
	acumulado     float32
	ultimaRolagem time.Time
	largurasAba   []int

	// chaves identifica O QUE cada aba é (protocolo + máquina) para não
	// abrir duas vezes a mesma coisa. Fica FORA do rótulo porque o rótulo
	// de uma conexão salva mostra só o nome da máquina — o protocolo já
	// está no selo colorido ao lado.
	chaves    map[Tab]string
	tabs      []Tab
	selectBtn []widget.Clickable
	closeBtn  []widget.Clickable
	idx       int
}

// Rolagem da tira: UM passo por gesto.
//
// Roda de alta definição não manda "um dente": manda uma enxurrada de
// deltas fracionários, dezenas por volta. Só somar e dividir por um passo
// fazia uma rodada varrer a tira inteira. Então valem as duas coisas: um
// mínimo de deslocamento (para o toque leve não contar) E um intervalo
// mínimo entre trocas (para a enxurrada contar como um gesto só).
const (
	passoRolagem     = 8                      // pixels que já valem um passo
	intervaloRolagem = 140 * time.Millisecond // uma aba por gesto
)

func newTabBar(tabs []Tab) *tabBar {
	return &tabBar{
		tagRolagem: new(int),
		tabs:       tabs,
		selectBtn:  make([]widget.Clickable, len(tabs)),
		closeBtn:   make([]widget.Clickable, len(tabs)),
	}
}

// append adiciona uma aba no fim e já foca nela (é o que se espera ao
// clicar num card do painel).
// appendFundo adiciona a aba SEM focar nela (botão do meio no card).
func (b *tabBar) appendFundo(t Tab, chave string) {
	idx := b.idx
	b.appendCom(t, chave)
	b.idx = idx
}

// appendCom adiciona a aba com uma chave de identidade própria.
func (b *tabBar) appendCom(t Tab, chave string) {
	b.append(t)
	if b.chaves == nil {
		b.chaves = map[Tab]string{}
	}
	b.chaves[t] = chave
}

// chave devolve a identidade da aba (cai no rótulo quando não há uma).
func (b *tabBar) chave(t Tab) string {
	if c, ok := b.chaves[t]; ok {
		return c
	}
	return t.Title()
}

func (b *tabBar) append(t Tab) {
	b.tabs = append(b.tabs, t)
	b.selectBtn = append(b.selectBtn, widget.Clickable{})
	b.closeBtn = append(b.closeBtn, widget.Clickable{})
	b.idx = len(b.tabs) - 1
}

// prependPinned insere t no início (índice 0) e seleciona-o — usado só
// para a aba Painel, na montagem inicial.
func (b *tabBar) prependPinned(t Tab) {
	b.tabs = append([]Tab{t}, b.tabs...)
	b.selectBtn = append([]widget.Clickable{{}}, b.selectBtn...)
	b.closeBtn = append([]widget.Clickable{{}}, b.closeBtn...)
	b.idx = 0
}

func (b *tabBar) active() Tab {
	if b.idx < 0 || b.idx >= len(b.tabs) {
		return nil
	}
	return b.tabs[b.idx]
}

func (b *tabBar) selectIndex(i int) {
	if i >= 0 && i < len(b.tabs) {
		b.idx = i
	}
}

// closeActive fecha a aba selecionada agora (Ctrl+W) — sem efeito na aba
// fixa (Painel).
func (b *tabBar) closeActive() { b.fechar(b.idx) }

func (b *tabBar) fechar(i int) {
	if i < 0 || i >= len(b.tabs) || b.tabs[i].Pinned() {
		return
	}
	b.tabs[i].Close()
	delete(b.chaves, b.tabs[i])
	b.tabs = append(b.tabs[:i], b.tabs[i+1:]...)
	b.selectBtn = append(b.selectBtn[:i], b.selectBtn[i+1:]...)
	b.closeBtn = append(b.closeBtn[:i], b.closeBtn[i+1:]...)
	if b.idx >= len(b.tabs) {
		b.idx = len(b.tabs) - 1
	}
}

func (b *tabBar) update(gtx layout.Context) {
	for i := range b.tabs {
		if b.selectBtn[i].Clicked(gtx) {
			b.idx = i
		}
	}
	for i := range b.tabs {
		if !b.tabs[i].Pinned() && b.closeBtn[i].Clicked(gtx) {
			b.fechar(i)
			break // as fatias mudaram de tamanho; o resto espera o próximo frame
		}
	}
}

func (b *tabBar) layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	b.update(gtx)
	b.rolagem(gtx)

	h := gtx.Dp(tabStripHeight)
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h

	// faixa com cor própria (ver fundoAbas) e fio de borda embaixo, pra
	// destacar da faixa da janela em cima e do conteúdo embaixo.
	fundoAbas(gtx, image.Pt(gtx.Constraints.Max.X, h))
	paint.FillShape(gtx.Ops, tema.Borda2,
		clip.Rect{Min: image.Pt(0, h-1), Max: image.Pt(gtx.Constraints.Max.X, h)}.Op())

	if len(b.largurasAba) != len(b.tabs) {
		b.largurasAba = make([]int, len(b.tabs))
	}
	children := make([]layout.FlexChild, len(b.tabs))
	for i := range b.tabs {
		i := i
		children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			d := b.selectBtn[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				var fechar *widget.Clickable
				if !b.tabs[i].Pinned() {
					fechar = &b.closeBtn[i]
				}
				return layoutAba(gtx, th, b.tabs[i], i == b.idx, b.selectBtn[i].Hovered(), fechar)
			})
			b.largurasAba[i] = d.Size.X
			return d
		})
	}
	dims := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)

	// Área de rolagem e de clique do meio sobre a tira INTEIRA, registrada
	// DEPOIS das abas (portanto por cima) e com PassOp para não tirar o
	// clique normal delas. Por baixo não funcionaria: no hit-test do Gio,
	// ao encontrar uma área sem PassOp o percurso salta para o nó pai e
	// ignora as irmãs registradas antes.
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, h)}.Push(gtx.Ops)
	event.Op(gtx.Ops, b.tagRolagem)
	area.Pop()
	pass.Pop()
	return dims
}

// layoutAba: ícone do protocolo (mesma cor do card), nome em mono, e "×"
// só nas abas fecháveis. Estado muda só COR — a altura é a mesma em
// repouso, hover e ativa, por isso o trilho é desenhado POR DENTRO.
func layoutAba(gtx layout.Context, th *material.Theme, t Tab, ativa, hover bool, fechar *widget.Clickable) layout.Dimensions {
	fundo, cor := tema.Vidro, tema.TopoSec
	switch {
	case ativa:
		fundo, cor = tema.VidroH, tema.TopoTxt
	case hover:
		fundo, cor = tema.VidroH, tema.TopoTxt
	}

	// TODAS as abas têm a altura da aba da casinha: o miolo é medido a
	// partir dela, não do texto de cada uma, senão a aba de conexão fica
	// mais alta que a fixa e a tira ganha dois degraus.
	return layout.Inset{Top: 2, Bottom: 2, Left: 1, Right: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		alt := gtx.Dp(tabStripHeight) - gtx.Dp(4)
		gtx.Constraints.Min.Y = alt
		gtx.Constraints.Max.Y = alt
		return layout.Stack{Alignment: layout.Center}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				borda := transparente
				if ativa {
					borda = tema.VidroB
				}
				superficie(gtx, size, fundo, borda, tabRaio)
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				if t.SoIcone() {
					// aba da casinha: quadrada, glifo no centro dos dois
					// eixos. O trilho de 2px do estado ativo é desenhado
					// por dentro, então não desloca o ícone.
					lado := gtx.Dp(tabStripHeight) - gtx.Dp(4)
					gtx.Constraints.Min = image.Pt(lado, lado)
					gtx.Constraints.Max.X = lado
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						ic, cor, _ := t.Selo()
						return icone(gtx, ic, cor, 15)
					})
				}
				return layout.Inset{Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return selo(gtx, t)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if t.SoIcone() {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: 5}.Layout(gtx,
								txt(th, fonteMono, spAbaNome, t.Title(), cor).Layout)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if fechar == nil {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return fechar.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									c := cor
									if fechar.Hovered() {
										c = tema.ErroFg // erro SÓ em ação destrutiva
									}
									return layout.UniformInset(1).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return icone(gtx, iconeFechar, c, 14)
									})
								})
							})
						}),
					)
				})
			}),
			// Trilho do estado ativo POR CIMA de tudo: desenhado antes do
			// conteúdo, qualquer selo um pouco mais alto passava por cima
			// dele e a aba parecia sem marcação. Inset de 2px, então não
			// ocupa espaço de layout e a aba não "desce" ao ser escolhida.
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				if !ativa {
					return layout.Dimensions{Size: size}
				}
				alt := gtx.Dp(2)
				r := clip.Rect{Min: image.Pt(0, size.Y-alt), Max: size}
				paint.FillShape(gtx.Ops, tema.Azul, r.Op())
				return layout.Dimensions{Size: size}
			}),
		)
	})
}

// selo é a caixinha colorida do protocolo — caixa FIXA para todos, senão
// um ícone de proporção diferente desalinha a aba inteira (tema.py). Sem
// o prefixo "VNC/SSH/RDP" no rótulo, é ESTE glifo que diz pra que serve a
// aba: ele tem que estar centrado nos dois eixos da caixinha, sempre.
func selo(gtx layout.Context, t Tab) layout.Dimensions {
	// A caixa nunca pode passar da altura útil da aba: com a aba em 24dp
	// e o selo em 20, o ícone encostava na borda e parecia sair dela.
	lado := gtx.Dp(tabTipoLado)
	// -10dp: 2 de inset em cima e embaixo da aba, 2 do trilho do estado
	// ativo e respiro. Sem essa folga o selo cobria o trilho azul, e o
	// ícone parecia vazar da pílula.
	if max := gtx.Dp(tabStripHeight) - gtx.Dp(10); lado > max {
		lado = max
	}
	quadrado := image.Pt(lado, lado)
	gtx.Constraints.Min = quadrado
	gtx.Constraints.Max = quadrado

	ic, cor, fundo := t.Selo()
	if ic == nil {
		return layout.Dimensions{Size: quadrado}
	}
	// Pintar a caixinha e centrar o glifo na MÃO, sem Stack: o Stack
	// dimensiona o filho Expanded pelo filho Stacked, e aí a caixa
	// encolhia pro tamanho do glifo em vez de ficar no lado fixo.
	superficie(gtx, quadrado, fundo, transparente, 5)
	// CLIPA na caixa: alguns glifos do conjunto Material desenham fora do
	// quadrado nominal (o raio do ImageFlashOn estourava a pílula da aba
	// de Massa). Com o recorte, nenhum ícone novo vaza.
	defer clip.Rect{Max: quadrado}.Push(gtx.Ops).Pop()
	// Respiro generoso: os glifos "cheios" do conjunto Material (a pasta
	// do SFTP, o raio do massa) pintam o quadrado inteiro, ao contrário
	// dos de traço, e encostavam na borda da caixinha — parecia que o
	// ícone estava vazando da pílula.
	tam := lado - gtx.Dp(8)
	desloc := image.Pt((lado-tam)/2, (lado-tam)/2)
	defer op.Offset(desloc).Push(gtx.Ops).Pop()
	gtx.Constraints.Min = image.Point{}
	gtx.Constraints.Max = image.Pt(tam, tam)
	icone(gtx, ic, cor, unit.Dp(float32(tam)/gtx.Metric.PxPerDp))
	return layout.Dimensions{Size: quadrado}
}

// rolagem troca de aba com a rodinha do mouse sobre a tira.
//
// O cuidado aqui é com mouse de alta resolução: ele não manda "um dente",
// manda uma chuva de deltas fracionários. Somando tudo e só trocando de
// aba a cada passoRolagem pixels, uma volta da rodinha anda algumas abas,
// e não a tira inteira.
func (b *tabBar) rolagem(gtx layout.Context) {
	for {
		ev, ok := gtx.Source.Event(pointer.Filter{
			Target:  b.tagRolagem,
			Kinds:   pointer.Scroll | pointer.Press,
			ScrollY: pointer.ScrollRange{Min: -1000, Max: 1000},
		})
		if !ok {
			break
		}
		pe, isP := ev.(pointer.Event)
		if !isP {
			continue
		}
		// clique do MEIO na tira fecha a aba sob o cursor — mesmo gesto do
		// navegador, e o que o app original faz.
		if pe.Kind == pointer.Press && pe.Buttons.Contain(pointer.ButtonTertiary) {
			if i := b.abaEm(pe.Position.X); i >= 0 {
				b.fechar(i)
			}
			continue
		}
		b.acumulado += pe.Scroll.Y
		if b.acumulado > -passoRolagem && b.acumulado < passoRolagem {
			continue
		}
		// dentro do intervalo, a enxurrada é o MESMO gesto: só descarta
		// o acumulado e espera o próximo.
		if time.Since(b.ultimaRolagem) < intervaloRolagem {
			b.acumulado = 0
			continue
		}
		passo := 1
		if b.acumulado < 0 {
			passo = -1
		}
		b.acumulado = 0
		b.ultimaRolagem = time.Now()
		b.selectIndex(b.idx + passo)
	}
}

// abaEm diz qual aba está sob a coordenada X da tira. As larguras são
// guardadas durante o desenho: a tira é um Flex, e sem isso não há como
// saber onde cada aba começa.
func (b *tabBar) abaEm(x float32) int {
	acc := 0
	for i, w := range b.largurasAba {
		if int(x) >= acc && int(x) < acc+w {
			return i
		}
		acc += w
	}
	return -1
}
