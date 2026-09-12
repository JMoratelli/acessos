// Package hostkey confere a identidade do servidor SSH contra o
// ~/.ssh/known_hosts do próprio usuário — o MESMO arquivo do comando ssh,
// para o que já é confiado no terminal continuar confiado aqui.
//
// Por que isso importa: sem conferir, uma máquina no meio do caminho pode
// se passar pelo PDV e receber a senha de suporte inteira.
//
// Três desfechos, diferentes de propósito:
//
//	OK           -> confere, conecta.
//	Desconhecida -> primeira vez. Mostra a impressão digital e oferece confiar.
//	Mudou        -> não bate com a gravada. NUNCA conecta calado.
package hostkey

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Resultado int

const (
	OK Resultado = iota
	Desconhecida
	Mudou
)

// ErroChave carrega o que a interface precisa mostrar.
type ErroChave struct {
	Resultado Resultado
	Host      string
	Impressao string // SHA256:… da chave oferecida
	Tipo      string // ssh-ed25519, ecdsa-sha2-nistp256…
	chave     ssh.PublicKey
}

func (e *ErroChave) Error() string {
	switch e.Resultado {
	case Mudou:
		return fmt.Sprintf("a chave de %s MUDOU (%s %s)", e.Host, e.Tipo, e.Impressao)
	case Desconhecida:
		return fmt.Sprintf("%s ainda não é conhecido (%s %s)", e.Host, e.Tipo, e.Impressao)
	}
	return "chave ok"
}

func caminho() string {
	lar, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(lar, ".ssh", "known_hosts")
}

// Callback devolve o HostKeyCallback do ssh.ClientConfig. A falha vem como
// *ErroChave para a interface saber QUAL dos dois casos aconteceu:
// "primeira vez" e "mudou" pedem conversas bem diferentes.
func Callback() ssh.HostKeyCallback {
	verificar, err := knownhosts.New(caminho())
	if err != nil {
		// sem known_hosts ainda: tudo é primeira vez
		verificar = func(string, net.Addr, ssh.PublicKey) error {
			return &knownhosts.KeyError{}
		}
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if err := verificar(hostname, remote, key); err != nil {
			e := &ErroChave{
				Host:      hostname,
				Impressao: ssh.FingerprintSHA256(key),
				Tipo:      key.Type(),
				chave:     key,
				Resultado: Desconhecida,
			}
			var ke *knownhosts.KeyError
			if errors.As(err, &ke) && len(ke.Want) > 0 {
				// Want preenchido = existe chave gravada e ela não bate.
				e.Resultado = Mudou
			}
			return e
		}
		return nil
	}
}

// Confiar grava a chave (primeira conexão) ou substitui a anterior, depois
// de o operador aceitar a troca.
func Confiar(e *ErroChave) error {
	arquivo := caminho()
	if arquivo == "" {
		return fmt.Errorf("sem HOME para gravar o known_hosts")
	}
	// Remove antes de gravar SEMPRE (não só quando mudou): confiar duas
	// vezes no mesmo host deixava duas linhas iguais no arquivo.
	if err := Remover(e.Host); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(arquivo), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(arquivo, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(knownhosts.Line([]string{e.Host}, e.chave) + "\n")
	return err
}

// Remover apaga as linhas daquele host — é o `ssh-keygen -R` feito aqui,
// para não depender de o binário estar instalado (Flatpak, container).
func Remover(host string) error {
	arquivo := caminho()
	b, err := os.ReadFile(arquivo)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var mantidas []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "" || casaHost(l, host) {
			continue
		}
		mantidas = append(mantidas, l)
	}
	tmp := arquivo + ".tmp"
	conteudo := strings.Join(mantidas, "\n")
	if conteudo != "" {
		conteudo += "\n"
	}
	if err := os.WriteFile(tmp, []byte(conteudo), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, arquivo)
}

// casaHost compara o primeiro campo da linha com o host, aceitando lista
// separada por vírgula e a forma "[host]:porta".
func casaHost(linha, host string) bool {
	campo := strings.Fields(linha)
	if len(campo) == 0 {
		return false
	}
	alvo := knownhosts.Normalize(host)
	for _, h := range strings.Split(campo[0], ",") {
		if h == host || h == alvo {
			return true
		}
	}
	return false
}
