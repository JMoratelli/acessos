package hostkey

import (
	"crypto/ed25519"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// chaveDeTeste devolve uma chave pública determinística: mesma semente,
// mesma chave, em qualquer máquina.
func chaveDeTeste(t *testing.T, semente byte) ssh.PublicKey {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(sementeDe(semente))
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func sementeDe(b byte) []byte {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = b
	}
	return s
}

// larDeMentira aponta o os.UserHomeDir() para um diretório temporário.
//
// Os DOIS nomes são obrigatórios: o Unix lê $HOME e o WINDOWS lê
// %USERPROFILE%. Só com HOME, o teste rodava contra o known_hosts DE
// VERDADE da máquina no Windows — passava por acaso quando o host de
// teste não estava lá, e falhava sem explicar nada quando estava.
func larDeMentira(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func escreverKnownHosts(t *testing.T, conteudo string) {
	t.Helper()
	dir := larDeMentira(t)
	dotSSH := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(dotSSH, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotSSH, "known_hosts"), []byte(conteudo), 0o600); err != nil {
		t.Fatal(err)
	}
}

// UMA linha ilegível não pode cegar o arquivo inteiro.
//
// O knownhosts.New aborta no primeiro parseLine que falha, e o código
// tratava esse erro como "ainda não existe known_hosts": todo host virava
// Desconhecida e o ramo Mudou ficava inalcançável — ou seja, a troca de
// chave, que é a razão de existir do pacote, deixava de ser detectada, em
// silêncio. Basta uma linha com tipo de chave que esta versão não conhece.
func TestLinhaIlegivelNaoCegaADeteccaoDeTrocaDeChave(t *testing.T) {
	const host = "10.0.0.7:22"
	gravada := chaveDeTeste(t, 7)
	intrusa := chaveDeTeste(t, 9)

	escreverKnownHosts(t,
		"10.0.0.99 ssh-tipo-que-nao-existe AAAAlixo\n"+
			knownhosts.Line([]string{host}, gravada)+"\n")

	verificar := Callback()
	remoto := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 22}

	if err := verificar(host, remoto, gravada); err != nil {
		t.Fatalf("a chave gravada devia passar, veio: %v", err)
	}

	err := verificar(host, remoto, intrusa)
	var e *ErroChave
	if !errors.As(err, &e) {
		t.Fatalf("chave trocada devia dar *ErroChave, veio: %v", err)
	}
	if e.Resultado != Mudou {
		t.Fatalf("chave trocada deu %v; era Mudou — é este o caso que "+
			"mostra o vermelho de 'alguém no meio do caminho'", e.Resultado)
	}
}

// Sem arquivo é o caso normal da primeira execução: tudo é primeira vez,
// e sem barulho.
func TestSemKnownHostsTodoHostEhPrimeiraVez(t *testing.T) {
	larDeMentira(t)

	err := Callback()("10.0.0.7:22",
		&net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 22}, chaveDeTeste(t, 7))
	var e *ErroChave
	if !errors.As(err, &e) || e.Resultado != Desconhecida {
		t.Fatalf("esperava Desconhecida, veio: %v", err)
	}
}

// Arquivo íntegro continua funcionando como sempre — a guarda nova não
// pode ter mexido no caminho normal.
func TestKnownHostsIntegro(t *testing.T) {
	const host = "10.0.0.7:22"
	gravada := chaveDeTeste(t, 7)
	escreverKnownHosts(t, knownhosts.Line([]string{host}, gravada)+"\n")

	remoto := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 22}
	if err := Callback()(host, remoto, gravada); err != nil {
		t.Fatalf("chave certa devia passar: %v", err)
	}
	err := Callback()(host, remoto, chaveDeTeste(t, 9))
	var e *ErroChave
	if !errors.As(err, &e) || e.Resultado != Mudou {
		t.Fatalf("esperava Mudou, veio: %v", err)
	}
}
