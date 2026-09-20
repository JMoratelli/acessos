//go:build linux || windows

package main

import (
	"fmt"
	"os"
	"sync"

	"acessos-go/internal/telaproc"
)

// Este arquivo é o app rodando como PROCESSO-FILHO de uma sessão remota:
// sem janela, sem interface, só a biblioteca C da sessão de um lado e o
// socket para o processo principal do outro. O porquê está em
// internal/telaproc — em uma linha: para um SIGSEGV dentro da libfreerdp
// ou da libvncclient matar uma aba e não o app inteiro.
//
// Aqui mora só o que os dois protocolos compartilham; o que é de cada um
// está em telaworker_rdp.go e telaworker_vnc.go.
//
// NÃO renomeie para telaworker_linux.go: o sufixo _linux valeria como
// restrição de build por cima do //go:build acima, silenciosamente
// (mesma armadilha documentada em protocolos_tela.go). Os sufixos _rdp e
// _vnc são seguros porque não são nomes de GOOS/GOARCH.

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
	// Teto de memória DESTA sessão: se este processo descontrolar, ele sai
	// sozinho em vez de arrastar a máquina junto. Ver telaproc.VigiarMemoria.
	telaproc.VigiarMemoria(protocolo)

	switch protocolo {
	case "rdp":
		rodarWorkerRDP(c)
	case "vnc":
		rodarWorkerVNC(c)
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

// ------------------------------------------------------- bomba de quadros

// bombaTela é o lado do filho que entrega pixels: acumula dano, espera
// crédito e manda UM retângulo por vez. É igual nos dois protocolos — só
// muda de onde o framebuffer vem, e isso entra por `capturar`.
type bombaTela struct {
	c    *telaproc.Conn
	dano danoTela

	sujo     chan struct{} // sinal de "tem o que mandar"
	creditos chan struct{} // autorizações do processo principal
	parar    chan struct{}
	umaVez   sync.Once

	// capturar devolve uma cópia do framebuffer INTEIRO, em BGRX de 32
	// bits. stride é o passo de linha em bytes: o RDP alinha a mais que
	// w*4, o VNC não — quem sabe disso é cada worker.
	capturar func() (buf []byte, w, h, stride int)
}

func novaBomba(c *telaproc.Conn, capturar func() ([]byte, int, int, int)) *bombaTela {
	return &bombaTela{
		c:        c,
		sujo:     make(chan struct{}, 1),
		creditos: make(chan struct{}, 1),
		parar:    make(chan struct{}),
		capturar: capturar,
	}
}

func (b *bombaTela) encerrar() { b.umaVez.Do(func() { close(b.parar) }) }

func (b *bombaTela) sinalizar() {
	select {
	case b.sujo <- struct{}{}:
	default: // já tem sinal pendente; um só basta
	}
}

func (b *bombaTela) creditar() {
	select {
	case b.creditos <- struct{}{}:
	default:
	}
}

// rodar manda um quadro por crédito recebido, e só quando há algo para
// mandar. Pegar o crédito ANTES de esperar a sujeira é o que mantém o
// quadro fresco: assim ele é capturado no instante em que o processo
// principal está pronto para desenhar, e não guardado envelhecendo numa
// fila.
func (b *bombaTela) rodar() {
	for {
		select {
		case <-b.creditos:
		case <-b.parar:
			return
		}
		select {
		case <-b.sujo:
		case <-b.parar:
			return
		}
		if !b.enviarQuadro() {
			// Nada capturado (sessão ainda sem framebuffer). Devolve o
			// crédito e volta a esperar sujeira — sem isto o crédito se
			// perderia e a sessão congelaria para sempre.
			b.creditar()
		}
	}
}

func (b *bombaTela) enviarQuadro() bool {
	// tomar ANTES de capturar, nessa ordem — de propósito. dano.tomar()
	// esvazia o que estiver marcado sujo AGORA; capturar() copia o
	// framebuffer JÁ COM esse esvaziamento feito, então qualquer pintura
	// que chegue entre as duas chamadas (dano novo, marcado por uma
	// goroutine de callback à parte) fica ainda pendente em dano — não
	// desaparece — e será pega no próximo quadro.
	//
	// Na ordem invertida (capturar antes de tomar, como era) uma pintura
	// bem nesse meio-tempo entrava no retângulo tomado, era marcada
	// consumida, mas os pixels dela não estavam no snapshot já tirado:
	// aquele pedaço da tela ficava com conteúdo velho PARA SEMPRE, até
	// alguma dano futura por acaso cobrir a mesma área de novo. Era isso
	// que aparecia como remendo/glitch visual sem nenhuma pista no log.
	x, y, w, h, ok := b.dano.tomar()
	if !ok {
		return false
	}
	// capturar copia a tela inteira do lado C sob trava (ver internal/rdp
	// e internal/vnc). Recortar antes da cópia exigiria mexer nos shims em
	// C; o que economizamos aqui, que é o que pesa, é o laço POR PIXEL e o
	// tráfego no socket — ambos só sobre o retângulo sujo.
	buf, fw, fh, stride := b.capturar()
	if len(buf) == 0 || fw <= 0 || fh <= 0 {
		// Sessão ainda sem framebuffer (ver rodar()). O retângulo já foi
		// TOMADO acima — devolve pra dano em vez de descartar, senão essa
		// área nunca mais seria reenviada assim que o framebuffer chegasse.
		b.dano.juntar(x, y, w, h)
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
	if b.c.EnviarQuadro(q, recortarBGRXparaRGBA(buf, stride, x0, y0, x1-x0, y1-y0)) != nil {
		b.encerrar()
	}
	return true
}

// recortarBGRXparaRGBA converte um retângulo do framebuffer (BGRX de 32
// bits, com stride próprio) para RGBA empacotado. É o mesmo laço que antes
// rodava na thread de desenho do app a cada quadro, sobre a tela INTEIRA.
//
// RGBA e NÃO NRGBA, e a diferença custa caro: o paint do Gio só tem caminho
// direto para *image.Uniform e *image.RGBA (op/paint/paint.go) — qualquer
// outro tipo ele converte com draw.Draw, pixel a pixel, na THREAD QUE
// DESENHA e a cada tela publicada. Eram 8,3 MB alocados e ~2 ms por quadro
// em 1080p (33 MB e ~8-12 ms em 4K) para chegar ao MESMO byte, porque o
// alfa aqui é sempre 255 — e com alfa 255 premultiplicado e não
// premultiplicado são a mesma coisa. Trocar o tipo não muda um pixel; só
// tira esse trabalho do caminho que entrega teclado e mouse.
//
// Para quem mexer aqui: se este laço um dia gravar alfa != 255, o tipo tem
// de voltar a ser NRGBA (ou os canais passam a ter de vir premultiplicados),
// senão a cor sai ERRADA em vez de sair devagar.
func recortarBGRXparaRGBA(buf []byte, stride, x, y, w, h int) []byte {
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
