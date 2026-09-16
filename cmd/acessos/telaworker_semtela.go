//go:build !linux && !windows

package main

// Fora de Linux e Windows não há sessão de tela remota (nem libfreerdp
// nem libvncclient compiladas), então não há processo-filho para rodar —
// ver protocolos_semtela.go.
func modoWorker() bool { return false }
