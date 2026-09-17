//go:build linux || windows

package main

import (
	"image"
	"sync/atomic"

	"acessos-go/internal/telaproc"
)

// O que as abas de tela remota (VNC e RDP) compartilham do lado do
// PROCESSO PRINCIPAL: montar a tela a partir dos retângulos que o filho
// manda, e o vocabulário de "por que a sessão terminou".
//
// O lado de lá, dentro do filho, está em telaworker.go.

// fimSessao diz por que uma sessão terminou, e com isso o que fazer em
// seguida.
type fimSessao int

const (
	fimParar   fimSessao = iota // aba fechada: não volta
	fimReligar                  // pedido manual: reconecta já
	fimCaiu                     // caiu sozinha (inclusive crash do filho)

	// fimFalhou é a conexão que NÃO subiu: credencial recusada, servidor
	// dizendo não. Não entra no backoff de propósito — insistir de poucos
	// em poucos segundos com a senha errada não é persistência, é força
	// bruta contra o próprio parque, e em domínio Windows bloqueia a conta
	// do operador. Quem quiser tentar de novo clica em Reconectar, que é o
	// comportamento que estas abas sempre tiveram.
	fimFalhou
)

// aplicarQuadro cola o retângulo recebido na tela acumulada, criando ou
// trocando a imagem quando a resolução remota muda.
func aplicarQuadro(acum *image.NRGBA, q telaproc.Quadro, pix []byte) *image.NRGBA {
	tw, th := int(q.TotalW), int(q.TotalH)
	if tw <= 0 || th <= 0 {
		return acum
	}
	if acum == nil || acum.Rect.Dx() != tw || acum.Rect.Dy() != th {
		acum = image.NewNRGBA(image.Rect(0, 0, tw, th))
	}
	x, y, w, h := int(q.X), int(q.Y), int(q.W), int(q.H)
	if x < 0 || y < 0 || x+w > tw || y+h > th {
		return acum
	}
	for linha := 0; linha < h; linha++ {
		dst := acum.Pix[(y+linha)*acum.Stride+x*4:]
		copy(dst[:w*4], pix[linha*w*4:])
	}
	return acum
}

// publicarTela congela a tela acumulada numa imagem nova e a entrega ao
// desenho. A cópia é o preço de não precisar de trava nenhuma do lado do
// Gio: a imagem publicada não muda mais depois de publicada, então a
// textura pode subir para a GPU com calma enquanto o próximo retângulo já
// está sendo colado no acumulador.
//
// Antes disto a interface fazia, A CADA QUADRO DELA, uma cópia da tela
// inteira vinda do C mais uma conversão BGRX->NRGBA pixel a pixel — mesmo
// quando nada tinha mudado na sessão remota. Agora a cópia acontece uma
// vez por quadro REMOTO, e fora da thread que desenha.
func publicarTela(destino *atomic.Pointer[image.NRGBA], acum *image.NRGBA) {
	if acum == nil {
		return
	}
	pub := image.NewNRGBA(acum.Rect)
	copy(pub.Pix, acum.Pix)
	destino.Store(pub)
}
