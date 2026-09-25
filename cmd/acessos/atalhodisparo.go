package main

// Quem ATENDE o atalho global, fora da linha de quem avisa.
//
// ----------------------------------------------------------------------
// O DEFEITO QUE ISTO FECHA
// ----------------------------------------------------------------------
//
// Nos dois lados o aviso do atalho chegava e o atendimento rodava ALI
// MESMO, dentro de quem avisa:
//
//   - no Linux, dentro da goroutine que consome os sinais do portal
//     (atalhoglobal_linux.go);
//   - no Windows, dentro do laço do GetMessageW (atalhoglobal_windows.go).
//
// E o atendimento não é barato: ele espera até 5 segundos pelo aperto de
// mão com o app (mandarParaApp, servico_linux.go) e depois LÊ O .ini, que
// mora num Drive sincronizado e pode engasgar em I/O. Enquanto isso:
//
//   - no Linux ninguém drena o canal de sinais. O godbus não bloqueia a
//     conexão — com o canal cheio ele abre uma goroutine por sinal
//     (deferredDeliver) —, mas essas chegam FORA DE ORDEM, e o laço sai
//     no Session.Closed: um Closed que fure a fila mata o consumidor com
//     avisos ainda pendentes;
//   - no Windows a bomba de mensagens da thread para. É pior: enquanto
//     ela está parada, o atalho inteiro deixa de responder.
//
// Este arquivo é o lugar único do atendimento, e existe justamente para
// os dois lados não saírem de sincronia de novo — era o mesmo defeito
// escrito duas vezes.
//
// POR QUE DESCARTAR, E NÃO ENFILEIRAR: apertar o atalho cinco vezes
// enquanto o app sobe tem de abrir UMA caixa de busca, não cinco. O
// guarda de "já há uma na tela" (buscaAberta) descartaria as extras de
// qualquer jeito — só que cada uma teria pago a leitura do .ini antes de
// ser jogada fora. Descartar aqui é a mesma decisão, tomada antes de
// gastar.

// disparador atende o atalho numa goroutine própria, um de cada vez.
type disparador struct {
	pedidos chan string
	parar   chan struct{}
}

// novoDisparador sobe o atendente. ao nulo devolve nil — plataforma sem
// atalho não precisa de goroutine parada à toa.
func novoDisparador(ao func(token string)) *disparador {
	if ao == nil {
		return nil
	}
	d := &disparador{
		// Capacidade 1: cabe UM pedido esperando enquanto outro é
		// atendido. Além disso é aperto repetido, que o guarda da caixa
		// descartaria adiante.
		pedidos: make(chan string, 1),
		parar:   make(chan struct{}),
	}
	go func() {
		for {
			select {
			case <-d.parar:
				return
			case tk := <-d.pedidos:
				ao(tk)
			}
		}
	}()
	return d
}

// disparar entrega o pedido SEM BLOQUEAR. É chamado de dentro do
// consumidor de sinais e do laço de mensagens — os dois lugares em que
// parar é justamente o que não pode acontecer.
func (d *disparador) disparar(token string) {
	if d == nil {
		return
	}
	select {
	case d.pedidos <- token:
	default:
		// Cheio: já há um sendo atendido e outro esperando. Ver o
		// cabeçalho — o terceiro aperto não vira uma terceira caixa.
	}
}

// pararTudo encerra o atendente. Idempotente não é: quem chama é o defer
// da goroutine que registrou o atalho, uma vez só.
func (d *disparador) pararTudo() {
	if d == nil {
		return
	}
	close(d.parar)
}
