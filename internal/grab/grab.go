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
#cgo LDFLAGS: -lpthread
#include <stdlib.h>
#include "grab_wayland.h"

extern void goTecla(void *ctx, uint32_t keysym, uint32_t keycode_x11, int pressionada);
extern void goClipOferta(void *ctx, int fd_leitura);
*/
import "C"

import (
	"io"
	"os"
	"sync"
	"unsafe"

	"acessos-go/internal/cgoregistry"
)

// Cada captura carrega os próprios callbacks, e o C devolve o ctx que
// identifica qual delas disparou.
//
// ISTO JÁ FOI GLOBAL, e o comentário aqui dizia que era de propósito
// porque "este app tem uma janela/grab por vez". Deixou de ser verdade
// quando a sessão remota ganhou janela própria: com os callbacks em
// variáveis de pacote, a segunda janela a chamar Start substituía os da
// primeira EM SILÊNCIO, e o teclado da janela original parava de chegar.
// Havia um aviso em stderr para o caso, o que é o contrário de resolver —
// ninguém lê stderr num app de janela.
//
// O padrão é o mesmo de internal/rdp e internal/vnc: handle inteiro no
// void* do C (o coletor de lixo não move um inteiro) e cgoregistry
// fazendo a volta.
//
// keysym é o valor X11 já calculado pelo layout ativo (o que o cliente VNC
// quer); keycodeX11 é o keycode cru evdev+8 (o que o cliente RDP quer — o
// FreeRDP faz sua própria tradução pra scancode). keysym vem 0 para teclas
// mortas/compostas; keycodeX11 continua válido nesse caso.
var registro = cgoregistry.New[Handle]()

// Handle é uma sessão de captura ativa. Chame Stop ao perder foco ou
// fechar a janela para devolver os atalhos ao compositor.
type Handle struct {
	// mu protege g contra um Stop concorrente.
	//
	// Antes não havia trava, e o único motivo de não quebrar era ninguém
	// chamar Stop: os três pontos de uso documentavam que não chamavam,
	// porque fazê-lo contra um wl_display em desmonte derrubava o
	// processo. Com a sessão remota indo para janela própria, Stop passa
	// a ser chamado de verdade — a janela que fecha solta a captura dela
	// — e a corrida deixa de ser teórica: grab_parar faz free(g) enquanto
	// o callback do Wayland, noutra thread, já está dentro de
	// grab_modificadores com o ponteiro antigo.
	//
	// Leitura compartilhada porque as chamadas do C já se protegem entre
	// si (ver o mutex de fonte/clip_local em grab_wayland.c); o que falta
	// é só impedir que o ponteiro morra debaixo delas.
	mu     sync.RWMutex
	g      *C.Grab
	handle uintptr

	onKey           func(keysym, keycodeX11 uint32, pressed bool)
	onClipboardText func(text string)
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
	// Registrar ANTES de grab_iniciar: a partir dele já podem chegar
	// teclas, e um callback que chegasse antes do registro não acharia o
	// Handle e seria descartado calado.
	h := &Handle{onKey: onKeyFn, onClipboardText: onClipboardFn}
	h.handle = registro.Registrar(h)

	ctx := unsafe.Pointer(h.handle) //nolint:govet // handle inteiro repassado como ponteiro opaco ao C
	g := C.grab_iniciar(ctx, display, surface,
		C.cb_tecla(C.goTecla), C.cb_clip_oferta(C.goClipOferta))
	if g == nil {
		registro.Remover(h.handle)
		return nil
	}
	// Sob a trava: o handle já está no registro desde antes do
	// grab_iniciar, então um callback do Wayland pode estar lendo h.g
	// neste instante — e todos os acessores o leem sob RLock.
	h.mu.Lock()
	h.g = g
	h.mu.Unlock()
	return h
}

// deHandle devolve a captura do handle, ou nil se ela já parou — um
// callback em voo quando o Stop acontece é normal, e cair em nil aqui é o
// tratamento certo para ele.
//
// Recebe uintptr, e não unsafe.Pointer, de propósito: só os //export
// precisam falar a língua do C, e manter a conversão confinada a eles
// deixa todo o resto (inclusive o teste) longe de unsafe — o checkptr do
// -race reclama, com razão, de transformar um handle pequeno em ponteiro.
func deHandle(h uintptr) *Handle { return registro.De(h) }

