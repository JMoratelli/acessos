package telaproc

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// TETO POR SESSÃO
//
// Este é o limite que faz sentido de verdade, e o único que este arquivo
// impõe: quanto UMA sessão pode consumir. Quantas sessões abrir é decisão
// de quem opera; o que não pode é UMA aba descontrolada comer a máquina
// inteira e travar tudo — que é exatamente o desastre que a arquitetura de
// processo por sessão existe para evitar.
//
// Por que aqui dentro e não um rlimit no filho: RLIMIT_AS conta espaço de
// endereçamento VIRTUAL, e o runtime do Go reserva arenas virtuais enormes
// que nunca viram memória de verdade — um teto de RSS honesto viraria
// falha de alocação com a máquina vazia. Vigiar o RSS de fato, e sair
// limpo quando estoura, é mais simples e diz a verdade no log.
//
// Quando estoura, o filho SAI. Do lado do processo principal isso é
// idêntico a qualquer outra queda: o canal fecha, a aba mostra "CAIU" e o
// backoff religa. Nenhuma outra aba sente nada.

// LimiteSessaoMiB é o teto de memória residente de UM processo de sessão.
//
// A margem é deliberadamente larga. O pior caso legítimo medido não chega
// perto: uma sessão RDP de 1024x768 fica em ~118 MiB de RSS, e uma de 4K
// (3840x2160) acrescenta ~33 MiB de framebuffer por cópia — algo como 250
// a 300 MiB no total. 768 MiB dá mais de duas vezes e meia de folga sobre
// isso, então só se chega aqui por defeito de verdade: vazamento na
// biblioteca C, ou um servidor malicioso mandando geometria absurda.
//
// Mexer neste número pede a mesma disciplina do resto: MEÇA antes (ver
// TestAoVivoCustoDeUmFilho e TestCustoDeBaseDosFilhos) em vez de estimar a
// partir do que está escrito aqui.
const LimiteSessaoMiB = 768

// VarLimiteSessao sobrepõe LimiteSessaoMiB. Existe por dois motivos: para
// o teste conseguir provocar o estouro sem alocar 768 MiB de verdade, e
// para dar uma saída a quem estiver depurando uma sessão gorda numa
// máquina apertada, sem recompilar. Valor inválido ou ausente = o padrão.
const VarLimiteSessao = "ACESSOS_LIMITE_SESSAO_MIB"

func limiteSessaoMiB() int {
	if v := os.Getenv(VarLimiteSessao); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return LimiteSessaoMiB
}

// intervaloVigia é de quanto em quanto tempo o filho se pesa. Memória não
// explode em milissegundos, e /proc/self/statm é barato mas não de graça.
const intervaloVigia = 2 * time.Second

// VigiarMemoria roda DENTRO do processo-filho e o derruba se ele passar do
// teto. Chame uma vez, no começo; ela não retorna.
//
// Numa plataforma onde não sabemos medir, devolve na hora sem vigiar nada:
// falha aberta de propósito (ver memoria_outros.go).
func VigiarMemoria(protocolo string) {
	if _, ok := residenteMiB(); !ok {
		return
	}
	limite := limiteSessaoMiB()
	go func() {
		for {
			time.Sleep(intervaloVigia)
			usado, ok := residenteMiB()
			if !ok || usado <= limite {
				continue
			}
			// stderr é herdado do processo principal, então esta linha cai
			// no mesmo log que o resto do app — é a única pista que vai
			// sobrar de por que a aba caiu.
			fmt.Fprintf(os.Stderr,
				"[filho %s] passei de %d MiB de memória (teto %d) — saindo para não travar a máquina;"+
					" a aba vai reconectar sozinha\n",
				protocolo, usado, limite)
			os.Exit(3)
		}
	}()
}
