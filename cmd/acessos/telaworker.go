//go:build linux || windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"acessos-go/internal/rdp"
	"acessos-go/internal/telaproc"
)

// Este arquivo é o app rodando como PROCESSO-FILHO de uma sessão remota:
// sem janela, sem interface, só a biblioteca C da sessão de um lado e o
// socket para o processo principal do outro. O porquê está em
// internal/telaproc — em uma linha: para um SIGSEGV dentro da libfreerdp
// matar uma aba e não o app inteiro.
//
// NÃO renomeie para telaworker_linux.go: o sufixo _linux valeria como
// restrição de build por cima do //go:build acima, silenciosamente
// (mesma armadilha documentada em protocolos_tela.go).

// modoWorker é chamado no começo de main(). Devolve false quando os
// argumentos não são de um filho, e aí o app segue como app normal.
func modoWorker() bool {
	if len(os.Args) != 5 || os.Args[1] != telaproc.ArgWorker {
		return false
	}
	protocolo, endereco, token := os.Args[2], os.Args[3], os.Args[4]

	c, err := telaproc.Atender(endereco, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[filho %s] %v\n", protocolo, err)
		os.Exit(1)
	}
	switch protocolo {
	case "rdp":
		rodarWorkerRDP(c)
	default:
		fmt.Fprintf(os.Stderr, "[filho] protocolo desconhecido: %q\n", protocolo)
		os.Exit(1)
	}
	os.Exit(0)
	return true
}

// ---------------------------------------------------------------- dano
//
// danoTela acumula o que mudou na tela remota entre um quadro e o
// seguinte. A biblioteca reporta retângulos pequenos e frequentes (um
// cursor piscando são poucos pixels); mandar a tela inteira a cada um
// desses seria jogar megabytes no socket para nada. Guardamos a CAIXA que
// envolve tudo o que sujou — é uma aproximação grosseira de propósito:
// manter uma lista de retângulos custaria mais do que economiza no caso
// comum, que é uma região só.
type danoTela struct {
	mu             sync.Mutex
	valido         bool
	x0, y0, x1, y1 int // x1 e y1 exclusivos
}

func (d *danoTela) juntar(x, y, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.valido {
		d.valido, d.x0, d.y0, d.x1, d.y1 = true, x, y, x+w, y+h
		return
	}
	d.x0 = min(d.x0, x)
	d.y0 = min(d.y0, y)
	d.x1 = max(d.x1, x+w)
	d.y1 = max(d.y1, y+h)
}

// tudo marca a tela inteira como suja sem saber o tamanho ainda: um
// retângulo deliberadamente maior que qualquer tela, recortado depois
// contra o tamanho real do quadro.
func (d *danoTela) tudo() { d.juntar(0, 0, 1<<20, 1<<20) }

// tomar devolve o acumulado e zera. ok=false quando nada mudou.
func (d *danoTela) tomar() (x, y, w, h int, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.valido {
		return 0, 0, 0, 0, false
	}
	x, y, w, h = d.x0, d.y0, d.x1-d.x0, d.y1-d.y0
	d.valido = false
	return x, y, w, h, true
}

// ----------------------------------------------------------- worker RDP

type workerRDP struct {
	c    *telaproc.Conn
	sess *rdp.Session

	// classCursor roda só na thread de callbacks da libfreerdp — é ela
	// que entrega forma de cursor —, então não precisa de trava.
	classCursor classificadorCursor

	dano     danoTela
	sujo     chan struct{} // sinal de "tem o que mandar"
	creditos chan struct{} // autorizações do processo principal
	certResp chan int      // resposta do diálogo de certificado
	parar    chan struct{}
	umaVez   sync.Once
}

func rodarWorkerRDP(c *telaproc.Conn) {
	wk := &workerRDP{
		c:        c,
		sess:     rdp.New(),
		sujo:     make(chan struct{}, 1),
		creditos: make(chan struct{}, 1),
		certResp: make(chan int, 1),
		parar:    make(chan struct{}),
	}
	wk.ligarCallbacks()
	go wk.bombaQuadros()
	wk.lacoComandos()
	wk.encerrar()
}

