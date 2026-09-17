//go:build !linux

package main

import "errors"

// Atalho global fora do Linux.
//
// No Windows o caminho é RegisterHotKey, que é global de verdade e deixa
// a tecla por nossa conta — ao contrário do Wayland, onde quem amarra é
// o sistema. Ainda não implementado; até lá o app sobe sem atalho, que é
// a mesma degradação de um desktop sem o portal: a busca continua
// existindo dentro da janela.
//
// Este arquivo existe para a build de Windows não quebrar por falta do
// símbolo (ver scripts/build-windows.sh).

type AtalhoGlobal struct {
	Gatilho string
}

func registrarAtalhoGlobal(id, descricao, gatilho string, ao func()) (*AtalhoGlobal, error) {
	return nil, errors.New("atalho global ainda não implementado nesta plataforma")
}
