package main

import (
	"testing"

	"acessos-go/internal/chaveiro"
)

// TestCredencialDigitadaResolveAlias cobre o bug relatado: um operador
// digitando "!Nome" num campo de usuário/senha da aba Massa (o app ensina
// esse alias em outros lugares — ver conexaodlg.go) tinha o texto "!Nome"
// enviado cru pro servidor em vez da credencial de verdade guardada no
// chaveiro.
func TestCredencialDigitadaResolveAlias(t *testing.T) {
	antigo := chaveiroAtual
	t.Cleanup(func() { chaveiroAtual = antigo })

	chaveiroAtual = &chaveiro.Arquivo{
		Credenciais: map[string]chaveiro.Credencial{
			"Zanthus": {Usuario: "suporte", Senha: "S3nh@Real"},
		},
		Ordem: []string{"Zanthus"},
	}

	t.Run("texto livre passa direto", func(t *testing.T) {
		c, err := credencialDigitada("fulano", "minhasenha")
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		if c.Usuario != "fulano" || c.Senha != "minhasenha" {
			t.Fatalf("got %+v, esperado usuário/senha inalterados", c)
		}
	})

	t.Run("alias resolve pra credencial de verdade", func(t *testing.T) {
		c, err := credencialDigitada("!Zanthus", "!Zanthus")
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		if c.Usuario != "suporte" || c.Senha != "S3nh@Real" {
			t.Fatalf("got %+v, esperado a credencial resolvida — não o alias cru", c)
		}
	})

	t.Run("alias quebrado na senha vira erro, não texto cru", func(t *testing.T) {
		_, err := credencialDigitada("qualquer", "!NaoExiste")
		if err == nil {
			t.Fatal("esperava erro de credencial inexistente")
		}
	})

	t.Run("sem chaveiro carregado, nada quebra", func(t *testing.T) {
		chaveiroAtual = nil
		c, err := credencialDigitada("fulano", "minhasenha")
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		if c.Usuario != "fulano" || c.Senha != "minhasenha" {
			t.Fatalf("got %+v", c)
		}
	})
}
