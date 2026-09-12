package executor

import (
	"errors"
	"time"

	"golang.org/x/crypto/ssh"

	"acessos-go/internal/massa/model"
)

// Erros de transporte. Erro de conexao e tratado de forma diferente de
// erro de comando: conexao nunca abre dialogo, so vai para o erro.txt.
var (
	ErrConexao    = errors.New("falha de conexao")
	ErrAutenticar = errors.New("falha de autenticacao")
	ErrElevar     = errors.New("falha ao elevar privilegio")
	// ErrPreparo: conectou e autenticou, mas o shell remoto nao chegou
	// a um estado utilizavel. E diferente de falha de rede e de falha
	// de comando, e merece mensagem propria.
	ErrPreparo     = errors.New("shell remoto nao ficou utilizavel")
	ErrTimeout     = errors.New("timeout de execucao")
	ErrIdle        = errors.New("sem saida pelo tempo de idle configurado")
	ErrNaoSuportad = errors.New("plataforma ainda nao suportada")
)

// Executor abstrai o transporte ate o alvo. Uma instancia serve um
// unico host e mantem a sessao aberta entre comandos, preservando
// estado (cd, variaveis de ambiente) de um comando para o proximo.
type Executor interface {
	// Conectar abre o transporte e prepara o shell.
	Conectar(host string, cred model.Credencial, timeout time.Duration) error

	// Elevar troca para o usuario privilegiado dentro da mesma sessao.
	Elevar(cred model.Credencial) error

	// Rodar executa um bloco de comando (pode ser multilinha) e devolve
	// a saida combinada e o codigo de saida.
	// timeoutExec 0 = sem limite. timeoutIdle 0 = desligado.
	Rodar(cmd string, timeoutExec, timeoutIdle time.Duration) (saida string, exitCode int, err error)

	// Fechar encerra a sessao.
	Fechar()
}

// Novo devolve o executor adequado a plataforma.
func Novo(p model.Plataforma) (Executor, error) {
	switch p {
	case model.Windows:
		return NovoWindows()
	default:
		return NovoLinuxSSH(), nil
	}
}

// AlgoritmosLegado amplia a negociacao para alcancar PDV antigo. A
// biblioteca do Go corta por padrao varios algoritmos que OpenSSH
// antigo ainda oferece; sem isto o aperto de mao falha em maquina
// velha, mesmo com a rede perfeita.
//
// Rede interna de PDV, alvo conhecido: aceitar algoritmo antigo aqui
// custa menos que nao conseguir administrar a maquina.
func AlgoritmosLegado() ssh.Config {
	return ssh.Config{
		KeyExchanges: []string{
			"curve25519-sha256", "curve25519-sha256@libssh.org",
			"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
			"diffie-hellman-group14-sha256", "diffie-hellman-group16-sha512",
			// os dois abaixo ficam fora do padrao do Go
			"diffie-hellman-group14-sha1", "diffie-hellman-group1-sha1",
		},
		Ciphers: []string{
			"aes128-gcm@openssh.com", "aes256-gcm@openssh.com",
			"chacha20-poly1305@openssh.com",
			"aes128-ctr", "aes192-ctr", "aes256-ctr",
			// idem: fora do padrao, presentes em servidor antigo
			"aes128-cbc", "3des-cbc",
		},
		MACs: []string{
			"hmac-sha2-256-etm@openssh.com", "hmac-sha2-512-etm@openssh.com",
			"hmac-sha2-256", "hmac-sha2-512", "hmac-sha1",
		},
	}
}
