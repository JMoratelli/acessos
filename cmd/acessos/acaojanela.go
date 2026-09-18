package main

// Ações de janela (minimizar, maximizar, trazer pra frente) NÃO podem ser
// disparadas de dentro do quadro. Este arquivo existe só para isso.
//
// ----------------------------------------------------------------------
// POR QUE: o bug do "minimiza e volta congelado" (Windows)
// ----------------------------------------------------------------------
//
// w.Perform() e w.Option() passam por Window.Run(). Chamados de dentro do
// layout, isso acontece com um quadro em voo — o laço do cliente está no meio
// do FrameEvent.
//
// No Windows, Configure() chama ShowWindow/SetWindowPos, que são SÍNCRONOS: o
// sistema despacha WM_WINDOWPOSCHANGED reentrantemente, dentro da própria
// chamada. Isso cai em update() → ProcessEvent → FlushEvents(), que vê
// `delivering == true` (third_party/gio/app/os.go:304) e retorna sem entregar.
// O redesenho da janela restaurada fica para depois, e a tela pode ficar
// parada na última imagem desenhada, com o botão da titlebar preso.
//
// ----------------------------------------------------------------------
// COMO ESTE ARQUIVO RESOLVE — E POR QUE NÃO COM GOROUTINE
// ----------------------------------------------------------------------
//
// A ação é enfileirada e executada PELO PRÓPRIO LAÇO DO CLIENTE, no topo da
// iteração seguinte: ali o FrameEvent anterior já retornou por completo e o
// w.Event() seguinte ainda não foi chamado. É o ponto mais tarde possível
// dentro da iteração, e continua sendo a mesma goroutine de sempre.
//
// ATENÇÃO — ISTO JÁ FOI FEITO ERRADO UMA VEZ, NÃO REFAÇA: uma versão anterior
// executava a ação numa GOROUTINE PRÓPRIA, liberada por um "pulso" emitido
// depois de e.Frame. Aquilo introduzia uma corrida de dados no Linux, que é
// justamente a plataforma onde o bug do Windows não existe:
//
//	third_party/gio/app/os_wayland.go:1602  func (w *window) Run(f func()) { f() }
//	third_party/gio/app/os_x11.go:402       func (w *x11Window) Run(f func()) { f() }
//	third_party/gio/app/os_windows.go:755   func (w *window) Run(f func()) { w.loop.Run(f) }
//
// Ou seja: no Wayland/X11 o Window.Run NÃO empurra nada para thread nenhuma —
// executa f() na goroutine de quem chamou. Chamando w.Perform de uma goroutine
// separada, driver.Configure/driver.Perform passavam a mexer no estado da
// janela EM PARALELO com o laço que estava desenhando. Trocava um bug de
// Windows por uma corrida no Linux.
//
// Rodando no laço, a ação fica serializada com o desenho por construção, em
// todas as plataformas — sem goroutine, sem pulso e sem timeout de segurança.
//
// NÃO volte a chamar w.Perform()/w.Option() de dentro de um layout, de um
// Clicked(gtx) ou do drenarFilaJanela(). Use foraDoQuadro().
//
// EXCEÇÃO: system.ActionClose é seguro direto — no Windows ele vira
// PostMessage (third_party/gio/app/os_windows.go:1048), assíncrono por
// natureza, sem reentrância nenhuma.

// acoesJanela são as ações pendentes. Buffer pequeno de propósito: ninguém
// clica em "minimizar" oito vezes antes do quadro fechar, e uma fila grande só
// serviria pra acumular cliques repetidos de uma janela travada.
var acoesJanela = make(chan func(), 8)

// foraDoQuadro agenda f para rodar no topo da próxima iteração do laço de
// eventos, fora de qualquer quadro. Não bloqueia quem chama.
//
// Se a fila estiver cheia o pedido é DESCARTADO em vez de bloquear: fila cheia
// significa janela que não fecha quadro, e nesse estado pendurar quem pede
// (que pode ser o próprio laço, via drenarFilaJanela) fecharia o impasse por
// cima de nós.
func foraDoQuadro(f func()) {
	select {
	case acoesJanela <- f:
	default:
	}
}

// drenarAcoesJanela executa as ações pendentes. Chamada NO LAÇO DE EVENTOS
// (main.go), no topo da iteração, antes de w.Event() — nunca de dentro de um
// FrameEvent, que é o ponto que este arquivo inteiro existe para evitar.
func drenarAcoesJanela() {
	for {
		select {
		case f := <-acoesJanela:
			f()
		default:
			return
		}
	}
}
