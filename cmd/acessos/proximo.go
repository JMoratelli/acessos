package main

import (
	"regexp"
	"strconv"
	"strings"
)

// Duplicar uma conexão é cadastrar a PRÓXIMA, não copiar a mesma: o
// cadastro de PDV é sequencial (CAIXA5201 → CAIXA5202, 10.1.1.101 →
// 10.1.1.102). É a mesma mecânica do app original: incrementa o último
// número do nome e o último octeto do IP, pulando o que já existe, e abre
// o editor em vez de gravar calado.

var reNumeroFinal = regexp.MustCompile(`^(.*?)(\d+)(\D*)$`)

// proximoNome incrementa o último número do nome preservando os zeros à
// esquerda ("PDV 001" -> "PDV 002"). Sem número, acrescenta " 2".
func proximoNome(nome string) string {
	m := reNumeroFinal.FindStringSubmatch(nome)
	if m == nil {
		return nome + " 2"
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return nome + " 2"
	}
	larg := len(m[2])
	prox := strconv.Itoa(n + 1)
	for len(prox) < larg {
		prox = "0" + prox
	}
	return m[1] + prox + m[3]
}

// proximoHost incrementa o último octeto de um IPv4. Devolve o mesmo host
// quando não é IPv4 ou quando o octeto já está no teto (254) — quem chama
// trata "não avançou" como fim da sequência.
func proximoHost(host string) string {
	partes := strings.Split(host, ".")
	if len(partes) != 4 {
		return host
	}
	ultimo, err := strconv.Atoi(partes[3])
	if err != nil || ultimo >= 254 {
		return host
	}
	partes[3] = strconv.Itoa(ultimo + 1)
	return strings.Join(partes, ".")
}
