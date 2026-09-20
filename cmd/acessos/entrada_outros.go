//go:build !linux

package main

import (
	"io"
	"strings"

	"gioui.org/app"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/transfer"
	"gioui.org/layout"
)

// Fora do Linux não há grab por Wayland. No Windows o teclado e a área de
// transferência vêm pelo próprio Gio (ver os_windows.go): key.Event já traz
// Modifiers confiável (diferente do Wayland) e clipboard.ReadCmd/WriteCmd já
// resolvem sozinhos contra a API do Windows. tratarTecladoFrame e
// tratarClipboardFrame são a ponte que faltava — ver BACKLOG.md, itens 1 e 2.
func tratarEventoPlataforma(est *estadoJanela, w *app.Window, e event.Event, activeTab func() Tab) {
}

// tagTecladoOutros é o alvo do foco de teclado que reivindicamos quando a
// aba ativa é uma sessão remota — ver tratarTecladoFrame.
var tagTecladoOutros = new(int)

// tratarTecladoFrame roda uma vez por quadro (chamada do main.go, com o gtx
// já criado) e faz o que o grab faz no Linux: decide se a aba ativa quer o
// teclado cru, reivindica o foco quando quer, e traduz cada key.Event para
// (keysym X11, keycode X11) antes de repassar pra Tab.HandleKey — os MESMOS
// valores que grab.Start entregaria, então VNC/RDP/SSH não precisam saber
// que a entrada veio de outro lugar.
//
// O foco só é reivindicado quando a aba ativa É uma sessão remota
// (querTeclado), não a qualquer momento: differente do Linux, aqui
// pedimos o foco de teclado do PRÓPRIO Gio (key.FocusCmd), e outros
// widgets (campo de busca, botões navegáveis por Tab) também disputam
// esse foco — tomá-lo fora de uma sessão tiraria o teclado deles sem
// necessidade. Atalhos do app (F12, Ctrl+W, Ctrl+G) por isso só
// funcionam dentro de uma sessão aberta por este caminho, não a partir
// do Painel — gap conhecido, aceitável perto do que falta sem isto (o
// item 1 do BACKLOG.md: nenhuma tecla chegava a lugar nenhum).
func tratarTecladoFrame(w *app.Window, gtx layout.Context, activeTab func() Tab) {
	// event.Op é o que faz o roteador do Gio criar (e manter "visível")
	// a entrada de tagTecladoOutros em handlers — sem isto, o foco pedido
	// logo abaixo (key.FocusCmd) é de um alvo que o roteador não conhece,
	// e keyQueue.Frame() zera de volta ao fim do quadro.
	event.Op(gtx.Ops, tagTecladoOutros)

	t := activeTab()
	podeTeclado := t != nil && querTeclado(t) && !temDialogo() && !focoEmCampo.Load()
	if podeTeclado {
		gtx.Execute(key.FocusCmd{Tag: tagTecladoOutros})
	}

	for {
		// key.FocusFilter PRECISA ser registrado toda vez: é o que marca
		// tagTecladoOutros como "focusable" pro Gio — sem ele, o próprio
		// keyQueue.Frame() (rodado ao fim deste quadro) acha que o foco
		// que acabamos de pedir com FocusCmd é de um alvo inexistente e
		// zera de volta, e nenhuma tecla chega no quadro seguinte.
		// Optional precisa listar TODOS os modificadores: um key.Filter
		// sem Required/Optional só combina com teclas SEM NENHUM
		// modificador (ver keyFilterMatch em io/input/key.go) — Ctrl+V,
		// Ctrl+C etc. vinham com o bit de Ctrl em Modifiers e o filtro
		// rejeitava o evento inteiro antes de chegar aqui.
		const qualquerModificador = key.ModCtrl | key.ModShift | key.ModAlt | key.ModSuper | key.ModCommand
		e, ok := gtx.Event(
			key.FocusFilter{Target: tagTecladoOutros},
			key.Filter{Focus: tagTecladoOutros, Name: "", Optional: qualquerModificador},
		)
		if !ok {
			break
		}
		ke, ok := e.(key.Event)
		if !ok || !podeTeclado {
			continue
		}
		pressed := ke.State == key.Press

		// Mesmo atalho do grab Linux (ver entrada_linux.go), pelo mesmo
		// motivo: F12 não depende de Ctrl e não colide com nada usado
		// dentro de uma sessão remota.
		if ke.Name == key.NameCtrl {
			ctrlDown.Store(pressed)
		}
		// F12 LIMPO. No Windows o Modifiers do Gio é confiável (foi o
		// que o item 1 do BACKLOG apurou), então dá para exigir aqui o
		// mesmo que o lado Linux exige pelos modificadores crus: sem
		// isso, o Ctrl+Shift+F12 do atalho global recolheria a lateral.
		if ke.Name == key.NameF12 && pressed && ke.Modifiers == 0 {
			pendingToggleSidebar.Store(true)
			w.Invalidate()
			continue
		}
		// Ctrl+W/Ctrl+G só são atalho do app aqui FORA de uma sessão
		// remota (podeTeclado falso) — dentro de uma são do programa
		// rodando lá: Ctrl+W é "apagar palavra" no readline do bash e
		// "Where Is" no nano, Ctrl+G é "abortar" no readline e "Get
		// Help" no nano. Mesma correção do lado Linux, ver o
		// comentário lá.
		if !podeTeclado && ctrlDown.Load() && pressed {
			switch ke.Name {
			case "W":
				pendingCloseActive.Store(true)
				w.Invalidate()
				continue
			case "G":
				reguaLigada = !reguaLigada
				w.Invalidate()
				continue
			}
		}

		keysym, keycodeX11, ok := traduzirTecla(ke.Name, ke.Modifiers.Contain(key.ModShift))
		if !ok {
			continue
		}
		t.HandleKey(keysym, keycodeX11, pressed)
	}
}

