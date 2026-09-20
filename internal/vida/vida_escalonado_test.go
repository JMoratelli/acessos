package vida

import (
	"testing"
	"time"
)

// Host morto não pode custar dois prazos cheios.
//
// A checagem era em série: o ICMP gastava o prazo inteiro e só então o
// toque TCP começava, gastando o dele. Agora o TCP entra depois de uma
// folga curta e os dois correm juntos, então o custo é ~um prazo, não
// dois. O endereço é da faixa TEST-NET-1 (RFC 5737), reservada para
// documentação: não é roteável, então não há como alguém responder.
func TestHostMortoNaoPagaDoisPrazos(t *testing.T) {
	if testing.Short() {
		t.Skip("mede tempo de rede")
	}
	const prazo = 400 * time.Millisecond

	inicio := time.Now()
	if e := Checar("192.0.2.1", 5900, prazo); e != Morta {
		t.Fatalf("esperava Morta para um endereço não roteável, veio %v", e)
	}
	gasto := time.Since(inicio)

	// Em série seriam 2*prazo = 800ms. A folga de 70ms mais um prazo dá
	// ~470ms; o limite abaixo deixa margem para máquina carregada, mas
	// ainda falha se alguém voltar a encadear as duas checagens.
	if limite := 2*prazo - 50*time.Millisecond; gasto > limite {
		t.Errorf("gastou %v, acima de %v — as checagens voltaram a ser em série?",
			gasto, limite)
	}
	t.Logf("host morto respondido em %v (em série seriam ~%v)", gasto, 2*prazo)
}

// Sem porta TCP não há reserva: só o ICMP decide, e a função não pode
// pendurar esperando um segundo canal que ninguém vai alimentar.
func TestSemPortaTCPSoUsaICMP(t *testing.T) {
	if testing.Short() {
		t.Skip("mede tempo de rede")
	}
	pronto := make(chan Estado, 1)
	go func() { pronto <- Checar("192.0.2.1", 0, 300*time.Millisecond) }()
	select {
	case e := <-pronto:
		if e != Morta {
			t.Fatalf("esperava Morta, veio %v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Checar não voltou — pendurou esperando o toque TCP que não existe")
	}
}
