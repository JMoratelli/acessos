package main

import (
	"fmt"
	"strings"
	"time"

	"acessos-go/internal/massa/model"
	"acessos-go/internal/massa/runner"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// As duas paradas da execução em massa — o canário e a trava de erro —
// são FAIXAS dentro da aba, não diálogos modais.
//
// Já foram modais e estava errado: a decisão de seguir depende de olhar o
// que aconteceu (abrir a saída de outra máquina, conferir um retorno, rever
// a fila), e um modal na frente impede exatamente isso. A goroutine do
// runner continua bloqueada no canal, que é o que dá a trava; o que mudou
// é que a interface segue inteira utilizável enquanto a resposta não vem.

type pausaTipo int

const (
	pausaNenhuma pausaTipo = iota
	pausaCanario
	pausaErro
)

// pausa é o pedido de decisão que veio do runner.
type pausa struct {
	tipo      pausaTipo
	resumo    runner.ResumoCanario
	restantes int
	erro      runner.ErroCtx
	respBool  chan bool
	respDec   chan runner.Decisao
	desde     time.Time

	btnSeguir  widget.Clickable
	btnParar   widget.Clickable
	btnPular   widget.Clickable
	btnTudo    widget.Clickable
	btnAbortar widget.Clickable
	btnSaida   widget.Clickable
}

func (t *massaTab) pausaAtual() *pausa {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pausa
}

func (t *massaTab) definirPausa(p *pausa) {
	t.mu.Lock()
	t.pausa = p
	t.mu.Unlock()
	t.w.Invalidate()
}

// esperarCanario é chamado DA GOROUTINE DO RUNNER e bloqueia nela.
func (t *massaTab) esperarCanario(r runner.ResumoCanario, restantes int) bool {
	resp := make(chan bool, 1)
	t.definirPausa(&pausa{
		tipo: pausaCanario, resumo: r, restantes: restantes,
		respBool: resp, desde: time.Now(),
	})
	v := <-resp
	t.definirPausa(nil)
	return v
}

// esperarErro idem, para a trava de erro de um host.
func (t *massaTab) esperarErro(c runner.ErroCtx) runner.Decisao {
	resp := make(chan runner.Decisao, 1)
	t.definirPausa(&pausa{tipo: pausaErro, erro: c, respDec: resp, desde: time.Now()})
	d := <-resp
	t.definirPausa(nil)
	return d
}

func (p *pausa) responderBool(v bool) {
	select {
	case p.respBool <- v:
	default:
	}
}

func (p *pausa) responderDec(d runner.Decisao) {
	select {
	case p.respDec <- d:
	default:
	}
}

// faixaPausa desenha a barra de decisão. Devolve dimensões zero quando não
// há nada pendente.
func (t *massaTab) faixaPausa(gtx layout.Context) layout.Dimensions {
	p := t.pausaAtual()
	if p == nil {
		return layout.Dimensions{}
	}
	th := t.th

	grave := p.tipo == pausaErro || p.resumo.Falhou
	fundo, borda := tema.OkBg, tema.OkFg
	if grave {
		fundo, borda = tema.ErroBg, tema.ErroFg
	}

	// Espera de 3s só quando seguir é arriscado: canário que falhou ou
	// "continuar sem perguntar" depois de um erro. Canário limpo não
	// precisa de pedágio.
	espera := time.Duration(0)
	if grave {
		espera = esperaDestrutiva - time.Since(p.desde)
	}
	pronto := espera <= 0
	if !pronto {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(200 * time.Millisecond)})
	}

	var titulo, detalhe string
	if p.tipo == pausaCanario {
		ok := 0
		for _, r := range p.resumo.Resultados {
			if r.Status == model.StatusOK {
				ok++
			}
		}
		titulo = fmt.Sprintf("Canário: %d de %d OK", ok, len(p.resumo.Resultados))
		var calib []string
		for i, d := range p.resumo.TimeoutsExec {
			calib = append(calib, fmt.Sprintf("cmd %d %s", i+1, d.Truncate(time.Second)))
		}
		detalhe = "timeout calibrado: " + strings.Join(calib, " · ") +
			" · conexão " + p.resumo.TimeoutConexao.Truncate(time.Second).String()
		if p.resumo.Falhou {
			detalhe = "Algum canário falhou. " + detalhe
		}
	} else {
		titulo = "Falha em " + p.erro.Host.IP
		detalhe = fmt.Sprintf("comando %d · exit %d", p.erro.IndiceCmd+1, p.erro.ExitCode)
		if p.erro.Erro != nil {
			detalhe = p.erro.Erro.Error()
		}
		detalhe += " — a execução está parada nesta máquina"
	}

	// cliques
	switch {
	case p.tipo == pausaCanario && p.btnParar.Clicked(gtx):
		p.responderBool(false)
	case p.tipo == pausaCanario && pronto && p.btnSeguir.Clicked(gtx):
		p.responderBool(true)
	case p.tipo == pausaErro && p.btnSeguir.Clicked(gtx):
		p.responderDec(runner.DecContinuarHost)
	case p.tipo == pausaErro && p.btnPular.Clicked(gtx):
		p.responderDec(runner.DecPularHost)
	case p.tipo == pausaErro && pronto && p.btnTudo.Clicked(gtx):
		p.responderDec(runner.DecContinuarTudo)
	case p.tipo == pausaErro && p.btnAbortar.Clicked(gtx):
		p.responderDec(runner.DecAbortar)
	case p.tipo == pausaErro && p.btnSaida.Clicked(gtx):
		abrirDialogo(&dlgSaida{titulo: p.erro.Host.IP, texto: p.erro.Saida})
	}

	return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				superficie(gtx, gtx.Constraints.Min, fundo, borda, 8)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			},
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: 8, Bottom: 8, Left: 10, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(negrito(txt(th, fonteCond, spCardHost, titulo, tema.Texto)).Layout),
						layout.Rigid(rotulo(th, fonteMono, spCardMeta, detalhe, tema.Sec)),
						espaco(8),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return t.botoesPausa(gtx, p, pronto, espera)
						}),
					)
				})
			},
		)
	})
}

