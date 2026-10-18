package main

import (
	"image/color"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
)

// HOVER COM TRANSIÇÃO
//
// Trocar a cor do hover de uma vez é o que fazia o card "piscar" ao
// passar o mouse por cima da grade. Uma transição curta — mais curta
// do que o olho registra como animação — lê como material mudando de
// luz, não como estado trocando.
//
// O estado mora num mapa por chave (o *widget.Clickable do componente,
// que vive enquanto ele vive). Com trava: a caixa do atalho global e a
// janela de sessão desenham botões nas goroutines delas.

const duracaoHover = 120 * time.Millisecond

type animHover struct {
	alvo  bool
	desde time.Time
	de    float32
}

func (a *animHover) valor(agora time.Time) float32 {
	alvo := float32(0)
	if a.alvo {
		alvo = 1
	}
	p := float32(agora.Sub(a.desde)) / float32(duracaoHover)
	if p >= 1 {
		return alvo
	}
	if p < 0 {
		p = 0
	}
	// saída suave (ease-out): começa rápido, assenta devagar
	p = 1 - (1-p)*(1-p)
	return a.de + (alvo-a.de)*p
}

var (
	animMu sync.Mutex
	anims  = map[any]*animHover{}
)

// progressoHover devolve 0 (repouso) a 1 (hover) para o componente da
// chave, andando em duracaoHover a cada troca. Na primeira vez que vê a
// chave já devolve o destino: nada anima ao aparecer na tela.
func progressoHover(gtx layout.Context, chave any, ativo bool) float32 {
	animMu.Lock()
	a := anims[chave]
	if a == nil {
		a = &animHover{alvo: ativo, desde: gtx.Now.Add(-duracaoHover)}
		anims[chave] = a
	}
	if ativo != a.alvo {
		a.de, a.alvo, a.desde = a.valor(gtx.Now), ativo, gtx.Now
	}
	v := a.valor(gtx.Now)
	andando := gtx.Now.Sub(a.desde) < duracaoHover
	animMu.Unlock()
	if andando {
		gtx.Execute(op.InvalidateCmd{})
	}
	return v
}

// misturarCor interpola duas cores (alfa junto), t de 0 a 1.
func misturarCor(a, b color.NRGBA, t float32) color.NRGBA {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	m := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t + 0.5) }
	return color.NRGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), m(a.A, b.A)}
}