// tagClipboardOutros identifica, pro Gio, quem está pedindo o clipboard do
// sistema — ver clipboard.ReadCmd.
var tagClipboardOutros = new(int)

// clipboardLidoOutros é o último texto que NÓS lemos do clipboard do
// sistema (ou escrevemos nele) — mesmo papel do wl_data_source no Linux:
// sem isto, aplicar o clipboard remoto no clipboard do sistema faria a
// PRÓXIMA leitura achar que "o clipboard mudou" e mandar de volta pro
// remoto, ida e volta pra sempre.
var clipboardLidoOutros clipboardSync

// tratarClipboardFrame substitui, fora do Linux, tanto o lado "recebe do
// sistema" (grab.Start's onClipboardFn) quanto o lado "publica no sistema"
// (entregarClipboard, que no Windows não fazia nada — currentGrab é
// sempre nil). O motor dos dois lados (VNC/RDP) já manda e recebe texto
// pelos próprios canais; só faltava esta ponte (BACKLOG.md, item 2).
func tratarClipboardFrame(est *estadoJanela, gtx layout.Context, activeTab func() Tab) {
	gtx.Execute(clipboard.ReadCmd{Tag: tagClipboardOutros})
	for {
		e, ok := gtx.Event(transfer.TargetFilter{Target: tagClipboardOutros, Type: "application/text"})
		if !ok {
			break
		}
		de, ok := e.(transfer.DataEvent)
		if !ok {
			continue
		}
		r := de.Open()
		if r == nil {
			continue
		}
		b, _ := io.ReadAll(r)
		r.Close()
		texto := string(b)
		if !clipboardLidoOutros.checkAndSet(texto) {
			continue
		}
		registrarClipboardSistema(texto)
		if h, ok := activeTab().(clipboardReceiver); ok {
			h.OnLocalClipboard(texto)
		}
	}

	// Só o que é DESTA janela, e tirado com CompareAndSwap — mesma regra
	// do lado Linux (ver entregarClipboard, em clipboard.go): com a sessão
	// em janela própria há duas goroutines de quadro passando por aqui, e
	// quem não é a dona não pode consumir a fila da outra.
	if p := clipPendente.Load(); p != nil && p.w == est.w &&
		clipPendente.CompareAndSwap(p, nil) {
		// Pré-marca como já conhecido: sem isto, o próximo ReadCmd lia de
		// volta o que acabamos de escrever e ecoava pro remoto nesse
		// instante como se fosse uma cópia local nova.
		clipboardLidoOutros.checkAndSet(p.texto)
		gtx.Execute(clipboard.WriteCmd{
			Type: "application/text",
			Data: io.NopCloser(strings.NewReader(p.texto)),
		})
	}
}
