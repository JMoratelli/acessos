//go:build linux || windows

package main

import (
	"fmt"

	"gioui.org/app"
)

// NÃO renomeie este arquivo para protocolos_linux.go: o sufixo _linux no
// NOME vale como restrição de build e o Go o aplica POR CIMA do
// //go:build acima, sem reclamar de nada. O efeito é silencioso e caro —
// o Windows passa a compilar o protocolos_semtela.go, e o app abre
// inteiro, só que sem VNC e sem RDP. O mesmo vale para vnctab.go,
// rdptab.go e certificadodlg.go.
//
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
