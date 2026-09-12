// Package chaveiro lê o chaveiro.ini do app original: é ele que guarda o
// COFRE (salt/kdf/verificador) e as credenciais NOMEADAS, reutilizáveis
// por várias conexões através de um alias.
//
//	chaveiro.ini
//	------------
//	[cofre]
//	salt = ...   kdf = ...   verificador = ...
//
//	[suporte-loja]
//	usuario = suporte
//	senha   = enc:v1:...
//
// No conexoes.ini, uma conexão aponta para a credencial com "!nome":
//
//	ssh_usuario = !suporte-loja
//	ssh_senha   = !suporte-loja
//
// POR QUE O COFRE VIVE AQUI, e não no conexoes.ini: o chaveiro existe
// desde a primeira execução, então é um lugar estável — não depende de
// haver alguma senha gravada numa conexão específica — e é
// autossuficiente: dá pra copiar só ele para outra máquina e levar as
// credenciais compartilhadas junto.
//
// Compatibilidade: arquivos antigos têm a seção [cofre] dentro do próprio
// conexoes.ini. Quem chama tenta o chaveiro primeiro e cai no
// conexoes.ini quando não houver chaveiro — ver cmd/acessos/abrir.go.
package chaveiro

import (
	"fmt"
	"os"
	"strings"
)

const aliasPrefixo = "!"

// Credencial é uma entrada nomeada do chaveiro. A senha vem como está no
// arquivo (possivelmente cifrada) — decifrar é decisão de quem tem o cofre.
type Credencial struct {
	Nome    string
	Usuario string
	Senha   string
}

// Arquivo é o chaveiro lido.
type Arquivo struct {
	Caminho     string
	Cofre       map[string]string // seção [cofre]
	Credenciais map[string]Credencial
	Ordem       []string // nomes na ordem do arquivo
}

// EhAlias diz se o valor de um campo aponta para o chaveiro.
func EhAlias(v string) bool { return len(v) > 1 && strings.HasPrefix(v, aliasPrefixo) }

// NomeDoAlias devolve o nome apontado ("" se não for alias).
func NomeDoAlias(v string) string {
	if !EhAlias(v) {
		return ""
	}
	return v[len(aliasPrefixo):]
}

// Resolver troca um alias pelo campo correspondente da credencial. campo
// termina em "usuario" ou "senha".
//
// Alias apontando para credencial que não existe (mais) devolve o valor
// COMO ESTÁ: quem chama decide o que fazer com um alias quebrado, em vez
// de o dado sumir em silêncio.
func (a *Arquivo) Resolver(valor, campo string) string {
	if a == nil {
		return valor
	}
	nome := NomeDoAlias(valor)
	if nome == "" {
		return valor
	}
	c, ok := a.Credenciais[nome]
	if !ok {
		return valor
	}
	if strings.HasSuffix(campo, "usuario") {
		return c.Usuario
	}
	return c.Senha
}

// Carregar lê o arquivo. Arquivo inexistente NÃO é erro (é o caso de quem
// ainda usa o cofre dentro do conexoes.ini) — devolve nil, nil.
func Carregar(caminho string) (*Arquivo, error) {
	b, err := os.ReadFile(caminho)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		// Existe mas não abre: ERRO, nunca "vazio". Tratar como vazio
		// levaria a regravar por cima e perder as credenciais.
		return nil, fmt.Errorf("chaveiro %s: %w", caminho, err)
	}
	a := &Arquivo{
		Caminho:     caminho,
		Cofre:       map[string]string{},
		Credenciais: map[string]Credencial{},
	}
	secao := ""
	for _, linha := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(linha)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			secao = t[1 : len(t)-1]
			if secao != "cofre" {
				a.Credenciais[secao] = Credencial{Nome: secao}
				a.Ordem = append(a.Ordem, secao)
			}
			continue
		}
		i := strings.IndexByte(t, '=')
		if i < 0 {
			continue
		}
		chave := strings.TrimSpace(t[:i])
		valor := strings.TrimSpace(t[i+1:])
		if secao == "cofre" {
			a.Cofre[chave] = valor
			continue
		}
		c := a.Credenciais[secao]
		switch chave {
		case "usuario":
			c.Usuario = valor
		case "senha":
			c.Senha = valor
		}
		a.Credenciais[secao] = c
	}
	return a, nil
}

