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
	"fmt"
	"image"
	"os"
	"runtime/debug"

	"gio.tools/icons"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// abaDestacavel é a aba que sabe trocar de janela. Só ela pode ser
// destacada — sem isto a aba continuaria pedindo quadro para a janela
// antiga, e a destacada nunca repintaria.
type abaDestacavel interface {
	Tab
	// TrocarJanela leva janela E tema juntos: o text.Shaper de um Theme é
	// cache sem trava, e a aba desenhada na goroutine desta janela com o
	// Theme da outra dá panic de mapa — que derruba o processo inteiro.
	// Ver tema.go (temaBusca) e o campo tha em rdptab.go.
	TrocarJanela(w *app.Window, th *material.Theme)
}

type janelaSessao struct {
	w   *app.Window
	est *estadoJanela
	t   abaDestacavel
	th  *material.Theme

	telaCheia bool

	btnTelaCheia widget.Clickable
	btnReatar    widget.Clickable
	btnMin       widget.Clickable
	btnMax       widget.Clickable
	btnFechar    widget.Clickable

	// decoraSistema e maximizada vêm do ConfigEvent DESTA janela. Os
	// globais equivalentes da janela principal não servem: são dela.
	//
	// decoraSistema existe porque o compositor pode recusar a decoração
	// do cliente e responder SERVER_SIDE (o Gio nunca manda set_mode
	// sozinho — é patch do fork). Quando recusa, a barra do KWin aparece
	// por cima e os nossos botões de janela virariam uma segunda fileira.
	decoraSistema bool
	maximizada    bool

	// tagConteudo é o alvo de ponteiro DESTA janela. Um por janela: o da
	// principal é outro, e compartilhar faria o Gio entregar o evento de
	// uma para a outra.
	tagConteudo *int

	// acaoPendente é a ação de JANELA pedida durante o quadro, executada
	// no topo da volta seguinte do laço — fora de qualquer quadro.
	//
	// Vale aqui a mesma regra de acaojanela.go, e pelo mesmo motivo: no
	// Windows, w.Option() de dentro do layout entra em Configure() →
	// ShowWindow, que despacha WM_WINDOWPOSCHANGED reentrantemente e cai
	// num FlushEvents que vê `delivering == true` e não entrega nada. A
	// janela fica parada na última imagem, com o botão preso.
	//
	// A fila de lá NÃO serve: ela é global e drenada pelo laço da janela
	// PRINCIPAL, e no Wayland/X11 o Window.Run executa f() na goroutine de
	// quem chama — a ação sairia na goroutine errada, mexendo nesta janela
	// em paralelo com o desenho dela. É exatamente a corrida que aquele
	// arquivo conta ter sido introduzida uma vez e removida.
	//
	// Campo simples, sem trava: quem escreve é o quadro e quem lê é o topo
	// do laço, a mesma goroutine.
	// morta marca que esta janela está em fechamento depois de um pânico:
	// não desenha mais nada, só fecha o quadro e deixa o laço terminar.
	morta bool

	acaoPendente func()

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
func abrirJanelaSessao(t abaDestacavel, cheia bool,
	aoDevolver func(abaDestacavel)) {

	j := &janelaSessao{
		t: t, telaCheia: cheia,
		tagConteudo: new(int),
		aoDevolver:  aoDevolver,
	}

	j.w = new(app.Window)
	j.w.Option(
		app.Title("Acessos — "+t.Title()),
		// Decoração PRÓPRIA, como a janela principal: a barrinha fina JÁ É
		// a barra de título desta janela — diz o destino, o estado e a
		// geometria. Deixar a do compositor por cima gastaria uma segunda
		// faixa de altura repetindo o nome, que é exatamente o que o app
		// evita na janela grande.
		//
		// Não é uma segunda cópia do CSD: os botões de janela saem dos
		// mesmos helpers da barra de topo (botaoIcone/botaoFechar), e o
		// arrasto é o mesmo ActionMove.
		app.Decorated(false),
		app.Size(unit.Dp(1024), unit.Dp(768)),
	)
	if cheia {
		j.w.Option(app.Fullscreen.Option())
	}

	j.est = novoEstadoJanelaSessao(j.w)
	marcarDestacada(t)
	go j.laco()
}

func (j *janelaSessao) laco() {
	// Theme PRÓPRIO desta janela, montado aqui dentro.
	//
	// Aqui e não em abrirJanelaSessao por dois motivos: shaperDoApp()
	// reparseia as seis fontes embutidas (fontes.go) e travaria o quadro
	// da janela principal, que é quem chama aquela função; e porque o
	// Theme tem de nascer na goroutine que vai usá-lo.
	j.th = material.NewTheme()
	j.th.Shaper = shaperDoApp()

	// Só agora a aba passa a desenhar aqui — com a janela E o tema desta
	// goroutine. Antes disso ela ainda aponta para a janela principal, o
	// que é o certo: um pedido de redesenho que chegue no meio cai lá,
	// onde ainda há quem o atenda.
	j.t.TrocarJanela(j.w, j.th)

	var ops op.Ops
	defer func() {
		esquecerJanela(j.w)
		desmarcarDestacada(j.t)

		// SÓ Liberar, nunca Stop: cada janela do Gio tem a PRÓPRIA
		// conexão Wayland (newWLWindow → newWLDisplay, em
		// third_party/gio/app/os_wayland.go), e o close() dela enfileira
		// o DestroyEvent e EM SEGUIDA chama wl_display_disconnect —
		// antes de este laço ler o evento. Destruir wl_proxy a partir
		// daqui é use-after-free num display liberado, e derruba o
		// processo inteiro, com todas as outras sessões junto.
		//
		// A inibição de atalhos não fica presa: o compositor solta o que
		// era do cliente quando a conexão cai. Quem fecha por decisão
		// PRÓPRIA (o botão de devolver) solta antes, com o display
		// ainda vivo — ver tratarBotoes.
		j.est.grab.Load().Liberar()

		// Devolver acontece SEMPRE: fechamento normal, X do compositor
		// ou pânico contido. A janela é um visor, não a dona da sessão,
		// e a sessão continua viva no processo-filho dela.
		if j.aoDevolver != nil {
			j.aoDevolver(j.t)
		}
	}()

	umaAba := func() Tab { return j.t }
	for {
		// Fora de qualquer quadro: o FrameEvent anterior já retornou por
		// completo e o próximo ainda não começou.
		if f := j.acaoPendente; f != nil {
			j.acaoPendente = nil
			f()
		}
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
			j.decoraSistema = e.Config.Decorated
			j.maximizada = e.Config.Mode == app.Maximized
			// O compositor também muda o modo por fora (atalho do
			// próprio desktop, botão da moldura). Sem ler daqui, o
			// ícone do botão passaria a mentir e um clique levaria a
			// janela para o estado em que ela já estava.
			j.telaCheia = e.Config.Mode == app.Fullscreen

		case app.FrameEvent:
			if !j.quadro(e, &ops) {
				// Pânico contido: para de desenhar e pede o fechamento.
				// O laço CONTINUA rodando — é ele que despacha o close
				// (no Wayland, Perform só marca closing; quem fecha de
				// fato é o dispatch, alcançado por j.w.Event() abaixo).
				// Sair daqui na hora deixaria a janela na tela, pintada e
				// congelada, com a aba já devolvida à outra — a mesma
				// sessão em dois lugares, um deles morto.
				j.morta = true
				j.w.Perform(system.ActionClose)
			}
		}
	}
}

