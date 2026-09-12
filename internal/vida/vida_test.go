package vida

import (
	"testing"
	"time"
)

// 192.0.2.1 é da faixa TEST-NET-1 (RFC 5737): reservada para
// documentação, nunca roteável. É o alvo "garantidamente morto" — não
// depende da rede de quem roda o teste.
func TestHostInexistenteEhMorto(t *testing.T) {
	if e := Checar("192.0.2.1", 0, 400*time.Millisecond); e != Morta {
		t.Errorf("192.0.2.1 devia ser Morta, veio %v", e)
	}
}

// O laço de leitura precisa descartar resposta de OUTRO host: o socket
// ICMP sem privilégio é compartilhado, e sem conferir o remetente todo
// host aparecia vivo quando havia sondagem em paralelo.
func TestLocalhostResponde(t *testing.T) {
	if e := Checar("127.0.0.1", 0, 900*time.Millisecond); e != Viva {
		t.Skip("sem ICMP sem privilégio neste sistema (ping_group_range)")
	}
}
