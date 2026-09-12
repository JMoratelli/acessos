package runner

import "strings"

// mataSessao lista builtins que encerram ou substituem o shell. Como
// todos os comandos rodam na MESMA sessao (para preservar `cd` e
// variaveis), qualquer um deles derruba a conexao no meio da fila e o
// PDV aparece como "sessao encerrada pelo host".
//
// Dentro de subshell nao ha problema: `(exit 3)` so encerra o subshell.
var mataSessao = map[string]bool{
	"exit":   true,
	"logout": true,
	"exec":   true,
}

// ComandosQueMatamSessao devolve os builtins perigosos encontrados no
// nivel de cima do comando (fora de parenteses, chaves e aspas).
// Lista vazia = comando seguro sob esse aspecto.
func ComandosQueMatamSessao(cmd string) []string {
	var achados []string
	vistos := map[string]bool{}

	for _, seg := range segmentosDeTopo(cmd) {
		p := primeiraPalavra(seg)
		if mataSessao[p] && !vistos[p] {
			vistos[p] = true
			achados = append(achados, p)
		}
	}
	return achados
}

// segmentosDeTopo quebra o comando nos separadores de shell que estao
// no nivel zero de aninhamento e fora de aspas.
func segmentosDeTopo(cmd string) []string {
	var segs []string
	var atual strings.Builder

	fecha := func() {
		if s := strings.TrimSpace(atual.String()); s != "" {
			segs = append(segs, s)
		}
		atual.Reset()
	}

	aspas := byte(0)
	prof := 0

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		if aspas != 0 {
			atual.WriteByte(c)
			if c == aspas && (i == 0 || cmd[i-1] != '\\') {
				aspas = 0
			}
			continue
		}

		switch c {
		case '\'', '"', '`':
			aspas = c
			atual.WriteByte(c)
			continue
		case '#':
			// comentario ate o fim da linha
			for i < len(cmd) && cmd[i] != '\n' {
				i++
			}
			fecha()
			continue
		case '(', '{':
			prof++
			atual.WriteByte(c)
			continue
		case ')', '}':
			if prof > 0 {
				prof--
			}
			atual.WriteByte(c)
			continue
		}

		if prof > 0 {
			atual.WriteByte(c)
			continue
		}

		switch {
		case c == '\n' || c == ';' || c == '&':
			fecha()
		case c == '|':
			fecha()
		default:
			atual.WriteByte(c)
		}
	}
	fecha()
	return segs
}
