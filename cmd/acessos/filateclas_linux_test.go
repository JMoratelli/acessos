//go:build linux

package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Testes de ESTRESSE da fila de teclas. Rode sempre com -race:
//
//	go test ./cmd/acessos/ -run 'Tecla|Ordem|Release' -race -count=3
//
// O que eles protegem está escrito por extenso em filateclas_linux.go. Em
// uma linha: o callback do grab roda na goroutine que despacha o Wayland
// (a mesma do laço de eventos), então escrever no socket de lá congela a
// interface inteira.
//
// esperarAte vive em acaojanela_test.go, que NÃO tem restrição de build —
// assim o pacote de testes continua compilando no Windows, onde este
// arquivo aqui não entra.

// abaLenta é uma aba cujo HandleKey demora — simula o worker engasgado, que
// é o estado em que o bug aparecia.
type abaLenta struct {
	abaFalsa
	atraso   time.Duration
	mu       sync.Mutex
	recebido []teclaPendente

	// segura a PRIMEIRA entrega até o teste liberar, para montar o estado
	// de fila cheia antes de qualquer coisa sair.
	liberar chan struct{}
	uma     sync.Once
}

func (a *abaLenta) HandleKey(ks, kc uint32, p bool) {
	if a.liberar != nil {
		a.uma.Do(func() { <-a.liberar })
	}
	if a.atraso > 0 {
		time.Sleep(a.atraso)
	}
	a.mu.Lock()
	a.recebido = append(a.recebido, teclaPendente{keysym: ks, keycode: kc, pressed: p})
	a.mu.Unlock()
}

func (a *abaLenta) contarRelease() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, k := range a.recebido {
		if !k.pressed {
			n++
		}
	}
	return n
}

// TestTeclaNaoBloqueiaChamador é O teste do bug: com a sessão travada,
// enfileirar NÃO pode segurar quem chamou. Antes da correção cada tecla
// custava até prazoEscrita (5s) na goroutine do laço de eventos.
func TestTeclaNaoBloqueiaChamador(t *testing.T) {
	f := novaFilaTeclas(512)
	go f.rodar()
	aba := &abaLenta{atraso: 2 * time.Second}

	inicio := time.Now()
	for i := 0; i < 100; i++ {
		f.enfileirar(aba, uint32(i), uint32(i), true)
	}
	if d := time.Since(inicio); d > 100*time.Millisecond {
		t.Fatalf("enfileirar bloqueou o chamador por %v — é isso que congela a interface", d)
	}
}

// TestOrdemPreservada: "soltou" antes de "apertou" prende a tecla do lado
// remoto. A fila tem que ser FIFO estrita.
func TestOrdemPreservada(t *testing.T) {
	f := novaFilaTeclas(512)
	go f.rodar()
	aba := &abaLenta{}

	const n = 400
	for i := 0; i < n; i++ {
		f.enfileirar(aba, uint32(i), uint32(i), i%2 == 0)
	}
	esperarAte(t, func() bool {
		aba.mu.Lock()
		defer aba.mu.Unlock()
		return len(aba.recebido) == n
	})

	aba.mu.Lock()
	defer aba.mu.Unlock()
	for i, k := range aba.recebido {
		if k.keysym != uint32(i) || k.pressed != (i%2 == 0) {
			t.Fatalf("fora de ordem na posição %d: %+v", i, k)
		}
	}
}

// TestReleaseNaoUltrapassaPress é o teste de REGRESSÃO do defeito que a
// revisão pegou, e o motivo de a fila ser uma fatia e não um canal.
//
// A versão anterior guardava o "soltou" que não coubesse numa lista à parte,
// drenada logo após CADA entrega. Com o "apertou" da mesma tecla parado no
// meio da fila, o "soltou" saía na frente dele — e a tecla ficava PRESA do
// lado remoto. Reproduzia assim, exatamente:
//
//	entregue: q↓ | A↑ | w↓ | A↓ | e↓      ← A soltou antes de apertar
//
// Se alguém voltar a criar um caminho paralelo à fila, este teste quebra.
func TestReleaseNaoUltrapassaPress(t *testing.T) {
	const teclaA = 0x0061
	f := novaFilaTeclas(4)
	aba := &abaLenta{liberar: make(chan struct{})}
	go f.rodar()

	// enche a fila inteira, com o press de A no MEIO
	f.enfileirar(aba, 0x0071, 100, true)  // q ↓
	f.enfileirar(aba, 0x0077, 101, true)  // w ↓
	f.enfileirar(aba, teclaA, 102, true)  // A ↓  <- o que não pode ser ultrapassado
	f.enfileirar(aba, 0x0065, 103, true)  // e ↓  (lotou)
	f.enfileirar(aba, teclaA, 102, false) // A ↑  <- entra com a fila cheia

	close(aba.liberar)

	esperarAte(t, func() bool {
		aba.mu.Lock()
		defer aba.mu.Unlock()
		p, r := false, false
		for _, k := range aba.recebido {
			if k.keysym == teclaA && k.pressed {
				p = true
			}
			if k.keysym == teclaA && !k.pressed {
				r = true
			}
		}
		return p && r
	})

	aba.mu.Lock()
	defer aba.mu.Unlock()
	posPress, posRelease := -1, -1
	for i, k := range aba.recebido {
		if k.keysym != teclaA {
			continue
		}
		if k.pressed && posPress < 0 {
			posPress = i
		}
		if !k.pressed && posRelease < 0 {
			posRelease = i
		}
	}
	if posRelease < posPress {
		t.Fatalf("release de A (pos %d) entregue ANTES do press (pos %d) — tecla presa no remoto;"+
			" ordem completa: %+v", posRelease, posPress, aba.recebido)
	}
}

// TestReleaseNuncaSePerde: com a fila estourada, "apertou" pode cair, mas
// TODO "soltou" tem que chegar — release perdido é modificador preso.
//
// Este é o teste que reprovou a primeira tentativa de correção (segurar o
// chamador por 50ms), que entregava ~10% dos releases. Não troque o
// mecanismo por um timeout de novo.
func TestReleaseNuncaSePerde(t *testing.T) {
	f := novaFilaTeclas(4) // minúscula de propósito: estoura fácil
	aba := &abaLenta{atraso: 5 * time.Millisecond}
	go f.rodar()

	var releases atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pressed := i%2 == 0
			if !pressed {
				releases.Add(1)
			}
			f.enfileirar(aba, uint32(i), uint32(i), pressed)
		}(i)
	}
	wg.Wait()

	esperarAte(t, func() bool { return aba.contarRelease() == int(releases.Load()) })
	if got := aba.contarRelease(); got != int(releases.Load()) {
		t.Fatalf("faltaram %d release(s) — tecla fica presa na sessão",
			int(releases.Load())-got)
	}
	t.Logf("%d releases entregues, %d apertou(s) sacrificados pela fila cheia",
		aba.contarRelease(), f.descartadas.Load())
}

// TestTeclasDeVariasThreads: o listener do wl_keyboard e a thread rep_loop
// (repetição) enfileiram ao mesmo tempo. Só faz sentido com -race.
func TestTeclasDeVariasThreads(t *testing.T) {
	f := novaFilaTeclas(512)
	go f.rodar()
	aba := &abaLenta{}

	var wg sync.WaitGroup
	for th := 0; th < 8; th++ {
		wg.Add(1)
		go func(th int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				f.enfileirar(aba, uint32(th), uint32(i), i%3 != 0)
			}
		}(th)
	}
	wg.Wait()
}
