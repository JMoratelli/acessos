//go:build linux

package main

import (
	"fmt"

	"acessos-go/internal/grab"

	"gioui.org/app"
	"gioui.org/io/event"
)

// tratarEventoPlataforma cuida do que só existe no Linux/Wayland: o grab
// de teclado, clipboard e inibição de atalhos do compositor, armado a
// partir dos ponteiros crus que o Gio expõe em app.WaylandViewEvent.
func tratarEventoPlataforma(w *app.Window, e event.Event, activeTab func() Tab) {
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
	if ev.Valid() && currentGrab.Load() == nil {
		gh := grab.Start(ev.Display, ev.Surface,
			func(keysym, keycodeX11 uint32, pressed bool) {
				// Ctrl+B recolhe a lateral, Ctrl+W fecha a aba
				// ativa — atalhos do PRÓPRIO app, do jeito que
				// um navegador reserva Ctrl+W independente da
				// página. Ainda repassamos Ctrl em si pro remoto
				// logo abaixo (Ctrl+C etc. têm que continuar
				// funcionando); só a tecla B/W some quando
				// combinada com Ctrl.
				const ctrlL, ctrlR = 0xffe3, 0xffe4
				if keysym == ctrlL || keysym == ctrlR {
					ctrlDown.Store(pressed)
				}
				if ctrlDown.Load() && pressed {
					switch keysym {
					case 'b', 'B':
						pendingToggleSidebar.Store(true)
						w.Invalidate()
						return
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
				if t := activeTab(); t != nil && querTeclado(t) {
					t.HandleKey(keysym, keycodeX11, pressed)
				}
			},
			func(text string) {
				if h, ok := activeTab().(clipboardReceiver); ok {
					h.OnLocalClipboard(text)
				}
			},
		)
		currentGrab.Store(gh)
		if gh != nil {
			fmt.Println("teclado + clipboard via wayland direto + captura de atalhos do compositor ativa")
		} else {
			fmt.Println("wl_seat indisponível — sem teclado, clipboard nem captura de atalhos")
		}
	}

}
