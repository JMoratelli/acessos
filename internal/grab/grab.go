//go:build linux

// Package grab fala Wayland direto (via os ponteiros crus que o Gio expõe
// em app.WaylandViewEvent) para recursos que o Gio não cobre nesta pilha
// (Wayland/KDE):
//
//   - inibir os atalhos globais do compositor (keyboard-shortcuts-inhibit-
//     unstable-v1), para que teclas como Alt+Tab cheguem à sessão remota
//     em vez de serem interceptadas localmente;
//   - ler teclado direto (wl_keyboard + xkbcommon): o key.Event do Gio não
//     preenche Modifiers e nunca entrega o "soltou" de Ctrl/Alt/Shift/Super
//     sozinhos nesta pilha, o que prendia esses modificadores para sempre
//     do lado remoto;
//   - clipboard direto (wl_data_device): o clipboard do Gio só reconhece
//     uma lista fixa e estreita de mime types ao ler, e no KDE/Wayland não
//     batia com nada — a leitura nunca voltava, sem erro nenhum.
//
// Todos degradam graciosamente: fora do Wayland, ou em compositor sem
// suporte a uma peça específica, aquela peça simplesmente não funciona,
// sem impedir as outras.
package grab

/*
#cgo pkg-config: wayland-client xkbcommon
#include <stdlib.h>
#include "grab_wayland.h"

extern void goTecla(uint32_t keysym, uint32_t keycode_x11, int pressionada);
extern void goClipOferta(int fd_leitura);
*/
import "C"

import (
	"io"
	"os"
	"unsafe"
)

// onKey e onClipboardText são globais de propósito: este app tem uma
// janela/grab por vez.
//
// keysym é o valor X11 já calculado pelo layout ativo (o que o cliente VNC
// quer); keycodeX11 é o keycode cru evdev+8 (o que o cliente RDP quer — o
// FreeRDP faz sua própria tradução pra scancode). keysym vem 0 para teclas
// mortas/compostas; keycodeX11 continua válido nesse caso.
var (
	onKey           func(keysym, keycodeX11 uint32, pressed bool)
	onClipboardText func(text string)
)

// Handle é uma sessão de captura ativa. Chame Stop ao perder foco ou
// fechar a janela para devolver os atalhos ao compositor.
type Handle struct {
	g *C.Grab
}

// Start arma a captura para a janela cujos display/surface vêm de
// app.WaylandViewEvent.{Display,Surface}.
//
// onKeyFn (pode ser nil) é chamado a cada tecla física apertada/solta —
// inclui modificadores puros (Ctrl/Shift/Alt/Super), o que o key.Event do
// Gio não garante nesta pilha.
//
// onClipboardFn (pode ser nil) é chamado quando o clipboard do SISTEMA
// muda para algo com um mime type de texto reconhecido.
//
// Devolve nil se nem wl_seat existir (não deveria acontecer em desktop
// Wayland de verdade); um Handle não-nil pode ainda assim não ter
// conseguido partes específicas (inibidor de atalhos, clipboard) se o
// compositor não suportar aquele protocolo — cada peça degrada sozinha.
func Start(display, surface unsafe.Pointer,
	onKeyFn func(keysym, keycodeX11 uint32, pressed bool),
	onClipboardFn func(text string),
) *Handle {
	onKey = onKeyFn
	onClipboardText = onClipboardFn
	g := C.grab_iniciar(display, surface, C.cb_tecla(C.goTecla), C.cb_clip_oferta(C.goClipOferta))
	if g == nil {
		return nil
	}
	return &Handle{g: g}
}

// Inibir liga ou desliga a captura dos atalhos do compositor (Alt+Tab e
// afins). Nasce DESLIGADA: só deve ficar ligada enquanto a aba em foco
// for uma sessão remota — com ela ligada no painel, o usuário perde os
// atalhos do próprio desktop sem entender por quê. Idempotente.
func (h *Handle) Inibir(ligar bool) {
	if h == nil || h.g == nil {
		return
	}
	v := C.int(0)
	if ligar {
		v = 1
	}
	C.grab_inibir(h.g, v)
}

// SetClipboardText anuncia text como o clipboard atual do sistema (participa
// da seleção do wl_data_device). Sem efeito num Handle nil ou sem suporte
// do compositor a wl_data_device_manager.
func (h *Handle) SetClipboardText(text string) {
	if h == nil || h.g == nil {
		return
	}
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	C.grab_clip_definir(h.g, cText, C.int(len(text)))
}

// Stop libera a captura. Idempotente; seguro chamar em um Handle nil.
func (h *Handle) Stop() {
	if h == nil || h.g == nil {
		return
	}
	C.grab_parar(h.g)
	h.g = nil
}

//export goTecla
func goTecla(keysym, keycodeX11 C.uint32_t, pressionada C.int) {
	if onKey != nil {
		onKey(uint32(keysym), uint32(keycodeX11), pressionada != 0)
	}
}

//export goClipOferta
func goClipOferta(fd C.int) {
	f := os.NewFile(uintptr(fd), "clipboard-wayland")
	go func() {
		defer f.Close()
		data, err := io.ReadAll(f)
		if err == nil && onClipboardText != nil {
			onClipboardText(string(data))
		}
	}()
}
