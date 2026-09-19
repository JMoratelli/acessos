package main

// Liga/desliga o atalho global, pela chave `[geral] atalho_global` do
// conexoes.ini:
//
//	(vazio)  ligado — é o padrão, e é o que vale em instalação nova
//	1        ligado
//	0        desligado; o atalho não é registrado, e se já estiver
//	         registrado é SOLTO (ver AtalhoGlobal.Fechar)
//
// Soltar de verdade, em vez de registrar e ignorar o disparo, é de
// propósito: enquanto o registro existe, o sistema mostra a tecla presa
// em nome do Acessos (no KDE, Preferências do Sistema → Atalhos) e não
// deixa outro programa usá-la. Quem desliga o atalho aqui espera a tecla
// livre lá.
//
// Não confundir com `atalho_autostart` (ver autostart_linux.go): aquela
// diz se o SISTEMA sobe o serviço no login; esta diz se o atalho existe.
// Desligar o atalho não mexe no autostart — o serviço continua de pé
// cuidando de instância única e de abrir conexão vinda de outra janela.

import "acessos-go/internal/conexoes"

const chaveAtalhoGlobal = "atalho_global"

// atalhoGlobalLigado lê a preferência. Qualquer problema de leitura
// devolve LIGADO: o padrão não pode depender de o arquivo estar são, e um
// atalho a mais é menos surpreendente que um atalho que sumiu sozinho.
func atalhoGlobalLigado(caminhoINI string) bool {
	arq, err := conexoes.Carregar(caminhoINI)
	if err != nil {
		return true
	}
	return arq.Geral[chaveAtalhoGlobal] != "0"
}

func salvarAtalhoGlobalLigado(caminhoINI string, ligado bool) error {
	valor := "1"
	if !ligado {
		valor = "0"
	}
	return conexoes.SalvarGeral(caminhoINI, map[string]string{chaveAtalhoGlobal: valor})
}
