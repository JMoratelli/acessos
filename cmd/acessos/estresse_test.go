//go:build linux && !race

package main

// Teste de ESTRESSE: muitas sessões remotas ao mesmo tempo, contra máquinas
// de verdade. Desligado por padrão.
//
// Rodando no terminal, ele PERGUNTA em quais máquinas bater:
//
//	go test ./cmd/acessos/ -run Estresse -v -timeout 30m
//
// Perguntar é melhor que exigir variável de ambiente por dois motivos: a
// senha digitada não fica no histórico do shell nem no /proc/<pid>/environ
// de quem passar por perto, e a confirmação com a lista expandida na tela
// é a última chance de perceber que se digitou a faixa errada — do outro
// lado costumam estar caixas em operação, não laboratório.
//
// Para rodar sem ninguém olhando (script, CI), as variáveis continuam
// valendo e desligam a pergunta:
//
//	ACESSOS_ESTRESSE_HOSTS=192.168.0.101-140 ACESSOS_ESTRESSE_SENHA=... \
//	go test ./cmd/acessos/ -run Estresse -v -timeout 30m
//
// Variáveis: ACESSOS_ESTRESSE_PROTO (vnc|rdp, padrão vnc),
// ACESSOS_ESTRESSE_PORTA, ACESSOS_ESTRESSE_SEGUNDOS (padrão 60),
// ACESSOS_ESTRESSE_USUARIO, ACESSOS_ESTRESSE_VISIVEIS (padrão 1).
//
// ACESSOS_ESTRESSE_VISIVEIS é o que torna o teste parecido com o uso real:
// por mais abas que estejam abertas, só UMA está à vista. As demais pedem
// quadro devagar (ver creditarConformeVisibilidade). Subir este número
// simula o caso impossível de várias abas visíveis ao mesmo tempo, que
// serve para medir o pior caso de banda.
//
// O que ele exercita, que é justamente o que a arquitetura de processo por
// sessão introduziu de risco: dezenas de processos-filhos vivos ao mesmo
// tempo, um socket para cada, quadros atravessando todos eles, e o
// consumo de memória somado. Monta a tela de cada sessão do MESMO jeito
// que a aba monta (aplicarQuadro + publicarTela), para a conta de memória
// do lado do processo principal ser a de verdade, e não uma otimista.
//
// NUNCA envia ponteiro nem teclado. As máquinas do outro lado são caixas
// em operação: mexer no cursor de quem está trabalhando não é teste, é
// estrago.
//
// Fora do -race pelo mesmo motivo dos outros testes que sobem filho com
// cgo (ver telaworker_filho_test.go).

import (
	"bufio"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"acessos-go/internal/telaproc"

	"golang.org/x/term"
)

// ---------------------------------------------------------- perguntas
//
// As perguntas saem no STDERR, e não via t.Log: o testing guarda o que
// passa por ele e só imprime no fim do teste, o que faria a pergunta
// aparecer depois da resposta.

func interativo() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func perguntar(rotulo string) string {
	fmt.Fprint(os.Stderr, rotulo)
	linha, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && linha == "" {
		return ""
	}
	return strings.TrimSpace(linha)
}

func perguntarSenha(rotulo string) string {
	fmt.Fprint(os.Stderr, rotulo)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return ""
	}
	return string(b)
}