// Modificadores devolve quais modificadores estão ativos AGORA, como
// máscara: 1=Ctrl, 2=Shift, 4=Alt, 8=Super.
//
// Vem do evento wl_keyboard.modifiers do compositor, e não de contar
// press/release — é a diferença entre saber e adivinhar. Quando o
// compositor captura uma combinação como atalho global dele, ele consome
// o evento e o release dos modificadores nunca chega ao app; quem conta
// fica com a tecla presa em "apertada" e passa a errar todo teste de
// tecla limpa a partir dali.
func (h *Handle) Modificadores() int {
	if h == nil {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.g == nil {
		return 0
	}
	return int(C.grab_modificadores(h.g))
}

// Inibir liga ou desliga a captura dos atalhos do compositor (Alt+Tab e
// afins). Nasce DESLIGADA: só deve ficar ligada enquanto a aba em foco
// for uma sessão remota — com ela ligada no painel, o usuário perde os
// atalhos do próprio desktop sem entender por quê. Idempotente.
func (h *Handle) Inibir(ligar bool) {
	if h == nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.g == nil {
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
//
// CHAME DE UMA GOROUTINE SÓ. O wl_data_source é destruído e recriado a cada
// chamada, e os callbacks dele (fonte_enviar, fonte_cancelada) rodam de
// dentro do dispatch do Wayland.
//
// Neste backend, o dispatch acontece na PRÓPRIA goroutine do laço de
// eventos: app.Window.Event() cai em driver.Event()
// (third_party/gio/app/os_wayland.go:1582), que chama dispatch() quando não
// há evento pendente. Então, para quem publica do laço de quadro, os dois
// lados são a mesma goroutine e não há corrida nenhuma.
//
// JÁ ESTEVE ESCRITO AQUI o contrário — que "o laço de quadro é a goroutine
// do cliente e quem despacha é uma thread interna do Gio, threads
// DIFERENTES". É falso, e a conclusão tirada dali (que existia um uso após
// liberação no cmd/acessos) também era.
//
// Quem de fato corre são os binários que publicam FORA do laço:
// cmd/vncview e cmd/rdpview chamam este método direto de sess.OnCutText, na
// goroutine da sessão. É por causa deles que o lado C protege `fonte` e
// `clip_local` com mutex (ver grab_wayland.c) — não remova a trava achando
// que o app não precisa dela.
//
// No cmd/acessos a regra continua sendo: quem publica é o laço, nunca as
// goroutines das sessões — ver clipboard.go no app.
func (h *Handle) SetClipboardText(text string) {
	if h == nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.g == nil {
		return
	}
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	C.grab_clip_definir(h.g, cText, C.int(len(text)))
}

// Stop libera a captura por inteiro. SÓ pode ser chamado com a conexão
// Wayland da janela AINDA VIVA — ou seja, por quem decidiu fechar a
// janela, antes de pedir o fechamento. Quem só descobriu depois (o X do
// compositor, que já derrubou a conexão) chama Liberar.
//
// Idempotente; seguro num Handle nil.
func (h *Handle) Stop() { h.desmontar(true) }

// Liberar solta só o que NÃO fala Wayland: a thread de repetição, o xkb e
// a memória da struct.
//
// Existe porque cada janela do Gio tem a PRÓPRIA conexão Wayland
// (newWLWindow → newWLDisplay, em third_party/gio/app/os_wayland.go) e o
// close() dela enfileira DestroyEvent e EM SEGUIDA desconecta, antes de o
// laço do cliente ler o evento. Destruir wl_proxy a partir dali é
// use-after-free num display já liberado, e derruba o processo inteiro —
// não só a janela que fechou.
//
// A inibição de atalhos não fica presa: o compositor solta o que era do
// cliente quando a conexão cai.
func (h *Handle) Liberar() { h.desmontar(false) }

func (h *Handle) desmontar(soltarWayland bool) {
	if h == nil {
		return
	}
	// Fora do registro primeiro: daqui em diante nenhum callback NOVO
	// acha este Handle.
	registro.Remover(h.handle)

	// Tira o ponteiro de circulação SOB A TRAVA e trabalha na cópia local
	// FORA dela. É o que evita dois problemas de uma vez:
	//
	//   - os acessores (Modificadores, Inibir, SetClipboardText) veem
	//     h.g == nil e voltam cedo, sem tocar no que está sendo liberado;
	//   - o pthread_join lá dentro espera a thread de repetição, que chama
	//     o callback de tecla, que no app pede Modificadores() e tomaria o
	//     RLock. Com a trava de escrita na mão aqui, o join esperaria por
	//     uma thread que espera pela trava. Impasse — e com a janela
	//     destacada fechando com uma tecla segurada, ele acontece.
	h.mu.Lock()
	g := h.g
	h.g = nil
	h.mu.Unlock()
	if g == nil {
		return
	}

	C.grab_parar_repeticao(g)
	if soltarWayland {
		C.grab_soltar_wayland(g)
	}
	C.grab_liberar(g)
}

//export goTecla
func goTecla(ctx unsafe.Pointer, keysym, keycodeX11 C.uint32_t, pressionada C.int) {
	entregarTecla(uintptr(ctx), uint32(keysym), uint32(keycodeX11), pressionada != 0)
}

// entregarTecla é o roteamento de verdade, separado de goTecla só porque
// arquivo _test.go não pode importar "C" — sem esta divisão o caminho que
// o defeito do singleton quebrava ficaria sem teste nenhum.
func entregarTecla(handle uintptr, keysym, keycodeX11 uint32, pressionada bool) {
	h := deHandle(handle)
	if h == nil || h.onKey == nil {
		return
	}
	h.onKey(keysym, keycodeX11, pressionada)
}

//export goClipOferta
func goClipOferta(ctx unsafe.Pointer, fd C.int) {
	entregarClipOferta(uintptr(ctx), int(fd))
}

// entregarClipOferta é o roteamento, separado do //export pelo mesmo
// motivo de entregarTecla.
func entregarClipOferta(handle uintptr, fd int) {
	// O fd é lido e fechado mesmo sem captura do outro lado: ele já veio
	// aberto do C, e largá-lo aqui vazaria um descritor por oferta de
	// clipboard — uma por cópia feita no sistema inteiro.
	f := os.NewFile(uintptr(fd), "clipboard-wayland")
	h := deHandle(handle)
	go func() {
		defer f.Close()
		data, err := io.ReadAll(f)
		if err == nil && h != nil && h.onClipboardText != nil {
			h.onClipboardText(string(data))
		}
	}()
}
