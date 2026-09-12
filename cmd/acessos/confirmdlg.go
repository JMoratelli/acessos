package main

import (
	"fmt"
	"time"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgConfirmar é a confirmação de ação destrutiva: lista O QUE será
// afetado (não só quantos), nasce com o botão desabilitado e conta 3s no
// próprio rótulo, com LARGURA FIXA para o botão não pular quando o texto
// muda. O foco natural é o Cancelar — ele é a saída segura, e por isso
// fica neutro: vermelho marca a ação que destrói, nunca a que recua.
type dlgConfirmar struct {
	titulo  string
	itens   []string
	rotulo  string // "Excluir definitivamente"
	acao    func()
	w       *app.Window
	btnOk   widget.Clickable
	btnCanc widget.Clickable
	desde   time.Time
}

const esperaDestrutiva = 3 * time.Second

func confirmarDestrutivo(w *app.Window, titulo string, itens []string, rotulo string, acao func()) {
	confirmarDestrutivoEm(w, titulo, itens, rotulo, true, acao)
}

// confirmarDestrutivoEm permite pedir confirmação SEM a contagem. A espera
// existe para o que é irreversível EM LOTE (excluir 40 arquivos, rodar em
// 200 máquinas); em ação de um item só ela vira pedágio, e pedágio
// constante ensina a clicar sem ler — que é o contrário do que se queria.
func confirmarDestrutivoEm(w *app.Window, titulo string, itens []string, rotulo string, comEspera bool, acao func()) {
	d := &dlgConfirmar{titulo: titulo, itens: itens, rotulo: rotulo, acao: acao, w: w, desde: time.Now()}
	if !comEspera {
		d.desde = time.Now().Add(-esperaDestrutiva)
	}
	abrirDialogo(d)
}

func (d *dlgConfirmar) Titulo() string   { return d.titulo }
func (d *dlgConfirmar) Largura() unit.Dp { return 420 }

func (d *dlgConfirmar) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	falta := esperaDestrutiva - time.Since(d.desde)
	pronto := falta <= 0
	if !pronto {
		// pede outro quadro enquanto a contagem anda; sem isto o rótulo
		// só mudaria quando algo mais redesenhasse a tela.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(200 * time.Millisecond)})
	}
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}
	if pronto && d.btnOk.Clicked(gtx) {
		acao := d.acao
		fecharDialogo()
		if acao != nil {
			acao()
		}
	}

	filhos := []layout.FlexChild{}
	for _, it := range d.itens {
		filhos = append(filhos, layout.Rigid(rotulo(th, fonteMono, spCorpo, "• "+it, tema.Texto)))
	}
	filhos = append(filhos,
		espaco(12),
		layout.Rigid(rotulo(th, fonteMono, spSecundario, "Esta ação não tem desfazer.", tema.Sec)),
		espaco(14),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnCanc, "Cancelar")
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// largura fixa: o rótulo muda, o botão não pula
					gtx = botaoLargura(gtx, 220)
					rot := d.rotulo
					if !pronto {
						rot = fmt.Sprintf("%s em %ds", d.rotulo, int(falta.Seconds())+1)
					}
					return botao(gtx, th, &d.btnOk, rot, pesoPerigo, !pronto)
				}),
			)
		}),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}
