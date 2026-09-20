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
	"bytes"
	"errors"
	"fmt"
	"io"
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
	arq := caminho()
	if arq == "" {
		// $HOME indisponível é problema de ambiente, bem diferente de um
		// known_hosts que ainda não existe (primeira execução, caso
		// normal). Não falhamos fechado aqui — isso tiraria toda conexão
		// do ar por um problema de ambiente — mas avisamos, porque quem
		// depurar "toda máquina aparece como desconhecida" precisa saber
		// que a causa é essa, e não um known_hosts vazio de verdade.
		fmt.Fprintln(os.Stderr,
			"hostkey: sem $HOME, não há como checar known_hosts; tratando todo host como desconhecido")
	}
	verificar, err := knownhosts.New(arq)
	if err != nil {
		verificar = semKnownHosts(arq, err)
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

// primeiraVez trata todo host como novo. É a degradação: perde-se a
// detecção de chave TROCADA, que é justamente a que protege contra alguém
// no meio do caminho.
func primeiraVez(string, net.Addr, ssh.PublicKey) error {
	return &knownhosts.KeyError{}
}

// semKnownHosts decide o que fazer quando o known_hosts não carrega.
//
// São dois mundos diferentes, e o código tratava os dois como um só ("sem
// known_hosts ainda: tudo é primeira vez"):
//
//   - o arquivo NÃO EXISTE — primeira execução, caso normal, silêncio;
//   - o arquivo existe e NÃO PARSEIA — e aqui morava um defeito sério.
//     Uma única linha ilegível (tipo de chave que esta versão não conhece,
//     base64 truncado, linha editada à mão) faz o knownhosts.New abortar o
//     arquivo INTEIRO. Todo host virava Desconhecida, o ramo Mudou ficava
//     inalcançável e o operador via o azul de rotina "confiar e conectar"
//     no lugar do vermelho de "alguém está interceptando" — em silêncio, e
//     para sempre, porque Confiar() só acrescenta uma linha e o arquivo
//     continua sem parsear.
//
// No segundo caso as linhas BOAS são salvas: o arquivo é relido linha a
// linha, o que não passa fica de fora, e o verificador é montado com o
// resto. A checagem de troca de chave volta a valer para todo host que
// tenha linha legível, que é o que importa.
func semKnownHosts(arq string, err error) ssh.HostKeyCallback {
	if errors.Is(err, os.ErrNotExist) {
		return primeiraVez // ainda não existe: tudo é primeira vez
	}
	if v, ruins, erroSaneado := sanear(arq); erroSaneado == nil {
		fmt.Fprintf(os.Stderr,
			"hostkey: %s tem %d linha(s) ilegível(is) — ignoradas, o resto do "+
				"arquivo continua valendo (%v)\n", arq, ruins, err)
		return v
	}
	// Não deu nem saneado: é melhor dizer alto do que conectar calado,
	// porque daqui em diante troca de chave não é mais detectada.
	fmt.Fprintf(os.Stderr,
		"hostkey: não consegui ler %s (%v); TODO host vai aparecer como "+
			"desconhecido e a TROCA de chave deixa de ser detectada\n", arq, err)
	return primeiraVez
}

// sanear monta um verificador só com as linhas que parseiam. Devolve
// quantas foram descartadas.
//
// O arquivo temporário existe porque knownhosts.New só aceita CAMINHO, e é
// apagado assim que ele termina de ler — o DB fica em memória. A conferência
// linha a linha usa ssh.ParseKnownHosts, que pula vazia e comentário
// devolvendo io.EOF.
func sanear(arq string) (ssh.HostKeyCallback, int, error) {
	bruto, err := os.ReadFile(arq)
	if err != nil {
		return nil, 0, err
	}
	var bons [][]byte
	ruins := 0
	for _, linha := range bytes.Split(bruto, []byte("\n")) {
		_, _, _, _, _, perr := ssh.ParseKnownHosts(append(append([]byte{}, linha...), '\n'))
		if perr != nil && !errors.Is(perr, io.EOF) {
			ruins++
			continue
		}
		bons = append(bons, linha)
	}
	if ruins == 0 {
		// A falha não era de linha: não há o que sanear, e insistir só
		// esconderia a causa real.
		return nil, 0, fmt.Errorf("nenhuma linha ilegível encontrada em %s", arq)
	}
	tmp, err := os.CreateTemp("", "acessos-known-hosts-*")
	if err != nil {
		return nil, ruins, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bytes.Join(bons, []byte("\n"))); err != nil {
		tmp.Close()
		return nil, ruins, err
	}
	if err := tmp.Close(); err != nil {
		return nil, ruins, err
	}
	v, err := knownhosts.New(tmp.Name())
	if err != nil {
		return nil, ruins, err
	}
	return v, ruins, nil
}
