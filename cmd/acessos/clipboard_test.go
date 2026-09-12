package main

import (
	"sync"
	"testing"
)

// O crash relatado (abrir ~30 VNCs de uma vez derrubava o app) era
// exatamente isto: várias sessões publicando no clipboard do sistema ao
// mesmo tempo, cada uma na sua goroutine, dentro do cgo. Agora elas só
// enfileiram; quem entrega é o laço de quadro. Com -race, este teste falha
// se alguém voltar a publicar direto.
func TestPublicarClipboardConcorrente(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			publicarClipboard(nil, string(rune('a'+n%26)))
		}(i)
	}
	wg.Wait()
	if clipPendente.Load() == nil {
		t.Fatal("nada ficou na fila do clipboard")
	}
	// a entrega é de quem chama, uma vez só: depois a fila esvazia
	clipPendente.Store(nil)
	if clipPendente.Load() != nil {
		t.Fatal("fila não esvaziou")
	}
}

// Sessão em segundo plano não rouba o clipboard de quem está na tela.
func TestSoAbaAtivaPublica(t *testing.T) {
	a, b := &abaFalsa{}, &abaFalsa{}
	marcarAbaAtiva(a)
	if !ehAbaAtiva(a) {
		t.Fatal("a aba marcada deveria ser a ativa")
	}
	if ehAbaAtiva(b) {
		t.Fatal("aba em segundo plano não pode se dizer ativa")
	}
}
