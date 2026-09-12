package runner

import "strings"

// comandosTolerantes retornam codigo != 0 em situacoes perfeitamente
// normais (grep sem match, diff com diferenca, find sem permissao...).
var comandosTolerantes = map[string]bool{
	"grep":   true,
	"egrep":  true,
	"fgrep":  true,
	"zgrep":  true,
	"diff":   true,
	"cmp":    true,
	"rsync":  true,
	"pgrep":  true,
	"pkill":  true,
	"test":   true,
	"[":      true,
	"find":   true,
	"mount":  true,
	"umount": true,
}

// DeveIgnorarExit olha o ultimo segmento efetivo do comando, porque no
// bash o codigo de saida de uma pipeline e o do ultimo elemento.
//
// A heuristica nao e perfeita: comando muito composto pode enganar ela.
// Preferimos errar perguntando demais a engolir um erro real em 200 PDVs.
func DeveIgnorarExit(cmd string) bool {
	ultimo := ultimoSegmento(cmd)
	if ultimo == "" {
		return false
	}
	return comandosTolerantes[primeiraPalavra(ultimo)]
}

// ultimoSegmento devolve o trecho apos o ultimo separador de shell
// relevante, ignorando o que estiver dentro de aspas.
func ultimoSegmento(cmd string) string {
	linhas := strings.Split(cmd, "\n")
	// ultima linha nao vazia e nao comentario
	ultima := ""
	for i := len(linhas) - 1; i >= 0; i-- {
		t := strings.TrimSpace(linhas[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		ultima = t
		break
	}
	if ultima == "" {
		return ""
	}

	seps := []string{"&&", "||", "|", ";"}
	melhor := -1
	tam := 0
	dentroAspas := byte(0)
	for i := 0; i < len(ultima); i++ {
		c := ultima[i]
		if dentroAspas != 0 {
			if c == dentroAspas {
				dentroAspas = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			dentroAspas = c
			continue
		}
		for _, s := range seps {
			if strings.HasPrefix(ultima[i:], s) {
				if i > melhor {
					melhor = i
					tam = len(s)
				}
			}
		}
	}
	if melhor >= 0 {
		return strings.TrimSpace(ultima[melhor+tam:])
	}
	return ultima
}

func primeiraPalavra(s string) string {
	s = strings.TrimSpace(s)
	// pula atribuicoes de ambiente tipo LC_ALL=C comando
	for {
		campos := strings.Fields(s)
		if len(campos) == 0 {
			return ""
		}
		p := campos[0]
		if strings.Contains(p, "=") && !strings.HasPrefix(p, "=") {
			s = strings.TrimSpace(strings.TrimPrefix(s, p))
			continue
		}
		if p == "sudo" || p == "time" || p == "nohup" ||
			p == "then" || p == "else" || p == "elif" || p == "do" {
			s = strings.TrimSpace(strings.TrimPrefix(s, p))
			continue
		}
		// remove caminho: /usr/bin/grep -> grep
		if i := strings.LastIndex(p, "/"); i >= 0 {
			p = p[i+1:]
		}
		return p
	}
}
