package main

// Balão de dica (hover) — hoje só a descrição do host.
//
// Vive num estado de PACOTE, como o menu de contexto (menu.go), e pelo
// mesmo motivo: ele precisa ser desenhado por último, depois de tudo, e
// quem sabe que está sob o cursor é um card lá no meio da lista. Pedir de
// lá e desenhar no fim do quadro é o que evita o balão sair recortado pelo
// card vizinho.
//
// O estado é de UM QUADRO: quem está sob o cursor chama pedirDica() toda
// vez que se desenha, e layoutDica apaga o pedido depois de usá-lo. Card
// que deixou de ser apontado simplesmente para de pedir — não existe
// "fechar a dica", que é a parte que costuma ficar presa na tela.

import (
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

const (
	// Atraso antes de aparecer. Sem ele, atravessar a grade do Painel com
	// o mouse acende um balão por card no caminho — o Painel é uma grade
	// densa, e o cursor cruza cinco ou seis cards só indo do canto até a
	// lateral. Meio segundo é o que o resto dos desktops usa e o que
	// separa "parei aqui para ler" de "passei por cima".
	dicaAtraso = 450 * time.Millisecond
	// Largura máxima do balão. A descrição vai até 255 runas
	// (conexoes.LimiteDescricao); em 320dp isso quebra em três ou quatro
	// linhas curtas, que ainda se lê de relance. Mais largo vira faixa
	// atravessando a tela.
	dicaLargura = unit.Dp(320)
	// Folga entre o cursor e o balão: encostado no ponteiro, a seta cobre
	// a primeira letra.
	dicaFolga = unit.Dp(16)
)

var dicaAtual struct {
	chave string // identidade do que está sob o cursor
	texto string
	desde time.Time
	// viva marca que ALGUÉM pediu a dica neste quadro. layoutDica zera a
	// marca depois de ler; quem não pediu, não aparece.
	viva bool
}

// pedirDica anuncia que `chave` está sob o cursor com este texto. Chamar
// todo quadro enquanto durar o hover.
//
// A chave é o que distingue um host do vizinho: ao trocar de host o
// relógio do atraso recomeça, senão passar de um card com descrição para
// outro mostraria o segundo na hora, sem a pausa que diz "parei aqui".
//
// Sem dono, diferente do menu de contexto: quem pede é o Painel ou a
// lateral, que só existem na janela principal, e é só ela que chama o
// layoutDica. O menu precisou de dono porque também é aberto da janela de
// sessão destacada; aqui não há segundo pedinte.
func pedirDica(chave, texto string) {
	if texto == "" {
		return
	}
	if dicaAtual.chave != chave {
		dicaAtual.chave, dicaAtual.texto, dicaAtual.desde = chave, texto, time.Now()
	}
	dicaAtual.viva = true
}

// layoutDica desenha o balão, se houver. Vai no FIM do quadro, depois do
// menu — é o elemento mais efêmero da tela e não deve cobrir nada que se
// possa clicar.
func layoutDica(gtx layout.Context, th *material.Theme) layout.Dimensions {
	viva := dicaAtual.viva
	dicaAtual.viva = false
	if !viva || dicaAtual.texto == "" {
		// Sem pedido neste quadro o alvo sai de cena: assim o próximo
		// hover no MESMO card recomeça o atraso, em vez de o balão
		// reaparecer instantaneamente depois de uma saída rápida.
		if !viva {
			dicaAtual.chave = ""
		}
		return layout.Dimensions{}
	}
	if falta := dicaAtraso - time.Since(dicaAtual.desde); falta > 0 {
		// O hover sozinho não gera quadro: parado sobre o card, nada se
		// move e o balão nunca chegaria a aparecer sem este acorde.
		gtx.Execute(op.InvalidateCmd{At: time.Now().Add(falta)})
		return layout.Dimensions{}
	}

	tela := gtx.Constraints.Max
	macro := op.Record(gtx.Ops)
	gtx.Constraints.Min = image.Point{}
	gtx.Constraints.Max.X = gtx.Dp(dicaLargura)
	dims := dicaCorpo(gtx, th, dicaAtual.texto)
	call := macro.Stop()

	// Canto: à direita e abaixo do cursor, virando para o outro lado
	// quando não cabe. Sem isso, host apontado na última coluna ou na
	// última fileira mostra a descrição cortada pela borda da janela —
	// justamente onde ficam as máquinas de nome mais longo.
	pos := ultimaPosPonteiro()
	x := pos.X + gtx.Dp(dicaFolga)
	if x+dims.Size.X > tela.X {
		x = pos.X - gtx.Dp(dicaFolga) - dims.Size.X
	}
	y := pos.Y + gtx.Dp(dicaFolga)
	if y+dims.Size.Y > tela.Y {
		y = pos.Y - gtx.Dp(dicaFolga) - dims.Size.Y
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	off := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	off.Pop()
	return dims
}

func dicaCorpo(gtx layout.Context, th *material.Theme, texto string) layout.Dimensions {
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			sombra(gtx, gtx.Constraints.Min, 7)
			// Opaco: a dica flutua sobre os cards de vidro do Painel, e
			// texto sobre texto não se lê.
			superficie(gtx, gtx.Constraints.Min, tema.Cartao, tema.Borda2, 7)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 7, Bottom: 8, Left: 10, Right: 10}.Layout(gtx,
				rotulo(th, fonteSans, spSecundario, texto, tema.Texto))
		},
	)
}
