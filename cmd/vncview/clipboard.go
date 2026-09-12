package main

import (
	"sync"

	"acessos-go/internal/grab"
	"acessos-go/internal/vnc"
)

// clipboardSync guarda o último texto conhecido (de qualquer direção) só
// para não ecoar de volta: sem isto, aplicar o clipboard remoto no sistema
// local disparava uma nova notificação de "clipboard local mudou" (o
// próprio wl_data_device avisa quem chamou set_selection também), que
// mandaríamos de volta pro remoto, ida e volta pra sempre.
type clipboardSync struct {
	mu   sync.Mutex
	last string
}

func (c *clipboardSync) checkAndSet(text string) (changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if text == c.last {
		return false
	}
	c.last = text
	return true
}

var clipSync clipboardSync

// fromRemote é chamado (via sess.OnCutText) quando o servidor VNC avisa
// que o clipboard REMOTO mudou — replica no clipboard do sistema local.
func (c *clipboardSync) fromRemote(text string, gh *grab.Handle) {
	if !c.checkAndSet(text) {
		return
	}
	gh.SetClipboardText(text)
}

// fromLocal é chamado (via grab.Start) quando o clipboard do SISTEMA
// muda — manda pro servidor VNC se for realmente uma mudança nova.
func (c *clipboardSync) fromLocal(text string, sess *vnc.Session) {
	if !c.checkAndSet(text) {
		return
	}
	if sess != nil {
		sess.SendCutText(text)
	}
}
