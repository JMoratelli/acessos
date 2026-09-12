//go:build !linux

package main

import (
	"gioui.org/app"
	"gioui.org/io/event"
)

// Fora do Linux não há grab por Wayland: o teclado e a área de
// transferência vêm do próprio Gio, pelo caminho normal da plataforma.
func tratarEventoPlataforma(w *app.Window, e event.Event, activeTab func() Tab) {}
