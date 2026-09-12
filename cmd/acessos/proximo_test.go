package main

import "testing"

func TestProximoNome(t *testing.T) {
	casos := map[string]string{
		"CAIXA5201":  "CAIXA5202",
		"PDV 001":    "PDV 002",
		"PDV 009":    "PDV 010",
		"SERV-AD":    "SERV-AD 2",
		"loja2-pdv7": "loja2-pdv8",
	}
	for entrada, esperado := range casos {
		if got := proximoNome(entrada); got != esperado {
			t.Errorf("proximoNome(%q) = %q, queria %q", entrada, got, esperado)
		}
	}
}

func TestProximoHost(t *testing.T) {
	casos := map[string]string{
		"10.1.1.101":    "10.1.1.102",
		"192.168.8.254": "192.168.8.254", // teto: não avança
		"servidor":      "servidor",
	}
	for entrada, esperado := range casos {
		if got := proximoHost(entrada); got != esperado {
			t.Errorf("proximoHost(%q) = %q, queria %q", entrada, got, esperado)
		}
	}
}
