//go:build linux

package main

import (
	"fmt"
	"os"

	"acessos-go/internal/rdp"
	"acessos-go/internal/vnc"
)

// remoto é o pouco que a captura precisa de uma sessão: conectar, bombear,
// mover o ponteiro, ler um quadro, fechar. Os dois bindings já têm essa
// forma — só divergem em detalhe de assinatura.
//
// Os DOIS adaptadores moram neste arquivo de propósito. São o caso clássico
// de irmão que sai de sincronia (o CLAUDE.md tem a lista de vezes que isso
// mordeu aqui: ssh_pdv.go/windows.go, sshtab.go/sftptab.go); lado a lado,
// a diferença fica na cara de quem mexer num deles.
type remoto interface {
	// aoCursor precisa estar ligado ANTES de conectar: a primeira forma
	// costuma chegar junto com o primeiro quadro.
	aoCursor(func(xhot, yhot, w, h int, mask []byte))
	credenciais(usuario, senha, dominio string)
	conectar(host string, porta int) error
	bombear(parar <-chan struct{}) error
	// quadro devolve BGRX 32bpp. O stride vem junto porque no RDP o gdi
	// alinha a linha a mais que w*4 — no VNC é sempre w*4.
	quadro() (buf []byte, w, h, stride int)
	moverPonteiro(x, y int)
	fechar()
}

// ---------------------------------------------------------------- RDP

type remotoRDP struct {
	s *rdp.Session
	// certMudouOK afrouxa a recusa de certificado trocado; ver a flag.
	certMudouOK bool
	// desconectou recebe o motivo mandado pelo servidor, se vier.
	desconectou chan string
}

func novoRDP(certMudouOK bool, desconectou chan string) *remotoRDP {
	r := &remotoRDP{s: rdp.New(), certMudouOK: certMudouOK, desconectou: desconectou}

	// Aceita o certificado NOVO só para esta execução (CertAceitarUmaVez)
	// em vez de gravar em ~/.config/freerdp/server: ferramenta de
	// diagnóstico não tem por que mexer no armazém de confiança de
	// ninguém. Certificado TROCADO segue recusado por padrão — é o caso
	// que merece alguém olhando, e é o padrão do binding quando não há
	// gancho (ver goRdpCertMudou).
	r.s.OnCertificado = func(c rdp.Certificado) int {
		if c.Mudou {
			fmt.Fprintf(os.Stderr,
				"!! certificado MUDOU em %s:%d\n   antes: %s\n   agora: %s\n",
				c.Host, c.Porta, c.DigitalAnterior, c.Digital)
			if !r.certMudouOK {
				return rdp.CertRecusar
			}
			return rdp.CertAceitarUmaVez
		}
		fmt.Fprintf(os.Stderr, "   certificado de %s:%d — %s\n", c.Host, c.Porta, c.Digital)
		return rdp.CertAceitarUmaVez
	}
	r.s.OnDisconnect = func(motivo string) {
		select {
		case r.desconectou <- motivo:
		default:
		}
	}
	return r
}

func (r *remotoRDP) aoCursor(f func(int, int, int, int, []byte)) { r.s.OnCursor = f }
func (r *remotoRDP) credenciais(u, s, d string)                  { r.s.SetCredentials(u, s, d) }
func (r *remotoRDP) conectar(host string, porta int) error       { return r.s.Connect(host, porta) }
func (r *remotoRDP) bombear(parar <-chan struct{}) error         { return r.s.Run(parar) }
func (r *remotoRDP) quadro() ([]byte, int, int, int)             { return r.s.Framebuffer() }
func (r *remotoRDP) moverPonteiro(x, y int)                      { r.s.PointerMove(x, y) }
func (r *remotoRDP) fechar()                                     { r.s.Close() }

// ---------------------------------------------------------------- VNC

type remotoVNC struct{ s *vnc.Session }

func novoVNC() *remotoVNC { return &remotoVNC{s: vnc.New()} }

func (r *remotoVNC) aoCursor(f func(int, int, int, int, []byte)) { r.s.OnCursor = f }

// O VNC clássico não tem domínio, e usuário costuma ser vazio (só senha).
func (r *remotoVNC) credenciais(u, s, _ string) { r.s.SetCredentials(u, s) }

func (r *remotoVNC) conectar(host string, porta int) error { return r.s.Connect(host, porta) }
func (r *remotoVNC) bombear(parar <-chan struct{}) error   { return r.s.Run(parar) }

// No VNC o quadro é contíguo: stride é sempre w*4 (ver Framebuffer, em
// internal/vnc/vnc.go). O RDP é que precisa do stride de verdade.
func (r *remotoVNC) quadro() ([]byte, int, int, int) {
	buf, w, h := r.s.Framebuffer()
	return buf, w, h, w * 4
}

// buttons=0: passeio NÃO clica. Ver o cabeçalho de main.go.
func (r *remotoVNC) moverPonteiro(x, y int) { r.s.PointerEvent(x, y, 0) }
func (r *remotoVNC) fechar()                { r.s.Close() }
