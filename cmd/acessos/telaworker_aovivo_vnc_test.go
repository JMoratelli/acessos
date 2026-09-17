//go:build linux && !race

package main

// Teste contra um servidor VNC DE VERDADE. Desligado por padrão, ligado por
// ACESSOS_VNC_AOVIVO. Nada de credencial mora aqui — e de propósito só a
// senha DAQUELE host, nunca a senha mestra do cofre:
//
//	ACESSOS_VNC_AOVIVO=192.168.0.10:5900 ACESSOS_VNC_SENHA=... \
//	go test ./cmd/acessos/ -run AoVivoVNC -v
//
// Os auxiliares (msgAoVivo, lerAoVivo, memoriaDe) são os mesmos do teste ao
// vivo de RDP, em telaworker_aovivo_test.go.

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"acessos-go/internal/telaproc"
)

func ligacaoVNCAoVivo(t *testing.T) telaproc.Ligacao {
	t.Helper()
	alvo := os.Getenv("ACESSOS_VNC_AOVIVO")
	if alvo == "" {
		t.Skip("ACESSOS_VNC_AOVIVO não definido — teste ao vivo desligado")
	}
	host, porta, ok := strings.Cut(alvo, ":")
	if !ok {
		host, porta = alvo, "5900"
	}
	n, err := strconv.Atoi(porta)
	if err != nil {
		t.Fatalf("porta inválida em ACESSOS_VNC_AOVIVO: %q", porta)
	}
	return telaproc.Ligacao{
		Host: host, Porta: n,
		Usuario: os.Getenv("ACESSOS_VNC_USUARIO"),
		Senha:   os.Getenv("ACESSOS_VNC_SENHA"),
	}
}

func conectarVNCAoVivo(t *testing.T) (*telaproc.Processo, <-chan msgAoVivo, telaproc.Quadro) {
	t.Helper()
	proc, err := telaproc.Iniciar("vnc")
	if err != nil {
		t.Fatalf("Iniciar: %v", err)
	}
	if err := proc.Conectar(ligacaoVNCAoVivo(t)); err != nil {
		t.Fatalf("Conectar: %v", err)
	}
	msgs := lerAoVivo(proc)

	prazo := time.After(60 * time.Second)
	for {
		select {
		case <-prazo:
			t.Fatal("o servidor não mandou a primeira tela em 60s")
		case m := <-msgs:
			if m.err != nil {
				t.Fatalf("canal caiu antes da primeira tela: %v", m.err)
			}
			switch m.tipo {
			case telaproc.EvtFalha:
				var f telaproc.Falha
				_ = json.Unmarshal(m.corpo, &f)
				t.Fatalf("o servidor recusou: %s (auth=%v precisa_usuario=%v recusado=%v)",
					f.Mensagem, f.AuthFalhou, f.PrecisaUsuario, f.Recusado)
			case telaproc.EvtConectado:
				t.Logf("conectado; filho é o processo %d", proc.PID())
				if err := proc.Credito(); err != nil {
					t.Fatalf("Credito: %v", err)
				}
			case telaproc.EvtQuadro:
				q, pix, err := telaproc.DecodificarQuadro(m.corpo)
				if err != nil {
					t.Fatalf("quadro inválido: %v", err)
				}
				t.Logf("primeira tela: %dx%d, retângulo (%d,%d %dx%d), %d bytes",
					q.TotalW, q.TotalH, q.X, q.Y, q.W, q.H, len(pix))
				return proc, msgs, q
			case telaproc.EvtDesconectado:
				t.Fatalf("desconectou antes da primeira tela: %s", string(m.corpo))
			}
		}
	}
}

// A mesma promessa do RDP, agora para o VNC: o filho morre do jeito mais
// violento possível e o processo principal apenas vê o canal fechar.
// Importa mais aqui do que no RDP, porque a libvncclient tem CVEs abertas
// de escrita fora dos limites no decodificador Tight — ver
// flatpak/patches/libvncserver/PATCH.md.
func TestAoVivoVNCMorteDoFilhoNaoDerrubaOPrincipal(t *testing.T) {
	proc, msgs, _ := conectarVNCAoVivo(t)
	defer proc.Encerrar()

	pid := proc.PID()
	if pid == 0 {
		t.Fatal("sem PID do filho")
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("Kill(%d): %v", pid, err)
	}
	t.Logf("matei o processo %d a SIGKILL", pid)

	prazo := time.After(15 * time.Second)
	for percebeu := false; !percebeu; {
		select {
		case m, aberto := <-msgs:
			if !aberto {
				t.Fatal("o canal fechou sem erro registrado")
			}
			if m.err != nil {
				t.Logf("o principal percebeu a queda: %v", m.err)
				percebeu = true
			}
		case <-prazo:
			t.Fatal("o principal NÃO percebeu a morte do filho — a aba ficaria pendurada")
		}
	}

	proc2, _, q := conectarVNCAoVivo(t)
	defer proc2.Encerrar()
	t.Logf("religou num processo novo (%d), tela %dx%d", proc2.PID(), q.TotalW, q.TotalH)
}

