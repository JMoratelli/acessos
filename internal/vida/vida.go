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
// folgaICMP é quanto se espera pelo ICMP sozinho antes de o toque TCP
// entrar em paralelo.
//
// O ICMP continua sendo o preferido e o TCP continua sendo reserva — o que
// muda é que a reserva não espera mais o prazo INTEIRO do ICMP para
// começar. Em série, um host morto custava dois prazos cheios (900ms +
// 900ms no padrão), e um host vivo com ICMP filtrado pagava o prazo
// inteiro antes de a porta ser tocada.
//
// 70ms é o suficiente para a máquina saudável da LAN responder e NÃO levar
// batida de porta nenhuma, que é o ponto: a maioria das conexões daqui
// aponta para VNC 5900, e servidor VNC em modo pergunta abre um aviso no
// lado remoto a cada toque. Quem passa dos 70ms é host morto ou host com
// ICMP filtrado — nos dois casos tocar a porta é o que se queria de
// qualquer forma.
const folgaICMP = 70 * time.Millisecond

func Checar(host string, portaTCP int, prazo time.Duration) Estado {
	if prazo <= 0 {
		prazo = 900 * time.Millisecond
	}
	// O canal tem buffer para a goroutine nunca ficar pendurada, mesmo
	// quando esta função devolve antes de ler.
	icmp := make(chan bool, 1)
	go func() { icmp <- pingICMP(host, prazo) }()

	if portaTCP <= 0 {
		if <-icmp {
			return Viva
		}
		return Morta
	}

	select {
	case viva := <-icmp:
		if viva {
			return Viva // respondeu na folga: a porta não chega a ser tocada
		}
		// O ICMP já disse que não: o TCP decide, com o prazo inteiro.
		if tocarTCP(host, portaTCP, prazo) {
			return Viva
		}
		return Morta
	case <-time.After(folgaICMP):
	}

	// Passou da folga sem resposta. Os dois correm juntos e o primeiro
	// "sim" ganha; para dizer Morta é preciso ouvir os dois.
	tcp := make(chan bool, 1)
	go func() { tcp <- tocarTCP(host, portaTCP, prazo) }()
	for i := 0; i < 2; i++ {
		select {
		case viva := <-icmp:
			if viva {
				return Viva
			}
		case viva := <-tcp:
			if viva {
				return Viva
			}
		}
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