// alvoEstresse decide em quem bater: variáveis de ambiente quando
// definidas, pergunta quando há alguém no terminal, e desiste quando não há
// nem uma coisa nem outra.
func alvoEstresse(t *testing.T) (hosts []string, senha, usuario string) {
	t.Helper()

	spec := os.Getenv("ACESSOS_ESTRESSE_HOSTS")
	senha = os.Getenv("ACESSOS_ESTRESSE_SENHA")
	usuario = os.Getenv("ACESSOS_ESTRESSE_USUARIO")
	perguntou := false

	if spec == "" {
		if !interativo() {
			t.Skip("teste de estresse desligado: rode num terminal para ele perguntar," +
				" ou defina ACESSOS_ESTRESSE_HOSTS")
		}
		spec = perguntar("Hosts para estressar (ex.: 10.0.0.101-140, ou separados por vírgula): ")
		if spec == "" {
			t.Skip("nenhum host informado")
		}
		perguntou = true
	}

	hosts, err := expandirHosts(spec)
	if err != nil {
		t.Fatalf("hosts: %v", err)
	}

	if perguntou {
		if senha == "" {
			senha = perguntarSenha("Senha (vazio se não houver): ")
		}
		// Confirmação com a lista expandida: é aqui que se percebe a faixa
		// digitada errada, ANTES de abrir conexão em máquina de gente
		// trabalhando.
		fmt.Fprintf(os.Stderr, "\n%d máquinas: %s … %s\n",
			len(hosts), hosts[0], hosts[len(hosts)-1])
		if r := perguntar("Confirma bater em todas elas? [s/N]: "); !strings.EqualFold(r, "s") {
			t.Skip("cancelado por quem rodou")
		}
	}
	return hosts, senha, usuario
}

// expandirHosts aceita "192.168.0.101-140", "a,b,c" ou os dois juntos.
func expandirHosts(spec string) ([]string, error) {
	var out []string
	for _, parte := range strings.Split(spec, ",") {
		parte = strings.TrimSpace(parte)
		if parte == "" {
			continue
		}
		base, faixa, temFaixa := strings.Cut(parte, "-")
		if !temFaixa {
			out = append(out, base)
			continue
		}
		ponto := strings.LastIndex(base, ".")
		if ponto < 0 {
			return nil, fmt.Errorf("faixa %q não parece um IP", parte)
		}
		ini, err := strconv.Atoi(base[ponto+1:])
		if err != nil {
			return nil, fmt.Errorf("início inválido em %q", parte)
		}
		fim, err := strconv.Atoi(faixa)
		if err != nil {
			return nil, fmt.Errorf("fim inválido em %q", parte)
		}
		if fim < ini {
			return nil, fmt.Errorf("faixa invertida em %q", parte)
		}
		for i := ini; i <= fim; i++ {
			out = append(out, fmt.Sprintf("%s.%d", base[:ponto], i))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nenhum host em %q", spec)
	}
	return out, nil
}

func envInt(chave string, padrao int) int {
	if v := os.Getenv(chave); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return padrao
}

// resultadoSessao é o que cada sessão relata no fim.
type resultadoSessao struct {
	host      string
	visivel   bool
	conectou  bool
	quadros   int
	bytes     int64
	telaW     int32
	telaH     int32
	morreu    bool   // o filho caiu sozinho no meio
	motivo    string // por que não conectou / por que caiu
	rssFinal  int    // KiB, do filho, medido perto do fim
	privFinal int    // KiB
}

func TestEstresseMuitasSessoes(t *testing.T) {
	hosts, senha, usuario := alvoEstresse(t)
	proto := os.Getenv("ACESSOS_ESTRESSE_PROTO")
	if proto == "" {
		proto = "vnc"
	}
	portaPadrao := 5900
	if proto == "rdp" {
		portaPadrao = 3389
	}
	porta := envInt("ACESSOS_ESTRESSE_PORTA", portaPadrao)
	duracao := time.Duration(envInt("ACESSOS_ESTRESSE_SEGUNDOS", 60)) * time.Second
	visiveis := envInt("ACESSOS_ESTRESSE_VISIVEIS", 1)

	t.Logf("estresse: %d hosts (%d como aba visível), protocolo %s, porta %d, por %s",
		len(hosts), visiveis, proto, porta, duracao)
	t.Logf("memória livre no início: %d MiB", livreMiB(t))

	fim := time.Now().Add(duracao)
	resultados := make([]resultadoSessao, len(hosts))
	var wg sync.WaitGroup
	var conectadas atomic.Int32

	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			resultados[i] = rodarSessaoEstresse(t, credenciaisEstresse{
				proto: proto, host: host, porta: porta,
				usuario: usuario, senha: senha,
			}, fim, &conectadas, i < visiveis)
		}(i, host)
	}

	// Enquanto rodam, acompanha a memória da máquina: é o número que diz
	// se dezenas de sessões cabem de verdade ou se a máquina começa a
	// sofrer.
	pararRelogio := make(chan struct{})
	go func() {
		for {
			select {
			case <-pararRelogio:
				return
			case <-time.After(15 * time.Second):
				t.Logf("  ... %d sessões vivas, %d MiB livres na máquina",
					conectadas.Load(), livreMiB(t))
			}
		}
	}()

	wg.Wait()
	close(pararRelogio)
	relatar(t, resultados, len(hosts))
}

