//go:build linux

package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// Entrega de teclas às sessões remotas SEM bloquear quem chamou.
//
// ----------------------------------------------------------------------
// POR QUE: o bug dos "botões que travam" (Linux)
// ----------------------------------------------------------------------
//
// O grab pega carona na fila default do Wayland — a MESMA que o Gio
// despacha (é o que grab_wayland.c documenta, e está certo: ler o socket por
// conta própria correria com a leitura do Gio). O efeito colateral é que o
// callback de tecla roda NA GOROUTINE QUE DESPACHA O WAYLAND, que neste
// backend é a própria goroutine do laço de eventos: app.Window.Event() cai em
// driver.Event() (third_party/gio/app/os_wayland.go:1582), que chama
// dispatch() quando não há evento pendente — e é o dispatch que executa os
// callbacks C do grab.
//
// De lá, t.HandleKey() ia direto em Conn.Enviar(), que é uma escrita
// BLOQUEANTE no socket do processo-filho, com prazoEscrita = 5s
// (internal/telaproc/protocolo.go). Worker ocupado ou buffer de socket cheio
// = laço de eventos parado por até cinco segundos. Nesse tempo não há clique,
// hover nem redesenho: a interface inteira fica morta.
//
// Batia com o sintoma relatado: raro, e NUNCA sem uma sessão aberta —
// HandleKey é no-op no painel, no SFTP e na execução em massa; só VNC, RDP e
// SSH escrevem no socket.
//
// O mesmo caminho era usado pela thread de repetição de tecla (rep_loop, em
// grab_wayland.c), que é um pthread próprio — segundo jeito de chegar no
// mesmo bloqueio.
//
// ----------------------------------------------------------------------
// COMO ESTE ARQUIVO RESOLVE
// ----------------------------------------------------------------------
//
// A DECISÃO (é atalho do app? tem diálogo aberto? a aba quer teclado?)
// continua na goroutine do callback: é cálculo puro, rápido, e mantém F12 e
// Ctrl+W respondendo na hora.
//
// Só a ENTREGA — a parte que fala com o socket — vai pra uma goroutine
// dedicada. Uma só, FIFO ESTRITA: a ordem das teclas é inegociável, porque um
// "soltou" chegando antes do "apertou" deixa a tecla PRESA do lado remoto.
//
// enfileirarTecla NUNCA bloqueia. Nem um milissegundo. Esse é o ponto
// inteiro deste arquivo.
//
// NÃO volte a chamar t.HandleKey() direto do callback do grab. Use
// enfileirarTecla().

// teclaPendente é uma tecla esperando a vez de ir pro socket.
type teclaPendente struct {
	t       Tab
	keysym  uint32
	keycode uint32
	pressed bool
}

// filaTeclas é a fila de entrega. É struct, e não um punhado de variáveis
// soltas, só para os testes poderem criar instâncias isoladas — em produção
// existe uma só (ver a variável teclas, abaixo).
//
// É uma fatia sob mutex, e não um canal com buffer, POR CAUSA DO CASO CHEIO:
// com canal, a única saída para um evento que não cabe é descartar ou esperar,
// e nenhuma das duas serve para um "soltou". Com a fatia dá para abrir espaço
// tirando um "apertou" que ainda não saiu, sem mexer na ordem do resto.
type filaTeclas struct {
	mu   sync.Mutex
	fila []teclaPendente
	teto int

	// sinal acorda a goroutine de escrita. Capacidade 1 e envio não
	// bloqueante: é aviso de "tem trabalho", não contagem de eventos.
	sinal chan struct{}

	descartadas atomic.Int64
}

// 512 é generoso de propósito: mesmo alguém "varrendo" o teclado não gera
// mais que algumas dezenas de eventos por segundo, e a goroutine só fica
// para trás se o worker do outro lado estiver mesmo engasgado.
const capacidadeFilaTeclas = 512

func novaFilaTeclas(capacidade int) *filaTeclas {
	return &filaTeclas{
		teto:  capacidade,
		sinal: make(chan struct{}, 1),
	}
}

