package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"os"

	"acessos-go/internal/chaveiro"
	"acessos-go/internal/cofre"
	"acessos-go/internal/conexoes"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgChaveiro é o gerenciador de credenciais NOMEADAS. Uma credencial
// guardada aqui é usada por várias conexões através do alias "!nome" no
// usuário e/ou na senha — trocar a senha do suporte em 200 caixas passa a
// ser trocar uma linha.
type dlgChaveiro struct {
	w       *app.Window
	caminho string
	arq     *chaveiro.Arquivo
	erro    string

	lista       widget.List
	btnLinha    []widget.Clickable
	btnRemove   []widget.Clickable
	sel         string // nome em edição ("" = nova)
	nome        widget.Editor
	usuario     widget.Editor
	senha       widget.Editor
	btnNovo     widget.Clickable
	btnImportar widget.Clickable
	btnSalvar   widget.Clickable
	btnFechar   widget.Clickable
}

func abrirChaveiro(w *app.Window) {
	d := &dlgChaveiro{w: w, caminho: caminhoChaveiro()}
	d.lista.Axis = layout.Vertical
	d.nome.SingleLine = true
	d.usuario.SingleLine = true
	d.senha.SingleLine = true
	d.senha.Mask = '•'
	d.recarregar()
	abrirDialogo(d)
}

// caminhoChaveiro: ao lado do conexoes.ini, como no app original.
func caminhoChaveiro() string {
	if caminhoINI == "" {
		return "chaveiro.ini"
	}
	return filepath.Join(filepath.Dir(caminhoINI), "chaveiro.ini")
}

func (d *dlgChaveiro) Titulo() string   { return "Chaveiro" }
func (d *dlgChaveiro) Largura() unit.Dp { return 660 }

func (d *dlgChaveiro) recarregar() {
	a, err := chaveiro.Carregar(d.caminho)
	if err != nil {
		d.erro = err.Error()
		return
	}
	if a == nil {
		a = &chaveiro.Arquivo{Caminho: d.caminho, Cofre: map[string]string{}, Credenciais: map[string]chaveiro.Credencial{}}
	}
	d.arq = a
	chaveiroAtual = a
	d.btnLinha = make([]widget.Clickable, len(a.Ordem))
	d.btnRemove = make([]widget.Clickable, len(a.Ordem))
}

func (d *dlgChaveiro) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	for i, nome := range d.arq.Ordem {
		if i < len(d.btnLinha) && d.btnLinha[i].Clicked(gtx) {
			c := d.arq.Credenciais[nome]
			d.sel = nome
			d.nome.SetText(nome)
			d.usuario.SetText(c.Usuario)
			d.senha.SetText("") // segredo não volta pra tela
		}
		if i < len(d.btnRemove) && d.btnRemove[i].Clicked(gtx) {
			alvo := nome
			confirmarDestrutivo(d.w, "Remover credencial",
				[]string{alvo + "  (conexões com !" + alvo + " ficam com alias quebrado)"},
				"Remover definitivamente", func() {
					if err := chaveiro.Remover(d.caminho, alvo); err != nil {
						fmt.Println(err)
					}
					d.sel = ""
					d.recarregar()
					abrirDialogo(d)
				})
		}
	}
	if d.btnNovo.Clicked(gtx) {
		d.sel = ""
		d.nome.SetText("")
		d.usuario.SetText("")
		d.senha.SetText("")
	}
	if d.btnImportar.Clicked(gtx) {
		d.importar()
	}
	if d.btnFechar.Clicked(gtx) {
		// recarrega as conexões: alias resolvido muda o que será enviado
		if recarregarINI != nil {
			recarregarINI()
		}
		fecharDialogo()
	}
	if d.btnSalvar.Clicked(gtx) {
		d.salvar()
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if cofreAberto == nil {
				// Sem cofre aberto o chaveiro é só uma lista de nomes: as
				// senhas estão cifradas e não há como usá-las nem gravá-las.
				return rotulo(th, fonteMono, spSecundario,
					"Cofre trancado: as senhas estão cifradas e não podem ser usadas nem alteradas.",
					tema.AtencaoFg)(gtx)
			}
			return rotulo(th, fonteMono, spCardMeta,
				"Aponte para elas no conexoes.ini com \"!nome\" no usuário e/ou na senha.", tema.Sec)(gtx)
		}),
		espaco(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = gtx.Constraints.Max.Y / 2
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					larg := gtx.Dp(280)
					gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
					return layout.Background{}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							superficie(gtx, gtx.Constraints.Min, tema.Vidro1, tema.Borda, 8)
							return layout.Dimensions{Size: gtx.Constraints.Min}
						},
						func(gtx layout.Context) layout.Dimensions {
							return layout.UniformInset(4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return d.listaCreds(gtx, th)
							})
						},
					)
				}),
				layout.Rigid(layout.Spacer{Width: 10}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return d.formulario(gtx, th)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if d.erro == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 6}.Layout(gtx, rotulo(th, fonteMono, spSecundario, d.erro, tema.ErroFg))
		}),
		espaco(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoSutil(gtx, th, &d.btnNovo, "+ nova credencial")
				}),
				layout.Rigid(layout.Spacer{Width: 6}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoSutil(gtx, th, &d.btnImportar, "importar do .ini")
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnFechar, "Fechar")
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoPrimario(gtx, th, &d.btnSalvar, "Salvar")
				}),
			)
		}),
	)
}

