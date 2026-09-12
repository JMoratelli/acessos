package logs

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"acessos-go/internal/massa/model"
)

// Escritor grava a saida crua em log.txt conforme ela acontece e
// monta o relatorio final, no mesmo formato do script.sh original.
type Escritor struct {
	mu  sync.Mutex
	log *os.File
}

// Novo trunca log.txt e erro.txt, como o `> arquivo` do script antigo.
func Novo(caminhoLog, caminhoErro string) (*Escritor, error) {
	f, err := os.Create(caminhoLog)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(caminhoErro, nil, 0o644); err != nil {
		f.Close()
		return nil, err
	}
	e := &Escritor{log: f}
	e.Linha(fmt.Sprintf("### Execucao iniciada em %s",
		time.Now().Format("02/01/2006 15:04:05")))
	return e, nil
}

func (e *Escritor) Linha(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.log == nil {
		return
	}
	fmt.Fprintln(e.log, s)
}

// Host grava o bloco de saida crua de um PDV.
func (e *Escritor) Host(res model.ResultadoHost, comandos []*model.Comando) {
	var b strings.Builder
	fmt.Fprintf(&b, "=== Inicio da execucao no IP: %s (%s) ===\n", res.Host.IP, res.Host.Filial)
	if res.TempoConexao > 0 {
		fmt.Fprintf(&b, "[abertura] conexao %s", res.TempoConexao.Round(time.Millisecond))
		if res.TempoElevacao > 0 {
			fmt.Fprintf(&b, " + elevacao %s", res.TempoElevacao.Round(time.Millisecond))
		}
		fmt.Fprintln(&b)
	}
	// Motivo completo, sem cortar: quando o PDV falha antes de rodar
	// comando, e nesta mensagem que esta o diagnostico (MOTD, erro do
	// shell). Cortar aqui foi o que escondia a causa.
	if res.Status != model.StatusOK && res.Motivo != "" {
		fmt.Fprintf(&b, "[%s] %s\n", strings.ToUpper(res.Status.String()), res.Motivo)
	}
	for _, rc := range res.Comandos {
		texto := ""
		if rc.IndiceCmd < len(comandos) {
			texto = comandos[rc.IndiceCmd].Texto
		}
		fmt.Fprintf(&b, "--- comando %d (%s) ---\n%s\n",
			rc.IndiceCmd+1, rc.Duracao.Round(time.Millisecond), texto)
		if rc.Saida != "" {
			fmt.Fprintln(&b, rc.Saida)
		}
		if rc.Erro != nil {
			fmt.Fprintf(&b, "[FALHA] %v\n", rc.Erro)
		} else {
			fmt.Fprintf(&b, "[exit %d]\n", rc.ExitCode)
		}
	}
	fmt.Fprintf(&b, "=== Fim da execucao no IP: %s | status: %s ===\n", res.Host.IP, res.Status)
	e.Linha(b.String())
}

// Finalizar escreve o relatorio de sucesso no fim do log.txt e o
// detalhamento de falhas no erro.txt. Tudo ordenado por loja.
func (e *Escritor) Finalizar(caminhoErro string, resultados []model.ResultadoHost) error {
	sucesso := map[string]int{}
	falhas := map[string]int{}
	detalhe := map[string][]string{}
	lojas := map[string]bool{}

	for _, r := range resultados {
		lojas[r.Host.Filial] = true
		if r.Status == model.StatusOK {
			sucesso[r.Host.Filial]++
			continue
		}
		falhas[r.Host.Filial]++
		detalhe[r.Host.Filial] = append(detalhe[r.Host.Filial],
			fmt.Sprintf("- IP: %s | %s | %s", r.Host.IP, r.Status, motivoCurto(r)))
	}

	ordenadas := chavesOrdenadas(lojas)

	var b strings.Builder
	b.WriteString("======================================\n")
	b.WriteString("RELATORIO DE SUCESSO POR LOJA\n")
	b.WriteString("======================================\n")
	total, totalOK := 0, 0
	for _, loja := range ordenadas {
		n := sucesso[loja]
		totalOK += n
		total += n + falhas[loja]
		fmt.Fprintf(&b, "%s: %d de %d\n", loja, n, n+falhas[loja])
	}
	fmt.Fprintf(&b, "--------------------------------------\nTOTAL: %d de %d\n", totalOK, total)
	e.Linha(b.String())

	var eb strings.Builder
	if len(falhas) == 0 {
		eb.WriteString("Nenhum erro detectado durante toda a execucao.\n")
	} else {
		for _, loja := range chavesOrdenadasInt(falhas) {
			eb.WriteString("======================================\n")
			fmt.Fprintf(&eb, "%s - ERRO em %d PDV(s)\n", loja, falhas[loja])
			eb.WriteString("PDVs com erro:\n")
			linhas := detalhe[loja]
			sort.Strings(linhas)
			for _, l := range linhas {
				eb.WriteString(l + "\n")
			}
			eb.WriteString("\n")
		}
	}
	return os.WriteFile(caminhoErro, []byte(eb.String()), 0o644)
}

func motivoCurto(r model.ResultadoHost) string {
	m := r.Motivo
	for _, rc := range r.Comandos {
		if rc.Erro != nil {
			m = rc.Erro.Error()
		}
	}
	m = strings.ReplaceAll(m, "\n", " ")
	m = strings.Join(strings.Fields(m), " ")
	if len(m) > 300 {
		m = m[:300] + "... (mensagem completa no log.txt)"
	}
	if m == "" {
		m = "sem detalhe"
	}
	return m
}

func chavesOrdenadas(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func chavesOrdenadasInt(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (e *Escritor) Fechar() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.log != nil {
		e.log.Close()
		e.log = nil
	}
}
