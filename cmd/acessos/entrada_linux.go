//go:build linux

package main

import (
	"fmt"

	"acessos-go/internal/grab"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/layout"
)

// tratarEventoPlataforma cuida do que só existe no Linux/Wayland: o grab
// de teclado, clipboard e inibição de atalhos do compositor, armado a
// partir dos ponteiros crus que o Gio expõe em app.WaylandViewEvent.
func tratarEventoPlataforma(est *estadoJanela, w *app.Window, e event.Event, activeTab func() Tab) {
	ev, ok := e.(app.WaylandViewEvent)
	if !ok {
		return
	}

	// SÓ cria o grab uma vez, na primeira view válida — nunca
	// recria nem para (Stop) num evento posterior. Dois motivos:
	//   1. o Gio manda um WaylandViewEvent{} (zero-value) durante
	//      o fechamento da janela, logo antes do DestroyEvent, com
	//      o display já a caminho de ser desmontado;
	//   2. mesmo uma SEGUNDA view VÁLIDA pode aparecer em operação
	//      normal (visto na prática com duas abas, cada uma
	//      chamando app.Size() ao conectar) — parar/recriar o
	//      grab nesse ponto corre contra a leitura Wayland que o
	//      próprio Gio faz internamente e derrubava o processo.
	// wl_seat/wl_display não mudam quando só a superfície é
	// recriada, então o grab original continua bom pra teclado e
	// clipboard; só o inibidor de atalhos, que é amarrado à
	// superfície original, poderia ficar desatualizado — trade-off
	// aceitável perto de derrubar o processo inteiro.
	if ev.Valid() && est.grab.Load() == nil {
		// Sobe antes do grab: a partir do grab.Start abaixo já podem
		// chegar teclas, e elas precisam de alguém drenando a fila.
		iniciarFilaTeclas()
		gh := grab.Start(ev.Display, ev.Surface,
			func(keysym, keycodeX11 uint32, pressed bool) {
				// F12 recolhe a lateral — atalho do PRÓPRIO app,
				// que não depende de Ctrl e não colide com nada
				// usado dentro de uma sessão remota (tmux, vim,
				// readline etc. não usam F12), diferente do
				// antigo Ctrl+B. Mas F12 SOZINHO: ver logo abaixo.
				const ctrlL, ctrlR = 0xffe3, 0xffe4
				const f12 = 0xffc9
				if keysym == ctrlL || keysym == ctrlR {
					ctrlDown.Store(pressed)
				}
				// F12 LIMPO, e não "qualquer coisa + F12": o atalho
				// global do sistema é Ctrl+Shift+F12, e sem esta
				// checagem ele recolhia a lateral em vez de abrir a
				// busca sempre que a janela do app estava à frente.
				//
				// A máscara vem do compositor (grab.Modificadores) e
				// não de contar press/release aqui: o KWin CONSOME a
				// combinação que virou atalho global dele, então o
				// release do Ctrl e do Shift não chega — contando,
				// eles ficariam presos e o F12 sozinho nunca mais
				// funcionaria.
				if keysym == f12 && pressed && est.grab.Load().Modificadores() == 0 {
					pendingToggleSidebar.Store(true)
					w.Invalidate()
					return
				}
				// Com diálogo aberto, ou com o cursor num campo de
				// texto do próprio app (a busca da lateral, por
				// exemplo), o teclado é DELE: sem isto, o que se
				// digita ia para a sessão remota que estiver atrás
				// e o campo ficava vazio.
				if temDialogo() || focoEmCampo.Load() {
					return
				}
				// Só a aba que É uma sessão remota consome o
				// teclado cru. No painel (ou numa aba de
				// arquivos) as teclas têm que seguir o caminho
				// normal do Gio, senão a busca não digita.
				t := activeTab()
				remoto := t != nil && querTeclado(t)
				// Ctrl+W/Ctrl+G só são atalho do APP fora de uma
				// sessão remota: dentro de uma, são do programa que
				// está rodando lá — Ctrl+W é "apagar palavra" no
				// readline do bash E "Where Is" no nano, Ctrl+G é
				// "abortar" no readline e "Get Help" no nano. Um
				// terminal de verdade nunca rouba essas duas pra
				// si; a aba continua fechando pelo X dela.
				if !remoto && ctrlDown.Load() && pressed {
					switch keysym {
					case 'w', 'W':
						pendingCloseActive.Store(true)
						w.Invalidate()
						return
					case 'g', 'G':
						reguaLigada = !reguaLigada
						w.Invalidate()
						return
					}
				}
				if !remoto {
					return
				}
				// O Wayland entrega esta MESMA tecla também ao
				// wl_keyboard interno do Gio (não existe
				// exclusividade entre dois listeners do mesmo
				// wl_seat) — se algum botão ficou com o foco de
				// teclado do Gio de antes de abrir esta aba (o "+"
				// de nova conexão, o "x" de fechar, etc.), um
				// Enter/Espaço digitado aqui dentro (confirmar
				// `nano arquivo`, salvar e sair) o ativa de novo.
				// Limpar o foco a cada tecla tira esse fluxo
				// paralelo de cima de qualquer widget.
				pendingLimparFocoGio.Store(true)
				// ATENÇÃO: NÃO chame t.HandleKey direto daqui. Este
				// callback roda de dentro do dispatch do Wayland, que
				// neste backend acontece na PRÓPRIA goroutine do laço
				// de eventos (app.Window.Event() → driver.Event() →
				// dispatch, em os_wayland.go:1582) — o grab compartilha
				// a fila default de propósito. HandleKey escreve no
				// socket do processo-filho com bloqueio de até 5s, ou
				// seja, daqui ele congela a interface inteira. O porquê
				// completo está em filateclas_linux.go.
				enfileirarTecla(t, keysym, keycodeX11, pressed)
			},
			func(text string) {
				// SEMPRE grava, não só na aba ativa: é o que faz colar
				// (Ctrl+Shift+V) no terminal SSH funcionar em qualquer aba,
				// não só na que por acaso estava em foco quando o Wayland
				// avisou que o clipboard mudou (ver clipboard.go).
				registrarClipboardSistema(text)
				// abaAtivaDe(w), e NÃO activeTab(): este callback roda numa
				// goroutine solta (ver grab.go), e activeTab() lê o índice
				// e a fatia de abas da barra sem trava, enquanto o laço de
				// quadro reescreve os dois ao abrir e fechar aba. É corrida
				// de dados limpa — das que só aparecem com -race — e o
				// desfecho provável é entregar o texto para a aba errada ou
				// já fechada. O ponteiro atômico existe exatamente para
				// isto e é atualizado a cada quadro (ver clipboard.go).
				//
				// O irmão entrada_outros.go NÃO precisa disto, e é de
				// propósito: lá o clipboard é lido de dentro do quadro
				// (tratarClipboardFrame recebe o gtx), ou seja na própria
				// goroutine que mexe na barra — ler activeTab() ali é
				// seguro. Conferir que continua assim antes de propagar.
				if t := abaAtivaDe(w); t != nil {
					if h, ok := t.(clipboardReceiver); ok {
						h.OnLocalClipboard(text)
					}
				}
			},
		)
		est.grab.Store(gh)
		if gh != nil {
			fmt.Println("teclado + clipboard via wayland direto + captura de atalhos do compositor ativa")
		} else {
			fmt.Println("wl_seat indisponível — sem teclado, clipboard nem captura de atalhos")
		}
	}

}

// tratarTecladoFrame não faz nada no Linux: o teclado já chega inteiro pelo
// grab acima, direto do Wayland — ver tratarEventoPlataforma.
func tratarTecladoFrame(w *app.Window, gtx layout.Context, activeTab func() Tab) {}

// tratarClipboardFrame só entrega o que já estava enfileirado — ver
// clipboard.go. Fora do Linux (entrada_outros.go) é diferente: lá não há
// grab nenhum publicando, então a entrega usa clipboard.WriteCmd do
// próprio Gio.
func tratarClipboardFrame(est *estadoJanela, gtx layout.Context, activeTab func() Tab) {
	entregarClipboard(est)
}
