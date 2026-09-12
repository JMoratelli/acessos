//go:build linux || windows

package main

import (
	"fmt"

	"gioui.org/app"
)

// novaAbaTela cria a aba de tela remota. Em Linux e Windows os dois
// protocolos existem de verdade, sobre libvncclient e libfreerdp3.
func novaAbaTela(w *app.Window, spec map[string]string) (Tab, error) {
	switch spec["type"] {
	case "vnc":
		return newVNCTab(w, spec), nil
	case "rdp":
		return newRDPTab(w, spec), nil
	}
	return nil, fmt.Errorf("protocolo de tela desconhecido: %q", spec["type"])
}