func (d *dlgChaveiro) listaCreds(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if len(d.arq.Ordem) == 0 {
		return layout.Center.Layout(gtx, rotulo(th, fonteMono, spCardMeta, "nenhuma credencial ainda", tema.Fraco))
	}
	return material.List(th, &d.lista).Layout(gtx, len(d.arq.Ordem), func(gtx layout.Context, i int) layout.Dimensions {
		nome := d.arq.Ordem[i]
		c := d.arq.Credenciais[nome]
		return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					fundo, borda := tema.Cartao, transparente
					if nome == d.sel {
						fundo, borda = tema.AzulFraco, tema.Azul
					} else if d.btnLinha[i].Hovered() {
						fundo, borda = tema.Hover, tema.Borda2
					}
					superficie(gtx, gtx.Constraints.Min, fundo, borda, 6)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				},
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 4, Bottom: 4, Left: 8, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return d.btnLinha[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min.X = gtx.Constraints.Max.X
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(rotuloLinha(th, fonteSans, spCorpo, "!"+nome, tema.Texto)),
										layout.Rigid(rotuloLinha(th, fonteMono, spCardMeta, c.Usuario, tema.Fraco)),
									)
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return d.btnRemove[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									cor := tema.Fraco
									if d.btnRemove[i].Hovered() {
										cor = tema.ErroFg
									}
									return layout.UniformInset(3).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return icone(gtx, icons.ActionDelete, cor, 15)
									})
								})
							}),
						)
					})
				},
			)
		})
	})
}

func (d *dlgChaveiro) formulario(gtx layout.Context, th *material.Theme) layout.Dimensions {
	dicaSenha := "senha"
	if d.sel != "" {
		dicaSenha = "senha (vazio mantém a atual)"
	}
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			superficie(gtx, gtx.Constraints.Min, tema.Cartao, tema.Borda, 8)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.UniformInset(10).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return caixaEditor(gtx, th, &d.nome, "nome (vira !nome)", 0)
					}),
					espaco(6),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return caixaEditor(gtx, th, &d.usuario, "usuário", 0)
					}),
					espaco(6),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return caixaEditor(gtx, th, &d.senha, dicaSenha, 0)
					}),
					espaco(8),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						aviso := "a senha será cifrada com o cofre"
						cor := tema.Sec
						if cofreAberto == nil {
							aviso, cor = "cofre trancado: destranque para gravar senha", tema.ErroFg
						}
						return rotulo(th, fonteMono, spCardMeta, aviso, cor)(gtx)
					}),
				)
			})
		},
	)
}

