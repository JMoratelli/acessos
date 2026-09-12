package snippets

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"acessos-go/internal/massa/model"
)

const snippetsExemplo = `# comandos.ini - biblioteca de snippets
#
# [id]
# descricao   = texto que aparece na lista
# plataforma  = linux | windows | ambos
# root        = sim | nao   (sugestao de elevacao)
# ignorar_exit= sim | nao   (nao tratar exit != 0 como falha)
# comando     = primeira linha
#               linhas seguintes indentadas fazem parte do mesmo comando

[data_hora]
descricao    = Mostrar data e hora do PDV
plataforma   = linux
root         = nao
ignorar_exit = nao
comando      = date "+%d/%m/%Y %H:%M:%S"

[uptime_disco]
descricao    = Uptime, memoria e uso de disco
plataforma   = linux
root         = nao
ignorar_exit = nao
comando      = uptime
               free -h
               df -h /

[versao_pdv]
descricao    = Versao do pacote do PDV
plataforma   = linux
root         = sim
ignorar_exit = sim
comando      = ls -la /home/zanthus 2>/dev/null | head -n 20

[reiniciar_pdv]
descricao    = Reiniciar o PDV (CUIDADO)
plataforma   = linux
root         = sim
ignorar_exit = nao
comando      = shutdown -r +1 "Reinicio agendado pelo suporte"
`

// CarregarSnippets le comandos.ini, criando um arquivo de exemplo se
// ele ainda nao existir.
func CarregarSnippets(caminho string) ([]model.Snippet, error) {
	if _, err := os.Stat(caminho); os.IsNotExist(err) {
		if err := os.WriteFile(caminho, []byte(snippetsExemplo), 0o644); err != nil {
			return nil, err
		}
	}

	secoes, err := LerINI(caminho)
	if err != nil {
		return nil, err
	}

	var out []model.Snippet
	for _, s := range secoes {
		if s.Nome == "" {
			continue
		}
		plat := model.Plataforma(strings.ToLower(s.Get("plataforma", "linux")))
		switch plat {
		case model.Linux, model.Windows, model.Ambos:
		default:
			plat = model.Linux
		}
		out = append(out, model.Snippet{
			ID:          s.Nome,
			Descricao:   s.Get("descricao", s.Nome),
			Comando:     s.Get("comando", ""),
			Root:        s.GetBool("root", false),
			IgnorarExit: s.GetBool("ignorar_exit", false),
			Plataforma:  plat,
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Descricao < out[b].Descricao })
	return out, nil
}

// SalvarSnippets reescreve comandos.ini ordenado por descricao.
func SalvarSnippets(caminho string, snips []model.Snippet) error {
	ordenados := make([]model.Snippet, len(snips))
	copy(ordenados, snips)
	sort.Slice(ordenados, func(a, b int) bool { return ordenados[a].Descricao < ordenados[b].Descricao })

	var b strings.Builder
	b.WriteString("# comandos.ini - biblioteca de snippets\n")
	b.WriteString("# Reescrito e reordenado por descricao a cada gravacao.\n\n")

	for _, s := range ordenados {
		fmt.Fprintf(&b, "[%s]\n", s.ID)
		fmt.Fprintf(&b, "descricao    = %s\n", s.Descricao)
		fmt.Fprintf(&b, "plataforma   = %s\n", s.Plataforma)
		fmt.Fprintf(&b, "root         = %s\n", simNao(s.Root))
		fmt.Fprintf(&b, "ignorar_exit = %s\n", simNao(s.IgnorarExit))
		fmt.Fprintf(&b, "comando      = %s\n\n", IndentarMultilinha(s.Comando))
	}
	return os.WriteFile(caminho, []byte(b.String()), 0o644)
}

func simNao(v bool) string {
	if v {
		return "sim"
	}
	return "nao"
}

// IDValido normaliza um texto livre para servir de id de secao.
func IDValido(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "snippet"
	}
	return out
}
