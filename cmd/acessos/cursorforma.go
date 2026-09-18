package main

import "gioui.org/io/pointer"

// classificadorCursor decide qual pointer.Cursor LOCAL mostrar em cima de
// uma sessão remota (VNC ou RDP), a partir do que o servidor manda sobre o
// cursor: a máscara de opacidade (1 byte por pixel) e o PONTO QUENTE
// (xhot/yhot) — a coordenada, dentro do bitmap, que é "a ponta" do cursor.
//
// ----------------------------------------------------------------------
// POR QUE ISTO EXISTE
// ----------------------------------------------------------------------
//
// Nem o RDP nem o VNC dizem QUE cursor é ("seta", "mão", "ocupado"): os
// dois mandam um bitmap arbitrário. Desenhar o bitmap do servidor não é
// opção neste app (o cursor local continua sendo do compositor), então a
// única saída é aproximar a FORMA para um dos cursores nomeados do Gio.
//
// ----------------------------------------------------------------------
// O QUE MUDOU, E POR QUE A VERSÃO ANTERIOR ERRAVA
// ----------------------------------------------------------------------
//
// A versão anterior guardava o PRIMEIRO cursor da sessão como "linha de
// base" e chamava de seta tudo o que se parecesse com ele; o resto virava
// mão. Dois defeitos, os dois relatados em uso:
//
//   - **mão presa**: se o primeiro cursor da sessão não fosse a seta
//     (chega-se numa tela de logon com ampulheta, ou com o ponteiro já em
//     cima de um botão), a base ficava errada PARA SEMPRE e a seta comum
//     passava a ser "diferente da base" — ou seja, mão, o tempo inteiro,
//     até a sessão cair;
//   - **nada de "carregando"**: o ocupado só saía se o bitmap preenchesse
//     mais de 55% da caixa, o que a ampulheta/anel do Windows não faz.
//
// Agora não há linha de base nenhuma: cada forma é classificada por si,
// sempre com o mesmo resultado para o mesmo bitmap. Uma sessão que comece
// errada não contamina o resto dela.
//
// ----------------------------------------------------------------------
// COMO CLASSIFICA
// ----------------------------------------------------------------------
//
// O ponto quente é o sinal mais forte e é de graça — vem no mesmo evento
// e a versão anterior o descartava. Ele separa as famílias sozinho:
//
//	canto superior esquerdo  -> seta (e variantes: seta+ampulheta)
//	topo, um pouco à direita -> mão
//	centro                   -> tudo que é simétrico: redimensionar,
//	                            ocupado, texto, mover, cruz
//
// Dentro de cada família decidem a caixa envolvente (proporção), o quanto
// ela está preenchida e as SIMETRIAS da silhueta, medidas numa grade 16x16
// (as setas duplas são simétricas: ↔ no espelho vertical, ↕ no
// horizontal, ↘↖ na diagonal principal, ↗↙ na antidiagonal).
//
// O que NÃO é distinguido, de propósito, por não ter forma confiável:
// "não permitido" (anel com risco) costuma cair em ocupado, e "ajuda"
// (seta + ?) cai em seta+ampulheta. Os dois são raros em sessão remota e
// errar neles custa menos que um falso positivo nos comuns.
type classificadorCursor struct{}

// alfaOpaco: a partir de que alfa o pixel entra na silhueta.
//
// NÃO é "!= 0" (o que a versão anterior usava): o cursor do Windows moderno
// vem em 32bpp com borda suavizada e, em alguns temas, SOMBRA — uma mancha
// de alfa baixo bem maior que o desenho. Contando a sombra, a caixa
// envolvente e o preenchimento de qualquer cursor ficavam parecidos entre
// si, que é o outro motivo de tudo virar "mão". O VNC manda 0/255 e não se
// importa com este corte.
const alfaOpaco = 128

// gradeN é o lado da grade em que a silhueta é reamostrada para medir
// simetria. 16 é grosso o bastante para o serrilhado não contar e fino o
// bastante para separar uma seta dupla de um anel.
const gradeN = 16

// formaCursor resume uma máscara: caixa envolvente, área opaca, ponto
// quente e a grade normalizada usada nas simetrias.
type formaCursor struct {
	w, h                   int // tamanho do bitmap
	minX, minY, maxX, maxY int // caixa envolvente da parte opaca
	opacos                 int
	hotX, hotY             int
	// topoCentro é o x do meio da PRIMEIRA linha opaca, relativo à caixa
	// (0 = encostado à esquerda). É o que separa seta de mão sem depender
	// do ponto quente: a seta começa com a ponta na quina esquerda; a mão
	// começa com a ponta do dedo lá pelo meio.
	topoCentro float64
	// larguraTopo é a largura da faixa opaca da PRIMEIRA linha, relativa
	// à caixa. Separa o cursor de texto da seta dupla ↕ sem depender do
	// tamanho do bitmap: o I-beam começa com a serifa ocupando a caixa
	// inteira (≈1), qualquer seta começa com um bico (≈0).
	larguraTopo float64
	grade       [gradeN * gradeN]float32
}

