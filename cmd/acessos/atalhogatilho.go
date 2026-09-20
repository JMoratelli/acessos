package main

// O que o SISTEMA amarrou no atalho global — e o aviso que faltava
// quando ele não amarrou nada.
//
// ----------------------------------------------------------------------
// O DEFEITO QUE ISTO FECHA
// ----------------------------------------------------------------------
//
// No Wayland o app não pega tecla sozinho: pede ao portal
// GlobalShortcuts, e o KDE confirma UMA VEZ por aplicativo. Quem fechar
// esse diálogo sem querer fica com o atalho registrado e SEM TECLA
// NENHUMA — o Ctrl+Shift+F12 simplesmente não faz nada, e nada na tela
// explica por quê.
//
// O estado já era conhecido: AtalhoGlobal.Gatilho vazio significa
// exatamente isso (ver atalhoglobal_linux.go). Mas virava uma linha em
// stderr, que ninguém lê — menos ainda num Flatpak aberto pelo menu.
//
// E há um segundo problema, que é o motivo deste arquivo existir em vez
// de um if nos Ajustes: QUEM SABE NÃO É QUEM PODE MOSTRAR. No Linux quem
// registra o atalho é o processo -servico, que não tem janela; o diálogo
// de Ajustes vive no app. O estado atravessa o socket (msgGatilho, ver
// instancia.go) e para aqui.
//
// Fora do Linux não há serviço: o próprio app registra e preenche isto
// na hora (ver main.go). Lá o estado "registrado sem tecla" não existe —
// RegisterHotKey ou amarra a tecla ou devolve erro —, então o aviso
// nunca aparece, sem precisar de if de plataforma no desenho.

import "sync/atomic"

// estadoGatilho é o que se sabe sobre a tecla do atalho global.
type estadoGatilho struct {
	// Sabido separa "ainda não chegou notícia" de "chegou, e é vazia".
	// Sem ele os dois seriam a mesma string vazia, e os Ajustes abertos
	// no primeiro segundo acusariam "sem tecla" antes de o serviço ter
	// tido chance de responder — um alarme falso a cada abertura.
	Sabido bool
	// Tecla é o que o sistema amarrou, como ele descreve ("Ctrl+Shift+F12").
	//
	// Vazia COM Sabido = não há tecla amarrada AGORA, e isso cobre três
	// caminhos diferentes: o diálogo do KDE foi recusado, o registro
	// falhou (portal fora do ar), ou a sessão do portal caiu. Os três
	// dão o mesmo sintoma — o atalho não faz nada —, e é por isso que a
	// mensagem na tela fala do fato ("não amarrou tecla") em vez de
	// chutar a causa.
	Tecla string
}

// Escrito da goroutine que lê o socket e lido do laço de quadro: são
// goroutines diferentes, daí o atomic em vez de uma variável comum.
var gatilhoAtual atomic.Pointer[estadoGatilho]

func definirGatilho(tecla string) {
	gatilhoAtual.Store(&estadoGatilho{Sabido: true, Tecla: tecla})
}

func lerEstadoGatilho() estadoGatilho {
	if e := gatilhoAtual.Load(); e != nil {
		return *e
	}
	return estadoGatilho{}
}

// atalhoSemTecla é a pergunta que os Ajustes fazem: o atalho está
// registrado e não pegou tecla nenhuma?
func atalhoSemTecla() bool {
	e := lerEstadoGatilho()
	return e.Sabido && e.Tecla == ""
}
