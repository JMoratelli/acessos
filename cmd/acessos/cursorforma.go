package main

import "gioui.org/io/pointer"

// classificadorCursor decide, quadro a quadro, qual pointer.Cursor LOCAL
// mostrar em cima de uma sessão remota (VNC ou RDP), a partir da máscara
// alfa do cursor que o servidor manda (1 byte por pixel: 0 = transparente,
// != 0 = opaco).
//
// Nem VNC nem RDP mandam um "tipo" de cursor (seta / mão / texto / espera)
// — só um bitmap arbitrário. Não dá pra reconhecer a FORMA com certeza (a
// seta padrão e o cursor de mão são do mesmo tamanho e ambos preenchem uma
// fração pequena e pontuda da caixa), então comparar a forma contra uma
// tabela fixa classificaria a seta comum como "mão" na maior parte do
// tempo — pior que não fazer nada.
//
// Em vez disso: o PRIMEIRO cursor que a sessão recebe é, na prática,
// sempre a seta ociosa (é o que o SO remoto mostra por padrão antes de
// qualquer interação). Guardamos a FORMA dele como linha de base.
// Daí em diante: cursor parecido com a base = seta (CursorDefault); cursor
// bem DIFERENTE da base = algo mudou, e aí sim vale a pena tentar
// diferenciar os poucos casos que têm forma inconfundível (texto: faixa
// fina e alta) — o resto cai num "mão" genérico, que ainda é mais
// informação que nenhuma.
//
// "Parecido" é por SEMELHANÇA, não igualdade byte a byte: o mesmo cursor
// reenviado pelo servidor pode decodificar com uma borda de anti-
// serrilhamento um pixel diferente da vez anterior (mais comum no RDP, que
// decodifica XOR+AND em RGBA); comparar o hash exato do bitmap classificava
// isso como "mudou" o tempo todo, e a seta comum virava "mão" o tempo
// todo — visto na prática, é exatamente esse o sintoma que motivou trocar
// a assinatura exata por uma comparação com tolerância.
type classificadorCursor struct {
	temBase bool
	base    formaCursor
}

// formaCursor resume a área opaca de uma máscara: quantos pixels e a caixa
// que os envolve. Duas máscaras quase iguais (mesma forma, ruído de
// decodificação nas bordas) produzem formaCursor quase iguais; formas
// realmente diferentes (seta vs. mão vs. I-beam) não.
type formaCursor struct {
	w, h                   int
	opacos                 int
	minX, minY, maxX, maxY int
}

func medirForma(w, h int, mask []byte) (formaCursor, bool) {
	f := formaCursor{w: w, h: h, minX: w, minY: h, maxX: -1, maxY: -1}
	if w <= 0 || h <= 0 || len(mask) < w*h {
		return f, false
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if mask[y*w+x] == 0 {
				continue
			}
			f.opacos++
			if x < f.minX {
				f.minX = x
			}
			if x > f.maxX {
				f.maxX = x
			}
			if y < f.minY {
				f.minY = y
			}
			if y > f.maxY {
				f.maxY = y
			}
		}
	}
	return f, f.opacos > 0
}

// parecidaCom: mesma dimensão, área opaca dentro de ~20% uma da outra, e
// caixa envolvente dentro de 2px por lado. Tolerância grosseira de
// propósito — o objetivo é só não confundir ruído de decodificação com
// troca de forma.
func (a formaCursor) parecidaCom(b formaCursor) bool {
	if a.w != b.w || a.h != b.h {
		return false
	}
	if a.opacos == 0 || b.opacos == 0 {
		return a.opacos == b.opacos
	}
	dif := a.opacos - b.opacos
	if dif < 0 {
		dif = -dif
	}
	maior := a.opacos
	if b.opacos > maior {
		maior = b.opacos
	}
	if float64(dif)/float64(maior) > 0.20 {
		return false
	}
	const tolPx = 2
	return abs(a.minX-b.minX) <= tolPx && abs(a.maxX-b.maxX) <= tolPx &&
		abs(a.minY-b.minY) <= tolPx && abs(a.maxY-b.maxY) <= tolPx
}

// classificar consome uma atualização de cursor e devolve o pointer.Cursor
// correspondente. mask deve ter w*h bytes (w<=0 ou mask vazia = "sem
// forma", ex.: SetNull/SetDefault do RDP) — sempre cai em CursorDefault.
func (c *classificadorCursor) classificar(w, h int, mask []byte) pointer.Cursor {
	f, temForma := medirForma(w, h, mask)
	if !temForma {
		return pointer.CursorDefault
	}
	if !c.temBase {
		c.temBase = true
		c.base = f
		return pointer.CursorDefault
	}
	if f.parecidaCom(c.base) {
		return pointer.CursorDefault
	}
	return formaAproximada(f)
}

// formaAproximada distingue os poucos casos com silhueta característica.
// Tudo que não bate em nenhum caso vira "mão" (CursorPointer): já que
// SABEMOS que a forma mudou (veio de fora do "parecida com a base" acima),
// é mais provável que seja algo interativo do que a seta parada.
func formaAproximada(f formaCursor) pointer.Cursor {
	// Cursor de texto (I-beam): faixa vertical estreita ocupando quase
	// toda a altura — bem diferente de qualquer seta ou mão, que são
	// sempre mais largas que isso.
	larguraOpaca := float64(f.maxX-f.minX+1) / float64(f.w)
	if larguraOpaca <= 0.35 && f.h >= f.w {
		return pointer.CursorText
	}
	// Bloco bem preenchido (ampulheta/spinner de ocupado costuma cobrir
	// mais da metade da caixa; seta e mão são silhuetas finas, bem menos
	// que isso).
	if float64(f.opacos)/float64(f.w*f.h) >= 0.55 {
		return pointer.CursorProgress
	}
	return pointer.CursorPointer
}
