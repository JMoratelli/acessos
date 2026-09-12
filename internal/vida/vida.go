// Package vida diz se uma máquina responde, sem depender do binário ping.
//
// O app original faz ICMP em Python puro pelo mesmo motivo: dentro de
// Flatpak (e de container em geral) não há /bin/ping com as permissões
// certas, e chamar um processo por host não escala para 274 máquinas.
//
// ICMP cru exige privilégio, e cada sistema tem o seu caminho sem root:
// em Linux o socket "datagram ICMP" (sysctl net.ipv4.ping_group_range),
// em Windows o IcmpSendEcho do iphlpapi. Daí pingICMP viver num arquivo
// por plataforma. Quando nem isso existe, caímos num toque TCP — que
// responde a pergunta que interessa de verdade ("está no ar?").
package vida

import (
	"net"
	"strconv"
	"time"
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

func tocarTCP(host string, porta int, prazo time.Duration) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(porta)), prazo)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
