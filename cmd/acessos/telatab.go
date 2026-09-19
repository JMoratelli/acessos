//go:build linux || windows

package main

import (
	"image"
	"sync/atomic"
	"time"

	"acessos-go/internal/telaproc"

	"gioui.org/app"
)

// creditarConformeVisibilidade libera o próximo quadro: na hora se a aba
// está à vista, depois de um respiro se não está. parar corta a espera
// quando a aba fecha.
func creditarConformeVisibilidade(proc *telaproc.Processo, ativa bool, parar <-chan struct{}) {
	if ativa {
		_ = proc.Credito()
		return
	}
	go func() {
		select {
		case <-time.After(intervaloSegundoPlano):
			_ = proc.Credito()
		case <-parar:
		}
	}()
}

// O que as abas de tela remota (VNC e RDP) compartilham do lado do
// PROCESSO PRINCIPAL: montar a tela a partir dos retângulos que o filho
// manda, e o vocabulário de "por que a sessão terminou".
//
// O lado de lá, dentro do filho, está em telaworker.go.

// fimSessao diz por que uma sessão terminou, e com isso o que fazer em
// seguida.
type fimSessao int

const (
	fimParar   fimSessao = iota // aba fechada: não volta
	fimReligar                  // pedido manual: reconecta já
	fimCaiu                     // caiu sozinha (inclusive crash do filho)

	// fimFalhou é a conexão que NÃO subiu: credencial recusada, servidor
	// dizendo não. Não entra no backoff de propósito — insistir de poucos
	// em poucos segundos com a senha errada não é persistência, é força
	// bruta contra o próprio parque, e em domínio Windows bloqueia a conta
	// do operador. Quem quiser tentar de novo clica em Reconectar, que é o
	// comportamento que estas abas sempre tiveram.
	fimFalhou
)

// intervaloSegundoPlano é de quanto em quanto tempo uma aba que NÃO está
// à vista pede o próximo quadro.
//
// Existe porque o crédito é o acelerador da sessão: enquanto a aba devolve
// crédito, o filho captura, converte e manda a tela inteira pelo socket. A
// aba visível deve fazer isso o mais rápido que conseguir desenhar; uma
// aba escondida, não — ninguém está olhando.
//
// Sem isto o custo era real e grande: no teste de estresse com 40 sessões
// VNC, NENHUMA delas à vista, passaram 62 GB de pixels em dois minutos.
// O desenho antigo (tela convertida dentro do Layout) não tinha esse
// problema por acidente: Layout só roda para a aba ativa. Ao mover a
// conversão para fora da thread de desenho, o freio saiu junto — este
// intervalo é o freio de volta.
//
// Um segundo, e não "nunca": a aba escondida continua viva e razoavelmente
// atual, então trocar para ela mostra a tela de agora em vez de uma
// lembrança, e a sessão não parece congelada ao voltar.
const intervaloSegundoPlano = time.Second

// aplicarQuadro cola o retângulo recebido na tela acumulada, criando ou
// trocando a imagem quando a resolução remota muda.
func aplicarQuadro(acum *image.NRGBA, q telaproc.Quadro, pix []byte) *image.NRGBA {
	tw, th := int(q.TotalW), int(q.TotalH)
	if tw <= 0 || th <= 0 {
		return acum
	}
	if acum == nil || acum.Rect.Dx() != tw || acum.Rect.Dy() != th {
		acum = image.NewNRGBA(image.Rect(0, 0, tw, th))
	}
	x, y, w, h := int(q.X), int(q.Y), int(q.W), int(q.H)
	if x < 0 || y < 0 || x+w > tw || y+h > th {
		return acum
	}
	for linha := 0; linha < h; linha++ {
		dst := acum.Pix[(y+linha)*acum.Stride+x*4:]
		copy(dst[:w*4], pix[linha*w*4:])
	}
	return acum
}

// rodarSessaoRemota sobe o processo-filho do protocolo indicado, conecta
// e entrega os eventos a lacoEventos até a sessão acabar — cuidando de
// sempre encerrar o processo e nunca vazar a goroutine vigia, que é a
// que traduz "fechar a aba"/"reconectar agora" num Fechar() que acorda a
// leitura do socket na hora em vez de deixá-la bloqueada.
//
// rdpTab e vncTab chamavam isto com o mesmo corpo, byte a byte, mudando
// só o protocolo e como conectar() monta a telaproc.Ligacao — lacoEventos
// fica de fora de propósito: RDP e VNC reagem a eventos diferentes
// (canal Display Control e diálogo de certificado só existem no RDP), e
// forçar isso numa função genérica só complicaria sem tirar duplicação
// de verdade.
func rodarSessaoRemota(
	protocolo, title, host string, port int,
	stop, religar <-chan struct{},
	conectar func(*telaproc.Processo) error,
	lacoEventos func(proc *telaproc.Processo, inicio time.Time) (falhou bool),
) fimSessao {
	proc, err := telaproc.Iniciar(protocolo)
	if err != nil {
		reg("[%s] %v", title, err)
		return fimCaiu
	}
	defer proc.Encerrar()

	var pedido atomic.Int32
	pedido.Store(int32(fimCaiu))
	saiu := make(chan struct{})
	defer close(saiu)
	go func() {
		select {
		case <-stop:
			pedido.Store(int32(fimParar))
		case <-religar:
			pedido.Store(int32(fimReligar))
		case <-saiu:
			return
		}
		_ = proc.Fechar()
	}()

	reg("[%s] conectando a %s:%d…", title, host, port)
	inicio := time.Now()
	if err := conectar(proc); err != nil {
		reg("[%s] não consegui pedir a conexão: %v", title, err)
		return fimSessao(pedido.Load())
	}

	if falhou := lacoEventos(proc, inicio); falhou {
		// A falha de conexão vence o que o vigia tiver anotado, EXCETO
		// fechar a aba: se o operador já mandou fechar, fechar é o que
		// vale.
		if fimSessao(pedido.Load()) == fimParar {
			return fimParar
		}
		return fimFalhou
	}
	if fimSessao(pedido.Load()) == fimCaiu {
		reg("[%s] processo %d da sessão terminou", title, proc.PID())
	}
	return fimSessao(pedido.Load())
}

