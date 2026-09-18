package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Testes do despachante de ações de janela. Rode com -race:
//
//	go test ./cmd/acessos/ -run 'Acao|Drenagem' -race -count=3
//
// O bug que eles protegem está escrito por extenso em acaojanela.go: chamar
// w.Perform de dentro do layout congela a janela ao restaurar no Windows.
//
// ATENÇÃO: estes testes exercitam foraDoQuadro/drenarAcoesJanela DE VERDADE,
// e não uma cópia da lógica. A versão anterior deste arquivo reimplementava o
// despachante dentro do próprio teste — com isso, qualquer defeito no código
// de produção passava despercebido, porque nada dele era executado.
//
// Como a fila é global (acoesJanela), cada teste limpa antes de começar.

func limparAcoesJanela() {
	for {
		select {
		case <-acoesJanela:
		default:
			return
		}
	}
}

// TestAcaoSoRodaNaDrenagem é O teste do bug do Windows: uma ação pedida
// durante o quadro não pode rodar enquanto o quadro está em voo. Ela só pode
// sair quando o laço chamar drenarAcoesJanela, que acontece com o FrameEvent
// já encerrado.
func TestAcaoSoRodaNaDrenagem(t *testing.T) {
	limparAcoesJanela()

	var rodou atomic.Bool
	// "dentro do layout": um Clicked(gtx) pedindo minimizar
	foraDoQuadro(func() { rodou.Store(true) })

	// o quadro ainda está em voo — nada pode ter rodado
	time.Sleep(20 * time.Millisecond)
	if rodou.Load() {
		t.Fatal("a ação rodou com o quadro em voo — o congelamento volta")
	}

	// laço volta ao topo da iteração, com o FrameEvent já encerrado
	drenarAcoesJanela()
	if !rodou.Load() {
		t.Fatal("a ação não rodou na drenagem")
	}
}

// TestAcaoRodaNaGoroutineDoLaco trava a correção mais importante: a ação tem
// de rodar em QUEM CHAMOU drenarAcoesJanela, e não numa goroutine própria.
//
// No Wayland/X11 o Window.Run executa f() na goroutine de quem chama
// (third_party/gio/app/os_wayland.go:1602), então despachar de fora do laço
// punha driver.Configure em paralelo com o desenho — uma corrida de dados no
// Linux, plataforma onde o bug do Windows nem existe.
func TestAcaoRodaNaGoroutineDoLaco(t *testing.T) {
	limparAcoesJanela()

	quemDrena := make(chan int, 1)
	var idDrenagem int
	foraDoQuadro(func() { idDrenagem++; quemDrena <- idDrenagem })

	// nenhuma goroutine é criada: a chamada abaixo é que executa a ação,
	// de forma síncrona. Se voltar a existir goroutine despachante, o
	// recebimento abaixo falha por timeout.
	drenarAcoesJanela()

	select {
	case <-quemDrena:
	default:
		t.Fatal("a ação não foi executada de forma síncrona por drenarAcoesJanela —" +
			" alguém reintroduziu a goroutine despachante")
	}
}

// TestDrenagemPreservaOrdem: minimizar e depois maximizar não podem trocar.
func TestDrenagemPreservaOrdem(t *testing.T) {
	limparAcoesJanela()

	var ordem []int
	for i := 1; i <= 5; i++ {
		i := i
		foraDoQuadro(func() { ordem = append(ordem, i) })
	}
	drenarAcoesJanela()

	if len(ordem) != 5 {
		t.Fatalf("saíram %d ações, esperava 5: %v", len(ordem), ordem)
	}
	for i, v := range ordem {
		if v != i+1 {
			t.Fatalf("fora de ordem: %v", ordem)
		}
	}
}

// TestAcaoNaoPenduraQuemPede: janela que não drena não pode pendurar quem
// clica. Descartar é o comportamento certo aqui.
func TestAcaoNaoPenduraQuemPede(t *testing.T) {
	limparAcoesJanela()

	inicio := time.Now()
	for i := 0; i < 1000; i++ { // muito além dos 8 de buffer
		foraDoQuadro(func() {})
	}
	if d := time.Since(inicio); d > 50*time.Millisecond {
		t.Fatalf("pedido bloqueou por %v com a fila cheia", d)
	}
	limparAcoesJanela()
}

// TestAcoesConcorrentes: titlebar, atalho global e a caixa de busca podem
// pedir ação ao mesmo tempo, enquanto o laço drena. Só faz sentido com -race.
func TestAcoesConcorrentes(t *testing.T) {
	limparAcoesJanela()

	parar := make(chan struct{})
	var drenando sync.WaitGroup
	drenando.Add(1)
	go func() { // faz o papel do laço de eventos
		defer drenando.Done()
		for {
			select {
			case <-parar:
				drenarAcoesJanela()
				return
			default:
				drenarAcoesJanela()
				time.Sleep(200 * time.Microsecond)
			}
		}
	}()

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				foraDoQuadro(func() {})
			}
		}()
	}
	wg.Wait()
	close(parar)
	drenando.Wait()
}

// esperarAte fica AQUI, e não em filateclas_linux_test.go, de propósito: este
// arquivo não tem restrição de build, e o outro é linux-only. Com o helper lá,
// o pacote de testes não compilava no Windows — justamente a plataforma do bug
// que acaojanela.go corrige.
func esperarAte(t *testing.T, cond func() bool) {
	t.Helper()
	limite := time.Now().Add(10 * time.Second)
	for time.Now().Before(limite) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condição não satisfeita a tempo")
}