// credenciaisEstresse junta o que uma sessão precisa para subir. É struct
// e não seis parâmetros soltos porque a ordem de "usuario, senha" trocada
// por engano não daria erro de compilação nenhum.
type credenciaisEstresse struct {
	proto, host    string
	porta          int
	usuario, senha string
}

func rodarSessaoEstresse(t *testing.T, cred credenciaisEstresse, fim time.Time, conectadas *atomic.Int32, visivel bool) resultadoSessao {
	proto, host, porta := cred.proto, cred.host, cred.porta
	r := resultadoSessao{host: host, visivel: visivel}
	parar := make(chan struct{})
	defer close(parar)

	proc, err := telaproc.Iniciar(proto)
	if err != nil {
		r.motivo = "não subiu o processo: " + err.Error()
		return r
	}
	defer proc.Encerrar()

	if err := proc.Conectar(telaproc.Ligacao{
		Host: host, Porta: porta,
		Usuario: cred.usuario, Senha: cred.senha,
	}); err != nil {
		r.motivo = "não pedi a conexão: " + err.Error()
		return r
	}

	// acum e tela: a MESMA montagem que a aba faz, para a memória do lado
	// de cá ser a de verdade.
	var acum *image.RGBA
	var tela atomic.Pointer[image.RGBA]

	msgs := lerAoVivo(proc)
	prazoConexao := time.After(45 * time.Second)
	medido := false

	// O pulso é o que permite a esta goroutine acordar mesmo quando a tela
	// remota está PARADA. Sem ele, um caixa ocioso não manda quadro
	// nenhum, o select fica bloqueado e a sessão nunca percebe que o
	// tempo do teste acabou — a primeira versão deste arquivo travava
	// exatamente assim, e o teste inteiro morria no timeout.
	pulso := time.NewTicker(time.Second)
	defer pulso.Stop()

	for {
		select {
		case <-pulso.C:
			if time.Now().After(fim) {
				if r.conectou {
					conectadas.Add(-1)
				}
				return r
			}
		case <-prazoConexao:
			if !r.conectou {
				r.motivo = "não conectou em 45s"
				return r
			}
			prazoConexao = nil
		case m, aberto := <-msgs:
			if !aberto {
				if r.conectou {
					r.morreu = true
					conectadas.Add(-1)
				}
				if r.motivo == "" {
					r.motivo = "canal fechou"
				}
				return r
			}
			if m.err != nil {
				if r.conectou {
					r.morreu = true
					conectadas.Add(-1)
					r.motivo = "canal caiu: " + m.err.Error()
				}
				return r
			}
			switch m.tipo {
			case telaproc.EvtConectado:
				r.conectou = true
				conectadas.Add(1)
				creditarConformeVisibilidade(proc, visivel, parar)
			case telaproc.EvtFalha:
				r.motivo = "servidor recusou: " + string(m.corpo)
				return r
			case telaproc.EvtDesconectado:
				if r.conectou {
					conectadas.Add(-1)
				}
				r.motivo = "desconectou: " + string(m.corpo)
				return r
			case telaproc.EvtQuadro:
				q, pix, err := telaproc.DecodificarQuadro(m.corpo)
				if err != nil {
					r.motivo = "quadro inválido: " + err.Error()
					return r
				}
				acum = aplicarQuadro(acum, q, pix)
				publicarTela(&tela, acum)
				r.quadros++
				r.bytes += int64(len(m.corpo))
				r.telaW, r.telaH = q.TotalW, q.TotalH

				// Mede o filho uma vez, já com a sessão trabalhando.
				if !medido && r.quadros >= 3 {
					medido = true
					if mm, err := memoriaDe(proc.PID()); err == nil {
						r.rssFinal = mm["Rss"]
						r.privFinal = mm["Private_Clean"] + mm["Private_Dirty"]
					}
				}
				if time.Now().After(fim) {
					conectadas.Add(-1)
					return r
				}
				creditarConformeVisibilidade(proc, visivel, parar)
			}
		}
	}
}