func (t *massaTab) botoesPausa(gtx layout.Context, p *pausa, pronto bool, espera time.Duration) layout.Dimensions {
	th := t.th
	rotEspera := func(base string) string {
		if pronto {
			return base
		}
		return fmt.Sprintf("%s em %ds", base, int(espera.Seconds())+1)
	}

	if p.tipo == pausaCanario {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoNeutro(gtx, th, &p.btnParar, "Parar aqui")
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx = botaoLargura(gtx, 300)
				base := fmt.Sprintf("Continuar nos %d restantes", p.restantes)
				if !p.resumo.Falhou {
					return botaoPrimario(gtx, th, &p.btnSeguir, base)
				}
				// canário que falhou: seguir é ação de risco
				return botao(gtx, th, &p.btnSeguir, rotEspera(base), pesoPerigo, !pronto)
			}),
		)
	}

	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoNeutro(gtx, th, &p.btnSaida, "ver saída")
		}),
		layout.Rigid(layout.Spacer{Width: 8}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoPerigo(gtx, th, &p.btnAbortar, "Abortar tudo")
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx = botaoLargura(gtx, 230)
			return botao(gtx, th, &p.btnTudo, rotEspera("Continuar sem perguntar"), pesoNeutro, !pronto)
		}),
		layout.Rigid(layout.Spacer{Width: 8}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoNeutro(gtx, th, &p.btnPular, "Pular esta")
		}),
		layout.Rigid(layout.Spacer{Width: 8}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoPrimario(gtx, th, &p.btnSeguir, "Continuar nesta")
		}),
	)
}

var _ = material.Label
