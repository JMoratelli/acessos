//go:build !linux

package main

import "fmt"

// Fora do Linux não há serviço: o atalho global do Windows é
// RegisterHotKey dentro do próprio app (ver atalhoglobal_windows.go), sem
// portal e sem sessão que morra junto com o processo — ou seja, sem o
// problema que o serviço existe para resolver.
//
// Estes stubs existem para main.go compilar igual nas duas plataformas.

const ArgServico = "-servico"

type clienteServico struct{ Msgs chan mensagem }

func (cli *clienteServico) Fechar() {}

// Enviar existe para main.go compilar igual: aqui não há serviço do outro
// lado, e quem liga/desliga o atalho é o próprio app, em processo.
func (cli *clienteServico) Enviar(m mensagem) error { return nil }

func rodarServico(caminhoINI string) {
	fmt.Println("o modo serviço só existe no Linux")
}

func ligarNoServico(caminhoINI string, specs []map[string]string) (*clienteServico, bool) {
	return nil, false
}
