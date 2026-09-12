package executor

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"acessos-go/internal/massa/model"
)

// Sonda e o resultado de espiar um PDV sem autenticar.
//
// O servidor SSH manda sua string de identificacao antes de qualquer
// negociacao ou senha (RFC 4253, secao 4.2). Isso da tres coisas de
// graca: se o TCP passa, qual a plataforma, e qual a versao do OpenSSH.
type Sonda struct {
	Host       string
	Banner     string // ex: SSH-2.0-OpenSSH_8.9p1 Ubuntu-3ubuntu0.4
	Plataforma model.Plataforma
	TempoTCP   time.Duration // so o aperto de mao TCP
	TempoTotal time.Duration // ate receber o banner
	Erro       error
}

// FaseFalha diz em que ponto parou, que e o que separa "rede bloqueada"
// de "SSH recusando".
func (s Sonda) FaseFalha() string {
	switch {
	case s.Erro == nil:
		return ""
	case errors.Is(s.Erro, ErrTCP):
		return "TCP nao completou (firewall, rota, host fora do ar)"
	case errors.Is(s.Erro, ErrBanner):
		return "TCP abriu mas o servico nao se identificou como SSH"
	}
	return "falha"
}

var (
	// ErrTCP: nem o aperto de mao TCP fechou.
	ErrTCP = errors.New("tcp nao conectou")
	// ErrBanner: conectou mas nao veio identificacao SSH.
	ErrBanner = errors.New("sem banner SSH")
)

// Sondar abre um TCP, le a linha de identificacao e fecha. Nao envia
// credencial nenhuma, entao e seguro rodar em parque inteiro.
func Sondar(host string, timeout time.Duration) Sonda {
	s := Sonda{Host: host}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	endereco := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		endereco = net.JoinHostPort(host, "22")
	}

	t0 := time.Now()
	conn, err := net.DialTimeout("tcp", endereco, timeout)
	if err != nil {
		s.Erro = fmt.Errorf("%w: %v", ErrTCP, err)
		s.TempoTotal = time.Since(t0)
		return s
	}
	defer conn.Close()
	s.TempoTCP = time.Since(t0)

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	linha, err := bufio.NewReader(conn).ReadString('\n')
	s.TempoTotal = time.Since(t0)
	if err != nil && linha == "" {
		s.Erro = fmt.Errorf("%w: %v", ErrBanner, err)
		return s
	}

	s.Banner = strings.TrimRight(linha, "\r\n")
	if !strings.HasPrefix(s.Banner, "SSH-") {
		s.Erro = fmt.Errorf("%w: respondeu %q", ErrBanner, corta(s.Banner, 60))
		return s
	}
	s.Plataforma = PlataformaDoBanner(s.Banner)
	return s
}

// PlataformaDoBanner deduz o sistema pela identificacao do servidor.
// Devolve string vazia quando nao da para afirmar - melhor admitir que
// nao sabe do que gravar palpite errado no csv.
func PlataformaDoBanner(banner string) model.Plataforma {
	b := strings.ToLower(banner)
	switch {
	case strings.Contains(b, "for_windows"), strings.Contains(b, "windows"):
		return model.Windows
	case strings.Contains(b, "ubuntu"), strings.Contains(b, "debian"),
		strings.Contains(b, "raspbian"), strings.Contains(b, "freebsd"),
		strings.Contains(b, "dropbear"), strings.Contains(b, "_sshlib"):
		return model.Linux
	case strings.Contains(b, "openssh"):
		// OpenSSH sem sufixo de distro: quase sempre Unix compilado do
		// fonte. Windows sempre carrega "for_Windows" no banner.
		return model.Linux
	}
	return ""
}

func corta(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
