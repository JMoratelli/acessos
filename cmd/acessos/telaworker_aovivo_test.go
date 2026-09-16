//go:build linux && !race

package main

// Teste contra um servidor RDP DE VERDADE. Fica desligado por padrão e só
// roda quando ACESSOS_RDP_AOVIVO aponta um host — nada de credencial mora
// aqui. Para rodar:
//
//	ACESSOS_RDP_AOVIVO=192.168.0.10:3389 ACESSOS_RDP_USUARIO=fulano \
//	ACESSOS_RDP_SENHA=... ACESSOS_RDP_DOMINIO=... \
//	go test ./cmd/acessos/ -run AoVivo -v
//
// ACESSOS_RDP_QUADROS muda quantos quadros o teste de fluxo coleta (5 por
// padrão); vale subir quando se quer OLHAR o tamanho dos retângulos sujos.
//
// Fora do -race pelo mesmo motivo do telaworker_filho_test.go: o checkptr
// recusa o esquema de handles inteiros do internal/rdp.

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"acessos-go/internal/rdp"
	"acessos-go/internal/telaproc"
)

func ligacaoAoVivo(t *testing.T) telaproc.Ligacao {
	t.Helper()
	alvo := os.Getenv("ACESSOS_RDP_AOVIVO")
	if alvo == "" {
		t.Skip("ACESSOS_RDP_AOVIVO não definido — teste ao vivo desligado")
	}
	host, porta, ok := strings.Cut(alvo, ":")
	if !ok {
		host, porta = alvo, "3389"
	}
	n, err := strconv.Atoi(porta)
	if err != nil {
		t.Fatalf("porta inválida em ACESSOS_RDP_AOVIVO: %q", porta)
	}
	return telaproc.Ligacao{
		Host: host, Porta: n,
		Usuario: os.Getenv("ACESSOS_RDP_USUARIO"),
		Senha:   os.Getenv("ACESSOS_RDP_SENHA"),
		Dominio: os.Getenv("ACESSOS_RDP_DOMINIO"),
	}
}

type msgAoVivo struct {
	tipo  telaproc.Tipo
	corpo []byte
	err   error
}

