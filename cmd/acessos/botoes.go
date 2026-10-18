package main

import (
	"image/color"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// TODO botão do app nasce aqui. Peso visual é informação, não enfeite, e
// só se lê como informação se for o MESMO em todas as telas:
//
//	primário   — a ação que a tela existe para fazer (salvar, executar).
//	            cheio, cor de acento.
//	neutro     — recuo e saída segura (cancelar, fechar, voltar). NUNCA
//	            vermelho: pintar a saída de vermelho apaga o significado
//	            da cor no botão ao lado.
//	perigo     — o que destrói (remover, abortar, encerrar). Vermelho
//	            SÓLIDO, sempre, não só no hover, e acompanhado de
//	            confirmação com contagem quando não tem desfazer.
//	sutil      — ação de barra/filtro, terciária. Só vidro e contorno.
//
// Nenhuma tela escreve cor de botão direto: mexeu em botão, mexe aqui.

type pesoBotao int

const (
	pesoPrimario pesoBotao = iota
	pesoNeutro
	pesoPerigo
	pesoSutil
)

func (p pesoBotao) cores(hover bool) (fundo, borda, texto color.NRGBA) {
	switch p {
	case pesoPrimario:
		f := tema.AzulCheio
		if hover {
			f = tema.AzulCheioH
		}
		return f, transparente, hex(0xffffff)
	case pesoPerigo:
		f := tema.ErroFg
		if hover {
			f = tema.ErroH
		}
		return f, transparente, hex(0xffffff)
	case pesoSutil:
		f, b := tema.Vidro1, tema.LuzB
		if hover {
			f, b = tema.Vidro2, tema.Borda2
		}
		return f, b, tema.Sec
	default: // neutro
		f, b := tema.Vidro2, tema.LuzB
		if hover {
			f, b = tema.Vidro3, tema.Borda2
		}
		return f, b, tema.Texto
	}
}

// botao desenha um botão de texto com o peso pedido. desligado deixa o
// botão apagado e inerte (a caixa continua igual: estado muda cor, nunca
// dimensão).
func botao(gtx layout.Context, th *material.Theme, btn *widget.Clickable,
	rot string, peso pesoBotao, desligado bool) layout.Dimensions {

	if desligado {
		if peso == pesoPerigo {
			// Destrutivo é vermelho SEMPRE, inclusive esperando a
			// contagem da confirmação em lote: cinza ali apagava o aviso
			// justamente nos 3s em que a pessoa está lendo o que vai
			// sumir. Vermelho apagado (fundo de erro, texto de erro) diz
			// "perigoso, ainda não" sem parecer clicável.
			return caixaBotao(gtx, th, nil, rot, tema.ErroBg, comAlfa(tema.ErroFg, 0.45), tema.ErroFg)
		}
		return caixaBotao(gtx, th, nil, rot, tema.Vidro2, tema.LuzB, tema.Fraco)
	}
	f0, b0, t0 := peso.cores(false)
	f1, b1, t1 := peso.cores(true)
	h := progressoHover(gtx, btn, btn.Hovered())
	return caixaBotao(gtx, th, btn, rot, misturarCor(f0, f1, h), misturarCor(b0, b1, h), misturarCor(t0, t1, h))
}

func caixaBotao(gtx layout.Context, th *material.Theme, btn *widget.Clickable,
	rot string, fundo, borda, texto color.NRGBA) layout.Dimensions {

	conteudo := func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				superficieRelevo(gtx, gtx.Constraints.Min, fundo, borda, 8)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			},
			func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 7, Bottom: 7, Left: 14, Right: 14}.Layout(gtx,
					negrito(txt(th, fonteMono, spSecundario, rot, texto)).Layout)
			},
		)
	}
	if btn == nil {
		return conteudo(gtx)
	}
	return btn.Layout(gtx, conteudo)
}

// Atalhos de leitura. São os nomes usados pelas telas.
func botaoPrimario(gtx layout.Context, th *material.Theme, b *widget.Clickable, rot string) layout.Dimensions {
	return botao(gtx, th, b, rot, pesoPrimario, false)
}

func botaoNeutro(gtx layout.Context, th *material.Theme, b *widget.Clickable, rot string) layout.Dimensions {
	return botao(gtx, th, b, rot, pesoNeutro, false)
}

func botaoPerigo(gtx layout.Context, th *material.Theme, b *widget.Clickable, rot string) layout.Dimensions {
	return botao(gtx, th, b, rot, pesoPerigo, false)
}

func botaoSutil(gtx layout.Context, th *material.Theme, b *widget.Clickable, rot string) layout.Dimensions {
	return botao(gtx, th, b, rot, pesoSutil, false)
}

// botaoIconePerigo é o botaoPerigo sem rótulo, para quando só cabe um
// ícone (ex: lixeira numa barra estreita) — mesmo vermelho SÓLIDO,
// sempre, não só no hover; a regra de peso não muda por faltar o texto.
func botaoIconePerigo(gtx layout.Context, b *widget.Clickable, ic *widget.Icon) layout.Dimensions {
	fundo, _, cor := pesoPerigo.cores(b.Hovered())
	return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				superficie(gtx, gtx.Constraints.Min, fundo, transparente, 7)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			},
			func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(7).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return icone(gtx, ic, cor, 14)
				})
			},
		)
	})
}

// botaoLargura força uma largura fixa — usado onde o rótulo muda (a
// contagem regressiva das ações destrutivas) e o botão não pode pular.
func botaoLargura(gtx layout.Context, larg unit.Dp) layout.Context {
	l := gtx.Dp(larg)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = l, l
	return gtx
}
