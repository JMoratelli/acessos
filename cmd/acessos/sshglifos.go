package main

import (
	"bytes"
	"fmt"
	"os"
	"sync"

	"github.com/go-text/typesetting/font"
)

// glifoNaGrade diz se o caractere pode ser desenhado DENTRO de um trecho
// de texto do terminal SSH sem tirar o resto da linha do lugar — ver
// desenharGrade, em sshtab.go.
//
// O terminal desenha cada trecho de mesma cor como uma string só e deixa
// o shaper avançar glifo a glifo; isso só bate com a grade enquanto todo
// glifo tem o avanço da Plex Mono. Dois casos quebram a conta:
//
//   - caractere que a Plex Mono NÃO tem: no Linux o shaper cai numa fonte
//     do sistema, com outro avanço (no Windows vira o quadradinho vazio,
//     que ao menos tem a largura certa);
//   - caractere LARGO (CJK, emoji): dois lugares num terminal de verdade,
//     um só na grade do vt10x, e o avanço do glifo é de dois.
//
// Nos dois casos o erro se acumulava para a direita: uma linha de `mc`
// com um ✔ ou um ideograma no meio saía inteira deslocada a partir dali.
// O chamador desenha esses sozinhos, recortados à célula.
func glifoNaGrade(r rune) bool {
	if r < 0x80 {
		return r >= 0x20
	}
	if larguraDupla(r) {
		return false
	}
	return plexMonoTem(r)
}

// plexMonoTem consulta o cmap da Plex Mono embutida — a mesma fonte que o
// shaper usa para fonteMono (ver tema.go e fontes.go), carregada uma vez.
func plexMonoTem(r rune) bool {
	cmapPlexMono.once.Do(cmapPlexMono.carregar)
	if cmapPlexMono.face == nil {
		return true // fonte que não carrega é erro de build, já avisado
	}
	_, ok := cmapPlexMono.face.Cmap.Lookup(r)
	return ok
}

type cmapFonte struct {
	once sync.Once
	face *font.Face
}

var cmapPlexMono cmapFonte

func (c *cmapFonte) carregar() {
	face, err := font.ParseTTF(bytes.NewReader(plexMonoRegular))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cmap da Plex Mono: %v\n", err)
		return
	}
	c.face = face
}

// larguraDupla aproxima a classe "wide/fullwidth" do East Asian Width
// pelos blocos que importam numa sessão remota: CJK, Hangul, formas de
// largura inteira e os emojis. Não é a tabela completa — o que escapar
// daqui e não estiver na Plex Mono cai no outro critério.
func larguraDupla(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // radicais CJK, kana, pontuação CJK
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF, // CJK ext. A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unificado
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibilidade
		r >= 0xFE30 && r <= 0xFE4F,
		r >= 0xFF00 && r <= 0xFF60, // formas de largura inteira
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emojis
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD: // CJK ext. B em diante
		return true
	}
	return false
}