// quadro desenha um quadro e devolve falso se ele entrou em pânico.
//
// PÂNICO NESTA JANELA NÃO PODE LEVAR O APP JUNTO. É a mesma garantia que a
// sessão já tem no nível do processo (internal/telaproc: segfault na
// libfreerdp mata só o filho), trazida para o nível da goroutine — a
// janela destacada é código novo desenhando numa goroutine própria.
//
// O QUE ISTO NÃO PEGA: erro FATAL do runtime não é pânico e não se
// recupera. "concurrent map read and map write" é o exemplo, e foi o que
// derrubou o app na primeira versão desta janela, por compartilhar o
// text.Shaper do Theme. Contra esse não há rede — só não cometer. Ver o
// campo tha em rdptab.go.
func (j *janelaSessao) quadro(e app.FrameEvent, ops *op.Ops) (ok bool) {
	if j.morta {
		// Já em fechamento: não desenha mais, mas o quadro precisa ser
		// fechado, senão o Gio fica esperando por ele.
		gtx := app.NewContext(ops, e)
		e.Frame(gtx.Ops)
		return true
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr,
				"janela de sessão: pânico contido, fechando e devolvendo a aba: %v\n%s\n",
				r, debug.Stack())
			ok = false
		}
	}()

	j.est.atualizarInibicao(j.t)
	gtx := app.NewContext(ops, e)
	tratarTecladoFrame(j.w, gtx, umaAbaDe(j))
	tratarClipboardFrame(j.est, gtx, umaAbaDe(j))
	gtx.Metric = escalaFonte(gtx.Metric)

	j.tratarBotoes(gtx)
	j.desenhar(gtx)

	e.Frame(gtx.Ops)
	// No fim do quadro, como na janela principal: a marca é de quem
	// acabou de ser DESENHADO.
	marcarAbaAtiva(j.w, j.t)
	return true
}

func umaAbaDe(j *janelaSessao) func() Tab { return func() Tab { return j.t } }

