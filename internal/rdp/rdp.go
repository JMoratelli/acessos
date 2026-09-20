//go:build linux || windows

// Package rdp expõe um cliente RDP em Go por cima do shim C existente
// (rdpshim.c), que por sua vez fala com libfreerdp3.
package rdp

/*
#cgo pkg-config: freerdp3 freerdp-client3 winpr3
// -D__STDC_NO_THREADS__: o winpr/platform.h inclui <threads.h> quando o
// compilador diz suportar C11 threads, e o MinGW-w64 anuncia suporte sem
// trazer o cabeçalho. Achado no porte Windows do Carlos-Daniel-Dev, que
// esbarrou no mesmo erro.
// -lws2_32: o Winsock não é implícito como no Linux.
#cgo windows CFLAGS: -D__STDC_NO_THREADS__
#cgo windows LDFLAGS: -lws2_32
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
extern void goRdpAoCursor(void *ctx, int xhot, int yhot, int w, int h, uint8_t *mask);
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"acessos-go/internal/cgoregistry"
)

// Session é uma conexão RDP ativa.
//
// mu tem o mesmo papel que em internal/vnc.Session: serializar Close (que
// libera a Sessao em C) contra qualquer outro método em uso por outra
// goroutine no momento — sem isto, Close podia liberar memória bem no meio
// de uma cópia do framebuffer.
type Session struct {
	mu     sync.RWMutex
	s      *C.Sessao
	handle uintptr

	// quadro é o destino da cópia do framebuffer, REAPROVEITADO entre
	// quadros: era um make() de tela cheia por quadro (33 MB em 4K) que o
	// coletor tinha de recolher logo em seguida. Só a goroutine que bombeia
	// tela chama Framebuffer, e quem recebe o buffer o consome no mesmo
	// quadro, sem guardar — ver bombaTela, em cmd/acessos/telaworker.go.
	quadro []byte

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

	// OnCursor é chamado quando o servidor manda uma forma de cursor
	// nova, ou pede o padrão de volta (w=h=0, mask=nil). mask é 1 byte
	// por pixel (0/255) — uma CÓPIA, válida além do escopo do callback.
	OnCursor func(xhot, yhot, w, h int, mask []byte)
}

var registro = cgoregistry.New[Session]()

// New cria uma sessão RDP (ainda não conectada).
//
// Certificado: os dois ganchos do shim são expostos aqui. Eles BLOQUEIAM
// o handshake esperando a resposta — é essa espera que dá sentido à
// pergunta. Sem gancho definido o shim aplica o padrão seguro do
// gtk-frdp: aceita e guarda certificado novo, RECUSA mudança.
func New() *Session {
	sess := &Session{}
	sess.handle = registro.Registrar(sess)

	ctx := unsafe.Pointer(sess.handle) //nolint:govet // handle inteiro repassado como ponteiro opaco ao C
	sess.s = C.rs_criar(ctx,
		C.cb_atualizou(C.goRdpAoAtualizar),
		C.cb_redimensionou(C.goRdpAoRedimensionar),
		C.cb_desconectou(C.goRdpAoDesconectar),
		C.cb_certificado_novo(C.goRdpCertNovo),
		C.cb_certificado_mudou(C.goRdpCertMudou),
		C.cb_clip_texto(C.goRdpAoClipTexto),
		C.cb_disp_pronto(C.goRdpDispPronto),
		C.cb_cursor(C.goRdpAoCursor),
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

		morto, n, ok, detalhe := s.poll()
		if morto {
			return errors.New("conexão encerrada")
		}
		if n < 0 {
			return erroComDetalhe("erro aguardando dados do servidor", detalhe)
		}
		if n == 0 {
			continue
		}
		if !ok {
			return erroComDetalhe("conexão perdida", detalhe)
		}
	}
}

// erroComDetalhe anexa o motivo especifico do FreeRDP (capturado em
// erro_msg por rs_processar/rs_esperar, ver guardar_erro_desconexao em
// rdpshim.c) à mensagem genérica — sem isto todo log de queda em
// runtime dizia só "conexão perdida", pra qualquer causa, sem dar pra
// diferenciar um problema específico de servidor de uma queda de rede.
func erroComDetalhe(generico, detalhe string) error {
	if detalhe == "" {
		return errors.New(generico)
	}
	return fmt.Errorf("%s: %s", generico, detalhe)
}

// poll lê o detalhe de erro (se houver) AINDA sob s.mu.RLock: fazer isso
// fora do lock arriscaria ler s.s depois de um Close() concorrente já
// ter chamado rs_destruir nele.
func (s *Session) poll() (morto bool, n int, ok bool, detalhe string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if C.rs_morto(s.s) != 0 {
		return true, 0, false, ""
	}
	n = int(C.rs_esperar(s.s, 200)) // ms
	if n <= 0 {
		if n < 0 {
			detalhe = C.GoString(C.rs_erro_msg(s.s))
		}
		return false, n, false, detalhe
	}
	ok = C.rs_processar(s.s) != 0
	if !ok {
		detalhe = C.GoString(C.rs_erro_msg(s.s))
	}
	return false, n, ok, detalhe
}

// Framebuffer devolve uma CÓPIA do buffer em C (formato BGRX/32bpp, igual
// ao VNC — ver PIXEL_FORMAT_BGRX32 em rdpshim.c) junto com o stride: ao
// contrário do VNC, aqui o gdi pode alinhar cada linha a mais que w*4
// bytes, então stride precisa ser respeitado ao interpretar o buffer.
//
// Trava o framebuffer em C e copia DIRETO dele para cá, uma vez só. Antes
// o C fazia malloc+memcpy da tela inteira e este lado copiava a cópia:
// eram dois buffers de tela cheia por quadro (33 MB cada em 4K) para o
// mesmo resultado.
//
// A geometria vem junto, sob a mesma trava, porque ler
// largura/altura/stride/ponteiro em chamadas separadas dava combinação
// inconsistente (tamanho novo com ponteiro velho) durante um resize — e
// isso derrubava o processo. Ver fb_lock, em rdpshim.c.
//
// O destino é REAPROVEITADO entre quadros: quem chama usa o buffer e o
// devolve para o recorte, sem guardá-lo.
func (s *Session) Framebuffer() (buf []byte, w, h, stride int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.s == nil {
		return nil, 0, 0, 0
	}
	var cw, ch, cstride C.int
	ptr := C.rs_travar_quadro(s.s, &cw, &ch, &cstride)
	if ptr == nil {
		return nil, 0, 0, 0
	}
	defer C.rs_destravar_quadro(s.s)

	w, h, stride = int(cw), int(ch), int(cstride)
	n := stride * h
	if cap(s.quadro) < n {
		s.quadro = make([]byte, n)
	}
	buf = s.quadro[:n]
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
	registro.Remover(s.handle)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s == nil {
		return
	}
	C.rs_destruir(s.s)
	s.s = nil
}

func sessionFromHandle(ctx unsafe.Pointer) *Session {
	return registro.De(uintptr(ctx))
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

//export goRdpAoCursor
func goRdpAoCursor(ctx unsafe.Pointer, xhot, yhot, w, h C.int, mask *C.uint8_t) {
	sess := sessionFromHandle(ctx)
	if sess == nil || sess.OnCursor == nil {
		return
	}
	if mask == nil || w <= 0 || h <= 0 {
		sess.OnCursor(int(xhot), int(yhot), 0, 0, nil)
		return
	}
	n := int(w) * int(h)
	src := unsafe.Slice((*byte)(unsafe.Pointer(mask)), n)
	buf := make([]byte, n)
	copy(buf, src)
	sess.OnCursor(int(xhot), int(yhot), int(w), int(h), buf)
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
