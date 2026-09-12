//go:build linux

// Package rdp expõe um cliente RDP em Go por cima do shim C existente
// (rdpshim.c), que por sua vez fala com libfreerdp3.
package rdp

/*
#cgo pkg-config: freerdp3 freerdp-client3 winpr3
#include <stdlib.h>
#include "rdpshim.h"

extern void goRdpAoAtualizar(void *ctx, int x, int y, int w, int h);
extern void goRdpAoRedimensionar(void *ctx, int w, int h);
extern void goRdpAoDesconectar(void *ctx, char *motivo);
extern void goRdpAoClipTexto(void *ctx, char *utf8, int tam);
extern void goRdpDispPronto(void *ctx);
extern int goRdpCertNovo(void *ctx, char *host, uint16_t porta,
                         char *nome_comum, char *assunto,
                         char *emissor, char *digital, uint32_t flags);
extern int goRdpCertMudou(void *ctx, char *host, uint16_t porta,
                          char *nome_comum, char *assunto,
                          char *emissor, char *digital_novo,
                          char *assunto_antigo, char *emissor_antigo,
                          char *digital_antigo, uint32_t flags);
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

// Session é uma conexão RDP ativa.
//
// mu tem o mesmo papel que em internal/vnc.Session: serializar Close (que
// libera a Sessao em C) contra qualquer outro método em uso por outra
// goroutine no momento — sem isto, Close podia liberar memória bem no meio
// de uma cópia do framebuffer.
type Session struct {
	mu sync.RWMutex
	s  *C.Sessao

	OnUpdate        func(x, y, w, h int)
	OnResize        func(w, h int)
	OnDisconnect    func(reason string)
	OnClipboardText func(text string)

	// OnDisplayPronto avisa que o canal Display Control terminou o
	// handshake. ANTES disso, pedido de resolução é descartado em
	// silêncio pelo shim — é por isso que a sessão nascia no tamanho do
	// servidor e só se ajustava quando alguém mexia na janela.
	OnDisplayPronto func()

	// OnCertificado decide sobre a identidade do servidor. Chamado DA
	// THREAD DE REDE do FreeRDP e bloqueia o handshake até responder.
	OnCertificado func(Certificado) int
}

var (
	registryMu sync.Mutex
	nextHandle uintptr
	handles    = map[uintptr]*Session{}
)

// New cria uma sessão RDP (ainda não conectada).
//
// Certificado: os dois ganchos do shim são expostos aqui. Eles BLOQUEIAM
// o handshake esperando a resposta — é essa espera que dá sentido à
// pergunta. Sem gancho definido o shim aplica o padrão seguro do
// gtk-frdp: aceita e guarda certificado novo, RECUSA mudança.
func New() *Session {
	sess := &Session{}

	registryMu.Lock()
	nextHandle++
	h := nextHandle
	handles[h] = sess
	registryMu.Unlock()

	ctx := unsafe.Pointer(h) //nolint:govet // handle inteiro repassado como ponteiro opaco ao C
	sess.s = C.rs_criar(ctx,
		C.cb_atualizou(C.goRdpAoAtualizar),
		C.cb_redimensionou(C.goRdpAoRedimensionar),
		C.cb_desconectou(C.goRdpAoDesconectar),
		C.cb_certificado_novo(C.goRdpCertNovo),
		C.cb_certificado_mudou(C.goRdpCertMudou),
		C.cb_clip_texto(C.goRdpAoClipTexto),
		C.cb_disp_pronto(C.goRdpDispPronto),
	)
	return sess
}

// SetCredentials define usuário/senha/domínio antes de Connect. domain
// pode ser vazio.
func (s *Session) SetCredentials(user, password, domain string) {
	cUser := C.CString(user)
	defer C.free(unsafe.Pointer(cUser))
	cPass := C.CString(password)
	defer C.free(unsafe.Pointer(cPass))

	var cDomain *C.char
	if domain != "" {
		cDomain = C.CString(domain)
		defer C.free(unsafe.Pointer(cDomain))
	}
	C.rs_definir_credenciais(s.s, cUser, cPass, cDomain)
}

// ConnectError descreve por que Connect falhou.
type ConnectError struct {
	Message    string
	AuthFailed bool
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

	ok := C.rs_conectar(s.s, cHost, C.int(port))
	if ok == 1 {
		return nil
	}
	return &ConnectError{
		Message:    C.GoString(C.rs_erro_msg(s.s)),
		AuthFailed: C.rs_erro_auth(s.s) != 0,
	}
}

// Run processa mensagens do servidor até a conexão cair ou stop ser fechado.
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
			continue
		}
		if !ok {
			return errors.New("conexão perdida")
		}
	}
}

func (s *Session) poll() (morto bool, n int, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if C.rs_morto(s.s) != 0 {
		return true, 0, false
	}
	n = int(C.rs_esperar(s.s, 200)) // ms
	if n <= 0 {
		return false, n, false
	}
	return false, n, C.rs_processar(s.s) != 0
}

// Framebuffer devolve uma CÓPIA do buffer em C (formato BGRX/32bpp, igual
// ao VNC — ver PIXEL_FORMAT_BGRX32 em rdpshim.c) junto com o stride: ao
// contrário do VNC, aqui o gdi pode alinhar cada linha a mais que w*4
// bytes, então stride precisa ser respeitado ao interpretar o buffer.
//
// Usa rs_capturar_quadro (tamanho+cópia numa única chamada em C, sob
// fb_lock) em vez de ler largura/altura/stride/ponteiro em chamadas
// separadas: um resize no meio dessas quatro chamadas lia uma combinação
// inconsistente de tamanho antigo com ponteiro novo (ou vice-versa) e
// derrubava o processo — ver comentário em rdpshim.c sobre fb_lock.
func (s *Session) Framebuffer() (buf []byte, w, h, stride int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.s == nil {
		return nil, 0, 0, 0
	}
	var cw, ch, cstride C.int
	ptr := C.rs_capturar_quadro(s.s, &cw, &ch, &cstride)
	if ptr == nil {
		return nil, 0, 0, 0
	}
	defer C.rs_liberar_quadro(ptr)

	w, h, stride = int(cw), int(ch), int(cstride)
	n := stride * h
	buf = make([]byte, n)
	copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), n))
	return buf, w, h, stride
}

func (s *Session) PointerMove(x, y int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	C.rs_ponteiro_mover(s.s, C.int(x), C.int(y))
}

// button: 1=esquerdo 2=meio 3=direito.
func (s *Session) PointerButton(x, y, button int, down bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pressed C.int
	if down {
		pressed = 1
	}
	C.rs_ponteiro_botao(s.s, C.int(x), C.int(y), C.int(button), pressed)
}

// RequestResize pede ao servidor que redimensione a área remota (canal
// Display Control). Sem efeito e sem erro se o canal ainda não anunciou
// suporte (ex.: logo após conectar, antes do DisplayControlCaps chegar) —
// devolve false nesse caso, chame de novo mais tarde se quiser confirmar.
func (s *Session) RequestResize(w, h int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return C.rs_pedir_resize(s.s, C.int(w), C.int(h)) != 0
}

// axis: 0=vertical 1=horizontal.
func (s *Session) PointerWheel(axis, steps int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	C.rs_ponteiro_roda(s.s, C.int(axis), C.int(steps))
}

// SendClipboardText anuncia ao servidor que o clipboard do HOST mudou
// (canal CLIPRDR). Sem efeito antes do canal conectar — o próprio shim
// guarda o texto e anuncia assim que possível (ver rs_clipboard_definir_texto).
func (s *Session) SendClipboardText(text string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	C.rs_clipboard_definir_texto(s.s, cText, C.int(len(text)))
}

// KeyEvent recebe o KEYCODE X11 (não keysym — o FreeRDP faz sua própria
// tradução keycode->scancode internamente, ver comentário em rs_tecla).
func (s *Session) KeyEvent(keycodeX11 uint32, down bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pressed C.int
	if down {
		pressed = 1
	}
	C.rs_tecla(s.s, C.uint32_t(keycodeX11), pressed)
}

// Close libera a sessão. Não chame Run/eventos depois disso.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s == nil {
		return
	}
	C.rs_destruir(s.s)
	s.s = nil
}

func sessionFromHandle(ctx unsafe.Pointer) *Session {
	h := uintptr(ctx)
	registryMu.Lock()
	sess := handles[h]
	registryMu.Unlock()
	return sess
}

//export goRdpAoAtualizar
func goRdpAoAtualizar(ctx unsafe.Pointer, x, y, w, h C.int) {
	sess := sessionFromHandle(ctx)
	if sess != nil && sess.OnUpdate != nil {
		sess.OnUpdate(int(x), int(y), int(w), int(h))
	}
}

//export goRdpAoRedimensionar
func goRdpAoRedimensionar(ctx unsafe.Pointer, w, h C.int) {
	sess := sessionFromHandle(ctx)
	if sess != nil && sess.OnResize != nil {
		sess.OnResize(int(w), int(h))
	}
}

//export goRdpAoDesconectar
func goRdpAoDesconectar(ctx unsafe.Pointer, motivo *C.char) {
	sess := sessionFromHandle(ctx)
	if sess != nil && sess.OnDisconnect != nil {
		sess.OnDisconnect(C.GoString(motivo))
	}
}

//export goRdpAoClipTexto
func goRdpAoClipTexto(ctx unsafe.Pointer, utf8 *C.char, tam C.int) {
	sess := sessionFromHandle(ctx)
	if sess != nil && sess.OnClipboardText != nil {
		sess.OnClipboardText(C.GoStringN(utf8, tam))
	}
}

// Certificado é o que a interface precisa mostrar para decidir.
type Certificado struct {
	Host            string
	Porta           int
	NomeComum       string
	Assunto         string
	Emissor         string
	Digital         string // impressão digital oferecida agora
	DigitalAnterior string // preenchida só quando o certificado MUDOU
	Mudou           bool
}

// Decisão devolvida pela interface, na convenção da libfreerdp.
const (
	CertRecusar       = 0
	CertAceitarSalvo  = 1 // aceita e guarda em ~/.config/freerdp/server
	CertAceitarUmaVez = 2
)

//export goRdpCertNovo
func goRdpCertNovo(ctx unsafe.Pointer, host *C.char, porta C.uint16_t,
	nomeComum, assunto, emissor, digital *C.char, flags C.uint32_t) C.int {

	s := sessionFromHandle(ctx)
	if s == nil || s.OnCertificado == nil {
		return CertAceitarSalvo
	}
	return C.int(s.OnCertificado(Certificado{
		Host: C.GoString(host), Porta: int(porta),
		NomeComum: C.GoString(nomeComum), Assunto: C.GoString(assunto),
		Emissor: C.GoString(emissor), Digital: C.GoString(digital),
	}))
}

//export goRdpCertMudou
func goRdpCertMudou(ctx unsafe.Pointer, host *C.char, porta C.uint16_t,
	nomeComum, assunto, emissor, digitalNovo *C.char,
	assuntoAntigo, emissorAntigo, digitalAntigo *C.char, flags C.uint32_t) C.int {

	s := sessionFromHandle(ctx)
	if s == nil || s.OnCertificado == nil {
		// Sem gancho, RECUSA: certificado trocado é o caso que merece
		// alguém olhando, não um "ok" automático.
		return CertRecusar
	}
	return C.int(s.OnCertificado(Certificado{
		Host: C.GoString(host), Porta: int(porta),
		NomeComum: C.GoString(nomeComum), Assunto: C.GoString(assunto),
		Emissor: C.GoString(emissor), Digital: C.GoString(digitalNovo),
		DigitalAnterior: C.GoString(digitalAntigo), Mudou: true,
	}))
}

//export goRdpDispPronto
func goRdpDispPronto(ctx unsafe.Pointer) {
	s := sessionFromHandle(ctx)
	if s == nil || s.OnDisplayPronto == nil {
		return
	}
	s.OnDisplayPronto()
}
