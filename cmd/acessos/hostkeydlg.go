package main

import (
	"errors"
	"fmt"

	"acessos-go/internal/hostkey"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgHostKey é a conversa sobre a identidade do servidor SSH. São duas
// conversas diferentes de propósito:
//
//   - primeira vez: mostra a impressão digital e oferece confiar. É o
//     mesmo que o comando ssh faz, e é rotina.
//   - a chave MUDOU: isso é esperado em máquina reinstalada, mas é também
//     exatamente o que um ataque no meio do caminho parece. O botão de
//     aceitar é vermelho e tem contagem — e o texto diz as duas coisas,
//     porque esconder a segunda seria mentir por omissão.
type dlgHostKey struct {
	w        *app.Window
	erro     *hostkey.ErroChave
	aoAceito func()

	btnOk   widget.Clickable
	btnCanc widget.Clickable
}

func pedirConfiancaHostKey(w *app.Window, e *hostkey.ErroChave, aoAceito func()) {
	abrirDialogo(&dlgHostKey{w: w, erro: e, aoAceito: aoAceito})
}

func (d *dlgHostKey) Titulo() string {
	if d.erro.Resultado == hostkey.Mudou {
		return "A chave do servidor MUDOU"
	}
	return "Primeira conexão com " + d.erro.Host
}

func (d *dlgHostKey) Largura() unit.Dp { return 560 }

func (d *dlgHostKey) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}
	if d.btnOk.Clicked(gtx) {
		if err := hostkey.Confiar(d.erro); err != nil {
			fmt.Println("known_hosts:", err)
		}
		fecharDialogo()
		if d.aoAceito != nil {
			d.aoAceito()
		}
	}

	mudou := d.erro.Resultado == hostkey.Mudou
	texto := "Esta máquina ainda não está no seu known_hosts. Confira a impressão digital antes de confiar."
	cor := tema.Sec
	if mudou {
		texto = "A identidade não bate com a que está gravada. Isso acontece quando a máquina foi reinstalada — " +
			"e também quando alguém está no meio do caminho interceptando a conexão."
		cor = tema.ErroFg
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(rotulo(th, fonteSans, spCorpo, texto, cor)),
		espaco(10),
		layout.Rigid(rotulo(th, fonteMono, spSecundario, d.erro.Tipo, tema.Sec)),
		layout.Rigid(rotulo(th, fonteMono, spCorpo, d.erro.Impressao, tema.Texto)),
		espaco(14),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnCanc, "Não conectar")
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if mudou {
						// aceitar troca de chave é a ação de risco
						return botaoPerigo(gtx, th, &d.btnOk, "Aceitar a nova chave")
					}
					return botaoPrimario(gtx, th, &d.btnOk, "Confiar e conectar")
				}),
			)
		}),
	)
}

// erroDeChave desembrulha o erro do x/crypto/ssh até achar o nosso — o
// Dial embrulha a falha do callback numa mensagem de handshake.
func erroDeChave(err error) *hostkey.ErroChave {
	var ec *hostkey.ErroChave
	if errors.As(err, &ec) {
		return ec
	}
	return nil
}