// enfileirar põe a tecla no FIM da fila. NUNCA bloqueia e NUNCA reordena.
//
// Fila cheia é o caso interessante, e as duas metades da tecla não valem o
// mesmo:
//
//   - "apertou" perdido custa um caractere que não apareceu. Chato, e só.
//   - "soltou" perdido deixa a tecla PRESA do lado remoto — um Ctrl ou Shift
//     preso estraga tudo o que vem depois, e o usuário não tem como desfazer
//     sem reconectar.
//
// Por isso, quando lota, quem sai é o "apertou" MAIS ANTIGO que ainda não foi
// entregue. Tirar um elemento do meio não inverte nada: os que ficam seguem na
// mesma ordem relativa. E um "soltou" entregue sem o "apertou" correspondente
// é inofensivo — servidor VNC/RDP trata soltar tecla não apertada como no-op.
//
// DUAS TENTATIVAS ANTERIORES QUE NÃO FUNCIONARAM (não repita nenhuma):
//
//  1. Segurar o chamador por 50ms esperando vaga para o "soltou". Sob pressão
//     de verdade entregava ~10% dos releases, e voltava a bloquear a goroutine
//     do Wayland — exatamente o que este arquivo existe para evitar.
//
//  2. Guardar o "soltou" que não coube numa lista `pendentes` à parte, drenada
//     pela goroutine de escrita depois de cada entrega. Isso criava uma SEGUNDA
//     fila com disciplina diferente, e o release guardado FURAVA a fila: saía
//     na frente do "apertou" da mesma tecla, que continuava parado no meio do
//     canal. Resultado no lado remoto: soltou-depois-apertou, ou seja, a tecla
//     presa — o mesmo defeito que a lista tentava evitar. Reproduzido em teste
//     (ver TestReleaseNaoUltrapassaPress).
func (f *filaTeclas) enfileirar(t Tab, keysym, keycode uint32, pressed bool) {
	k := teclaPendente{t: t, keysym: keysym, keycode: keycode, pressed: pressed}

	f.mu.Lock()
	if len(f.fila) >= f.teto {
		if i := f.indicePrimeiroPress(); i >= 0 {
			// abre espaço tirando o "apertou" mais antigo
			f.fila = append(f.fila[:i], f.fila[i+1:]...)
			f.descartadas.Add(1)
		} else if pressed {
			// fila inteira de "soltou" (sessão travada há bastante tempo):
			// não há o que sacrificar, e o novo é um "apertou" — cai ele.
			f.mu.Unlock()
			f.avisarCheia()
			return
		}
		// fila inteira de "soltou" e o novo TAMBÉM é "soltou": deixa passar
		// do teto de propósito. Release não se descarta; o teto é um alvo,
		// não uma parede.
	}
	f.fila = append(f.fila, k)
	f.mu.Unlock()

	select {
	case f.sinal <- struct{}{}:
	default: // já há aviso pendente; um só basta
	}
}

// indicePrimeiroPress devolve a posição do "apertou" mais antigo ainda na
// fila, ou -1 se só houver "soltou". Chamar com f.mu travado.
func (f *filaTeclas) indicePrimeiroPress() int {
	for i, k := range f.fila {
		if k.pressed {
			return i
		}
	}
	return -1
}

func (f *filaTeclas) avisarCheia() {
	if n := f.descartadas.Add(1); n%100 == 1 {
		fmt.Fprintf(os.Stderr,
			"fila de teclas cheia — %d evento(s) perdido(s); sessão provavelmente travada\n", n)
	}
}

// rodar é a goroutine de escrita. É AQUI que a escrita bloqueante de até 5s
// pode acontecer, e é o lugar certo pra ela: nenhum laço de interface depende
// desta goroutine.
func (f *filaTeclas) rodar() {
	for range f.sinal {
		for {
			f.mu.Lock()
			if len(f.fila) == 0 {
				f.mu.Unlock()
				break
			}
			k := f.fila[0]
			// compacta no mesmo array em vez de deslizar o início com
			// f.fila[1:]: assim o array de trás não cresce sem fim.
			f.fila = append(f.fila[:0], f.fila[1:]...)
			f.mu.Unlock()

			// FORA da trava: HandleKey pode bloquear por segundos, e quem
			// enfileira não pode ficar preso esperando por isso.
			k.t.HandleKey(k.keysym, k.keycode, k.pressed)
		}
	}
}

// teclas é a fila de produção. Uma só: este app tem um grab por vez.
var teclas = novaFilaTeclas(capacidadeFilaTeclas)

var umaVezFilaTeclas sync.Once

// iniciarFilaTeclas sobe a goroutine de escrita. Idempotente — chamada junto
// com o grab (ver entrada_linux.go), que também só arma uma vez.
func iniciarFilaTeclas() { umaVezFilaTeclas.Do(func() { go teclas.rodar() }) }

// enfileirarTecla é o que o callback do grab chama. Ver filaTeclas.enfileirar.
func enfileirarTecla(t Tab, keysym, keycode uint32, pressed bool) {
	teclas.enfileirar(t, keysym, keycode, pressed)
}
