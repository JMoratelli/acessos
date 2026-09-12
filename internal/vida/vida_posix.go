//go:build !windows

package vida

import (
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

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
