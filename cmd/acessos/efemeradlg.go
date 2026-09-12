package main

import (
	"strings"

	"acessos-go/internal/conexoes"

	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgEfemera pergunta as credenciais de um destino que NÃO está
// cadastrado, antes de abrir a sessão.
//
// Sem isto a sessão temporária nascia sem senha e a conexão morria com
// "autenticação recusada" — o operador tinha que adivinhar que o problema
// era esse. Os campos mudam com o protocolo, porque cada um pede coisas
// diferentes: VNC quase sempre só a senha, shell exige usuário, RDP ainda
// tem domínio.
//
// Nada é gravado: o destino continua fora do inventário, e a senha vive só
// enquanto a aba existir.
type dlgEfemera struct {
	w     *app.Window
	cx    conexoes.Conexao
	proto conexoes.Protocolo
	abrir func(conexoes.Conexao)

	usuario  widget.Editor
	senha    widget.Editor
	dominio  widget.Editor
	btnOk    widget.Clickable
	btnCanc  widget.Clickable
	erro     string
	iniciado bool
}

// pedirCredenciaisEfemeras abre o diálogo e chama abrir com a conexão já
// preenchida.
func pedirCredenciaisEfemeras(w *app.Window, cx conexoes.Conexao, p conexoes.Protocolo,
	abrir func(conexoes.Conexao)) {
	abrirDialogo(&dlgEfemera{w: w, cx: cx, proto: p, abrir: abrir})
}

func (d *dlgEfemera) Titulo() string {
	return "Conectar a " + d.cx.Host + " (" + estiloDe(d.proto).rotulo + ")"
}

func (d *dlgEfemera) Largura() unit.Dp { return 420 }

func (d *dlgEfemera) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if !d.iniciado {
		d.iniciado = true
		d.usuario.SetText(d.usuarioInicial())
	}
	// Enter em qualquer campo confirma.
	for _, ed := range []*widget.Editor{&d.usuario, &d.senha, &d.dominio} {
		for {
			ev, ok := ed.Update(gtx)
			if !ok {
				break
			}
			if _, sub := ev.(widget.SubmitEvent); sub {
				d.confirmar()
			}
		}
	}
	if d.btnOk.Clicked(gtx) {
		d.confirmar()
	}
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			fecharDialogo()
		}
	}

	filhos := []layout.FlexChild{
		layout.Rigid(paragrafo(th, fonteSans, spSecundario,
			"Destino não cadastrado: a senha vale só para esta aba e nada é gravado.", tema.Fraco)),
		espaco(10),
	}
	if d.proto != conexoes.VNC {
		filhos = append(filhos,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return caixaEditor(gtx, th, &d.usuario, "usuário", 0)
			}),
			espaco(6),
		)
	}
	filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return campoSenha(gtx, th, &d.senha, "senha")
	}))
	if d.proto == conexoes.RDP {
		filhos = append(filhos, espaco(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return caixaEditor(gtx, th, &d.dominio, "domínio (opcional)", 0)
			}))
	}
	if d.proto == conexoes.VNC {
		// usuário no VNC é a exceção (UltraVNC MS-Logon, VeNCrypt), então
		// ele vem DEPOIS da senha e dizendo que quase sempre fica vazio.
		filhos = append(filhos, espaco(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return caixaEditor(gtx, th, &d.usuario, "usuário (só se o servidor pedir)", 0)
			}))
	}
	if d.erro != "" {
		filhos = append(filhos, espaco(6),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, d.erro, tema.ErroFg)))
	}
	filhos = append(filhos, espaco(14), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoNeutro(gtx, th, &d.btnCanc, "Cancelar")
			}),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoPrimario(gtx, th, &d.btnOk, "Conectar")
			}),
		)
	}))

	foco := &d.senha
	if d.proto != conexoes.VNC && d.usuario.Text() == "" {
		foco = &d.usuario
	}
	if !gtx.Focused(&d.usuario) && !gtx.Focused(&d.senha) && !gtx.Focused(&d.dominio) {
		gtx.Execute(key.FocusCmd{Tag: foco})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

func (d *dlgEfemera) usuarioInicial() string {
	switch d.proto {
	case conexoes.SSH, conexoes.SFTP:
		return d.cx.SSH.Usuario
	case conexoes.RDP:
		return d.cx.RDP.Usuario
	}
	return d.cx.VNC.Usuario
}

func (d *dlgEfemera) confirmar() {
	usuario := strings.TrimSpace(d.usuario.Text())
	senha := d.senha.Text()
	cx := d.cx
	switch d.proto {
	case conexoes.SSH, conexoes.SFTP:
		if usuario == "" {
			d.erro = "shell precisa de usuário"
			return
		}
		cx.SSH.Usuario, cx.SSH.Senha = usuario, senha
	case conexoes.RDP:
		if usuario == "" {
			d.erro = "RDP precisa de usuário"
			return
		}
		cx.RDP.Usuario, cx.RDP.Senha = usuario, senha
		cx.RDP.Dominio = strings.TrimSpace(d.dominio.Text())
	default:
		cx.VNC.Usuario, cx.VNC.Senha = usuario, senha
	}
	abrir := d.abrir
	fecharDialogo()
	if abrir != nil {
		abrir(cx)
	}
	d.w.Invalidate()
}
