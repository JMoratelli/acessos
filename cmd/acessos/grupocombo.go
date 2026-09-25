package main

// Campo de grupo com sugestão dos grupos que JÁ EXISTEM.
//
// O grupo é digitado à mão desde sempre, e é o campo que mais erra: ele é
// o caminho da árvore do Painel ("Loja 06;Caixas"), separado por ";", e um
// acento a menos ou um "Caixa" no singular não dá erro nenhum — cria um
// galho novo, quase igual ao certo, com uma máquina dentro. O estrago só
// aparece depois, procurando a máquina onde ela não está.
//
// Por isso o campo continua sendo um editor comum, e não uma lista
// fechada: grupo NOVO tem de nascer aqui, é o fluxo normal de cadastrar a
// primeira máquina de uma loja. A sugestão é um atalho para acertar o que
// já existe, não uma gaiola.

import (
	"image"
	"sort"
	"strings"

	"acessos-go/internal/conexoes"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	// Altura de uma sugestão e quantas cabem antes de rolar. Seis é o que
	// cabe sem o balão passar por cima do bloco de protocolos inteiro —
	// abaixo do campo de grupo ainda vêm host e o acordeão.
	comboItemAlt  = unit.Dp(30)
	comboMaxItens = 6
)

// comboGrupo é o editor de grupo mais a lista de sugestões.
type comboGrupo struct {
	ed    widget.Editor
	todos []string // grupos existentes, já ordenados e sem repetição

	achados []string
	btns    []widget.Clickable
	lista   widget.List
	// escolhido guarda o texto aplicado pelo clique para o quadro
	// seguinte: mexer no editor no meio da leitura de eventos faz o
	// próprio clique ser reprocessado.
	escolhido  string
	temEscolha bool
}

func (c *comboGrupo) iniciar(todos []string, valor string) {
	c.ed.SingleLine = true
	c.ed.SetText(valor)
	c.todos = todos
	c.lista.Axis = layout.Vertical
}

// gruposDoArquivo tira da lista de conexões os caminhos de grupo que
// existem, em ordem e sem repetir.
//
// Os ANCESTRAIS entram junto: com "Loja 06;Caixas" cadastrado, "Loja 06"
// também é um destino válido (é onde ficam as máquinas da loja que não
// são caixa), e ele só apareceria na lista por acaso — se alguma máquina
// estivesse cadastrada exatamente ali.
func gruposDoArquivo(arq *conexoes.Arquivo) []string {
	if arq == nil {
		return nil
	}
	vistos := map[string]bool{}
	for _, cx := range arq.Conexoes {
		for i := range cx.Grupo {
			g := strings.Join(cx.Grupo[:i+1], ";")
			if g != "" && g != "Sem grupo" {
				vistos[g] = true
			}
		}
	}
	out := make([]string, 0, len(vistos))
	for g := range vistos {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// filtrarGrupos casa por pedaço, sem exigir começo: quem digita "caixa"
// quer ver "Loja 06;Caixas" — o termo útil quase nunca é o prefixo do
// caminho, é o último galho dele.
func filtrarGrupos(todos []string, termo string) []string {
	termo = strings.ToLower(strings.TrimSpace(termo))
	var out []string
	for _, g := range todos {
		if termo == "" || strings.Contains(strings.ToLower(g), termo) {
			// O que já está escrito por extenso não é sugestão: oferecer
			// exatamente o que está no campo só gasta uma linha do balão.
			if !strings.EqualFold(g, termo) {
				out = append(out, g)
			}
		}
	}
	return out
}

// layout desenha o campo e, com ele em foco, o balão de sugestões.
//
// O balão sai por op.Defer: ele nasce DENTRO da coluna do formulário, e
// sem isso empurraria host e o acordeão para baixo a cada tecla — o
// formulário pulando enquanto se digita. Deferido, ele é desenhado por
// cima, depois de todo o resto, e a coluna não sente que ele existe.
func (c *comboGrupo) layout(gtx layout.Context, th *material.Theme, dica string) layout.Dimensions {
	if c.temEscolha {
		c.ed.SetText(c.escolhido)
		c.ed.SetCaret(c.ed.Len(), c.ed.Len())
		c.escolhido, c.temEscolha = "", false
	}

	dims := caixaEditor(gtx, th, &c.ed, dica, 0)
	if !gtx.Focused(&c.ed) {
		return dims
	}

	c.achados = filtrarGrupos(c.todos, c.ed.Text())
	if len(c.achados) == 0 {
		return dims
	}
	for len(c.btns) < len(c.achados) {
		c.btns = append(c.btns, widget.Clickable{})
	}
	for i := range c.achados {
		if c.btns[i].Clicked(gtx) {
			c.escolhido, c.temEscolha = c.achados[i], true
			gtx.Execute(op.InvalidateCmd{})
		}
	}

	n := len(c.achados)
	if n > comboMaxItens {
		n = comboMaxItens
	}
	alt := gtx.Dp(comboItemAlt)*n + gtx.Dp(8)

	macro := op.Record(gtx.Ops)
	off := op.Offset(image.Pt(0, dims.Size.Y+gtx.Dp(3))).Push(gtx.Ops)
	gtx.Constraints.Min = image.Pt(dims.Size.X, alt)
	gtx.Constraints.Max = gtx.Constraints.Min
	c.balao(gtx, th)
	off.Pop()
	op.Defer(gtx.Ops, macro.Stop())

	return dims
}

func (c *comboGrupo) balao(gtx layout.Context, th *material.Theme) layout.Dimensions {
	size := gtx.Constraints.Min
	sombra(gtx, size, 7)
	// Opaco, e não vidro: o balão cobre campos do próprio formulário, e
	// texto por trás de texto é o que faz uma lista de sugestão virar
	// borrão. O menu de contexto usa a mesma superfície pela mesma razão.
	superficie(gtx, size, tema.Cartao, tema.Borda2, 7)
	return layout.UniformInset(4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.List(th, &c.lista).Layout(gtx, len(c.achados),
			func(gtx layout.Context, i int) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				gtx.Constraints.Min.Y = gtx.Dp(comboItemAlt)
				return c.btns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Background{}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							if c.btns[i].Hovered() {
								superficie(gtx, gtx.Constraints.Min, tema.Hover, transparente, 5)
							}
							return layout.Dimensions{Size: gtx.Constraints.Min}
						},
						func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return layout.Inset{Left: 8, Right: 8, Top: 6, Bottom: 6}.Layout(gtx,
								rotuloLinha(th, fonteMono, spCorpo, c.achados[i], tema.Texto))
						},
					)
				})
			})
	})
}
