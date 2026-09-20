package main

// A sessão remota fora da tira de abas: janela própria, com a barrinha
// fina no topo e mais nada.
//
// ----------------------------------------------------------------------
// O QUE ELA É, E O QUE NÃO É
// ----------------------------------------------------------------------
//
// Destacar NÃO mexe na sessão. Cada sessão de tela já roda num
// processo-filho (ver internal/telaproc), e o que muda aqui é só quem
// desenha o quadro e quem entrega teclado e ponteiro. O processo do outro
// lado não fica sabendo de nada: nem reconecta, nem renegocia resolução.
//
// Por isso "destacar" e "fechar" são coisas diferentes, e a tira de abas
// tem um método para cada (retirar vs fechar). Fechar chama Close() e
// derruba a sessão; destacar não.
//
// FECHAR A JANELA DEVOLVE A ABA, não encerra a sessão. É a escolha
// conservadora: a janela é um visor, e um visor que some não deveria
// levar junto o trabalho de quem estava conectado. Quem quer encerrar
// fecha a aba pelo X dela, como sempre — ação destrutiva continua
// explícita e num lugar só.
//
// ----------------------------------------------------------------------
// A BARRINHA
// ----------------------------------------------------------------------
//
// É a MESMA layoutBarraSessao da janela principal, com botões de janela
// no extremo direito. Ela já era o que o pedido descrevia — 26dp, chip de
// estado, destino e geometria —, então a tela cheia não precisou de barra
// nova: precisou parar de desenhar a faixa de título e a tira de abas em
// volta dela.