// Fluxo de quadros com controle de crédito, e o tamanho real dos retângulos
// sujos — a tela de um PDV parado é o melhor caso possível para o rastreio
// de dano, e vale ver quanto ele economiza de verdade.
func TestAoVivoVNCFluxoDeQuadros(t *testing.T) {
	proc, msgs, primeiro := conectarVNCAoVivo(t)
	defer proc.Encerrar()

	querQuadros := 5
	if v := os.Getenv("ACESSOS_VNC_QUADROS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			querQuadros = n
		}
	}

	telaCheia := int(primeiro.TotalW) * int(primeiro.TotalH) * 4
	recebidos, bytesTotal := 0, 0
	prazo := time.After(90 * time.Second)

	for recebidos < querQuadros {
		_ = proc.Credito()
		select {
		case <-prazo:
			t.Fatalf("só chegaram %d de %d quadros (tela remota pode estar parada)", recebidos, querQuadros)
		case m, aberto := <-msgs:
			if !aberto {
				t.Fatalf("canal fechou no quadro %d", recebidos)
			}
			if m.err != nil {
				t.Fatalf("canal caiu no quadro %d: %v", recebidos, m.err)
			}
			switch m.tipo {
			case telaproc.EvtDesconectado:
				t.Skipf("o servidor derrubou a sessão no quadro %d: %s", recebidos, string(m.corpo))
			case telaproc.EvtQuadro:
				q, _, err := telaproc.DecodificarQuadro(m.corpo)
				if err != nil {
					t.Fatalf("quadro %d inválido: %v", recebidos, err)
				}
				recebidos++
				bytesTotal += len(m.corpo)
				pct := 100 * float64(q.W) * float64(q.H) /
					(float64(q.TotalW) * float64(q.TotalH))
				t.Logf("quadro %2d: sujo (%4d,%4d %4dx%4d) = %5.1f%% da tela, %7d bytes",
					recebidos, q.X, q.Y, q.W, q.H, pct, len(m.corpo))
			}
		}
	}

	t.Logf("TOTAL: %d quadros, %d bytes; tela cheia toda vez seriam %d (%.0f%%)",
		recebidos, bytesTotal, telaCheia*recebidos,
		100*float64(bytesTotal)/float64(telaCheia*recebidos))
}

// O número que decide o custo reservado ao VNC agora que ele também gasta um
// processo. O custo de BASE (sem conectar) está em TestCustoDeBaseDosFilhos;
// este mede a sessão VIVA, que é onde a memória de verdade aparece.
func TestAoVivoVNCCustoDeUmFilho(t *testing.T) {
	proc, msgs, q := conectarVNCAoVivo(t)
	defer proc.Encerrar()

	for i := 0; i < 5; i++ {
		_ = proc.Credito()
		select {
		case <-msgs:
		case <-time.After(20 * time.Second):
			t.Log("tela remota parada; medindo assim mesmo")
		}
	}

	m, err := memoriaDe(proc.PID())
	if err != nil {
		t.Fatalf("não consegui medir o filho: %v", err)
	}
	privada := m["Private_Clean"] + m["Private_Dirty"]
	t.Logf("filho VNC %d, tela %dx%d: RSS %.1f MiB | PSS %.1f MiB | PRIVADA %.1f MiB",
		proc.PID(), q.TotalW, q.TotalH,
		float64(m["Rss"])/1024, float64(m["Pss"])/1024, float64(privada)/1024)
	t.Logf("orçamento %d MiB; VNC reserva %d MiB por sessão => cabem ~%d telas VNC",
		telaproc.OrcamentoMiB, telaproc.CustoDe("vnc"),
		telaproc.OrcamentoMiB/telaproc.CustoDe("vnc"))
}