// ------------------------------------------------------------ escrita

// A escrita é linha a linha, como no conexoes.ini: o chaveiro também é
// editável à mão e não pode perder comentários numa gravação.

func gravarAtomico(caminho string, linhas []string) error {
	tmp := caminho + ".tmp"
	conteudo := strings.Join(linhas, "\n")
	if !strings.HasSuffix(conteudo, "\n") {
		conteudo += "\n"
	}
	// 0600: o arquivo guarda segredo, mesmo cifrado.
	if err := os.WriteFile(tmp, []byte(conteudo), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}

func linhasDe(caminho string) ([]string, error) {
	b, err := os.ReadFile(caminho)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n"), nil
}

func faixa(linhas []string, secao string) (int, int, bool) {
	ini := -1
	for i, l := range linhas {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
			continue
		}
		if ini >= 0 {
			return ini, i, true
		}
		if t[1:len(t)-1] == secao {
			ini = i
		}
	}
	if ini >= 0 {
		return ini, len(linhas), true
	}
	return 0, 0, false
}

// Salvar cria ou atualiza uma credencial. A senha entra como vier (quem
// chama cifra antes, se o cofre estiver aberto); senha vazia mantém a que
// já está gravada — o chamador que quiser apagar passa explicitamente.
func Salvar(caminho, nome, usuario, senha string) error {
	if strings.TrimSpace(nome) == "" {
		return fmt.Errorf("nome da credencial não pode ser vazio")
	}
	if nome == "cofre" {
		return fmt.Errorf("%q é reservado para a seção do cofre", nome)
	}
	linhas, err := linhasDe(caminho)
	if err != nil {
		return err
	}
	novas := []string{"[" + nome + "]", "usuario = " + usuario}
	if senha != "" {
		novas = append(novas, "senha = "+senha)
	}
	ini, fim, achou := faixa(linhas, nome)
	if !achou {
		if len(linhas) > 0 {
			linhas = append(linhas, "")
		}
		return gravarAtomico(caminho, append(linhas, novas...))
	}
	if senha == "" {
		// mantém a senha que já estava lá
		for _, l := range linhas[ini+1 : fim] {
			if strings.HasPrefix(strings.TrimSpace(l), "senha") {
				novas = append(novas, strings.TrimSpace(l))
				break
			}
		}
	}
	out := append([]string{}, linhas[:ini]...)
	out = append(out, novas...)
	out = append(out, "")
	return gravarAtomico(caminho, append(out, linhas[fim:]...))
}

// Remover apaga a credencial. Conexões que apontavam para ela ficam com o
// alias quebrado — e isso é melhor que o app trocar a senha sozinho: o
// alias quebrado aparece na tela, uma troca silenciosa não.
func Remover(caminho, nome string) error {
	linhas, err := linhasDe(caminho)
	if err != nil {
		return err
	}
	ini, fim, achou := faixa(linhas, nome)
	if !achou {
		return fmt.Errorf("credencial %q não existe", nome)
	}
	return gravarAtomico(caminho, append(append([]string{}, linhas[:ini]...), linhas[fim:]...))
}

// GravarCofre escreve a seção [cofre] (criando o arquivo se preciso). É o
// que a migração do conexoes.ini usa.
func GravarCofre(caminho string, params map[string]string) error {
	linhas, err := linhasDe(caminho)
	if err != nil {
		return err
	}
	novas := []string{"[cofre]", "versao = 1"}
	for _, k := range []string{"kdf", "salt", "verificador"} {
		if v := params[k]; v != "" {
			novas = append(novas, k+" = "+v)
		}
	}
	ini, fim, achou := faixa(linhas, "cofre")
	if !achou {
		// o cofre vai no TOPO: é o que o arquivo tem de mais importante,
		// e quem abrir no editor tem que ver primeiro.
		out := append(novas, "")
		return gravarAtomico(caminho, append(out, linhas...))
	}
	out := append([]string{}, linhas[:ini]...)
	out = append(out, novas...)
	return gravarAtomico(caminho, append(out, linhas[fim:]...))
}
