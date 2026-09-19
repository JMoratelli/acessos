//go:build linux && !race

package main

// memoriaDe, no Linux. O irmão é memoriaproc_win_test.go — os dois dão o
// mesmo mapa para quem mede o custo de um processo-filho de sessão (ver
// telaworker_filho_test.go, que roda nos DOIS sistemas, e os testes ao
// vivo, que só existem no Linux).
//
// O nome do arquivo NÃO usa o sufixo _linux: ele valeria como restrição
// de build por cima do //go:build acima, e a armadilha silenciosa disso
// já está documentada em protocolos_tela.go. A tag aqui é explícita, e
// o !race acompanha os arquivos que chamam esta função.

import (
	"os"
	"strconv"
	"strings"
)

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
