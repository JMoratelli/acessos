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

	// enviosFora é a fila de EvtCursor/EvtClipboard — ver
	// enviarForaDoProcessamento logo abaixo, motivo de existir.
	enviosFora chan func()

	// dispPronto sinaliza o disparo de OnDisplayPronto por um canal
	// PRÓPRIO, separado de enviosFora, com uma vaga só. OnCursor/
	// OnClipboardText descrevem "qual é o estado AGORA" — perder um
	// no meio de uma rajada não importa, o próximo já corrige. Já
	// OnDisplayPronto é um evento ÚNICO por sessão (dispara uma vez,
	// quando o canal Display Control termina o handshake): se
	// competisse pela mesma fila de 8 vagas e caísse numa rajada de
	// cursor/clipboard cheia, ele se perdia PRA SEMPRE — nada mais
	// reenvia — e a sessão ficava presa na resolução padrão do
	// servidor até a janela ser redimensionada manualmente. Era
	// exatamente esse o sintoma: funcionava "às vezes", dependendo de
	// quão cheia a fila compartilhada estava bem naquele instante.
	dispPronto chan struct{}
}

func rodarWorkerRDP(c *telaproc.Conn) {
	sess := rdp.New()
	wk := &workerRDP{
		c:    c,
		sess: sess,
		bomba: novaBomba(c, func() ([]byte, int, int, int) {
			return sess.Framebuffer()
		}),
		certResp:   make(chan int, 1),
		enviosFora: make(chan func(), 8),
		dispPronto: make(chan struct{}, 1),
	}
	wk.ligarCallbacks()
	go wk.bomba.rodar()
	go wk.despacharEnviosFora()
	go wk.despacharDispPronto()
	wk.lacoComandos()
	wk.bomba.encerrar()
}

// enviarForaDoProcessamento manda algo por uma goroutine PRÓPRIA, nunca
// bloqueando quem chamou. Existe porque OnCursor/OnClipboardText/
// OnDisplayPronto (ver ligarCallbacks) rodam dentro de rs_processar — a
// MESMA chamada C que processa a sessão RDP inteira (ver Session.Run,
// internal/rdp/rdp.go) —, e Conn.Enviar disputa o mesmo mutex de
// escrita que a bomba de quadros usa para mandar um EvtQuadro de vários
// MB. Uma dessas três chamando Enviar DIRETO e travando esperando esse
// mutex travava a sessão INTEIRA até a escrita do quadro (ou o timeout
// dela) resolver — medido em auditoria: no pior caso os 5s do timeout
// de escrita (protocolo.go) estouravam e derrubavam a conexão por causa
// de um simples cursor piscando, sem nada na tela explicando por quê.
//
// Fila pequena com descarte no cheio, de propósito: cursor e clipboard
// são "qual é o estado AGORA", não uma entrega que precise ser garantida
// — perder um no meio de uma rajada não importa, o próximo já corrige.
func (wk *workerRDP) enviarForaDoProcessamento(f func()) {
	select {
	case wk.enviosFora <- f:
	default:
	}
}

func (wk *workerRDP) despacharEnviosFora() {
	for f := range wk.enviosFora {
		f()
	}
}

// despacharDispPronto entrega o EvtDisplayPronto sozinho, fora da fila
// compartilhada — ver o comentário em dispPronto. Em loop (e não só uma
// vez) por segurança, caso o canal Display Control caia e reconecte
// dentro da MESMA sessão RDP, disparando hook_disp_caps de novo.
// wk.c.Enviar pode bloquear (mesmo write-mutex do quadro), mas isto
// roda numa goroutine própria que não faz mais nada, então bloquear
// aqui nunca atrasa rs_processar nem o resto da sessão.
func (wk *workerRDP) despacharDispPronto() {
	for range wk.dispPronto {
		_ = wk.c.Enviar(telaproc.EvtDisplayPronto, nil)
	}
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
	// O ponto quente (xhot/yhot) entra na conta: é o sinal mais forte de
	// QUE cursor é (quina superior esquerda = seta, meio = redimensionar)
	// e era descartado aqui. Ver cursorforma.go.
	//
	// As três chamadas abaixo classificam/copiam o que precisam NA HORA
	// (classificar() já roda síncrono; texto é string, já é cópia) e só
	// ENFILEIRAM o envio de verdade — nunca chamam Conn.Enviar direto
	// daqui. Ver o comentário grande em enviarForaDoProcessamento.
	s.OnCursor = func(xhot, yhot, w, h int, mask []byte) {
		forma := uint32(wk.classCursor.classificar(xhot, yhot, w, h, mask))
		wk.enviarForaDoProcessamento(func() { _ = wk.c.EnviarCursor(forma) })
	}
	s.OnClipboardText = func(texto string) {
		wk.enviarForaDoProcessamento(func() { _ = wk.c.Enviar(telaproc.EvtClipboard, []byte(texto)) })
	}
	s.OnDisplayPronto = func() {
		select {
		case wk.dispPronto <- struct{}{}:
		default:
		}
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
