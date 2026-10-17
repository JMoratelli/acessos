package vt10x

func isControlCode(c rune) bool {
	return c < 0x20 || c == 0177
}

func (t *State) parse(c rune) {
	if t.DebugLogger != nil { // patch acessos: string(c) alocava por rune mesmo sem logger
		t.logf("%q", string(c))
	}
	if isControlCode(c) {
		if t.handleControlCodes(c) || t.cur.Attr.Mode&attrGfx == 0 {
			return
		}
	}
	// TODO: update selection; see st.c:2450

	if t.mode&ModeWrap != 0 && t.cur.State&cursorWrapNext != 0 {
		t.lines[t.cur.Y][t.cur.X].Mode |= attrWrap
		t.newline(true)
	}

	if t.mode&ModeInsert != 0 && t.cur.X+1 < t.cols {
		// patch acessos: IRM (CSI 4 h). Empurra o resto da linha uma
		// coluna à direita antes de escrever, como st.c faz.
		t.insertBlanks(1)
	}

	t.ultimoChar = c
	t.setChar(c, &t.cur.Attr, t.cur.X, t.cur.Y)
	if t.cur.X+1 < t.cols {
		t.moveTo(t.cur.X+1, t.cur.Y)
	} else {
		t.cur.State |= cursorWrapNext
	}
}

func (t *State) parseEsc(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	next := t.parse
	t.logf("%q", string(c))
	switch c {
	case '[':
		next = t.parseEscCSI
	case '#':
		next = t.parseEscTest
	case 'P', // DCS - Device Control String
		'_', // APC - Application Program Command
		'^', // PM - Privacy Message
		']', // OSC - Operating System Command
		'k': // old title set compatibility
		t.str.reset()
		t.str.typ = c
		next = t.parseEscStr
	case '(': // set primary charset G0
		t.charsetAlvo = 0
		next = t.parseEscAltCharset
	case ')': // set secondary charset G1 (patch acessos: era ignorado)
		t.charsetAlvo = 1
		next = t.parseEscAltCharset
	case '*', // set tertiary charset G2 (ignored)
		'+': // set quaternary charset G3 (ignored)
	case 'D': // IND - linefeed
		if t.cur.Y == t.bottom {
			t.scrollUp(t.top, 1)
		} else {
			t.moveTo(t.cur.X, t.cur.Y+1)
		}
	case 'E': // NEL - next line
		t.newline(true)
	case 'H': // HTS - horizontal tab stop
		t.tabs[t.cur.X] = true
	case 'M': // RI - reverse index
		if t.cur.Y == t.top {
			t.scrollDown(t.top, 1)
		} else {
			t.moveTo(t.cur.X, t.cur.Y-1)
		}
	case 'Z': // DECID - identify terminal
		// TODO: write to our writer our id
	case 'c': // RIS - reset to initial state
		t.reset()
	case '=': // DECPAM - application keypad
		t.mode |= ModeAppKeypad
	case '>': // DECPNM - normal keypad
		t.mode &^= ModeAppKeypad
	case '7': // DECSC - save cursor
		t.saveCursor()
	case '8': // DECRC - restore cursor
		t.restoreCursor()
	case '\\': // ST - stop
	default:
		t.logf("unknown ESC sequence '%c'\n", c)
	}
	t.state = next
}

func (t *State) parseEscCSI(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	t.logf("%q", string(c))
	if t.csi.put(byte(c)) {
		t.state = t.parse
		t.handleCSI()
	}
}

func (t *State) parseEscStr(c rune) {
	t.logf("%q", string(c))
	switch c {
	case '\033':
		t.state = t.parseEscStrEnd
	case '\a': // backwards compatiblity to xterm
		t.state = t.parse
		t.handleSTR()
	default:
		t.str.put(c)
	}
}

func (t *State) parseEscStrEnd(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	t.logf("%q", string(c))
	t.state = t.parse
	if c == '\\' {
		t.handleSTR()
	}
}

func (t *State) parseEscAltCharset(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	t.logf("%q", string(c))
	// patch acessos: o conjunto vai para a tabela G0/G1 (charsetAlvo) em
	// vez de direto no atributo do cursor — SO/SI escolhem qual das duas
	// vale, e aplicarCharset traduz isso para o attrGfx que setChar lê.
	switch c {
	case '0': // line drawing set
		t.trantbl[t.charsetAlvo] = true
	case 'B': // USASCII
		t.trantbl[t.charsetAlvo] = false
	case 'A', // UK (ignored)
		'<', // multinational (ignored)
		'5', // Finnish (ignored)
		'C', // Finnish (ignored)
		'K': // German (ignored)
		t.trantbl[t.charsetAlvo] = false
	default:
		t.logf("unknown alt. charset '%c'\n", c)
	}
	t.aplicarCharset()
	t.state = t.parse
}

func (t *State) parseEscTest(c rune) {
	if t.handleControlCodes(c) {
		return
	}
	// DEC screen alignment test
	if c == '8' {
		for y := 0; y < t.rows; y++ {
			for x := 0; x < t.cols; x++ {
				t.setChar('E', &t.cur.Attr, x, y)
			}
		}
	}
	t.state = t.parse
}

func (t *State) handleControlCodes(c rune) bool {
	if !isControlCode(c) {
		return false
	}
	switch c {
	// HT
	case '\t':
		t.putTab(true)
	// BS
	case '\b':
		t.moveTo(t.cur.X-1, t.cur.Y)
	// CR
	case '\r':
		t.moveTo(0, t.cur.Y)
	// LF, VT, LF
	case '\f', '\v', '\n':
		// go to first col if mode is set
		t.newline(t.mode&ModeCRLF != 0)
	// BEL
	case '\a':
		// TODO: emit sound
		// TODO: window alert if not focused
	// ESC
	case 033:
		t.csi.reset()
		t.state = t.parseEsc
	// SO, SI — patch acessos: escolhem G1/G0 (terminfo `linux`/`screen`
	// desenha borda com `smacs=^N`, e um `)0` antes disso)
	case 016:
		t.charset = 1
		t.aplicarCharset()
	case 017:
		t.charset = 0
		t.aplicarCharset()
	// SUB, CAN
	case 032, 030:
		t.csi.reset()
	// ignore ENQ, NUL, XON, XOFF, DEL
	case 005, 000, 021, 023, 0177:
	default:
		return false
	}
	return true
}
