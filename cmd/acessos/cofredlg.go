package main

import (
	"fmt"

	"acessos-go/internal/chaveiro"
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

	// Cofre ainda não existe neste inventário: em vez de pedir uma senha
	// que nunca vai bater, o diálogo CRIA o cofre. Um arquivo novo (o
	// exemplo que o app escreve na primeira execução) não tem seção
	// [cofre] nenhuma, e sem isto o cadeado ficava fechado para sempre.
	criando bool
}

func (d *dlgCofre) Titulo() string {
	if d.criando {
		return "Criar cofre"
	}
	return "Destrancar cofre"
}
func (d *dlgCofre) Largura() unit.Dp { return 380 }

// pedirCofre abre o diálogo (ou reaproveita o que já está aberto),
// guardando a ação a repetir depois.
func pedirCofre(w *app.Window, arq *conexoes.Arquivo, caminho string, recarrega func(), depois func()) {
	if d, ok := dialogoAtual().(*dlgCofre); ok {
		d.depois = depois
		return
	}
	abrirDialogo(&dlgCofre{arq: arq, caminho: caminho, w: w, recarrega: recarrega,
		depois: depois, criando: !temCofre(arq)})
}

func (d *dlgCofre) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Enter no campo vale por clicar em Destrancar.
	for {
		ev, ok := d.senha.Update(gtx)
		if !ok {
			break
		}
		if _, sub := ev.(widget.SubmitEvent); sub {
			d.confirmar()
		}
	}
	if d.btnOk.Clicked(gtx) {
		d.confirmar()
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
	var filhos []layout.FlexChild
	if d.criando {
		filhos = []layout.FlexChild{
			layout.Rigid(rotulo(th, fonteMono, spSecundario,
				"Este inventário ainda não tem cofre.", tema.Sec)),
			espaco(4),
			layout.Rigid(paragrafo(th, fonteSans, spSecundario,
				"A senha mestra cifra as senhas das conexões e do chaveiro. Ela não é guardada em lugar nenhum: perdeu, perdeu.", tema.Fraco)),
			espaco(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return campoSenha(gtx, th, &d.nova, "senha mestra nova")
			}),
			espaco(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return campoSenha(gtx, th, &d.confirma, "repita a senha")
			}),
		}
	} else {
		filhos = []layout.FlexChild{
			layout.Rigid(rotulo(th, fonteMono, spSecundario,
				fmt.Sprintf("%d segredos guardados neste arquivo.", d.cifrados()), tema.Sec)),
			espaco(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return campoSenha(gtx, th, &d.senha, dica)
			}),
		}
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
				if d.trocando || d.criando {
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
				switch {
				case d.criando:
					rot = "Criar cofre"
				case d.trocando:
					rot = "Trocar senha"
				}
				return botaoPrimario(gtx, th, &d.btnOk, rot)
			}),
		)
	}))

	foco := &d.senha
	if d.criando {
		foco = &d.nova
	}
	gtx.Execute(key.FocusCmd{Tag: foco})
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

// confirmar roteia o Enter e o botão principal para o que o diálogo está
// fazendo neste momento.
func (d *dlgCofre) confirmar() {
	switch {
	case d.criando:
		d.criar()
	case d.trocando:
		d.trocar()
	default:
		d.destrancar()
	}
}

// temCofre diz se já existe cofre para este inventário. Sem verificador
// não há o que conferir — qualquer senha "erra", que era o beco sem saída
// do cadeado fechado num arquivo novo.
func temCofre(arq *conexoes.Arquivo) bool {
	return paramsCofre(arq)["verificador"] != ""
}

// criar inicializa o cofre: gera sal e verificador a partir da senha
// mestra e grava a seção [cofre] no chaveiro.ini, que é onde ela mora
// desde a 1.2.0 (o conexoes.ini segue sendo lido para instalações
// antigas). Nenhum segredo é recifrado aqui: não existe nenhum ainda —
// as senhas passam a ser cifradas conforme as conexões forem salvas.
func (d *dlgCofre) criar() {
	nova, conf := d.nova.Text(), d.confirma.Text()
	if nova == "" {
		d.erro = "digite a senha mestra"
		return
	}
	if nova != conf {
		d.erro = "a confirmação não bate com a senha"
		return
	}
	novo, params, err := cofre.Criar(nova)
	if err != nil {
		d.erro = err.Error()
		return
	}
	caminho := caminhoChaveiro()
	if err := chaveiro.GravarCofre(caminho, map[string]string{
		"kdf":         params.KDF,
		"salt":        params.Salt,
		"verificador": params.Verificador,
	}); err != nil {
		d.erro = err.Error()
		return
	}
	// recarrega o chaveiro para o cofre recém-gravado valer já nesta
	// sessão (paramsCofre lê de chaveiroAtual).
	if ch, err := chaveiro.Carregar(caminho); err == nil && ch != nil {
		chaveiroAtual = ch
	}
	cofreAberto = novo
	fmt.Println("cofre criado e destrancado")
	depois := d.depois
	fecharDialogo()
	if depois != nil {
		depois()
	}
	d.w.Invalidate()
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