func (f formaCursor) bw() int { return f.maxX - f.minX + 1 }
func (f formaCursor) bh() int { return f.maxY - f.minY + 1 }

// aspecto é largura/altura da caixa. >1 é deitado, <1 é em pé.
func (f formaCursor) aspecto() float64 {
	if f.bh() <= 0 {
		return 0
	}
	return float64(f.bw()) / float64(f.bh())
}

// preenchimento é quanto da caixa está opaco. Silhuetas finas (seta, mão,
// setas duplas) ficam bem abaixo de 0,5; ampulheta e anel, acima.
func (f formaCursor) preenchimento() float64 {
	a := f.bw() * f.bh()
	if a <= 0 {
		return 0
	}
	return float64(f.opacos) / float64(a)
}

// hotRel devolve o ponto quente relativo à CAIXA (0..1 em cada eixo). O
// bool é falso quando ele cai fora da caixa — acontece com cursor que tem
// a ponta no vazio, e aí só a forma decide.
func (f formaCursor) hotRel() (float64, float64, bool) {
	bw, bh := f.bw(), f.bh()
	if bw <= 1 || bh <= 1 {
		return 0, 0, false
	}
	x := float64(f.hotX-f.minX) / float64(bw-1)
	y := float64(f.hotY-f.minY) / float64(bh-1)
	if x < -0.2 || x > 1.2 || y < -0.2 || y > 1.2 {
		return 0, 0, false
	}
	return x, y, true
}

