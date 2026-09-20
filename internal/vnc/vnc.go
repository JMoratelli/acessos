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
extern void goAoCursor(void *ctx, int xhot, int yhot, int w, int h, uint8_t *mask);
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"acessos-go/internal/cgoregistry"
)

// Session é uma conexão VNC ativa.
//
// mu protege s contra a corrida entre Close (que libera a Sessao em C,
// junto com o framebuffer) e qualquer outro método chamado de uma goroutine
// diferente enquanto isso acontece — sem isto, Close podia liberar a
// memória bem no meio de uma cópia do framebuffer feita pela goroutine de
// desenho, use-after-free clássico.
type Session struct {
	mu     sync.RWMutex
	s      *C.Sessao
	handle uintptr

	OnUpdate  func(x, y, w, h int)
	OnResize  func(w, h int)
	OnCutText func(text string)
	// OnCursor é chamado quando o servidor manda uma nova forma de
	// cursor. mask é 1 byte por pixel (0/255), w*h bytes — uma CÓPIA,
	// válida além do escopo do callback.
	OnCursor func(xhot, yhot, w, h int, mask []byte)
}

var registro = cgoregistry.New[Session]()

// New cria uma sessão VNC (ainda não conectada).
func New() *Session {
	sess := &Session{}
	sess.handle = registro.Registrar(sess)

	ctx := unsafe.Pointer(sess.handle) //nolint:govet // handle inteiro repassado como ponteiro opaco ao C
	sess.s = C.vs_criar(ctx,
		C.cb_atualizou(C.goAoAtualizar),
		C.cb_redimensionou(C.goAoRedimensionar),
		C.cb_texto(C.goAoReceberTexto),
		C.cb_cursor(C.goAoCursor),
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
	// UMA chamada para o trio (ponteiro, largura, altura): ler os três
	// em chamadas separadas não era atômico, e a libvncclient grava o
	// tamanho novo ANTES de trocar o buffer — dava para copiar o tamanho
	// novo de dentro do buffer velho e ler além do fim da alocação. Ver
	// vs_capturar_quadro, em vncshim.c.
	var cw, ch C.int
	ptr := C.vs_capturar_quadro(s.s, &cw, &ch)
	if ptr == nil {
		return nil, 0, 0
	}
	defer C.vs_liberar_quadro(ptr)

	w, h = int(cw), int(ch)
	n := w * h * 4
	buf = make([]byte, n)
	copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), n))
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
	// LATIN-1 no fio: é o que o protocolo RFB manda e o que a
	// libvncclient documenta. Ver utf8ParaLatin1.
	b := utf8ParaLatin1(text)
	p := C.CBytes(b)
	defer C.free(p)
	C.vs_enviar_texto(s.s, (*C.char)(p), C.int(len(b)))
}

// Close libera a sessão. Não chame Run/eventos depois disso.
func (s *Session) Close() {
	registro.Remover(s.handle)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s == nil {
		return
	}
	C.vs_destruir(s.s)
	s.s = nil
}

func sessionFromHandle(ctx unsafe.Pointer) *Session {
	return registro.De(uintptr(ctx))
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
		sess.OnCutText(latin1ParaUTF8(C.GoBytes(unsafe.Pointer(texto), tam)))
	}
}

//export goAoCursor
func goAoCursor(ctx unsafe.Pointer, xhot, yhot, w, h C.int, mask *C.uint8_t) {
	sess := sessionFromHandle(ctx)
	if sess == nil || sess.OnCursor == nil {
		return
	}
	// mask nil é o aviso DELIBERADO de "cursor escondido" que o
	// hook_cursor manda de propósito (vncshim.c) — descartá-lo aqui
	// anulava a correção do lado C e deixava o ponteiro local preso na
	// última forma (I-beam, ampulheta) até o servidor mandar outra. O
	// irmão RDP já repassa assim; ver rdp.go.
	n := int(w) * int(h)
	if mask == nil || n <= 0 {
		sess.OnCursor(int(xhot), int(yhot), 0, 0, nil)
		return
	}
	src := unsafe.Slice((*byte)(unsafe.Pointer(mask)), n)
	buf := make([]byte, n)
	copy(buf, src)
	sess.OnCursor(int(xhot), int(yhot), int(w), int(h), buf)
}

// O clipboard do RFB é LATIN-1, não UTF-8 — está no protocolo e no header
// da libvncclient (rfbclient.h, em SendClientCutText). Os bytes viajavam
// crus nos dois sentidos e eram tratados como UTF-8 na outra ponta, contra
// a invariante escrita em telaproc/protocolo.go: copiar "conferência" de um
// PDV trazia um 0xEA solto — UTF-8 inválido — para o clipboard do sistema,
// e colar "ç" chegava no servidor como "Ã§". Silencioso nos dois sentidos, e
// invisível em qualquer teste que use só ASCII.
//
// A conversão fica no Go de propósito: o C não precisa saber de codificação,
// e aqui ela é testável sem servidor nenhum.
func latin1ParaUTF8(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c) // byte Latin-1 == code point Unicode, de 0 a 255
	}
	return string(r)
}

// utf8ParaLatin1 é o caminho de volta. O que não cabe em um byte vira '?':
// o protocolo não tem como carregar, e um '?' visível é melhor que um byte
// truncado que o servidor exibe como outra letra.
func utf8ParaLatin1(s string) []byte {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x100 {
			b = append(b, byte(r))
			continue
		}
		b = append(b, '?')
	}
	return b
}
