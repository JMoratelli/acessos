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
	// grava JÁ no cache global, sem esperar o Wayland avisar que o
	// clipboard mudou: nada garante que esse aviso volte pra quem
	// acabou de publicar (depende do compositor), e sem isto colar logo
	// em seguida — mesmo na mesma aba — podia devolver o texto antigo.
	registrarClipboardSistema(texto)
	t := texto
	clipPendente.Store(&t)
	if w != nil {
		w.Invalidate()
	}
}

// entregarClipboard roda no laço de quadro, e só ali. Recebe o estado da
// janela porque a captura é DELA: publicar pela captura de outra janela
// mandaria o texto por uma superfície que não é a que está em foco.
func entregarClipboard(e *estadoJanela) {
	if p := clipPendente.Swap(nil); p != nil {
		e.grab.Load().SetClipboardText(*p)
	}
}

// ---- último clipboard do sistema conhecido ----
//
// O terminal SSH cola (Ctrl+Shift+V) o que estiver aqui. Guardar
// INDEPENDENTE de qual aba estava ativa é o que faz "copiar na aba 101,
// colar na 102" funcionar: o aviso de "clipboard mudou" do Wayland chega
// só UMA vez, para quem estiver em foco NAQUELE instante exato — sem este
// cache global, uma aba que não estava em foco nesse instante (a maioria
// delas, na prática) nunca fica sabendo do que foi copiado, e Ctrl+Shift+V
// nela ou não faz nada ou cola um texto antigo, de uma cópia anterior.
var clipboardSistemaAtual atomic.Pointer[string]

// registrarClipboardSistema é chamado do grab do Wayland toda vez que o
// clipboard do sistema muda, não importa qual aba está ativa.
func registrarClipboardSistema(texto string) {
	t := texto
	clipboardSistemaAtual.Store(&t)
}

// clipboardSistema devolve o último texto conhecido do clipboard do
// sistema ("" se nada foi visto ainda nesta sessão do app).
func clipboardSistema() string {
	if p := clipboardSistemaAtual.Load(); p != nil {
		return *p
	}
	return ""
}

// ---- aba ativa ----
//
// A marca de aba à vista mora em janela.go: virou um mapa por janela
// quando a sessão remota passou a poder sair para janela própria.
