package main

import "gioui.org/app"

// Trabalho que NASCEU noutra goroutine e precisa acontecer no laço da
// janela principal.
//
// A caixa de busca é uma segunda janela, com laço próprio: quando ela
// escolhe uma máquina, quem abre a aba não pode ser a goroutine dela.
// abrirConexao mexe na tira de abas, cria processo filho e mexe em
// estado que o laço principal lê a cada quadro — duas goroutines ali
// dentro é corrida de dados, do tipo que aparece uma vez por semana e
// nunca no momento de depurar.
//
// É a mesma regra que o clipboard já segue neste app: quem publica é o
// laço, nunca as goroutines das sessões (ver clipboard.go).
var filaJanela = make(chan func(), 16)

// naJanelaPrincipal enfileira f e acorda a janela. Não bloqueia: se a
// fila encher (16 pedidos sem um quadro no meio, o que significa janela
// travada), o pedido é descartado — melhor perder uma abertura de aba do
// que pendurar a goroutine de quem chamou.
func naJanelaPrincipal(w *app.Window, f func()) {
	select {
	case filaJanela <- f:
		w.Invalidate()
	default:
	}
}

// drenarFilaJanela roda o que estiver pendente. Chamada no início de
// cada quadro da janela principal.
func drenarFilaJanela() {
	for {
		select {
		case f := <-filaJanela:
			f()
		default:
			return
		}
	}
}