func livreMiB(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	for _, linha := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(linha, "MemAvailable:") {
			continue
		}
		campos := strings.Fields(linha)
		if len(campos) < 2 {
			return -1
		}
		kb, err := strconv.Atoi(campos[1])
		if err != nil {
			return -1
		}
		return kb / 1024
	}
	return -1
}

func relatar(t *testing.T, rs []resultadoSessao, total int) {
	t.Helper()
	var conectaram, morreram, semQuadro int
	var somaQuadros int
	var somaBytes int64
	var somaRSS, somaPriv, medidas int

	for _, r := range rs {
		if r.conectou {
			conectaram++
		}
		if r.morreu {
			morreram++
		}
		if r.conectou && r.quadros == 0 {
			semQuadro++
		}
		somaQuadros += r.quadros
		somaBytes += r.bytes
		if r.privFinal > 0 {
			somaRSS += r.rssFinal
			somaPriv += r.privFinal
			medidas++
		}
	}

	t.Logf("──────── RESULTADO ────────")
	t.Logf("hosts alvo:        %d", total)
	t.Logf("conectaram:        %d", conectaram)
	t.Logf("morreram no meio:  %d", morreram)
	t.Logf("sem quadro nenhum: %d", semQuadro)
	t.Logf("quadros no total:  %d (%.1f MiB de pixels)", somaQuadros, float64(somaBytes)/(1<<20))
	var visQuadros, escQuadros int
	var visBytes, escBytes int64
	for _, r := range rs {
		if r.visivel {
			visQuadros += r.quadros
			visBytes += r.bytes
		} else {
			escQuadros += r.quadros
			escBytes += r.bytes
		}
	}
	t.Logf("  abas à vista:    %d quadros (%.1f MiB)", visQuadros, float64(visBytes)/(1<<20))
	t.Logf("  abas escondidas: %d quadros (%.1f MiB)", escQuadros, float64(escBytes)/(1<<20))
	if medidas > 0 {
		t.Logf("por filho (média de %d): RSS %.1f MiB | PRIVADA %.1f MiB",
			medidas, float64(somaRSS)/float64(medidas)/1024, float64(somaPriv)/float64(medidas)/1024)
		t.Logf("privada somada dos filhos: %.0f MiB", float64(somaPriv)/1024)
	}
	t.Logf("memória livre no fim: %d MiB", livreMiB(t))

	for _, r := range rs {
		if r.motivo != "" && !r.conectou {
			t.Logf("  %s: %s", r.host, r.motivo)
		}
	}

	// O que FALHA o teste é instabilidade da nossa arquitetura, não host
	// desligado: caixa fora do ar é condição de campo, filho que morre
	// sozinho depois de conectar é defeito nosso.
	if morreram > 0 {
		t.Errorf("%d sessões caíram sozinhas depois de conectar — isto é instabilidade, não host fora do ar", morreram)
	}
	if conectaram == 0 {
		t.Fatal("nenhuma sessão conectou: confira hosts, senha e rede antes de concluir qualquer coisa")
	}
	if semQuadro > 0 {
		t.Errorf("%d sessões conectaram mas não entregaram quadro nenhum", semQuadro)
	}
}