func (j *janelaSessao) tratarBotoes(gtx layout.Context) {
	if j.btnTelaCheia.Clicked(gtx) {
		// Sair da tela cheia NÃO devolve a aba: a janela continua solta,
		// separada do app. Devolver é o outro botão, de propósito — são
		// duas decisões diferentes, e misturá-las tiraria de quem está
		// trabalhando a opção de ver a sessão em janela normal ao lado do
		// painel.
		quer := !j.telaCheia
		j.telaCheia = quer
		// AGENDADA, não executada: ver acaoPendente.
		j.acaoPendente = func() {
			if quer {
				j.w.Option(app.Fullscreen.Option())
				return
			}
			j.w.Option(app.Windowed.Option())
		}
	}
	// Botões de janela. Minimizar e maximizar passam pela ação agendada
	// pelo mesmo motivo do foraDoQuadro da janela principal (ver
	// acaojanela.go); fechar pode ir direto.
	if j.btnMin.Clicked(gtx) {
		j.acaoPendente = func() { j.w.Perform(system.ActionMinimize) }
	}
	if j.btnMax.Clicked(gtx) {
		// Lê o estado que veio do ConfigEvent, não um toggle próprio:
		// encostar a janela na borda aciona o snap do compositor sem
		// passar por este botão, e um bool nosso ficaria dessincronizado.
		if j.maximizada {
			j.acaoPendente = func() { j.w.Perform(system.ActionUnmaximize) }
		} else {
			j.acaoPendente = func() { j.w.Perform(system.ActionMaximize) }
		}
	}
	if j.btnFechar.Clicked(gtx) {
		// Fechar devolve a aba, como o botão de reacoplar — ver o defer do
		// laço. Direto, sem agendar: ActionClose é a exceção documentada
		// em acaojanela.go.
		j.w.Perform(system.ActionClose)
	}
	if j.btnReatar.Clicked(gtx) {
		// Aqui a janela fecha por DECISÃO NOSSA, e é a única
		// oportunidade de soltar o grab direito: o display ainda está
		// vivo. Depois do fechamento ele já foi desconectado, e aí só
		// resta Liberar (ver o defer do laço).
		//
		// Agendado para fora do quadro pela regra de acaojanela.go.
		j.acaoPendente = func() {
			g := j.est.grab.Load()
			g.Inibir(false)
			g.Stop()
			j.est.grab.Store(nil)
			j.w.Perform(system.ActionClose)
		}
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

			// Arrastar a janela pela barrinha: aqui ela É a barra de
			// título (decoração própria), e é daqui que o compositor
			// recebe o pedido de mover.
			//
			// ANTES da barra, e não dentro de layoutBarraSessao: aquela
			// função é compartilhada com a janela principal, onde a faixa
			// fica abaixo da barra de título de verdade e virar área de
			// arrasto seria mudança de comportamento que ninguém pediu.
			// O que é desenhado DEPOIS recebe o clique primeiro, então os
			// botões da barra continuam clicáveis.
			h := gtx.Dp(barraSessaoAltura)
			arrasto := clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, h)}.Push(gtx.Ops)
			system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
			arrasto.Pop()
			extras := []layout.Widget{
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
			}
			// Botões de janela só quando a decoração é NOSSA e fora da
			// tela cheia: com a do sistema por cima eles seriam uma
			// segunda fileira, e em tela cheia não há janela para
			// minimizar ou maximizar.
			if !j.decoraSistema && !j.telaCheia {
				extras = append(extras,
					func(gtx layout.Context) layout.Dimensions {
						return botaoIcone(gtx, &j.btnMin, icons.ContentRemove,
							tema.Sec, tema.Texto)
					},
					func(gtx layout.Context) layout.Dimensions {
						ic := icons.ActionOpenInNew
						if j.maximizada {
							ic = icons.ActionFlipToFront
						}
						return botaoIcone(gtx, &j.btnMax, ic, tema.Sec, tema.Texto)
					},
					func(gtx layout.Context) layout.Dimensions {
						return botaoFechar(gtx, &j.btnFechar)
					})
			}
			return layoutBarraSessao(gtx, j.th, a, extras...)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			size := gtx.Constraints.Max
			d := rotearPonteiroEDesenhar(gtx, j.t, j.tagConteudo, size)

			// Rastreio do ponteiro e MENU, na mesma ordem da janela
			// principal. Sem eles, o botão de teclas especiais da
			// barrinha abria um menu que esta janela não desenhava: ele
			// aparecia na outra, na posição que o ponteiro tinha lá.
			// Justamente o botão que mais importa em tela cheia.
			//
			// O menu só sai onde foi aberto — ver o dono, em menu.go.
			rastrearPonteiroGlobal(gtx)
			layoutMenu(gtx, j.th, j.w)
			return d
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
