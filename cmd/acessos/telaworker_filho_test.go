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

// Custo de BASE de um filho, por protocolo: binário, runtime do Go e a
// biblioteca C carregada, SEM sessão conectada. É a maior parcela do que
// o orçamento de telaproc limita — o framebuffer entra por cima disto e é
// calculável (largura x altura x 4, algumas cópias).
//
// Não precisa de servidor nenhum: o filho sobe, se apresenta e fica
// esperando um CmdConectar que não vem. Refaça esta medição antes de mexer
// no teto.
func TestCustoDeBaseDosFilhos(t *testing.T) {
	for _, proto := range []string{"rdp", "vnc"} {
		t.Run(proto, func(t *testing.T) {
			proc, err := telaproc.Iniciar(proto)
			if err != nil {
				t.Fatalf("Iniciar(%s): %v", proto, err)
			}
			defer proc.Encerrar()

			// Deixa o filho terminar de subir antes de medir: sem isso a
			// medição pega o processo no meio do carregamento das
			// bibliotecas e sai baixa demais para servir de base.
			esperarEstavel(t, proc.PID())

			m, err := memoriaDe(proc.PID())
			if err != nil {
				t.Fatalf("medindo %s: %v", proto, err)
			}
			privada := m["Private_Clean"] + m["Private_Dirty"]
			t.Logf("filho %s (sem conectar): RSS %.1f MiB | PSS %.1f MiB | PRIVADA %.1f MiB",
				proto, float64(m["Rss"])/1024, float64(m["Pss"])/1024, float64(privada)/1024)
			t.Logf("  reservado para %s: %d MiB por sessão", proto, telaproc.CustoDe(proto))
		})
	}
}

// esperarEstavel espera o RSS parar de crescer, que é quando o filho
// terminou de carregar.
func esperarEstavel(t *testing.T, pid int) {
	t.Helper()
	anterior := -1
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		m, err := memoriaDe(pid)
		if err != nil {
			t.Fatalf("processo %d sumiu durante a medição: %v", pid, err)
		}
		atual := m["Rss"]
		if atual == anterior {
			return
		}
		anterior = atual
	}
}
