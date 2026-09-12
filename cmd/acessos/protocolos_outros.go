//go:build !linux

package main

import (
	"fmt"

	"gioui.org/app"
)

// Fora do Linux, VNC e RDP ainda não existem: eles dependem de
// libvncclient e libfreerdp3 por cgo, e essas bibliotecas precisam ser
// compiladas para a plataforma alvo.
//
// O resto do app funciona inteiro — painel, SSH, SFTP, execução em massa,
// cofre e chaveiro. Recusar com a razão na tela é melhor que esconder o
// botão: quem usa os dois sistemas precisa saber por que a mesma conexão
// abre numa máquina e não na outra.
func novaAbaTela(w *app.Window, spec map[string]string) (Tab, error) {
	return nil, fmt.Errorf(
		"%s ainda não está disponível nesta plataforma (falta compilar a biblioteca C)",
		spec["type"])
}
