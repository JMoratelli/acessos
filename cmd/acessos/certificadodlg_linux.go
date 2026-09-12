//go:build linux

package main

import (
	"fmt"
	"time"

	"acessos-go/internal/rdp"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgCertificado é o equivalente RDP do diálogo de host key: mesma
// conversa, mesmo peso de botão. A libfreerdp guarda o aceito em
// ~/.config/freerdp/server, então "aceitar e salvar" pergunta uma vez só.
type dlgCertificado struct {
	w        *app.Window
	cert     rdp.Certificado
	resposta func(int)
	desde    time.Time

	btnSalvar widget.Clickable
	btnUmaVez widget.Clickable
	btnNao    widget.Clickable
}

func pedirConfiancaCertificado(w *app.Window, c rdp.Certificado, resposta func(int)) {
	abrirDialogo(&dlgCertificado{w: w, cert: c, resposta: resposta, desde: time.Now()})
}

func (d *dlgCertificado) Titulo() string {
	if d.cert.Mudou {
		return "O certificado do servidor MUDOU"
	}
	return "Primeira conexão com " + d.cert.Host
}

func (d *dlgCertificado) Largura() unit.Dp { return 620 }

func (d *dlgCertificado) responder(v int) {
	fecharDialogo()
	if d.resposta != nil {
		d.resposta(v)
	}
	d.w.Invalidate()
}

func (d *dlgCertificado) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	espera := time.Duration(0)
	if d.cert.Mudou {
		espera = esperaDestrutiva - time.Since(d.desde)
	}
	pronto := espera <= 0
	if !pronto {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(200 * time.Millisecond)})
	}

	switch {
	case d.btnNao.Clicked(gtx):
		d.responder(rdp.CertRecusar)
	case d.btnUmaVez.Clicked(gtx):
		d.responder(rdp.CertAceitarUmaVez)
	case pronto && d.btnSalvar.Clicked(gtx):
		d.responder(rdp.CertAceitarSalvo)
	}

	texto, cor := "Confira a impressão digital antes de confiar.", tema.Sec
	if d.cert.Mudou {
		texto = "A identidade não bate com a que estava guardada. Acontece em máquina reinstalada — " +
			"e é também o que um ataque no meio do caminho parece."
		cor = tema.ErroFg
	}

	filhos := []layout.FlexChild{
		layout.Rigid(rotulo(th, fonteSans, spCorpo, texto, cor)),
		espaco(10),
		layout.Rigid(rotuloLinha(th, fonteMono, spSecundario, "emissor: "+d.cert.Emissor, tema.Sec)),
		layout.Rigid(rotuloLinha(th, fonteMono, spSecundario, "assunto: "+d.cert.Assunto, tema.Sec)),
		espaco(6),
		layout.Rigid(rotuloLinha(th, fonteMono, spCorpo, d.cert.Digital, tema.Texto)),
	}
	if d.cert.Mudou && d.cert.DigitalAnterior != "" {
		filhos = append(filhos,
			espaco(4),
			layout.Rigid(rotuloLinha(th, fonteMono, spCardMeta, "anterior: "+d.cert.DigitalAnterior, tema.Fraco)))
	}
	filhos = append(filhos, espaco(14), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoNeutro(gtx, th, &d.btnNao, "Não conectar")
			}),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoNeutro(gtx, th, &d.btnUmaVez, "Só desta vez")
			}),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				rot := "Confiar e salvar"
				if d.cert.Mudou {
					if !pronto {
						rot = fmt.Sprintf("Aceitar o novo em %ds", int(espera.Seconds())+1)
					} else {
						rot = "Aceitar o novo certificado"
					}
					return botao(gtx, th, &d.btnSalvar, rot, pesoPerigo, !pronto)
				}
				return botaoPrimario(gtx, th, &d.btnSalvar, rot)
			}),
		)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}
