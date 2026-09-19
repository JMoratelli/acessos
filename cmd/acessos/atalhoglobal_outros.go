//go:build !linux && !windows

package main

import "errors"

// Atalho global fora do Linux e do Windows (ver atalhoglobal_windows.go
// para o RegisterHotKey de lá).
//
// Ainda não implementado nestas outras plataformas; até lá o app sobe
// sem atalho, que é a mesma degradação de um desktop sem o portal: a
// busca continua existindo dentro da janela.
//
// Este arquivo existe para essas builds não quebrarem por falta do
// símbolo (ver scripts/build-windows.sh).

type AtalhoGlobal struct {
	Gatilho string
	// Caiu existe para a mesma espera do lado Linux compilar aqui; nunca
	// fecha, porque não há sessão de portal para cair.
	Caiu chan struct{}
}

func registrarAtalhoGlobal(id, descricao, gatilho string, ao func(token string)) (*AtalhoGlobal, error) {
	return nil, errors.New("atalho global ainda não implementado nesta plataforma")
}

// Fechar existe para o desligar dos Ajustes compilar aqui. Nada a soltar:
// registrarAtalhoGlobal nunca devolve um atalho nestas plataformas.
func (a *AtalhoGlobal) Fechar() {}
