// Package iniutil reúne a manipulação de .ini linha a linha comum a
// internal/conexoes e internal/chaveiro: os dois editam o arquivo do
// jeito que o app original em Python grava (comentários, ordem e
// espaçamento preservados, nunca uma regravação a partir de uma
// estrutura em memória) e os dois precisam de escrita atômica — um
// arquivo truncado no meio de uma gravação não pode levar junto as
// conexões ou as credenciais do chaveiro.
package iniutil

import (
	"os"
	"strings"
)

// GravarAtomico grava linhas num arquivo temporário ao lado de caminho e
// troca por cima com rename — nunca deixa caminho num estado parcial.
// 0600 porque tanto conexoes.ini quanto chaveiro.ini podem guardar
// segredo (cifrado, mas ainda assim).
func GravarAtomico(caminho string, linhas []string) error {
	tmp := caminho + ".tmp"
	conteudo := strings.Join(linhas, "\n")
	if !strings.HasSuffix(conteudo, "\n") {
		conteudo += "\n"
	}
	if err := os.WriteFile(tmp, []byte(conteudo), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}

// LerLinhas lê caminho e devolve suas linhas, sem a quebra final. Erro
// de arquivo inexistente é devolvido como veio de os.ReadFile — quem
// chama decide se isso é normal (chaveiro.ini é opcional) ou fatal
// (conexoes.ini já deveria existir a esta altura).
func LerLinhas(caminho string) ([]string, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n"), nil
}

// NomeSecao devolve o nome de uma linha de cabeçalho [assim].
func NomeSecao(linha string) (string, bool) {
	t := strings.TrimSpace(linha)
	if len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']' {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// Faixa acha o intervalo [ini,fim) das linhas de uma seção, cabeçalho
// incluído.
func Faixa(linhas []string, secao string) (ini, fim int, ok bool) {
	achouIni := -1
	for i, l := range linhas {
		nome, cab := NomeSecao(l)
		if !cab {
			continue
		}
		if achouIni >= 0 {
			return achouIni, i, true
		}
		if nome == secao {
			achouIni = i
		}
	}
	if achouIni >= 0 {
		return achouIni, len(linhas), true
	}
	return 0, 0, false
}
