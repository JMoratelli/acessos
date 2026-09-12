package main

import (
	"image"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Combinações enviadas PELO PROTOCOLO, não pelo teclado: Ctrl+Alt+Del e
// Alt+Tab nunca chegam à sessão remota por tecla, porque o compositor
// local os reserva antes. Mandando os keysyms pela conexão, a máquina
// remota recebe exatamente o que receberia de um teclado físico dela.
//
// Pressiona na ordem e solta na ORDEM INVERSA — teclado real faz isso, e
// soltar na mesma ordem deixa modificador presa do outro lado.
type combinacao struct {
	rotulo  string
	keysyms []uint32
}

// keycodeDoKeysym: o RDP não recebe keysym, recebe o keycode cru do X11
// (evdev+8) e traduz para scancode do lado dele. Só as teclas usadas
// aqui precisam de tradução.
var keycodeDoKeysym = map[uint32]uint32{
	ksCtrlEsq:  37,
	ksAltEsq:   64,
	ksSuperEsq: 133,
	ksShiftEsq: 50,
	ksDelete:   119,
	ksEscape:   9,
	ksTab:      23,
	ksF1:       67,
	ksF2:       68,
	ksF4:       70,
	ksPrint:    107,
}

// keysyms X11 dos modificadores e teclas usadas aqui.
const (
	ksCtrlEsq  = 0xffe3
	ksAltEsq   = 0xffe9
	ksSuperEsq = 0xffeb
	ksShiftEsq = 0xffe1
	ksDelete   = 0xffff
	ksEscape   = 0xff1b
	ksTab      = 0xff09
	ksF4       = 0xffc1
	ksF1       = 0xffbe
	ksF2       = 0xffbf
	ksPrint    = 0xff61
)

var combinacoes = []combinacao{
	{"Ctrl+Alt+Del", []uint32{ksCtrlEsq, ksAltEsq, ksDelete}},
	{"Alt+F4", []uint32{ksAltEsq, ksF4}},
	{"Alt+Tab", []uint32{ksAltEsq, ksTab}},
	{"Ctrl+Esc (menu Iniciar)", []uint32{ksCtrlEsq, ksEscape}},
	{"Super", []uint32{ksSuperEsq}},
	{"Ctrl+Shift+Esc", []uint32{ksCtrlEsq, ksShiftEsq, ksEscape}},
	{"Ctrl+Alt+F1", []uint32{ksCtrlEsq, ksAltEsq, ksF1}},
	{"Ctrl+Alt+F2", []uint32{ksCtrlEsq, ksAltEsq, ksF2}},
	{"PrintScreen", []uint32{ksPrint}},
}

// enviarCombinacao aperta tudo na ordem e solta ao contrário.
func enviarCombinacao(c combinacao, tecla func(keysym uint32, pressionada bool)) {
	for _, k := range c.keysyms {
		tecla(k, true)
	}
	for i := len(c.keysyms) - 1; i >= 0; i-- {
		tecla(c.keysyms[i], false)
	}
}

// menuTeclas monta o menu de combinações ancorado no ponteiro. bloqueado
// desabilita tudo (sessão em somente-leitura não envia tecla nenhuma).
func menuTeclas(pos image.Point, bloqueado bool, tecla func(uint32, bool)) {
	var itens []*itemMenu
	for _, c := range combinacoes {
		c := c
		rot := c.rotulo
		acao := func() { enviarCombinacao(c, tecla) }
		if bloqueado {
			rot += "  (desbloqueie o olho)"
			acao = nil
		}
		itens = append(itens, &itemMenu{rotulo: rot, acao: acao})
	}
	abrirMenu(pos, itens)
}

// botaoTeclas é o "⌁" da barra de sessão.
func botaoTeclas(gtx layout.Context, th *material.Theme, btn *widget.Clickable) layout.Dimensions {
	return botaoSessao(gtx, th, btn, "⌁ teclas")
}
