package main

import (
	"fmt"

	"gioui.org/io/pointer"
	"github.com/hinshun/vt10x"
)

// tipoEventoMouse é o gesto — a mesma função monta o byte certo pros
// quatro casos em vez de cada chamador (Press/Drag/Release/Scroll)
// duplicar a conta de botão+modificador+SGR-ou-legado.
type tipoEventoMouse int

const (
	mousePress tipoEventoMouse = iota
	mouseRelease
	mouseMotion
	mouseScrollUp
	mouseScrollDown
)

// botaoXterm traduz o bitmask de botão do Gio pro código xterm: 0=
// esquerdo, 1=meio, 2=direito. ok=false quando nenhum botão relevante
// está pressionado (não deveria acontecer num Press/Drag, mas evita
// mandar lixo se acontecer).
func botaoXterm(b pointer.Buttons) (int, bool) {
	switch {
	case b&pointer.ButtonPrimary != 0:
		return 0, true
	case b&pointer.ButtonTertiary != 0:
		return 1, true
	case b&pointer.ButtonSecondary != 0:
		return 2, true
	}
	return 0, false
}

// sequenciaMouse monta os bytes de relato de mouse (o que htop/less/mc
// esperam receber pra reagir a clique e roda), ou nil se nenhum modo de
// relato estiver ligado — quem chama cai de volta pro comportamento
// local (seleção com o mouse, roda = scrollback) nesse caso.
//
// x,y são a célula (0-based); botao só importa para mousePress/mouseMotion
// (Release e Scroll têm código próprio, fixo).
func sequenciaMouse(modo vt10x.ModeFlag, tipo tipoEventoMouse, botao int, mods modificadores, x, y int) []byte {
	if modo&vt10x.ModeMouseMask == 0 {
		return nil
	}
	sgr := modo&vt10x.ModeMouseSgr != 0

	cb := botao
	switch tipo {
	case mouseScrollUp:
		cb = 64
	case mouseScrollDown:
		cb = 65
	case mouseMotion:
		// Só reportamos motion enquanto o Gio já está mandando Drag, que
		// só acontece com um botão apertado — cobre 1002 igualzinho a
		// 1003 nesta pilha (não dá pra distinguir "motion sem botão
		// nenhum" sem um evento de Move que HandlePointer não recebe
		// hoje).
		cb = botao | 32
	case mouseRelease:
		// No legado (X10/1000/1002/1003 sem SGR) release não carrega o
		// botão, é sempre 3. No SGR o botão original vai junto e quem
		// distingue é o 'm' minúsculo no final.
		if !sgr {
			cb = 3
		}
	}
	if mods.shift {
		cb |= 4
	}
	if mods.alt {
		cb |= 8
	}
	if mods.ctrl {
		cb |= 16
	}

	// coordenadas 1-based nos dois formatos.
	x, y = x+1, y+1

	if sgr {
		final := byte('M')
		if tipo == mouseRelease {
			final = 'm'
		}
		return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", cb, x, y, final))
	}

	// Protocolo legado: 1 byte por campo, offset fixo de 32 — uma tela
	// com mais de 223 colunas ou linhas estoura o byte. Limitação do
	// PRÓPRIO protocolo (por isso o SGR existe); só clampamos pra não
	// mandar um byte corrompido, não pra fingir que cabe.
	clampar := func(v int) byte {
		v += 32
		if v > 255 {
			v = 255
		}
		return byte(v)
	}
	return []byte{0x1b, '[', 'M', byte(cb + 32), clampar(x), clampar(y)}
}
