package main

import (
	"sync"

	"acessos-go/internal/grab"
	"acessos-go/internal/rdp"
)

// clipboardSync guarda o último texto conhecido só para não ecoar de
// volta — mesma lógica do vncview/clipboard.go.
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

// fromRemote é chamado (via sess.OnClipboardText) quando o servidor RDP
// avisa que o clipboard REMOTO mudou — replica no clipboard do sistema.
func (c *clipboardSync) fromRemote(text string, gh *grab.Handle) {
	if !c.checkAndSet(text) {
		return
	}
	gh.SetClipboardText(text)
}

// fromLocal é chamado (via grab.Start) quando o clipboard do SISTEMA
// muda — manda pro servidor RDP se for realmente uma mudança nova.
func (c *clipboardSync) fromLocal(text string, sess *rdp.Session) {
	if !c.checkAndSet(text) {
		return
	}
	if sess != nil {
		sess.SendClipboardText(text)
	}
}
