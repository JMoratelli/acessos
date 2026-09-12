package runner

import "time"

const (
	// PisoExec e o minimo absoluto para um comando QUE FOI MEDIDO.
	PisoExec = 30 * time.Second

	// SemBaseline e o teto usado quando o canario nao conseguiu medir o
	// comando (host inacessivel, comando falhou antes de rodar). Nao
	// medir tem de significar generoso, nao apertado: aplicar o piso de
	// 30s a 200 maquinas so porque a amostra falhou derrubava tudo por
	// timeout. Serve apenas para nao pendurar a sessao para sempre.
	SemBaseline = 30 * time.Minute

	// TimeoutConexaoPadrao. Voltou aos 10s do script original: 3s nao
	// sobrevive a link de filial por VPN, e o canario nao ajuda aqui
	// (ele so mede as maquinas que responderam, ou seja, as rapidas).
	TimeoutConexaoPadrao = 10 * time.Second

	// TetoConexao limita apenas a SUGESTAO automatica; o valor digitado
	// pelo usuario na interface nao e limitado por isto.
	TetoConexao = 30 * time.Second
)

// CalcularTimeoutExec aplica margem escalonada sobre o PIOR tempo
// observado no canario (nao a media: 3 amostras e pouco, e se os
// canarios forem maquinas boas a media subestima o parque velho).
//
//	ate 5s     -> x10, piso de 30s   (date: 0,2s -> 30s)
//	5s a 1min  -> x5                 (30s -> 2min30)
//	acima 1min -> x3                 (20min -> 1h)
func CalcularTimeoutExec(pior time.Duration) time.Duration {
	if pior <= 0 {
		return SemBaseline
	}
	var t time.Duration
	switch {
	case pior < 5*time.Second:
		t = pior * 10
	case pior < time.Minute:
		t = pior * 5
	default:
		t = pior * 3
	}
	if t < PisoExec {
		t = PisoExec
	}
	return arredondar(t)
}

// AjustarTimeoutConexao usa o canario apenas como sanity check. O canario
// so mede maquinas que responderam, e o timeout de conexao existe
// justamente para as que nao respondem - por isso nao ha margem aqui,
// so um ajuste se o link se mostrar lento (VPN, enlace ruim).
func AjustarTimeoutConexao(base, piorConexao time.Duration) (time.Duration, bool) {
	if base <= 0 {
		base = TimeoutConexaoPadrao
	}
	if piorConexao <= 0 {
		return base, false
	}
	sugerido := piorConexao * 3
	if sugerido <= base {
		return base, false
	}
	if sugerido > TetoConexao {
		sugerido = TetoConexao
	}
	return arredondar(sugerido), true
}

func arredondar(d time.Duration) time.Duration {
	switch {
	case d < 10*time.Second:
		return d.Round(time.Second)
	case d < 10*time.Minute:
		return d.Round(5 * time.Second)
	default:
		return d.Round(time.Minute)
	}
}