func (wk *workerRDP) encerrar() { wk.umaVez.Do(func() { close(wk.parar) }) }

func (wk *workerRDP) sinalizar() {
	select {
	case wk.sujo <- struct{}{}:
	default: // já tem sinal pendente; um só basta
	}
}

func (wk *workerRDP) ligarCallbacks() {
	s := wk.sess
	s.OnUpdate = func(x, y, w, h int) {
		wk.dano.juntar(x, y, w, h)
		wk.sinalizar()
	}
	s.OnResize = func(w, h int) {
		wk.dano.tudo()
		wk.sinalizar()
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
		case <-wk.parar:
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
			select {
			case wk.creditos <- struct{}{}:
			default:
			}

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
	wk.dano.tudo()
	wk.sinalizar()

	err := wk.sess.Run(wk.parar)
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

// bombaQuadros manda um quadro por crédito recebido, e só quando há algo
// para mandar. Pegar o crédito ANTES de esperar a sujeira é o que mantém o
// quadro fresco: assim ele é capturado no instante em que o processo
// principal está pronto para desenhar, e não guardado envelhecendo numa
// fila.
func (wk *workerRDP) bombaQuadros() {
	for {
		select {
		case <-wk.creditos:
		case <-wk.parar:
			return
		}
		select {
		case <-wk.sujo:
		case <-wk.parar:
			return
		}
		if !wk.enviarQuadro() {
			// Nada capturado (sessão ainda sem framebuffer). Devolve o
			// crédito e volta a esperar sujeira — sem isto o crédito se
			// perderia e a sessão congelaria para sempre.
			select {
			case wk.creditos <- struct{}{}:
			default:
			}
		}
	}
}

func (wk *workerRDP) enviarQuadro() bool {
	// Framebuffer copia a tela inteira do lado C sob trava (ver
	// internal/rdp). Recortar antes da cópia exigiria mexer no shim em C;
	// o que economizamos aqui, que é o que pesa, é o laço POR PIXEL e o
	// tráfego no socket — ambos só sobre o retângulo sujo.
	buf, fw, fh, stride := wk.sess.Framebuffer()
	if len(buf) == 0 || fw <= 0 || fh <= 0 {
		return false
	}
	x, y, w, h, ok := wk.dano.tomar()
	if !ok {
		return false
	}
	// Recorte contra o tamanho atual: o dano foi acumulado com a geometria
	// de antes, e um resize no meio do caminho a teria mudado.
	x0, y0 := max(x, 0), max(y, 0)
	x1, y1 := min(x+w, fw), min(y+h, fh)
	if x1 <= x0 || y1 <= y0 {
		return false
	}
	q := telaproc.Quadro{
		X: int32(x0), Y: int32(y0), W: int32(x1 - x0), H: int32(y1 - y0),
		TotalW: int32(fw), TotalH: int32(fh),
	}
	if wk.c.EnviarQuadro(q, recortarBGRXparaNRGBA(buf, stride, x0, y0, x1-x0, y1-y0)) != nil {
		wk.encerrar()
		return true
	}
	return true
}

// recortarBGRXparaNRGBA converte um retângulo do framebuffer (BGRX de 32
// bits, com stride próprio) para NRGBA empacotado, que é o que o Gio
// consome direto do outro lado. É o mesmo laço que antes rodava na thread
// de desenho do app a cada quadro, sobre a tela INTEIRA.
func recortarBGRXparaNRGBA(buf []byte, stride, x, y, w, h int) []byte {
	out := make([]byte, w*h*4)
	for linha := 0; linha < h; linha++ {
		src := buf[(y+linha)*stride+x*4:]
		dst := out[linha*w*4:]
		for i := 0; i < w; i++ {
			dst[i*4+0] = src[i*4+2] // R <- B
			dst[i*4+1] = src[i*4+1] // G
			dst[i*4+2] = src[i*4+0] // B <- R
			dst[i*4+3] = 255
		}
	}
	return out
}
