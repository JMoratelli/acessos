//go:build linux || windows

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"acessos-go/internal/rdp"
	"acessos-go/internal/telaproc"
)

// O lado RDP do processo-filho. O que é comum aos dois protocolos (dano,
// bomba de quadros, conversão de pixel) está em telaworker.go.

type workerRDP struct {
	c     *telaproc.Conn
	sess  *rdp.Session
	bomba *bombaTela

	// classCursor roda só na thread de callbacks da libfreerdp — é ela
	// que entrega forma de cursor —, então não precisa de trava.
	classCursor classificadorCursor

	certResp chan int // resposta do diálogo de certificado
}

func rodarWorkerRDP(c *telaproc.Conn) {
	sess := rdp.New()
	wk := &workerRDP{
		c:    c,
		sess: sess,
		bomba: novaBomba(c, func() ([]byte, int, int, int) {
			return sess.Framebuffer()
		}),
		certResp: make(chan int, 1),
	}
	wk.ligarCallbacks()
	go wk.bomba.rodar()
	wk.lacoComandos()
	wk.bomba.encerrar()
}

func (wk *workerRDP) ligarCallbacks() {
	s := wk.sess
	s.OnUpdate = func(x, y, w, h int) {
		wk.bomba.dano.juntar(x, y, w, h)
		wk.bomba.sinalizar()
	}
	s.OnResize = func(w, h int) {
		wk.bomba.dano.tudo()
		wk.bomba.sinalizar()
	}
	s.OnCursor = func(_, _, w, h int, mask []byte) {
		_ = wk.c.EnviarCursor(uint32(wk.classCursor.classificar(w, h, mask)))
	}
	s.OnClipboardText = func(texto string) {
		_ = wk.c.Enviar(telaproc.EvtClipboard, []byte(texto))
	}
	s.OnDisplayPronto = func() {
		_ = wk.c.Enviar(telaproc.EvtDisplayPronto, nil)
	}
	// OnCertificado roda NA THREAD DE REDE da libfreerdp e BLOQUEIA o
	// handshake até responder — é isso que dá sentido à pergunta. Aqui a
	// espera atravessa o socket: quem mostra o diálogo é o processo
	// principal. Se o canal cair enquanto esperamos, recusar é a saída
	// segura (mesma convenção do gtk-frdp para certificado sem resposta).
	s.OnCertificado = func(c rdp.Certificado) int {
		err := wk.c.EnviarJSON(telaproc.EvtCertPedido, telaproc.Certificado{
			Host: c.Host, Porta: c.Porta,
			NomeComum: c.NomeComum, Assunto: c.Assunto, Emissor: c.Emissor,
			Digital: c.Digital, DigitalAnterior: c.DigitalAnterior,
			Mudou: c.Mudou,
		})
		if err != nil {
			return rdp.CertRecusar
		}
		select {
		case d := <-wk.certResp:
			return d
		case <-wk.bomba.parar:
			return rdp.CertRecusar
		}
	}
}

// lacoComandos é o ÚNICO leitor do socket. Sai quando o processo principal
// fecha o canal (aba fechada, ou o app inteiro saindo).
func (wk *workerRDP) lacoComandos() {
	for {
		tipo, corpo, err := wk.c.Ler()
		if err != nil {
			return
		}
		switch tipo {
		case telaproc.CmdConectar:
			var l telaproc.Ligacao
			if err := json.Unmarshal(corpo, &l); err != nil {
				fmt.Fprintf(os.Stderr, "[filho rdp] conectar inválido: %v\n", err)
				return
			}
			go wk.conectar(l)

		case telaproc.CmdCredito:
			wk.bomba.creditar()

		case telaproc.CmdCertResposta:
			if len(corpo) > 0 {
				select {
				case wk.certResp <- int(corpo[0]):
				default:
				}
			}

		case telaproc.CmdPonteiroMover:
			if x, y, ok := telaproc.LerPonteiroMover(corpo); ok {
				wk.sess.PointerMove(x, y)
			}

		case telaproc.CmdPonteiroBotao:
			if x, y, b, pressionado, ok := telaproc.LerPonteiroBotao(corpo); ok {
				wk.sess.PointerButton(x, y, b, pressionado)
			}

		case telaproc.CmdPonteiroRoda:
			if eixo, passos, ok := telaproc.LerPonteiroRoda(corpo); ok {
				wk.sess.PointerWheel(eixo, passos)
			}

		case telaproc.CmdTecla:
			// No RDP a tecla é o KEYCODE X11 (ver telaproc.Processo.Tecla).
			if kc, pressionada, ok := telaproc.LerTecla(corpo); ok {
				wk.sess.KeyEvent(kc, pressionada)
			}

		case telaproc.CmdClipboard:
			wk.sess.SendClipboardText(string(corpo))

		case telaproc.CmdResize:
			if w, h, ok := telaproc.LerResize(corpo); ok {
				wk.sess.RequestResize(w, h)
			}
		}
	}
}

func (wk *workerRDP) conectar(l telaproc.Ligacao) {
	wk.sess.SetCredentials(l.Usuario, l.Senha, l.Dominio)
	if err := wk.sess.Connect(l.Host, l.Porta); err != nil {
		ce := err.(*rdp.ConnectError)
		_ = wk.c.EnviarJSON(telaproc.EvtFalha, telaproc.Falha{
			Mensagem: ce.Message, AuthFalhou: ce.AuthFailed,
		})
		_ = wk.c.Fechar()
		return
	}
	_ = wk.c.Enviar(telaproc.EvtConectado, nil)
	// A tela inteira é "nova" agora: sem isto o primeiro quadro só sairia
	// quando algo se mexesse do lado de lá.
	wk.bomba.dano.tudo()
	wk.bomba.sinalizar()

	err := wk.sess.Run(wk.bomba.parar)
	motivo := "sessão encerrada"
	if err != nil {
		motivo = err.Error()
	}
	_ = wk.c.Enviar(telaproc.EvtDesconectado, []byte(motivo))

	// os.Exit sem sess.Close() é DE PROPÓSITO, não esquecimento: o crash
	// catalogado no BACKLOG.md §7 mora justamente na desmontagem do canal
	// dinâmico (dvcman_channel_close), e não há nada a liberar que o fim
	// do processo não libere melhor. Sair aqui também garante que um filho
	// nunca fique órfão segurando uma sessão depois de cair.
	os.Exit(0)
}
