//go:build !linux

// Fora do Linux não há Wayland: o pacote existe só para o resto do
// programa não precisar de condicional em cada chamada.
//
// Cada recurso daqui degrada sozinho — é a mesma regra do caminho
// Wayland, onde um compositor sem o protocolo de inibição simplesmente
// não inibe. Quem chama trata Handle nil, e o Windows resolve teclado e
// área de transferência pelo próprio Gio.
package grab

import "unsafe"

// Handle é uma sessão de captura. Fora do Linux, sempre nil.
type Handle struct{}

func Start(display, surface unsafe.Pointer,
	onKeyFn func(keysym, keycodeX11 uint32, pressed bool),
	onClipboardFn func(text string),
) *Handle {
	return nil
}

func (h *Handle) Inibir(bool)             {}
func (h *Handle) Modificadores() int      { return 0 }
func (h *Handle) SetClipboardText(string) {}
func (h *Handle) Stop()                   {}

// Liberar existe para a assinatura bater com a do Linux. Lá ela solta só o
// que NÃO fala Wayland, para quem descobriu tarde demais que a conexão da
// janela já caiu (ver o cabeçalho de grab.go); aqui não há captura
// nenhuma, então não há o que soltar.
func (h *Handle) Liberar() {}