// lerAoVivo é o ÚNICO leitor do canal, do começo ao fim do teste. Conn.Ler
// é de um leitor só (o corpo devolvido é reaproveitado entre chamadas), e
// uma versão anterior deste arquivo abria um leitor no helper de conexão e
// outro no teste: as duas goroutines roubavam mensagem uma da outra e o
// teste via ZERO quadros chegando. Quem conecta devolve ESTE canal, e
// ninguém mais chama proc.Ler().
func lerAoVivo(proc *telaproc.Processo) <-chan msgAoVivo {
	ch := make(chan msgAoVivo, 16)
	go func() {
		defer close(ch)
		for {
			tipo, corpo, err := proc.Ler()
			ch <- msgAoVivo{tipo, append([]byte(nil), corpo...), err}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// conectarAoVivo sobe o filho, conecta e devolve assim que a primeira tela
// chega. Certificado é aceito SÓ DESTA VEZ, para o teste não deixar nada
// gravado em ~/.config/freerdp na máquina de quem rodou.
func conectarAoVivo(t *testing.T) (*telaproc.Processo, <-chan msgAoVivo, telaproc.Quadro) {
	t.Helper()
	proc, err := telaproc.Iniciar("rdp")
	if err != nil {
		t.Fatalf("Iniciar: %v", err)
	}
	if err := proc.Conectar(ligacaoAoVivo(t)); err != nil {
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
			case telaproc.EvtCertPedido:
				t.Log("servidor apresentou certificado — aceitando só desta vez")
				if err := proc.CertResposta(rdp.CertAceitarUmaVez); err != nil {
					t.Fatalf("CertResposta: %v", err)
				}
			case telaproc.EvtFalha:
				t.Fatalf("o servidor recusou: %s", string(m.corpo))
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

// Este é O teste que justifica a arquitetura inteira: o filho morre do
// jeito mais violento possível (SIGKILL, que é o que um SIGSEGV dentro da
// libfreerdp vira na prática para quem está de fora) e o processo
// principal apenas VÊ o canal fechar. Nada de pânico, nada de heap
// compartilhado indo junto — e, logo em seguida, ele consegue subir outra
// sessão, que é o que a aba faz com o backoff de reconexão.
func TestAoVivoMorteDoFilhoNaoDerrubaOPrincipal(t *testing.T) {
	proc, msgs, _ := conectarAoVivo(t)
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

	// E agora a outra metade da promessa: dá para religar.
	proc2, _, q := conectarAoVivo(t)
	defer proc2.Encerrar()
	t.Logf("religou num processo novo (%d), tela %dx%d", proc2.PID(), q.TotalW, q.TotalH)
}

// Quadros continuam chegando enquanto se devolve crédito: confere o
// controle de fluxo de ponta a ponta contra um servidor real, e de quebra
// mostra o tamanho real dos retângulos sujos — que é o que decide se
// mandar pixels por socket sai caro ou barato.
func TestAoVivoFluxoDeQuadros(t *testing.T) {
	proc, msgs, primeiro := conectarAoVivo(t)
	defer proc.Encerrar()

	querQuadros := 5
	if v := os.Getenv("ACESSOS_RDP_QUADROS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			querQuadros = n
		}
	}

	telaCheia := int(primeiro.TotalW) * int(primeiro.TotalH) * 4
	recebidos, bytesTotal := 0, 0
	prazo := time.After(90 * time.Second)

	for recebidos < querQuadros {
		// Crédito mais um empurrãozinho no ponteiro: sem nada se mexendo
		// do lado de lá, um servidor parado não tem o que redesenhar e o
		// teste ficaria esperando por uma tela que nunca vem.
		_ = proc.Credito()
		_ = proc.PonteiroMover(40+recebidos*37%900, 40+recebidos*29%700)

		select {
		case <-prazo:
			t.Fatalf("só chegaram %d de %d quadros", recebidos, querQuadros)
		case m, aberto := <-msgs:
			if !aberto {
				t.Fatalf("canal fechou no quadro %d", recebidos)
			}
			if m.err != nil {
				t.Fatalf("canal caiu no quadro %d: %v", recebidos, m.err)
			}
			switch m.tipo {
			case telaproc.EvtDesconectado:
				// O servidor derrubar a sessão é condição de ambiente
				// (tomada de sessão por outro login, política de
				// ociosidade), não defeito do canal — e é justamente o caso
				// do BACKLOG §6, que aqui mata só o filho.
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

// Custo real de um filho em memória, medido e não estimado: é ele que
// justifica o teto de telaproc.MaxSessoes. Roda sozinho e imprime o RSS —
// quando alguém mexer no teto, é este número que precisa ser refeito.
func TestAoVivoCustoDeUmFilho(t *testing.T) {
	proc, msgs, q := conectarAoVivo(t)
	defer proc.Encerrar()

	// Deixa a sessão respirar alguns quadros: o RSS logo depois do
	// handshake ainda não tem o framebuffer todo tocado.
	for i := 0; i < 5; i++ {
		_ = proc.Credito()
		_ = proc.PonteiroMover(100+i*50, 100+i*40)
		select {
		case <-msgs:
		case <-time.After(20 * time.Second):
			t.Fatal("sessão não entregou quadros para medir")
		}
	}

	m, err := memoriaDe(proc.PID())
	if err != nil {
		t.Fatalf("não consegui medir o filho: %v", err)
	}
	privada := m["Private_Clean"] + m["Private_Dirty"]
	t.Logf("filho %d, tela %dx%d: RSS %.1f MiB | PSS %.1f MiB | PRIVADA %.1f MiB",
		proc.PID(), q.TotalW, q.TotalH,
		float64(m["Rss"])/1024, float64(m["Pss"])/1024, float64(privada)/1024)
	// O RSS conta binário e bibliotecas que TODOS os filhos compartilham,
	// então ele superestima muito o custo da segunda sessão em diante. O
	// custo marginal de verdade é a memória privada; o PSS fica no meio e
	// serve de conferência.
	t.Logf("teto telaproc.MaxSessoes = %d  =>  ~%.0f MiB pelo custo privado, ~%.0f MiB pelo PSS",
		telaproc.MaxSessoes,
		float64(privada)/1024*float64(telaproc.MaxSessoes),
		float64(m["Pss"])/1024*float64(telaproc.MaxSessoes))
}

// memoriaDe lê /proc/<pid>/smaps_rollup, que já soma o mapeamento todo do
// processo e distingue o que é privado do que é compartilhado. Valores em
// KiB.
func memoriaDe(pid int) (map[string]int, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/smaps_rollup")
	if err != nil {
		return nil, err
	}
	m := map[string]int{}
	for _, linha := range strings.Split(string(b), "\n") {
		chave, resto, ok := strings.Cut(linha, ":")
		if !ok {
			continue
		}
		campos := strings.Fields(resto)
		if len(campos) == 0 {
			continue
		}
		if v, err := strconv.Atoi(campos[0]); err == nil {
			m[chave] = v
		}
	}
	if len(m) == 0 {
		return nil, os.ErrNotExist
	}
	return m, nil
}