// medirForma lê a máscara e devolve a forma. O segundo valor é falso
// quando não há pixel opaco nenhum (cursor escondido, ou máscara que não
// decodificou) — o chamador trata isso como "volta ao cursor padrão".
func medirForma(xhot, yhot, w, h int, mask []byte) (formaCursor, bool) {
	f := formaCursor{w: w, h: h, hotX: xhot, hotY: yhot,
		minX: w, minY: h, maxX: -1, maxY: -1}
	if w <= 0 || h <= 0 || len(mask) < w*h {
		return f, false
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if mask[y*w+x] < alfaOpaco {
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
	if f.opacos == 0 {
		return f, false
	}
	f.topoCentro, f.larguraTopo = medidasDaPrimeiraLinha(f, mask)
	preencherGrade(&f, mask)
	return f, true
}

// medidasDaPrimeiraLinha: centro e largura da faixa opaca da linha mais
// alta, as duas normalizadas pela largura da caixa.
func medidasDaPrimeiraLinha(f formaCursor, mask []byte) (centro, largura float64) {
	bw := f.bw()
	if bw <= 1 {
		return 0, 1
	}
	soma, n := 0, 0
	for x := f.minX; x <= f.maxX; x++ {
		if mask[f.minY*f.w+x] >= alfaOpaco {
			soma += x - f.minX
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return float64(soma) / float64(n) / float64(bw-1), float64(n) / float64(bw)
}

// preencherGrade reamostra a CAIXA (não o bitmap inteiro) numa grade
// gradeN x gradeN de cobertura. Normalizar pela caixa é o que deixa
// comparar simetria diagonal de cursor que não é quadrado.
//
// A grade é AMOSTRADA (4x4 pontos por célula), não acumulada a partir dos
// pixels: cursor pequeno tem caixa mais estreita que a grade, e distribuir
// os pixels em baldes deixava células vazias em posições que não se
// espelham — a silhueta mais simétrica do mundo media assimétrica, e todo
// o resto da classificação desabava a partir daí.
const amostrasPorCelula = 4

func preencherGrade(f *formaCursor, mask []byte) {
	bw, bh := f.bw(), f.bh()
	for gy := 0; gy < gradeN; gy++ {
		for gx := 0; gx < gradeN; gx++ {
			opacos := 0
			for sy := 0; sy < amostrasPorCelula; sy++ {
				v := (float64(gy) + (float64(sy)+0.5)/amostrasPorCelula) / gradeN
				py := f.minY + int(v*float64(bh))
				if py > f.maxY {
					py = f.maxY
				}
				for sx := 0; sx < amostrasPorCelula; sx++ {
					u := (float64(gx) + (float64(sx)+0.5)/amostrasPorCelula) / gradeN
					px := f.minX + int(u*float64(bw))
					if px > f.maxX {
						px = f.maxX
					}
					if mask[py*f.w+px] >= alfaOpaco {
						opacos++
					}
				}
			}
			f.grade[gy*gradeN+gx] = float32(opacos) / (amostrasPorCelula * amostrasPorCelula)
		}
	}
}

// semelhanca compara a grade com ela mesma depois de uma transformação de
// índice. 1 = igual, 0 = sem sobreposição nenhuma.
func (f formaCursor) semelhanca(troca func(x, y int) (int, int)) float64 {
	var dif, soma float64
	for y := 0; y < gradeN; y++ {
		for x := 0; x < gradeN; x++ {
			tx, ty := troca(x, y)
			a := float64(f.grade[y*gradeN+x])
			b := float64(f.grade[ty*gradeN+tx])
			d := a - b
			if d < 0 {
				d = -d
			}
			dif += d
			soma += a + b
		}
	}
	if soma == 0 {
		return 1
	}
	return 1 - dif/soma
}

// As quatro simetrias que interessam. Espelho vertical (simH) e
// horizontal (simV) marcam as setas duplas ↔ e ↕ e as formas redondas;
// as duas diagonais marcam ↘↖ e ↗↙.
func (f formaCursor) simH() float64 {
	return f.semelhanca(func(x, y int) (int, int) { return gradeN - 1 - x, y })
}

func (f formaCursor) simV() float64 {
	return f.semelhanca(func(x, y int) (int, int) { return x, gradeN - 1 - y })
}

func (f formaCursor) simDiag() float64 {
	return f.semelhanca(func(x, y int) (int, int) { return y, x })
}

func (f formaCursor) simAnti() float64 {
	return f.semelhanca(func(x, y int) (int, int) { return gradeN - 1 - y, gradeN - 1 - x })
}

// correlacao é o coeficiente de correlação entre x e y na massa da
// grade: +1 quando a silhueta se deita sobre a diagonal principal (↘),
// -1 sobre a antidiagonal (↗), ~0 quando não pende para lado nenhum.
//
// Ele existe porque as DUAS simetrias diagonais não separam ↘↖ de ↗↙:
// refletir em torno da antidiagonal leva a diagonal principal nela mesma
// (o ponto (t,t) vira (N-1-t, N-1-t)), então uma seta ↘↖ mede simétrica
// nas duas — descoberto tentando separá-las só por simetria. Quem diz o
// SENTIDO é este sinal.
func (f formaCursor) correlacao() float64 {
	var peso, somaX, somaY float64
	for y := 0; y < gradeN; y++ {
		for x := 0; x < gradeN; x++ {
			g := float64(f.grade[y*gradeN+x])
			peso += g
			somaX += g * float64(x)
			somaY += g * float64(y)
		}
	}
	if peso == 0 {
		return 0
	}
	mx, my := somaX/peso, somaY/peso
	var vxy, vxx, vyy float64
	for y := 0; y < gradeN; y++ {
		for x := 0; x < gradeN; x++ {
			g := float64(f.grade[y*gradeN+x])
			dx, dy := float64(x)-mx, float64(y)-my
			vxy += g * dx * dy
			vxx += g * dx * dx
			vyy += g * dy * dy
		}
	}
	if vxx <= 0 || vyy <= 0 {
		return 0
	}
	return vxy / sqrt(vxx*vyy)
}

// sqrt sem puxar o math inteiro para um único uso: Newton basta para um
// valor que só é comparado contra limiares grosseiros.
func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 20; i++ {
		x = (x + v/x) / 2
	}
	return x
}

// classificar consome uma atualização de cursor e devolve o pointer.Cursor
// correspondente. Máscara vazia ou w<=0 (SetNull/SetDefault do RDP, cursor
// escondido do VNC) cai em CursorDefault: quem quer esconder o ponteiro
// manda o caminho próprio, não uma máscara vazia.
func (c *classificadorCursor) classificar(xhot, yhot, w, h int, mask []byte) pointer.Cursor {
	f, ok := medirForma(xhot, yhot, w, h, mask)
	if !ok {
		return pointer.CursorDefault
	}
	// Máscara SEM informação nenhuma: ou está toda opaca (decodificador
	// que devolve alfa 255 em tudo quando o ponteiro não tem canal alfa —
	// acontece com cursor monocromático), ou tem uns poucos pixels
	// soltos. Nos dois casos não há silhueta para ler, e inventar uma
	// classificação a partir de um retângulo cheio daria "ocupado" em
	// cima de qualquer cursor. Melhor a seta comum.
	if f.opacos >= f.w*f.h || f.opacos < 4 {
		return pointer.CursorDefault
	}
	return f.cursor()
}

func (f formaCursor) cursor() pointer.Cursor {
	hx, hy, temHot := f.hotRel()

	// Seta: ponta na quina superior esquerda. Vale tanto pelo ponto
	// quente (a seta padrão tem hotspot (0,0) em Windows e X11) quanto
	// pela silhueta, que começa com um bico de 1-2px encostado na
	// esquerda — o segundo critério salva o servidor que manda hotspot
	// zerado ou fora da caixa.
	naQuina := temHot && hx <= 0.28 && hy <= 0.28
	bicoNaEsquerda := f.topoCentro <= 0.18 && f.aspecto() <= 1.1
	if naQuina || (!temHot && bicoNaEsquerda) {
		return f.familiaSeta()
	}

	// Mão: ponta no topo, mas deslocada para dentro — é a ponta do dedo,
	// não a quina. A mão do Windows tem hotspot por volta de 1/4 a 1/2 da
	// largura da caixa.
	if temHot && hy <= 0.35 && hx > 0.28 && hx <= 0.65 && f.aspecto() <= 1.35 {
		return pointer.CursorPointer
	}

	return f.familiaCentrada()
}

// familiaSeta separa a seta comum das compostas (seta + ampulheta/anel,
// que é o "trabalhando em segundo plano" do Windows, e seta + "?", que é
// a ajuda). O que as denuncia é a caixa ficar LARGA: a seta sozinha é bem
// mais alta que larga (proporção ~0,6); com um segundo glifo ao lado ela
// passa de 0,9.
//
// As duas compostas caem em CursorProgress. Ajuda não tem cursor nomeado
// no Gio, e "seta com ocupado ao lado" descreve bem melhor a de espera —
// que é a que aparece o tempo todo numa sessão remota.
func (f formaCursor) familiaSeta() pointer.Cursor {
	if f.aspecto() >= 0.9 {
		return pointer.CursorProgress
	}
	return pointer.CursorDefault
}

// familiaCentrada é tudo o que tem ponto quente no meio: as seis setas de
// redimensionar, mover, cruz, texto e ocupado. Aqui mandam as simetrias.
func (f formaCursor) familiaCentrada() pointer.Cursor {
	simH, simV := f.simH(), f.simV()
	simD, simA := f.simDiag(), f.simAnti()
	asp, cheio := f.aspecto(), f.preenchimento()

	// Diagonais primeiro: ↘↖ e ↗↙ não são simétricas nos espelhos (o
	// espelho troca uma pela outra) e são nas duas diagonais. Qual das
	// duas é, quem diz é o sinal da correlação. Testar antes evita que o
	// empate das outras medidas as engula.
	if simH < 0.72 && simV < 0.72 && simD >= 0.72 && simA >= 0.72 {
		if f.correlacao() >= 0 {
			return pointer.CursorNorthWestSouthEastResize
		}
		return pointer.CursorNorthEastSouthWestResize
	}

	if simH >= 0.72 && simV >= 0.72 {
		switch {
		case asp >= 1.55:
			// Deitada e simétrica: seta dupla ↔.
			return pointer.CursorEastWestResize
		case asp <= 0.66:
			// Em pé e simétrica: ou é a seta dupla ↕, ou é o cursor de
			// texto. O que separa é COMO A FORMA COMEÇA — o I-beam abre
			// com a serifa ocupando a largura inteira; a seta dupla abre
			// com a ponta da cabeça, um bico. Medir assim não depende do
			// tamanho do bitmap nem da folga que o servidor deixou em
			// volta do desenho.
			if f.larguraTopo >= 0.6 {
				return pointer.CursorText
			}
			return pointer.CursorNorthSouthResize
		case simD >= 0.72 && simA >= 0.72:
			// Simétrica nos quatro sentidos: cruz, mover, ou redondo.
			switch {
			case cheio >= 0.45:
				return pointer.CursorWait
			case cheio <= 0.24:
				return pointer.CursorCrosshair
			default:
				return pointer.CursorAllScroll
			}
		default:
			return pointer.CursorWait
		}
	}

	// Sem simetria nenhuma e com o ponto quente longe da quina: sobrou o
	// genérico "algo interativo". É o que a versão anterior devolvia para
	// quase tudo; aqui ele é o ÚLTIMO caso, não o primeiro.
	return pointer.CursorPointer
}
