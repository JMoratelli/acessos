package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgAjustes mostra ONDE o app está lendo cada coisa e deixa apontar para
// outro conexoes.ini. É a informação que mais some quando o arquivo está
// no Drive/Insync e existem duas cópias: sem ver o caminho, ninguém
// descobre que está editando o inventário errado.
type dlgAjustes struct {
	w       *app.Window
	ini     widget.Editor
	btnOk   widget.Clickable
	btnCanc widget.Clickable
	verDiag widget.Clickable
	diagOn  bool
	lista   widget.List
	erro    string
	aviso   string
}

func abrirAjustes(w *app.Window, ini string) {
	d := &dlgAjustes{w: w}
	d.ini.SingleLine = true
	d.ini.SetText(ini)
	abrirDialogo(d)
}

func (d *dlgAjustes) Titulo() string   { return "Ajustes" }
func (d *dlgAjustes) Largura() unit.Dp { return 620 }

func (d *dlgAjustes) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}
	if d.btnOk.Clicked(gtx) {
		d.aplicar()
	}
	if d.verDiag.Clicked(gtx) {
		d.diagOn = !d.diagOn
	}
	d.lista.Axis = layout.Vertical

	dir := filepath.Dir(d.ini.Text())
	linhaInfo := func(rot, valor string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						larg := gtx.Dp(110)
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
						return rotulo(th, fonteMono, spSecundario, rot, tema.Sec)(gtx)
					}),
					layout.Flexed(1, rotulo(th, fonteMono, spSecundario, valor, tema.Texto)),
				)
			})
		})
	}

	filhos := []layout.FlexChild{
		layout.Rigid(rotulo(th, fonteMono, spSecundario, "conexões (arquivo em uso)", tema.Sec)),
		espaco(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return caixaEditor(gtx, th, &d.ini, "caminho do conexoes.ini", 0)
		}),
		espaco(12),
		linhaInfo("snippets", caminhoSnippets(d.ini.Text())),
		linhaInfo("chaveiro", filepath.Join(dir, "chaveiro.ini")),
		linhaInfo("cofre", ondeEstaOCofre()),
		linhaInfo("tema", nomeDoTema()),
	}
	if d.erro != "" {
		filhos = append(filhos, espaco(8),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, d.erro, tema.ErroFg)))
	}
	if d.aviso != "" {
		filhos = append(filhos, espaco(8),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, d.aviso, tema.OkFg)))
	}
	filhos = append(filhos, espaco(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return caixaMarcar(gtx, th, &d.verDiag, d.diagOn, "mostrar diagnóstico")
		}))
	if d.diagOn {
		linhas := diagnostico()
		filhos = append(filhos, espaco(6), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = gtx.Constraints.Max.Y / 2
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					superficie(gtx, gtx.Constraints.Min, tema.TermBg, tema.Borda2, 8)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				},
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.List(th, &d.lista).Layout(gtx, len(linhas), func(gtx layout.Context, i int) layout.Dimensions {
							// mais recentes primeiro: é o que se quer ver
							return rotuloLinha(th, fonteMono, spCardMeta, linhas[len(linhas)-1-i], tema.TermFg)(gtx)
						})
					})
				},
			)
		}))
	}
	filhos = append(filhos, espaco(14), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoNeutro(gtx, th, &d.btnCanc, "Fechar")
			}),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoPrimario(gtx, th, &d.btnOk, "Usar este arquivo")
			}),
		)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

// aplicar troca o arquivo em uso. Não regrava nada: só passa a ler de
// outro lugar, e o cofre volta a ficar trancado — a senha mestra de um
// arquivo não vale para outro.
func (d *dlgAjustes) aplicar() {
	novo := d.ini.Text()
	if _, err := os.Stat(novo); err != nil {
		d.erro = err.Error()
		return
	}
	if trocarArquivoINI == nil {
		d.erro = "troca de arquivo indisponível nesta sessão"
		return
	}
	if err := trocarArquivoINI(novo); err != nil {
		d.erro = err.Error()
		return
	}
	d.erro = ""
	d.aviso = fmt.Sprintf("lendo de %s", novo)
	d.w.Invalidate()
}

// trocarArquivoINI é preenchido no main: recarrega o painel a partir de
// outro arquivo.
var trocarArquivoINI func(string) error

func ondeEstaOCofre() string {
	if chaveiroAtual != nil && len(chaveiroAtual.Cofre) > 0 {
		return "chaveiro.ini"
	}
	return "conexoes.ini (formato antigo)"
}

func nomeDoTema() string {
	if tema.Fundo == temaEscuro.Fundo {
		return "escuro"
	}
	return "claro"
}
