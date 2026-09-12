package main

import (
	"sync"
	"sync/atomic"

	"gioui.org/app"
)

// clipboardSync guarda o último texto conhecido de UMA aba (só para não
// ecoar de volta): aplicar o clipboard remoto no clipboard do sistema
// dispara uma nova notificação de "clipboard local mudou" (o wl_data_device
// avisa quem chamou set_selection também), que sem isto mandaríamos de
// volta pro remoto, ida e volta pra sempre. Cada aba com clipboard próprio
// (VNC, RDP) tem a sua — não é compartilhada entre abas.
type clipboardSync struct {
	mu   sync.Mutex
	last string
}

// checkAndSet devolve true se text for diferente do último conhecido (e já
// atualiza), false se for eco do que acabamos de aplicar nós mesmos.
func (c *clipboardSync) checkAndSet(text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if text == c.last {
		return false
	}
	c.last = text
	return true
}

// ---- publicação no clipboard do sistema ----
//
// O clipboard do Wayland (internal/grab) é do laço de quadro, e só dele:
// os objetos wl_data_source vivem numa estrutura que as callbacks do
// próprio Wayland também mexem, despachadas pelo Gio na thread da janela.
//
// Sem isto o app MORRIA ao abrir muitas telas de uma vez (relatado com ~30
// VNCs): cada sessão manda o clipboard dela assim que conecta, cada uma na
// sua goroutine, e três dessas chamadas ao mesmo tempo destruíam o
// wl_data_source que a outra estava usando — SIGSEGV dentro do cgo, com o
// app inteiro junto.
//
// Então qualquer lugar que queira publicar apenas ENFILEIRA aqui; quem
// entrega é o laço de quadro. Só o último texto interessa: publicar dois
// clipboards no mesmo quadro deixaria valer o último de qualquer forma.
var clipPendente atomic.Pointer[string]

// publicarClipboard pode ser chamada de QUALQUER goroutine.
func publicarClipboard(w *app.Window, texto string) {
	t := texto
	clipPendente.Store(&t)
	if w != nil {
		w.Invalidate()
	}
}

// entregarClipboard roda no laço de quadro, e só ali.
func entregarClipboard() {
	if p := clipPendente.Swap(nil); p != nil {
		currentGrab.Load().SetClipboardText(*p)
	}
}

// ---- aba ativa ----
//
// Marcada a cada quadro pelo laço principal. Serve para as sessões em
// segundo plano não disputarem o clipboard do sistema: VNC e RDP mandam o
// clipboard do servidor assim que o canal abre, e sem este filtro a
// última aba a conectar roubava o que o operador tinha acabado de copiar.
var abaAtivaRef atomic.Pointer[Tab]

func marcarAbaAtiva(t Tab) { abaAtivaRef.Store(&t) }

func ehAbaAtiva(t Tab) bool {
	p := abaAtivaRef.Load()
	return p != nil && *p == t
}
