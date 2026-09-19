package main

import (
	"sync/atomic"
	"time"

	"gioui.org/app"
)

// invalidador dispara Window.Invalidate() e insiste por uma janela curta
// contra uma guarda real do Gio: mayInvalidate silencia a chamada se um
// quadro já estiver "em vôo" por qualquer motivo, e nada reagenda depois
// — quem chamou naquele instante simplesmente perde o pedido. Isso é
// inofensivo para eventos do próprio laço de quadro (tecla, mouse): o
// quadro em vôo, se houver, já reflete o estado atualizado. Mas uma
// goroutine de fundo (leitura de rede, transferência de arquivo) bate
// Invalidate() a qualquer momento, e se acertar o instante errado a tela
// só atualiza quando ALGO MAIS pedir um quadro depois (medido: 200ms de
// atraso entre o dado chegar e a tela mostrar, sem NENHUMA rede
// envolvida). As três tentativas abaixo garantem que, mesmo perdendo a
// primeira, uma das seguintes cai com a guarda já rearmada.
//
// No máximo UMA rajada de retries fica viva por vez, não importa quantas
// vezes disparar() for chamado enquanto ela dura: uma leitura contínua e
// pesada (cat de arquivo grande no SSH, transferência SFTP) chama isto a
// cada pedaço de dado, e cada chamada subindo sua própria goroutine de
// retry já chegou a somar milhares de goroutines por segundo numa sessão
// barulhenta.
type invalidador struct {
	emVoo atomic.Bool
}

// disparar chama Invalidate() na hora e arma a rajada de retries se
// nenhuma outra já estiver em andamento — a rajada em andamento cobre
// este pedido também.
func (n *invalidador) disparar(w *app.Window) {
	if w == nil {
		return
	}
	w.Invalidate()
	if !n.emVoo.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer n.emVoo.Store(false)
		for _, espera := range []time.Duration{
			8 * time.Millisecond, 24 * time.Millisecond, 64 * time.Millisecond,
		} {
			time.Sleep(espera)
			w.Invalidate()
		}
	}()
}