func (d *dlgChaveiro) salvar() {
	nome := strings.TrimSpace(d.nome.Text())
	if nome == "" {
		d.erro = "o nome não pode ficar vazio"
		return
	}
	senha := d.senha.Text()
	if senha != "" {
		// Senha do chaveiro é SEMPRE cifrada. Com o cofre trancado ela
		// iria em claro para o arquivo — e aí o cofre deixa de valer
		// alguma coisa: bastaria abrir o chaveiro.ini num editor para ter
		// a credencial que abre o parque inteiro.
		if cofreAberto == nil {
			d.erro = "destranque o cofre para gravar uma senha (ela precisa ser cifrada)"
			return
		}
		selada, err := cofreAberto.Cifrar(senha)
		if err != nil {
			d.erro = err.Error()
			return
		}
		senha = selada
	}
	if err := chaveiro.Salvar(d.caminho, nome, d.usuario.Text(), senha); err != nil {
		d.erro = err.Error()
		return
	}
	d.erro = ""
	d.sel = nome
	d.senha.SetText("")
	d.recarregar()
}

// importar varre o conexoes.ini e cria uma credencial para cada par
// usuário+senha REPETIDO — que é o caso real: o mesmo login de suporte em
// dezenas de caixas. Par usado uma vez só não vira credencial: não ganha
// nada e só polui a lista.
//
// Não mexe nas conexões: quem troca `senha` por `!alias` é o operador, no
// editor, quando quiser. Importar é o passo seguro; reescrever 274 seções
// de uma vez não é.
func (d *dlgChaveiro) importar() {
	if caminhoINI == "" {
		d.erro = "nenhum conexoes.ini carregado"
		return
	}
	arq, err := conexoes.Carregar(caminhoINI)
	if err != nil {
		d.erro = err.Error()
		return
	}

	type par struct{ usuario, senha string }
	conta := map[par]int{}
	for _, cx := range arq.Conexoes {
		for _, a := range []struct{ u, s string }{
			{cx.VNC.Usuario, cx.VNC.Senha},
			{cx.SSH.Usuario, cx.SSH.Senha},
			{cx.RDP.Usuario, cx.RDP.Senha},
		} {
			if a.u == "" || a.s == "" || chaveiro.EhAlias(a.s) {
				continue
			}
			conta[par{a.u, a.s}]++
		}
	}

	criadas := 0
	for p, n := range conta {
		if n < 2 {
			continue
		}
		nome := p.usuario
		if _, existe := d.arq.Credenciais[nome]; existe {
			continue
		}
		if err := chaveiro.Salvar(d.caminho, nome, p.usuario, p.senha); err != nil {
			d.erro = err.Error()
			break
		}
		criadas++
	}
	d.recarregar()
	if criadas == 0 {
		d.erro = "nada a importar: nenhum usuário/senha se repete no arquivo"
		return
	}
	d.erro = ""
	fmt.Printf("chaveiro: %d credencial(is) importada(s)\n", criadas)
}

// migrarChaveiro cifra as senhas do chaveiro que ainda estiverem em
// claro. Roda assim que o cofre é destrancado, porque é o único momento
// em que existe chave para isso.
//
// Por que é necessário: versões anteriores gravavam em claro quando o
// cofre estava trancado. Uma credencial de suporte em claro no disco anula
// o cofre inteiro — quem abrir o arquivo num editor tem a senha que entra
// no parque todo.
func migrarChaveiro() {
	if cofreAberto == nil {
		return
	}
	caminho := caminhoChaveiro()
	arq, err := chaveiro.Carregar(caminho)
	if err != nil || arq == nil {
		return
	}
	migradas := 0
	for _, nome := range arq.Ordem {
		c := arq.Credenciais[nome]
		if c.Senha == "" || cofre.Cifrado(c.Senha) {
			continue
		}
		selada, err := cofreAberto.Cifrar(c.Senha)
		if err != nil {
			fmt.Fprintln(os.Stderr, "chaveiro:", err)
			continue
		}
		if err := chaveiro.Salvar(caminho, nome, c.Usuario, selada); err != nil {
			fmt.Fprintln(os.Stderr, "chaveiro:", err)
			continue
		}
		migradas++
	}
	if migradas > 0 {
		reg("chaveiro: %d senha(s) em claro foram cifradas", migradas)
	}
	// recarrega para o resto do app enxergar o arquivo novo
	if novo, err := chaveiro.Carregar(caminho); err == nil && novo != nil {
		chaveiroAtual = novo
	}
}
