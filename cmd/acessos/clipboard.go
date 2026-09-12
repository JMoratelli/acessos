package main

import "sync"

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
