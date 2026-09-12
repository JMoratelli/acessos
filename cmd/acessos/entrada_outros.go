//go:build !linux

package main

import (
	"gioui.org/app"
	"gioui.org/io/event"
)

// Fora do Linux não há grab por Wayland — e, HOJE, também não há
// substituto: nenhuma tecla chega às abas remotas no Windows, porque
// Tab.HandleKey só é chamado do caminho Wayland. As sessões abrem, a
// tela desenha e o mouse funciona; o teclado, não. Ver BACKLOG.md,
// item 1, que é o que falta para o app ser operável no Windows.
func tratarEventoPlataforma(w *app.Window, e event.Event, activeTab func() Tab) {}
