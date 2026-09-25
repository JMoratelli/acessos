package main

import (
	"testing"
	"time"
)

const prazoTeste = 2 * time.Second

func TestDisparadorNaoBloqueiaQuemAvisa(t *testing.T) {
	// O ponto do arquivo inteiro: disparar() é chamado de dentro do
	// consumidor de sinais do D-Bus e do laço do GetMessageW, e parar ali
	// é justamente o defeito que isto fecha. Com o atendente ocupado e a
	// fila cheia, disparar ainda tem de voltar na hora.
	preso := make(chan struct{})
	entrou := make(chan string, 8)
	d := novoDisparador(func(tk string) {
		entrou <- tk
		<-preso
	})
	defer d.pararTudo()

	d.disparar("1") // vai para o atendente
	select {
	case tk := <-entrou:
		if tk != "1" {
			t.Fatalf("atendeu %q", tk)
		}
	case <-time.After(prazoTeste):
		t.Fatal("o primeiro pedido não foi atendido")
	}

	// Atendente preso: estes não podem bloquear quem avisa.
	pronto := make(chan struct{})
	go func() {
		d.disparar("2") // ocupa a vaga da fila
		d.disparar("3") // fila cheia — descartado
		d.disparar("4") // idem
		close(pronto)
	}()
	select {
	case <-pronto:
	case <-time.After(prazoTeste):
		t.Fatal("disparar BLOQUEOU com o atendente ocupado")
	}

	close(preso)
	select {
	case tk := <-entrou:
		if tk != "2" {
			t.Fatalf("depois do 1 veio %q, queria o 2", tk)
		}
	case <-time.After(prazoTeste):
		t.Fatal("o pedido enfileirado não foi atendido")
	}

	// 3 e 4 foram descartados de propósito: apertar o atalho cinco vezes
	// enquanto o app sobe abre UMA caixa, não cinco.
	select {
	case tk := <-entrou:
		t.Fatalf("atendeu %q, que devia ter sido descartado", tk)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestDisparadorUmDeCadaVez(t *testing.T) {
	// Serialização é do atendente, e não do guarda da caixa de busca: dois
	// atendimentos simultâneos abririam duas janelas.
	var emVoo, maximo int
	mudou := make(chan struct{}, 32)
	d := novoDisparador(func(string) {
		emVoo++
		if emVoo > maximo {
			maximo = emVoo
		}
		time.Sleep(5 * time.Millisecond)
		emVoo--
		mudou <- struct{}{}
	})
	defer d.pararTudo()

	for i := 0; i < 4; i++ {
		d.disparar("")
		<-mudou
	}
	if maximo != 1 {
		t.Fatalf("chegou a %d atendimentos ao mesmo tempo", maximo)
	}
}

func TestDisparadorSemCallback(t *testing.T) {
	// Plataforma sem atalho (atalhoglobal_outros.go) não deve deixar uma
	// goroutine parada à toa. E disparar num nil não pode explodir.
	var d *disparador = novoDisparador(nil)
	if d != nil {
		t.Fatal("subiu atendente para callback nulo")
	}
	d.disparar("x")
	d.pararTudo()
}
