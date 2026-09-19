//go:build linux || windows

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"acessos-go/internal/telaproc"
	"acessos-go/internal/vnc"
)

// O lado VNC do processo-filho. O que é comum aos dois protocolos (dano,
// bomba de quadros, conversão de pixel) está em telaworker.go.
//
// É mais simples que o RDP porque o VNC não tem canal de resolução
// dinâmica (Display Control), não negocia certificado e não manda roda do
// mouse por canal próprio. Em compensação fala de botão por MÁSCARA, e de
// tecla por KEYSYM — ver telaproc.CmdPonteiroMascara e Processo.Tecla.

type workerVNC struct {
	c     *telaproc.Conn
	sess  *vnc.Session
	bomba *bombaTela

	// classCursor roda só na thread de callbacks da libvncclient, então
	// não precisa de trava.
	classCursor classificadorCursor

	// enviosFora é a fila de EvtCursor/EvtClipboard — ver
	// enviarForaDoProcessamento em telaworker_rdp.go, motivo de existir
	// (mesmo risco aqui: OnCursor/OnCutText rodam na thread de eventos
	// da libvncclient, que é a mesma que processa a sessão inteira).
	enviosFora chan func()
}

func rodarWorkerVNC(c *telaproc.Conn) {
	sess := vnc.New()
	wk := &workerVNC{
		c:    c,
		sess: sess,
		bomba: novaBomba(c, func() ([]byte, int, int, int) {
			// O VNC não tem stride próprio: o shim entrega as linhas
			// coladas, então o passo é exatamente w*4.
			buf, w, h := sess.Framebuffer()
			return buf, w, h, w * 4
		}),
		enviosFora: make(chan func(), 8),
	}
	wk.ligarCallbacks()
	go wk.bomba.rodar()
	go wk.despacharEnviosFora()
	wk.lacoComandos()
	wk.bomba.encerrar()
}

// enviarForaDoProcessamento: mesma função e mesmo motivo de
// telaworker_rdp.go — nunca chamar Conn.Enviar direto de dentro de um
// callback da libvncclient, que roda na mesma thread que processa a
// sessão inteira e disputaria o mutex de escrita com um EvtQuadro grande.
func (wk *workerVNC) enviarForaDoProcessamento(f func()) {
	select {
	case wk.enviosFora <- f:
	default:
	}
}

func (wk *workerVNC) despacharEnviosFora() {
	for f := range wk.enviosFora {
		f()
	}
}

func (wk *workerVNC) ligarCallbacks() {
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
	s.OnCursor = func(xhot, yhot, w, h int, mask []byte) {
		forma := uint32(wk.classCursor.classificar(xhot, yhot, w, h, mask))
		wk.enviarForaDoProcessamento(func() { _ = wk.c.EnviarCursor(forma) })
	}
	s.OnCutText = func(texto string) {
		wk.enviarForaDoProcessamento(func() { _ = wk.c.Enviar(telaproc.EvtClipboard, []byte(texto)) })
	}
}

// lacoComandos é o ÚNICO leitor do socket. Sai quando o processo principal
// fecha o canal (aba fechada, ou o app inteiro saindo).
func (wk *workerVNC) lacoComandos() {
	for {
		tipo, corpo, err := wk.c.Ler()
		if err != nil {
			return
		}
		switch tipo {
		case telaproc.CmdConectar:
			var l telaproc.Ligacao
			if err := json.Unmarshal(corpo, &l); err != nil {
				fmt.Fprintf(os.Stderr, "[filho vnc] conectar inválido: %v\n", err)
				return
			}
			go wk.conectar(l)

		case telaproc.CmdCredito:
			wk.bomba.creditar()

		case telaproc.CmdPonteiroMascara:
			if x, y, mascara, ok := telaproc.LerPonteiroMascara(corpo); ok {
				wk.sess.PointerEvent(x, y, mascara)
			}

		case telaproc.CmdTecla:
			// No VNC a tecla é o KEYSYM X11 (ver telaproc.Processo.Tecla).
			if ks, pressionada, ok := telaproc.LerTecla(corpo); ok {
				wk.sess.KeyEvent(ks, pressionada)
			}

		case telaproc.CmdClipboard:
			wk.sess.SendCutText(string(corpo))
		}
	}
}

func (wk *workerVNC) conectar(l telaproc.Ligacao) {
	wk.sess.SetCredentials(l.Usuario, l.Senha)
	if err := wk.sess.Connect(l.Host, l.Porta); err != nil {
		ce := err.(*vnc.ConnectError)
		_ = wk.c.EnviarJSON(telaproc.EvtFalha, telaproc.Falha{
			Mensagem:       ce.Message,
			AuthFalhou:     ce.AuthFailed,
			PrecisaUsuario: ce.NeedsUsername,
			Recusado:       ce.Rejected,
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

	// Sair sem sess.Close(), pelo mesmo motivo do lado RDP: não há nada a
	// liberar que o fim do processo não libere melhor, e a desmontagem é
	// justamente onde uma biblioteca C costuma explodir. Também garante
	// que um filho nunca fique órfão segurando uma sessão depois de cair.
	os.Exit(0)
}
