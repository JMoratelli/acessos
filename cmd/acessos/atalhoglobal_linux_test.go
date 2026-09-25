package main

import (
	"testing"
	"time"
)

func TestAvisarMudancaNaoBloqueia(t *testing.T) {
	// avisarMudanca roda DENTRO do consumidor de sinais do portal. Se ele
	// bloqueasse quando ninguém leu o aviso anterior, seria exatamente o
	// defeito que o canal existe para fechar.
	ch := make(chan string, 1)
	pronto := make(chan struct{})
	go func() {
		avisarMudanca(ch, "Ctrl+Shift+F12")
		avisarMudanca(ch, "Ctrl+Alt+B")
		avisarMudanca(ch, "Meta+K")
		close(pronto)
	}()
	select {
	case <-pronto:
	case <-time.After(prazoTeste):
		t.Fatal("avisarMudanca BLOQUEOU sem ninguém lendo")
	}
}

func TestAvisarMudancaEntregaAMaisRecente(t *testing.T) {
	// A tecla intermediária não interessa: o que a janela mostra é a de
	// AGORA. Um aviso velho não lido é substituído, não enfileirado.
	ch := make(chan string, 1)
	avisarMudanca(ch, "Ctrl+Shift+F12")
	avisarMudanca(ch, "Meta+K")
	select {
	case tecla := <-ch:
		if tecla != "Meta+K" {
			t.Fatalf("entregou %q, queria a mais recente", tecla)
		}
	default:
		t.Fatal("não entregou nada")
	}
	select {
	case tecla := <-ch:
		t.Fatalf("sobrou aviso velho na fila: %q", tecla)
	default:
	}
}

func TestAvisarMudancaAceitaTeclaVazia(t *testing.T) {
	// Vazia é notícia legítima: significa que a tecla foi SOLTA em
	// Preferências do Sistema, e é o que faz a janela avisar "sem tecla"
	// em vez de seguir anunciando a antiga.
	ch := make(chan string, 1)
	avisarMudanca(ch, "")
	select {
	case tecla := <-ch:
		if tecla != "" {
			t.Fatalf("entregou %q", tecla)
		}
	default:
		t.Fatal("engoliu a notícia de tecla solta")
	}
}
