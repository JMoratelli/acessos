//go:build linux || windows

// Package vnc expõe um cliente VNC em Go por cima do shim C existente
// (vncshim.c), que por sua vez fala com libvncclient.
package vnc

/*
#cgo pkg-config: libvncclient
#include <stdlib.h>
#include "vncshim.h"

extern void goAoAtualizar(void *ctx, int x, int y, int w, int h);
extern void goAoRedimensionar(void *ctx, int w, int h);
extern void goAoReceberTexto(void *ctx, char *texto, int tam);
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

// Session é uma conexão VNC ativa.
//
// mu protege s contra a corrida entre Close (que libera a Sessao em C,
// junto com o framebuffer) e qualquer outro método chamado de uma goroutine
// diferente enquanto isso acontece — sem isto, Close podia liberar a
// memória bem no meio de uma cópia do framebuffer feita pela goroutine de
// desenho, use-after-free clássico.
type Session struct {
	mu sync.RWMutex
	s  *C.Sessao

	OnUpdate  func(x, y, w, h int)
	OnResize  func(w, h int)
	OnCutText func(text string)
}

var (
	registryMu sync.Mutex
	registry   = map[unsafe.Pointer]*Session{}
	nextHandle uintptr
	handles    = map[uintptr]*Session{}
)

// New cria uma sessão VNC (ainda não conectada).
func New() *Session {
	sess := &Session{}

	registryMu.Lock()
	nextHandle++
	h := nextHandle
	handles[h] = sess
	registryMu.Unlock()

	ctx := unsafe.Pointer(h) //nolint:govet // handle inteiro repassado como ponteiro opaco ao C
	sess.s = C.vs_criar(ctx,
		C.cb_atualizou(C.goAoAtualizar),
		C.cb_redimensionou(C.goAoRedimensionar),
		C.cb_texto(C.goAoReceberTexto),
	)
	return sess
}

// SetCredentials define usuário/senha antes de Connect. Usuário pode ser
// vazio para VNC clássico (só senha).
func (s *Session) SetCredentials(user, password string) {
	cPass := C.CString(password)
	defer C.free(unsafe.Pointer(cPass))
	C.vs_definir_senha(s.s, cPass)

	if user != "" {
		cUser := C.CString(user)
		defer C.free(unsafe.Pointer(cUser))
		C.vs_definir_usuario(s.s, cUser)
	}
}

// ConnectError descreve por que Connect falhou, distinguindo os mesmos
// casos que o app original tratava (rede, credencial, recusa do servidor).
type ConnectError struct {
	Message       string
	AuthFailed    bool
	NeedsUsername bool
	Rejected      bool
}

func (e *ConnectError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "falha ao conectar"
}

// Connect abre a conexão. Bloqueia até conectar ou falhar — chame de uma
// goroutine própria.
func (s *Session) Connect(host string, port int) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cHost := C.CString(host)
	defer C.free(unsafe.Pointer(cHost))

	ok := C.vs_conectar(s.s, cHost, C.int(port))
	if ok == 1 {
		return nil
	}

	return &ConnectError{
		Message:       C.GoString(C.vs_erro_msg(s.s)),
		AuthFailed:    C.vs_erro_auth(s.s) != 0,
		NeedsUsername: C.vs_falta_usuario(s.s) != 0,
		Rejected:      C.vs_recusado(s.s) != 0,
	}
}

// Run processa mensagens do servidor até a conexão cair ou stop ser fechado.
// Deve rodar em sua própria goroutine; use os callbacks OnUpdate/OnResize
// para repassar quadros à UI.
func (s *Session) Run(stop <-chan struct{}) error {
	for {
		select {
		case <-stop:
			return nil
		default:
		}

		morto, n, ok := s.poll()
		if morto {
			return errors.New("conexão encerrada")
		}
		if n < 0 {
			return errors.New("erro aguardando dados do servidor")
		}
		if n == 0 {
			continue // timeout, dá outra chance de checar stop
		}
		if !ok {
			return errors.New("conexão perdida")
		}
	}
}

// poll faz uma rodada de espera+processamento sob RLock, para nunca correr
// concorrente com Close.
func (s *Session) poll() (morto bool, n int, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if C.vs_morto(s.s) != 0 {
		return true, 0, false
	}
	n = int(C.vs_esperar(s.s, 200000)) // 200ms
	if n <= 0 {
		return false, n, false
	}
	return false, n, C.vs_processar(s.s) != 0
}

// Framebuffer devolve uma CÓPIA do buffer em C (formato BGRX/32bpp, ver
// vncshim.c) — não um ponteiro direto, para poder ser usada com segurança
// depois de Close ou de um redimensionamento.
func (s *Session) Framebuffer() (buf []byte, w, h int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.s == nil {
		return nil, 0, 0
	}
	w = int(C.vs_largura(s.s))
	h = int(C.vs_altura(s.s))
	ptr := C.vs_framebuffer(s.s)
	if ptr == nil || w == 0 || h == 0 {
		return nil, 0, 0
	}
	n := w * h * 4
	src := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), n)
	buf = make([]byte, n)
	copy(buf, src)
	return buf, w, h
}

func (s *Session) PointerEvent(x, y, buttons int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	C.vs_ponteiro(s.s, C.int(x), C.int(y), C.int(buttons))
}

func (s *Session) KeyEvent(keysym uint32, down bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pressed C.int
	if down {
		pressed = 1
	}
	C.vs_tecla(s.s, C.uint32_t(keysym), pressed)
}

func (s *Session) SendCutText(text string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	C.vs_enviar_texto(s.s, cText, C.int(len(text)))
}

// Close libera a sessão. Não chame Run/eventos depois disso.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s == nil {
		return
	}
	C.vs_destruir(s.s)
	s.s = nil
}

func sessionFromHandle(ctx unsafe.Pointer) *Session {
	h := uintptr(ctx)
	registryMu.Lock()
	sess := handles[h]
	registryMu.Unlock()
	return sess
}

//export goAoAtualizar
func goAoAtualizar(ctx unsafe.Pointer, x, y, w, h C.int) {
	sess := sessionFromHandle(ctx)
	if sess != nil && sess.OnUpdate != nil {
		sess.OnUpdate(int(x), int(y), int(w), int(h))
	}
}

//export goAoRedimensionar
func goAoRedimensionar(ctx unsafe.Pointer, w, h C.int) {
	sess := sessionFromHandle(ctx)
	if sess != nil && sess.OnResize != nil {
		sess.OnResize(int(w), int(h))
	}
}

//export goAoReceberTexto
func goAoReceberTexto(ctx unsafe.Pointer, texto *C.char, tam C.int) {
	sess := sessionFromHandle(ctx)
	if sess != nil && sess.OnCutText != nil {
		sess.OnCutText(C.GoStringN(texto, tam))
	}
}
