// Package vida diz se uma máquina responde, sem depender do binário ping.
//
// O app original faz ICMP em Python puro pelo mesmo motivo: dentro de
// Flatpak (e de container em geral) não há /bin/ping com as permissões
// certas, e chamar um processo por host não escala para 274 máquinas.
//
// ICMP cru exige privilégio; em Linux o caminho sem root é o socket
// "datagram ICMP", liberado pelo sysctl net.ipv4.ping_group_range. Quando
// nem isso existe, caímos num toque TCP — que responde a pergunta que
// interessa de verdade ("está no ar?") mesmo sem ICMP.
package vida

import (
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// Estado do host.
type Estado int

const (
	Desconhecido Estado = iota
	Viva
	Morta
)

// Checar devolve Viva se o host respondeu dentro do prazo. portaTCP > 0
// habilita o toque TCP de reserva (use uma porta que a máquina tenha,
// como a do VNC ou a do SSH).
func Checar(host string, portaTCP int, prazo time.Duration) Estado {
	if prazo <= 0 {
		prazo = 900 * time.Millisecond
	}
	if pingICMP(host, prazo) {
		return Viva
	}
	if portaTCP > 0 && tocarTCP(host, portaTCP, prazo) {
		return Viva
	}
	return Morta
}

func pingICMP(host string, prazo time.Duration) bool {
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return false
	}
	defer c.Close()

	alvo, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		return false
	}
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: 1, Data: []byte("acessos")},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		return false
	}
	if _, err := c.WriteTo(b, &net.UDPAddr{IP: alvo.IP}); err != nil {
		return false
	}
	// O socket ICMP sem privilégio é COMPARTILHADO: chega resposta de
	// ping de qualquer destino, inclusive dos outros hosts que estamos
	// sondando em paralelo. Sem conferir de QUEM veio, todo host
	// aparecia vivo — foi o que aconteceu na primeira versão. Então:
	// lê até o prazo e só aceita a resposta cujo remetente é o alvo.
	limite := time.Now().Add(prazo)
	resp := make([]byte, 1500)
	for time.Now().Before(limite) {
		_ = c.SetReadDeadline(limite)
		n, peer, err := c.ReadFrom(resp)
		if err != nil {
			return false
		}
		if !mesmoIP(peer, alvo.IP) {
			continue
		}
		m, err := icmp.ParseMessage(1, resp[:n]) // 1 = ICMP v4
		if err != nil {
			continue
		}
		if m.Type == ipv4.ICMPTypeEchoReply {
			return true
		}
	}
	return false
}

// mesmoIP compara o remetente da resposta com o alvo, aceitando tanto
// *net.UDPAddr (socket sem privilégio) quanto *net.IPAddr (socket cru).
func mesmoIP(peer net.Addr, alvo net.IP) bool {
	switch a := peer.(type) {
	case *net.UDPAddr:
		return a.IP.Equal(alvo)
	case *net.IPAddr:
		return a.IP.Equal(alvo)
	}
	return false
}

func tocarTCP(host string, porta int, prazo time.Duration) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(porta)), prazo)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