// sessaoRemotaCfg agrupa o que gerenciarSessaoRemota precisa de uma aba
// de tela remota para tocar o laço de reconexão com backoff. Os dois
// hooks são opcionais (nil vira no-op) — é onde cada protocolo limpa o
// que só ele tem: o RDP apaga a tela congelada e esquece a resolução já
// pedida ao servidor: nenhum dos dois existe no VNC.
type sessaoRemotaCfg struct {
	title   string
	stop    <-chan struct{}
	religar <-chan struct{}
	w       *app.Window
	proc    *atomic.Pointer[telaproc.Processo]
	caiu    *atomic.Bool
	auto    *atomic.Bool

	// rodar sobe uma tentativa de sessão e bloqueia até ela terminar.
	rodar func() fimSessao

	// antesDeConectar roda logo antes de CADA tentativa, a primeira
	// inclusive — estado que precisa estar limpo antes de rodar() pedir
	// a conexão de novo.
	antesDeConectar func()
	// aoTerminar roda logo depois que uma tentativa termina, antes de
	// decidir o que fazer a partir do fimSessao que ela devolveu.
	aoTerminar func()
}

// gerenciarSessaoRemota é o laço de reconexão comum a rdpTab e vncTab:
// religa com backoff crescente até a aba fechar, e pula o backoff quando
// quem pediu foi um clique em Reconectar. Falha de credencial/política
// (fimFalhou) NUNCA entra no backoff — ver o comentário em fimFalhou:
// insistir de poucos em poucos segundos com a senha errada não é
// persistência, é força bruta contra o próprio parque.
func gerenciarSessaoRemota(cfg sessaoRemotaCfg) {
	rodarHook := func() fimSessao {
		if cfg.antesDeConectar != nil {
			cfg.antesDeConectar()
		}
		return cfg.rodar()
	}

	attempt := 0
	for {
		fim := rodarHook()
		cfg.proc.Store(nil)
		if cfg.aoTerminar != nil {
			cfg.aoTerminar()
		}
		cfg.w.Invalidate()

		switch fim {
		case fimParar:
			return
		case fimReligar:
			attempt = 0
			continue
		case fimFalhou:
			cfg.caiu.Store(true)
			cfg.w.Invalidate()
			select {
			case <-cfg.religar:
				attempt = 0
				continue
			case <-cfg.stop:
				return
			}
		}

		cfg.caiu.Store(true)
		cfg.w.Invalidate()
		if !cfg.auto.Load() {
			// Reconexão automática desligada: fica parada até alguém
			// clicar em Reconectar.
			select {
			case <-cfg.religar:
				attempt = 0
				continue
			case <-cfg.stop:
				return
			}
		}

		wait := backoffSchedule[min(attempt, len(backoffSchedule)-1)]
		attempt++
		reg("[%s] reconectando em %s (tentativa #%d)", cfg.title, wait, attempt)
		select {
		case <-time.After(wait):
		case <-cfg.religar:
			attempt = 0
		case <-cfg.stop:
			return
		}
	}
}

// publicarTela congela a tela acumulada numa imagem nova e a entrega ao
// desenho. A cópia é o preço de não precisar de trava nenhuma do lado do
// Gio: a imagem publicada não muda mais depois de publicada, então a
// textura pode subir para a GPU com calma enquanto o próximo retângulo já
// está sendo colado no acumulador.
//
// Antes disto a interface fazia, A CADA QUADRO DELA, uma cópia da tela
// inteira vinda do C mais uma conversão BGRX->NRGBA pixel a pixel — mesmo
// quando nada tinha mudado na sessão remota. Agora a cópia acontece uma
// vez por quadro REMOTO, e fora da thread que desenha.
func publicarTela(destino *atomic.Pointer[image.NRGBA], acum *image.NRGBA) {
	if acum == nil {
		return
	}
	pub := image.NewNRGBA(acum.Rect)
	copy(pub.Pix, acum.Pix)
	destino.Store(pub)
}
