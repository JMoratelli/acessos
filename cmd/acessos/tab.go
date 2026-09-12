package main

import (
	"image"
	"image/color"

	"gio.tools/icons"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/widget"
)

var iconeFechar = icons.NavigationClose

// telaRemota marca as abas que desenham a TELA de outra máquina (VNC,
// RDP). Elas são as que consomem o teclado cru e as que inibem os atalhos
// do compositor. É uma interface, e não um type switch nos tipos
// concretos, porque esses tipos só existem no build de Linux — no
// Windows o pacote nem é compilado.
type telaRemota interface {
	EhTelaRemota()
}

// Tab é uma conexão viva com um lugar pra desenhar. main.go dá entrada de
// teclado (vinda direto do Wayland, ver internal/grab) e ponteiro (vindas
// do Gio) só pra aba ATIVA; abas em segundo plano continuam rodando suas
// próprias goroutines de rede (reconexão, etc.) mesmo sem receber input.
type Tab interface {
	// Title aparece no rótulo da aba.
	Title() string

	// SoIcone diz que a aba se representa só pelo ícone (a aba fixa do
	// Painel é a casinha, sem texto — economiza a largura da tira pras
	// abas de conexão, que é onde o nome importa).
	SoIcone() bool

	// Selo é o ícone do protocolo mostrado na aba: o MESMO glifo e a
	// MESMA cor do card, porque um protocolo tem uma cor só em toda a
	// interface (gtk.md §6). Devolve ícone, cor do glifo e cor do fundo
	// tênue; ícone nil = sem selo (aba Painel).
	Selo() (*widget.Icon, color.NRGBA, color.NRGBA)

	// Pinned marca a aba fixa (Painel): sem botão de fechar, evita o
	// estado "zero abas" — ver gtk.md, padrão B.
	Pinned() bool

	// Layout desenha o conteúdo da aba na área abaixo da barra de abas.
	Layout(gtx layout.Context) layout.Dimensions

	// HandleKey recebe teclas físicas já traduzidas (ver internal/grab):
	// keysym é o valor X11 (o que VNC quer), keycodeX11 o keycode cru
	// evdev+8 (o que RDP quer); a aba usa o que fizer sentido pra ela.
	HandleKey(keysym, keycodeX11 uint32, pressed bool)

	// HandlePointer recebe um evento de ponteiro já na área de conteúdo,
	// e o tamanho atual dessa área (pra abas que escalam, tipo VNC/RDP).
	HandlePointer(ev pointer.Event, size image.Point)

	// Close libera a conexão e qualquer goroutine associada.
	Close()
}
