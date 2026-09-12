package main

import (
	"fmt"

	"acessos-go/internal/cofre"
	"acessos-go/internal/conexoes"

	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgCofre pede a senha mestra. Ele nasce de uma AÇÃO que esbarrou no
// cofre trancado (abrir uma conexão com senha cifrada): destrancando, a
// ação continua de onde parou, em vez de o operador ter que clicar de novo
// e adivinhar por que nada aconteceu.
type dlgCofre struct {
	arq     *conexoes.Arquivo
	caminho string
	w       *app.Window
	senha   widget.Editor
	btnOk   widget.Clickable
	btnCanc widget.Clickable
	erro    string
	depois  func() // repetido assim que o cofre abrir

	// modo troca de senha mestra: recifra TODOS os segredos do arquivo.
	trocando  bool
	btnTrocar widget.Clickable
	nova      widget.Editor
	confirma  widget.Editor
	aviso     string
	recarrega func() // recarrega o .ini depois de reescrever
}

func (d *dlgCofre) Titulo() string   { return "Destrancar cofre" }
func (d *dlgCofre) Largura() unit.Dp { return 380 }

// pedirCofre abre o diálogo (ou reaproveita o que já está aberto),
// guardando a ação a repetir depois.
func pedirCofre(w *app.Window, arq *conexoes.Arquivo, caminho string, recarrega func(), depois func()) {
	if d, ok := dialogoAtual().(*dlgCofre); ok {
		d.depois = depois
		return
	}
	abrirDialogo(&dlgCofre{arq: arq, caminho: caminho, w: w, recarrega: recarrega, depois: depois})
}

func (d *dlgCofre) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Enter no campo vale por clicar em Destrancar.
	for {
		ev, ok := d.senha.Update(gtx)
		if !ok {
			break
		}
		if _, sub := ev.(widget.SubmitEvent); sub {
			if d.trocando {
				d.trocar()
			} else {
				d.destrancar()
			}
		}
	}
	if d.btnOk.Clicked(gtx) {
		if d.trocando {
			d.trocar()
		} else {
			d.destrancar()
		}
	}
	if d.btnTrocar.Clicked(gtx) {
		d.trocando = true
		d.erro, d.aviso = "", ""
	}
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}
	// Esc fecha — saída sempre disponível.
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			fecharDialogo()
		}
	}

	dica := "senha mestra"
	if d.trocando && cofreAberto != nil {
		dica = "senha mestra atual (já destrancada)"
	}
	filhos := []layout.FlexChild{
		layout.Rigid(rotulo(th, fonteMono, spSecundario,
			fmt.Sprintf("%d segredos guardados neste arquivo.", d.cifrados()), tema.Sec)),
		espaco(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return campoSenha(gtx, th, &d.senha, dica)
		}),
	}
	if d.trocando {
		filhos = append(filhos,
			espaco(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return campoSenha(gtx, th, &d.nova, "senha nova")
			}),
			espaco(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return campoSenha(gtx, th, &d.confirma, "repita a senha nova")
			}),
			espaco(6),
			layout.Rigid(rotulo(th, fonteMono, spCardMeta,
				"Todos os segredos serão recifrados e o arquivo regravado.", tema.Fraco)),
		)
	}
	if d.aviso != "" {
		filhos = append(filhos, espaco(6),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, d.aviso, tema.OkFg)))
	}
	if d.erro != "" {
		// validação INLINE, sob o campo — nunca um segundo diálogo.
		filhos = append(filhos, espaco(6),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, d.erro, tema.ErroFg)))
	}
	filhos = append(filhos, espaco(14), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if d.trocando {
					return layout.Dimensions{}
				}
				return botaoNeutro(gtx, th, &d.btnTrocar, "trocar senha")
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoNeutro(gtx, th, &d.btnCanc, "Cancelar")
			}),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				rot := "Destrancar"
				if d.trocando {
					rot = "Trocar senha"
				}
				return botaoPrimario(gtx, th, &d.btnOk, rot)
			}),
		)
	}))

	gtx.Execute(key.FocusCmd{Tag: &d.senha})
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

func (d *dlgCofre) cifrados() int {
	n := 0
	for _, cx := range d.arq.Conexoes {
		for _, v := range []string{cx.VNC.Senha, cx.SSH.Senha, cx.RDP.Senha} {
			if cofre.Cifrado(v) {
				n++
			}
		}
	}
	return n
}

func (d *dlgCofre) destrancar() {
	senha := d.senha.Text()
	if senha == "" {
		d.erro = "digite a senha mestra"
		return
	}
	p := paramsCofre(d.arq)
	c, err := cofre.Abrir(cofre.Parametros{
		KDF:         p["kdf"],
		Salt:        p["salt"],
		Verificador: p["verificador"],
	}, senha)
	if err != nil {
		d.erro = err.Error()
		d.senha.SetText("")
		return
	}
	cofreAberto = c
	fmt.Println("cofre destrancado")
	migrarChaveiro()
	depois := d.depois
	fecharDialogo()
	if depois != nil {
		depois()
	}
	d.w.Invalidate()
}

// trocar recifra todos os segredos do arquivo com uma senha mestra nova.
// A ordem importa: só regrava depois de conseguir abrir o cofre atual E
// criar o novo — se qualquer um falhar, o arquivo fica intacto.
func (d *dlgCofre) trocar() {
	nova, conf := d.nova.Text(), d.confirma.Text()
	if nova == "" {
		d.erro = "digite a senha nova"
		return
	}
	if nova != conf {
		d.erro = "a confirmação não bate com a senha nova"
		return
	}
	velho := cofreAberto
	if velho == nil {
		p := paramsCofre(d.arq)
		c, err := cofre.Abrir(cofre.Parametros{
			KDF:         p["kdf"],
			Salt:        p["salt"],
			Verificador: p["verificador"],
		}, d.senha.Text())
		if err != nil {
			d.erro = err.Error()
			return
		}
		velho = c
	}
	novo, params, err := cofre.Criar(nova)
	if err != nil {
		d.erro = err.Error()
		return
	}
	err = conexoes.TrocarSenhaMestra(d.caminho, velho.Decifrar, novo.Cifrar, map[string]string{
		"kdf":         params.KDF,
		"salt":        params.Salt,
		"verificador": params.Verificador,
	})
	if err != nil {
		d.erro = err.Error()
		return
	}
	cofreAberto = novo
	if d.recarrega != nil {
		d.recarrega()
	}
	fmt.Println("senha mestra trocada; segredos recifrados")
	fecharDialogo()
	d.w.Invalidate()
}
