//go:build linux

package grab

import (
	"sync"
	"testing"
)

// A captura JÁ FOI singleton: onKey e onClipboardText eram variáveis de
// pacote, e a segunda janela a chamar Start substituía os callbacks da
// primeira em silêncio — o teclado da janela original simplesmente parava
// de chegar, sem erro no caminho. Havia só um aviso em stderr, que num app
// de janela ninguém lê.
//
// Este teste guarda o que passou a valer: cada captura recebe o que é
// dela, identificada pelo ctx que o C devolve em cada callback.
func TestCadaCapturaRecebeSoAsProprias(t *testing.T) {
	var teclasA, teclasB []uint32
	a := &Handle{onKey: func(ks, _ uint32, _ bool) { teclasA = append(teclasA, ks) }}
	b := &Handle{onKey: func(ks, _ uint32, _ bool) { teclasB = append(teclasB, ks) }}
	a.handle = registro.Registrar(a)
	b.handle = registro.Registrar(b)
	t.Cleanup(func() {
		registro.Remover(a.handle)
		registro.Remover(b.handle)
	})

	if a.handle == b.handle {
		t.Fatal("duas capturas ganharam o mesmo handle")
	}

	entregarTecla(a.handle, 65, 38, true)
	entregarTecla(b.handle, 66, 56, true)
	entregarTecla(a.handle, 67, 54, true)

	if len(teclasA) != 2 || teclasA[0] != 65 || teclasA[1] != 67 {
		t.Errorf("A recebeu %v, esperado [65 67]", teclasA)
	}
	if len(teclasB) != 1 || teclasB[0] != 66 {
		t.Errorf("B recebeu %v, esperado [66]", teclasB)
	}
}

// Callback em voo no instante do Stop é normal: o C pode já ter entrado na
// chamada quando o Go remove o handle. Cair em nil e devolver é o
// tratamento certo — o que não pode é estourar nem entregar para o Handle
// errado.
func TestCapturaParadaNaoRecebeMais(t *testing.T) {
	chamou := false
	h := &Handle{onKey: func(uint32, uint32, bool) { chamou = true }}
	h.handle = registro.Registrar(h)

	entregarTecla(h.handle, 65, 38, true)
	if !chamou {
		t.Fatal("deveria ter recebido antes de parar")
	}

	chamou = false
	registro.Remover(h.handle) // é o que o Stop faz
	entregarTecla(h.handle, 65, 38, true)
	if chamou {
		t.Fatal("captura parada não pode receber mais tecla")
	}
}

// Captura sem callback de tecla é legítima (quem só quer o inibidor de
// atalhos passa nil) e não pode estourar.
func TestCapturaSemCallbackNaoEstoura(t *testing.T) {
	h := &Handle{}
	h.handle = registro.Registrar(h)
	t.Cleanup(func() { registro.Remover(h.handle) })
	entregarTecla(h.handle, 65, 38, true)
}

// ctx desconhecido (handle que nunca existiu) também não pode estourar.
func TestCtxDesconhecidoNaoEstoura(t *testing.T) {
	entregarTecla(uintptr(0xdead), 65, 38, true)
}

// Stop tem de ser seguro de chamar concorrentemente com o resto — é o que
// acontece quando a janela destacada fecha enquanto o callback do Wayland,
// noutra thread, ainda está dentro de Modificadores. Sem a trava, o
// grab_parar liberava o ponteiro debaixo dele.
//
// O Handle daqui não tem C por trás (não há Wayland em teste), então o que
// se exercita é a disciplina de travas: com -race, um Stop sem
// sincronização contra os leitores aparece aqui.
func TestStopConcorrenteNaoQuebra(t *testing.T) {
	h := &Handle{onKey: func(uint32, uint32, bool) {}}
	h.handle = registro.Registrar(h)

	pronto := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-pronto
			for j := 0; j < 200; j++ {
				h.Modificadores()
				h.Inibir(j%2 == 0)
				h.SetClipboardText("x")
				entregarTecla(h.handle, 65, 38, true)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-pronto
		h.Stop()
		h.Stop() // idempotente
	}()

	close(pronto)
	wg.Wait()

	if registro.De(h.handle) != nil {
		t.Fatal("Stop tem de tirar o handle do registro")
	}
}
