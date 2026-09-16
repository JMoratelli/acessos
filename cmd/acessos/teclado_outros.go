//go:build !linux

package main

import "gioui.org/io/key"

// keysimDoNome traduz key.Name — que fora do Linux vem de convertKeyCode em
// os_windows.go, por VK, não por caractere digitado — para o keysym X11 que
// o VNC espera. Cobre o necessário pra digitar e usar os atalhos comuns;
// layout assumido é US, então símbolos que dependem de outro layout (ex.:
// teclado ABNT2) ficam de fora por ora — ver traduzirTecla.
var keysimDoNome = map[key.Name]uint32{
	key.NameLeftArrow:      0xff51,
	key.NameUpArrow:        0xff52,
	key.NameRightArrow:     0xff53,
	key.NameDownArrow:      0xff54,
	key.NameHome:           0xff50,
	key.NameEnd:            0xff57,
	key.NamePageUp:         0xff55,
	key.NamePageDown:       0xff56,
	key.NameReturn:         0xff0d,
	key.NameEscape:         0xff1b,
	key.NameDeleteBackward: 0xff08, // Backspace
	key.NameDeleteForward:  0xffff, // Delete
	key.NameTab:            0xff09,
	key.NameSpace:          0x0020,
	key.NameCtrl:           0xffe3,
	key.NameShift:          0xffe1,
	key.NameAlt:            0xffe9,
	key.NameSuper:          0xffeb,
	key.NameF1:             0xffbe,
	key.NameF2:             0xffbf,
	key.NameF3:             0xffc0,
	key.NameF4:             0xffc1,
	key.NameF5:             0xffc2,
	key.NameF6:             0xffc3,
	key.NameF7:             0xffc4,
	key.NameF8:             0xffc5,
	key.NameF9:             0xffc6,
	key.NameF10:            0xffc7,
	key.NameF11:            0xffc8,
	key.NameF12:            0xffc9,
	";":                    0x3b,
	"+":                    0x2b,
	",":                    0x2c,
	"-":                    0x2d,
	".":                    0x2e,
	"/":                    0x2f,
	"`":                    0x60,
	"[":                    0x5b,
	"\\":                   0x5c,
	"]":                    0x5d,
	"'":                    0x27,
}

// keycodeX11DoNome traduz para o keycode X11 (evdev+8) que o RDP espera
// (ver internal/rdp e rdpshim.c, rs_tecla). Os valores das teclas com nome
// batem com os que teclas.go já usa nos atalhos fixos (Ctrl+Alt+Del etc.)
// — mesma tabela evdev+8, conferida ali.
var keycodeX11DoNome = map[key.Name]uint32{
	key.NameLeftArrow:      113,
	key.NameUpArrow:        111,
	key.NameRightArrow:     114,
	key.NameDownArrow:      116,
	key.NameHome:           110,
	key.NameEnd:            115,
	key.NamePageUp:         112,
	key.NamePageDown:       117,
	key.NameReturn:         36,
	key.NameEscape:         9,
	key.NameDeleteBackward: 22,
	key.NameDeleteForward:  119,
	key.NameTab:            23,
	key.NameSpace:          65,
	key.NameCtrl:           37,
	key.NameShift:          50,
	key.NameAlt:            64,
	key.NameSuper:          133,
	key.NameF1:             67,
	key.NameF2:             68,
	key.NameF3:             69,
	key.NameF4:             70,
	key.NameF5:             71,
	key.NameF6:             72,
	key.NameF7:             73,
	key.NameF8:             74,
	key.NameF9:             75,
	key.NameF10:            76,
	key.NameF11:            95,
	key.NameF12:            96,
	";":                    47,
	"+":                    21,
	",":                    59,
	"-":                    20,
	".":                    60,
	"/":                    61,
	"`":                    49,
	"[":                    34,
	"\\":                   51,
	"]":                    35,
	"'":                    48,
}

// keycodeX11DoAlfanumerico cobre letras e dígitos: evdev não numera por
// ordem alfabética, segue a posição física da tecla no teclado (linha
// QWERTY, depois ASDF, depois ZXCV) — por isso a tabela explícita, em vez
// de derivar de 'A'-'Z'.
var keycodeX11DoAlfanumerico = map[byte]uint32{
	'Q': 24, 'W': 25, 'E': 26, 'R': 27, 'T': 28, 'Y': 29, 'U': 30, 'I': 31, 'O': 32, 'P': 33,
	'A': 38, 'S': 39, 'D': 40, 'F': 41, 'G': 42, 'H': 43, 'J': 44, 'K': 45, 'L': 46,
	'Z': 52, 'X': 53, 'C': 54, 'V': 55, 'B': 56, 'N': 57, 'M': 58,
	'0': 19, '1': 10, '2': 11, '3': 12, '4': 13, '5': 14, '6': 15, '7': 16, '8': 17, '9': 18,
}

// traduzirTecla devolve (keysym X11, keycode X11) para um key.Event — os
// mesmos dois valores que grab.Start entrega no Linux, então Tab.HandleKey
// não precisa saber de onde a tecla veio.
//
// Letras e dígitos são caso especial: o Windows manda o MESMO Name (VK,
// sempre maiúsculo pra letra) esteja Shift apertado ou não — quem decide
// maiúscula/minúscula aqui é o parâmetro shift, não o Name. Dígitos com
// Shift (os símbolos "!@#$..." da fileira de cima) não são tratados: o
// keysym do dígito é mandado do jeito que é, sem o símbolo — layout US
// tem esse limite por ora, igual ao das pontuações que dependem de OEM
// não mapeado.
func traduzirTecla(nome key.Name, shift bool) (keysym, keycodeX11 uint32, ok bool) {
	if len(nome) == 1 {
		c := nome[0]
		if kc, achou := keycodeX11DoAlfanumerico[c]; achou {
			switch {
			case c >= 'A' && c <= 'Z':
				if shift {
					return uint32(c), kc, true
				}
				return uint32(c) + ('a' - 'A'), kc, true
			case c >= '0' && c <= '9':
				return uint32(c), kc, true
			}
		}
	}
	ks, achouKs := keysimDoNome[nome]
	kc, achouKc := keycodeX11DoNome[nome]
	if achouKs && achouKc {
		return ks, kc, true
	}
	return 0, 0, false
}
