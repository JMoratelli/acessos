package main

// Tradução de tecla física (o que o internal/grab entrega, direto do
// Wayland) para os bytes que um terminal espera. O grab dá keysym X11 já
// resolvido pelo layout ativo, então acento e pontuação saem certos sem
// tabela de teclado nossa.
//
// Escopo desta versão: uso normal de shell. Fora: teclado morto/composição
// (o keysym vem 0), modo de aplicação do teclado numérico e relatório de
// mouse.

// keysyms de modificador — o grab entrega apertar E soltar de cada um, que
// é justamente o que o key.Event do Gio não dá nesta pilha.
const (
	ksShiftL = 0xffe1
	ksShiftR = 0xffe2
	ksCtrlL  = 0xffe3
	ksCtrlR  = 0xffe4
	ksAltL   = 0xffe9
	ksAltR   = 0xffea
)

// modificadores acompanha o estado de Ctrl/Shift/Alt.
type modificadores struct{ ctrl, shift, alt bool }

// anotar registra o aperta/solta de um modificador. Devolve true se a
// tecla ERA um modificador (e portanto não gera bytes).
func (m *modificadores) anotar(keysym uint32, pressed bool) bool {
	switch keysym {
	case ksShiftL, ksShiftR:
		m.shift = pressed
		return true
	case ksCtrlL, ksCtrlR:
		m.ctrl = pressed
		return true
	case ksAltL, ksAltR:
		m.alt = pressed
		return true
	}
	return false
}

// especiais: keysym -> sequência. Só as que independem de modificador.
var especiais = map[uint32]string{
	0xff0d: "\r",      // Return
	0xff8d: "\r",      // KP_Enter
	0xff09: "\t",      // Tab
	0xff1b: "\x1b",    // Escape
	0xff08: "\x7f",    // BackSpace (DEL, como todo terminal moderno)
	0xffff: "\x1b[3~", // Delete
	0xff63: "\x1b[2~", // Insert
	0xff50: "\x1b[H",  // Home
	0xff57: "\x1b[F",  // End
	0xff55: "\x1b[5~", // Page Up
	0xff56: "\x1b[6~", // Page Down
	0xff52: "\x1b[A",  // Up
	0xff54: "\x1b[B",  // Down
	0xff53: "\x1b[C",  // Right
	0xff51: "\x1b[D",  // Left
	0xffbe: "\x1bOP",  // F1
	0xffbf: "\x1bOQ",  // F2
	0xffc0: "\x1bOR",  // F3
	0xffc1: "\x1bOS",  // F4
	0xffc2: "\x1b[15~",
	0xffc3: "\x1b[17~",
	0xffc4: "\x1b[18~",
	0xffc5: "\x1b[19~",
	0xffc6: "\x1b[20~",
	0xffc7: "\x1b[21~",
	0xffc8: "\x1b[23~",
	0xffc9: "\x1b[24~", // F12
}

// bytesDaTecla traduz um keysym (com os modificadores correntes) para os
// bytes a mandar. Devolve nil quando a tecla não produz nada.
func bytesDaTecla(keysym uint32, m modificadores) []byte {
	if s, ok := especiais[keysym]; ok {
		if m.alt {
			return append([]byte{0x1b}, s...)
		}
		return []byte(s)
	}

	r := runaDoKeysym(keysym)
	if r == 0 {
		return nil
	}

	if m.ctrl {
		// Ctrl+letra = o caractere de controle correspondente. Vale para
		// @ [ \ ] ^ _ e espaço também, que é a faixa 0x40-0x5f da tabela.
		c := r
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		if c == ' ' {
			c = '@'
		}
		if c >= '@' && c <= '_' {
			b := []byte{byte(c) & 0x1f}
			if m.alt {
				return append([]byte{0x1b}, b...)
			}
			return b
		}
		return nil
	}

	b := []byte(string(r))
	if m.alt {
		return append([]byte{0x1b}, b...)
	}
	return b
}

// runaDoKeysym converte um keysym X11 imprimível em rune. Latin-1 é
// identidade; o resto usa a convenção Unicode do X (0x01000000 + ponto de
// código).
func runaDoKeysym(keysym uint32) rune {
	switch {
	case keysym >= 0x20 && keysym <= 0x7e:
		return rune(keysym)
	case keysym >= 0xa0 && keysym <= 0xff:
		return rune(keysym)
	case keysym >= 0x01000100 && keysym <= 0x0110ffff:
		return rune(keysym - 0x01000000)
	// teclado numérico com Num Lock: keysyms próprios, mesmo resultado
	case keysym >= 0xffb0 && keysym <= 0xffb9:
		return rune('0' + (keysym - 0xffb0))
	case keysym == 0xffaa:
		return '*'
	case keysym == 0xffab:
		return '+'
	case keysym == 0xffad:
		return '-'
	case keysym == 0xffae:
		return '.'
	case keysym == 0xffaf:
		return '/'
	}
	return 0
}
