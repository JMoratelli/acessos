//go:build (linux || windows) && !race

package main

// Este teste sobe um processo-filho DE VERDADE, com a libfreerdp junto, e
// por isso fica fora do -race: o detector liga junto o checkptr, e o
// internal/rdp passa um HANDLE INTEIRO (1, 2, 3...) como o void* de
// contexto dos callbacks do shim — um valor que nunca é desreferenciado
// do lado Go, mas que o checkptr recusa por não apontar para alocação
// nenhuma. O tropeço é do esquema de handles do internal/rdp, anterior a
// este arquivo, e não do canal entre os processos; o resto dos testes
// (inclusive os de internal/telaproc, que sobem filho igual) roda sob
// -race normalmente.

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"acessos-go/internal/telaproc"
)

// Conexão recusada tem de virar EvtFalha no processo principal, e não um
// filho pendurado: é o caminho de "host desligado", o mais comum de todos.
func TestFilhoRDPRelataFalhaDeConexao(t *testing.T) {
	// Uma porta que se sabe fechada: abre e fecha para o SO não a reusar
	// tão cedo.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	porta := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	proc, err := telaproc.Iniciar("rdp")
	if err != nil {
		t.Fatalf("Iniciar: %v", err)
	}
	defer proc.Encerrar()

	if err := proc.Conectar(telaproc.Ligacao{Host: "127.0.0.1", Porta: porta}); err != nil {
		t.Fatalf("Conectar: %v", err)
	}

	tipo, corpo, err := lerComPrazo(t, proc, 30*time.Second)
	if err != nil {
		t.Fatalf("Ler: %v", err)
	}
	if tipo != telaproc.EvtFalha {
		t.Fatalf("recebi tipo %d, esperava EvtFalha", tipo)
	}
	var f telaproc.Falha
	if err := json.Unmarshal(corpo, &f); err != nil {
		t.Fatalf("falha ilegível: %v", err)
	}
	if f.Mensagem == "" {
		t.Fatal("falha sem mensagem — a aba não teria o que registrar no log")
	}
}

func lerComPrazo(t *testing.T, p *telaproc.Processo, prazo time.Duration) (telaproc.Tipo, []byte, error) {
	t.Helper()
	type res struct {
		tipo  telaproc.Tipo
		corpo []byte
		err   error
	}
	ch := make(chan res, 1)
	go func() {
		tipo, corpo, err := p.Ler()
		ch <- res{tipo, append([]byte(nil), corpo...), err}
	}()
	select {
	case r := <-ch:
		return r.tipo, r.corpo, r.err
	case <-time.After(prazo):
		t.Fatal("o filho não respondeu no prazo")
		return 0, nil, nil
	}
}