import (
	"image"

	"gio.tools/icons"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// abaDestacavel é a aba que sabe trocar de janela. Só ela pode ser
// destacada — sem isto a aba continuaria pedindo quadro para a janela
// antiga, e a destacada nunca repintaria.
type abaDestacavel interface {
	Tab
	TrocarJanela(w *app.Window)
}

type janelaSessao struct {
	w   *app.Window
	est *estadoJanela
	t   abaDestacavel
	th  *material.Theme

	telaCheia bool

	btnTelaCheia widget.Clickable
	btnReatar    widget.Clickable

	// tagConteudo é o alvo de ponteiro DESTA janela. Um por janela: o da
	// principal é outro, e compartilhar faria o Gio entregar o evento de
	// uma para a outra.
	tagConteudo *int

	// aoDevolver recoloca a aba na tira. Roda na goroutine da JANELA
	// PRINCIPAL (enfileirado), nunca aqui: mexer na tira de abas de fora
	// do laço dela é corrida de dados com o desenho.
	aoDevolver func(abaDestacavel)
}

// abrirJanelaSessao tira a aba da tira e a mostra em janela própria, já em
// tela cheia — que é o pedido que originou o destacamento.
//
// aoDevolver é chamado uma vez, quando a aba tem de voltar: pelo botão, ou
// porque a janela foi fechada.
func abrirJanelaSessao(th *material.Theme, t abaDestacavel, cheia bool,
	aoDevolver func(abaDestacavel)) {

	j := &janelaSessao{
		th: th, t: t, telaCheia: cheia,
		tagConteudo: new(int),
		aoDevolver:  aoDevolver,
	}

	j.w = new(app.Window)
	j.w.Option(
		app.Title("Acessos — "+t.Title()),
		// Decoração do sistema: ao contrário da janela principal, aqui não
		// desenhamos faixa de título nenhuma. A barrinha fina já diz o que
		// precisa ser dito, e inventar uma segunda moldura só para esta
		// janela criaria a segunda cópia do CSD para manter.
		app.Size(unit.Dp(1024), unit.Dp(768)),
	)
	if cheia {
		j.w.Option(app.Fullscreen.Option())
	}

	// A aba passa a pedir quadro AQUI. Antes de qualquer evento: a
	// goroutine de rede da sessão pode pedir redesenho no mesmo
	// instante, e apontar para a janela velha perderia o primeiro quadro.
	t.TrocarJanela(j.w)

	j.est = novoEstadoJanela(j.w)
	go j.laco()
}

func (j *janelaSessao) laco() {
	var ops op.Ops
	defer func() {
		esquecerJanela(j.w)

		// Devolve os atalhos ao compositor ANTES de soltar a captura. É
		// a parte que o operador sente: com a inibição presa, o Alt+Tab
		// dele continuaria sumindo depois que esta janela fechou. Fazer
		// isto primeiro garante que aconteça mesmo se o Stop abaixo der
		// problema.
		g := j.est.grab.Load()
		g.Inibir(false)

		// Este é o PRIMEIRO lugar do app que chama Stop de verdade — até
		// aqui ninguém chamava, porque fazê-lo contra o wl_display da
		// janela principal, em desmonte, derrubava o processo. Aqui é
		// outro caso: a janela que morre é secundária e o display segue
		// vivo com a principal. Vale conferir em uso.
		g.Stop()
		// Devolver é o último passo, e acontece SEMPRE — inclusive
		// quando a janela foi fechada pelo botão do compositor. Ver o
		// cabeçalho: a janela é um visor, não a dona da sessão.
		if j.aoDevolver != nil {
			j.aoDevolver(j.t)
		}
	}()

	umaAba := func() Tab { return j.t }
	for {
		e := j.w.Event()
		switch e := e.(type) {
		case app.DestroyEvent:
			// Não soltar a captura aqui, e sim no defer acima: neste
			// ponto o Gio já pode estar desmontando a conexão Wayland,
			// e mexer nela aqui derrubava o processo (mesma razão
			// documentada em cmd/vncview e no laço principal).
			return

		default:
			tratarEventoPlataforma(j.est, j.w, e, umaAba)

		case app.ConfigEvent:
			// O compositor também muda o modo por fora (atalho do
			// próprio desktop, botão da moldura). Sem ler daqui, o
			// ícone do botão passaria a mentir e um clique levaria a
			// janela para o estado em que ela já estava.
			j.telaCheia = e.Config.Mode == app.Fullscreen

		case app.FrameEvent:
			j.est.atualizarInibicao(j.t)
			gtx := app.NewContext(&ops, e)
			tratarTecladoFrame(j.w, gtx, umaAba)
			tratarClipboardFrame(j.est, gtx, umaAba)
			gtx.Metric = escalaFonte(gtx.Metric)

			j.tratarBotoes(gtx)
			j.desenhar(gtx)

			e.Frame(gtx.Ops)
			// No fim do quadro, como na janela principal: a marca é de
			// quem acabou de ser DESENHADO.
			marcarAbaAtiva(j.w, j.t)
		}
	}
}

func (j *janelaSessao) tratarBotoes(gtx layout.Context) {
	if j.btnTelaCheia.Clicked(gtx) {
		j.telaCheia = !j.telaCheia
		if j.telaCheia {
			j.w.Option(app.Fullscreen.Option())
		} else {
			// Sair da tela cheia NÃO devolve a aba: a janela continua
			// solta, separada do app. Devolver é o outro botão, de
			// propósito — são duas decisões diferentes e misturá-las
			// tiraria de quem está trabalhando a opção de ver a sessão
			// em janela normal ao lado do painel.
			j.w.Option(app.Windowed.Option())
		}
	}
	if j.btnReatar.Clicked(gtx) {
		// Fechar a janela basta: o defer do laço devolve a aba.
		j.w.Perform(system.ActionClose)
	}
}

func (j *janelaSessao) desenhar(gtx layout.Context) layout.Dimensions {
	// Fundo próprio: sem ele a área não pintada mostra a moldura do
	// compositor por baixo (no Windows, o frame do DWM — ver o cabeçalho
	// de buscapop.go).
	paintFundoJanela(gtx)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			a, ok := j.t.(abaSessao)
			if !ok {
				return layout.Dimensions{}
			}
			return layoutBarraSessao(gtx, j.th, a,
				func(gtx layout.Context) layout.Dimensions {
					return botaoIcone(gtx, &j.btnReatar, icons.ActionExitToApp,
						tema.Sec, tema.Texto)
				},
				func(gtx layout.Context) layout.Dimensions {
					ic := icons.NavigationFullscreen
					if j.telaCheia {
						ic = icons.NavigationFullscreenExit
					}
					return botaoIcone(gtx, &j.btnTelaCheia, ic, tema.Sec, tema.Texto)
				},
			)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			size := gtx.Constraints.Max
			return rotearPonteiroEDesenhar(gtx, j.t, j.tagConteudo, size)
		}),
	)
}

// paintFundoJanela pinta o fundo liso da janela de sessão. Liso, e não o
// gradiente da janela principal, porque aqui ele nunca aparece: a sessão
// ocupa tudo abaixo da barrinha, e o gradiente só custaria quadro.
func paintFundoJanela(gtx layout.Context) {
	superficie(gtx, image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Max.Y),
		tema.Fundo, transparente, 0)
}
