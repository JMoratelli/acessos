package telaproc

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"
)

// O próprio binário de teste faz de processo-filho: Iniciar reexecuta
// os.Executable(), que aqui é o teste. Assim o caminho exercitado é o de
// verdade — sobe processo, liga de volta, apresenta o token — e não uma
// imitação em memória que passaria mesmo com o handshake quebrado.
func TestMain(m *testing.M) {
	if len(os.Args) == 5 && os.Args[1] == ArgWorker {
		os.Exit(filhoDeTeste(os.Args[2], os.Args[3], os.Args[4]))
	}
	os.Exit(m.Run())
}

// filhoDeTeste é um eco: devolve cada comando como um evento, para o teste
// poder conferir os dois sentidos do canal.
func filhoDeTeste(protocolo, endereco, token string) int {
	if protocolo != "eco" {
		return 2
	}
	c, err := Atender(endereco, token)
	if err != nil {
		return 3
	}
	// O filho de teste liga o vigia igual ao worker de verdade (ver
	// modoWorker em cmd/acessos/telaworker.go), senão o teste do vigia
	// estaria exercitando um caminho que o app não usa.
	VigiarMemoria(protocolo)
	for {
		tipo, corpo, err := c.Ler()
		if err != nil {
			return 0
		}
		switch tipo {
		case CmdConectar:
			_ = c.Enviar(EvtConectado, nil)
		case CmdTecla:
			kc, pressionada, ok := LerTecla(corpo)
			if !ok {
				return 4
			}
			_ = c.EnviarCursor(kc + map[bool]uint32{true: 1, false: 0}[pressionada])
		case CmdPonteiroBotao:
			x, y, b, pressionado, ok := LerPonteiroBotao(corpo)
			if !ok {
				return 5
			}
			_ = c.Enviar(EvtDesconectado, []byte(
				string(rune('0'+x))+string(rune('0'+y))+
					string(rune('0'+b))+map[bool]string{true: "P", false: "S"}[pressionado]))
		case CmdCredito:
			// um quadro 2x1 no meio de uma tela 8x4
			pix := []byte{1, 2, 3, 4, 5, 6, 7, 8}
			_ = c.EnviarQuadro(Quadro{X: 3, Y: 2, W: 2, H: 1, TotalW: 8, TotalH: 4}, pix)
		}
	}
}

func iniciarEco(t *testing.T) *Processo {
	t.Helper()
	p, err := Iniciar("eco")
	if err != nil {
		t.Fatalf("Iniciar: %v", err)
	}
	t.Cleanup(p.Encerrar)
	return p
}

func esperar(t *testing.T, p *Processo, quer Tipo) []byte {
	t.Helper()
	tipo, corpo, err := p.Ler()
	if err != nil {
		t.Fatalf("Ler: %v", err)
	}
	if tipo != quer {
		t.Fatalf("recebi tipo %d, esperava %d", tipo, quer)
	}
	return append([]byte(nil), corpo...)
}

func TestHandshakeEIdaEVolta(t *testing.T) {
	p := iniciarEco(t)

	if err := p.Conectar(Ligacao{Host: "exemplo", Porta: 3389}); err != nil {
		t.Fatalf("Conectar: %v", err)
	}
	esperar(t, p, EvtConectado)

	if err := p.Tecla(42, true); err != nil {
		t.Fatalf("Tecla: %v", err)
	}
	cur, ok := LerCursor(esperar(t, p, EvtCursor))
	if !ok || cur != 43 {
		t.Fatalf("cursor %d ok=%v, esperava 43", cur, ok)
	}

	if err := p.PonteiroBotao(1, 2, 3, false); err != nil {
		t.Fatalf("PonteiroBotao: %v", err)
	}
	if got := string(esperar(t, p, EvtDesconectado)); got != "123S" {
		t.Fatalf("botão voltou %q, esperava %q", got, "123S")
	}
}

func TestQuadroAtravessaOCanal(t *testing.T) {
	p := iniciarEco(t)
	if err := p.Credito(); err != nil {
		t.Fatalf("Credito: %v", err)
	}
	q, pix, err := DecodificarQuadro(esperar(t, p, EvtQuadro))
	if err != nil {
		t.Fatalf("DecodificarQuadro: %v", err)
	}
	if q != (Quadro{X: 3, Y: 2, W: 2, H: 1, TotalW: 8, TotalH: 4}) {
		t.Fatalf("cabeçalho veio %+v", q)
	}
	if !bytes.Equal(pix, []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("pixels vieram %v", pix)
	}
}

// O filho morrendo tem de virar um erro de leitura no processo principal —
// é DISSO que depende a aba detectar o crash da biblioteca C e religar.
func TestMorteDoFilhoViraErroDeLeitura(t *testing.T) {
	p, err := Iniciar("eco")
	if err != nil {
		t.Fatalf("Iniciar: %v", err)
	}
	defer p.Encerrar()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	fim := make(chan error, 1)
	go func() { _, _, err := p.Ler(); fim <- err }()
	select {
	case err := <-fim:
		if err == nil {
			t.Fatal("Ler devolveu nil depois do filho morrer")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ler não acordou com a morte do filho")
	}
}

// Token errado tem de ser recusado: sem isso qualquer processo local
// poderia se conectar primeiro e passar a receber a tela e as teclas.
func TestTokenErradoNaoEntra(t *testing.T) {
	ln, err := escutarLocal()
	if err != nil {
		t.Fatalf("escutarLocal: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := Atender(ln.Addr().String(), "token-errado")
		if err == nil {
			c.Fechar()
		}
	}()
	if _, err := aceitar(ln, []byte("token-certo")); err == nil {
		t.Fatal("aceitou apresentação com token errado")
	}
}

func TestQuadroCorrompidoNaoPassa(t *testing.T) {
	// cabeçalho diz 2x2 (16 bytes de pixel) mas só vêm 4
	corpo := Quadro{W: 2, H: 2, TotalW: 2, TotalH: 2}.Codificar([]byte{0, 0, 0, 0})
	if _, _, err := DecodificarQuadro(corpo); err == nil {
		t.Fatal("aceitou quadro com tamanho inconsistente")
	}
	if _, _, err := DecodificarQuadro([]byte{1, 2, 3}); err == nil {
		t.Fatal("aceitou cabeçalho truncado")
	}
}

// A rede de segurança olha a MÁQUINA, não um número inventado: recusa
// quando abrir mais uma sessão deixaria o sistema sem fôlego, e deixa
// passar quando há memória. Aqui a leitura é simulada, para o teste não
// depender de quanta memória a máquina que o roda tem no momento.
func TestRecusaQuandoAMaquinaEstaSemFolego(t *testing.T) {
	originalDisponivel := disponivelMiB
	vivasMu.Lock()
	vivasAntes := vivas
	vivasMu.Unlock()
	t.Cleanup(func() {
		disponivelMiB = originalDisponivel
		vivasMu.Lock()
		vivas = vivasAntes
		vivasMu.Unlock()
	})

	custo := CustoDe("eco") // protocolo não medido: paga o preço do mais caro

	// Sobra exatamente o mínimo depois de abrir: ainda cabe.
	disponivelMiB = func() (int, bool) { return ReservaMinimaMiB + custo, true }
	p, err := Iniciar("eco")
	if err != nil {
		t.Fatalf("recusou com folga exata: %v", err)
	}
	p.Encerrar()

	// Falta 1 MiB para o mínimo: não cabe.
	disponivelMiB = func() (int, bool) { return ReservaMinimaMiB + custo - 1, true }
	_, err = Iniciar("eco")
	var semMem *ErrSemMemoria
	if !errors.As(err, &semMem) {
		t.Fatalf("recusou com %v, esperava *ErrSemMemoria", err)
	}
	if semMem.PorContagem {
		t.Fatal("recusou por contagem, devia ser por memória")
	}
	if semMem.PrecisaMiB != custo {
		t.Fatalf("erro diz precisar de %d MiB, esperava %d", semMem.PrecisaMiB, custo)
	}

	// Recusa não pode contar sessão que não subiu.
	if SessoesVivas() != vivasAntes {
		t.Fatalf("a recusa deixou %d sessões contadas, esperava %d", SessoesVivas(), vivasAntes)
	}

	// Plataforma sem como medir: FALHA ABERTA, não impede.
	disponivelMiB = func() (int, bool) { return 0, false }
	p, err = Iniciar("eco")
	if err != nil {
		t.Fatalf("sem saber medir, devia deixar passar; recusou com %v", err)
	}
	p.Encerrar()
}

// O teto POR SESSÃO precisa ter folga larga sobre o pior caso legítimo,
// senão vira queda de aba boa em vez de proteção. O pior caso medido é uma
// sessão RDP grande; este teste trava a relação entre os dois números para
// ninguém apertar o teto sem perceber.
func TestTetoPorSessaoTemFolgaSobreOCustoReal(t *testing.T) {
	maisCaro := CustoDe("rdp")
	if LimiteSessaoMiB < 4*maisCaro {
		t.Fatalf("teto por sessão (%d MiB) tem menos de 4x o custo estimado do RDP (%d MiB) —"+
			" com pouca folga, sessão legítima em tela grande passa a ser morta",
			LimiteSessaoMiB, maisCaro)
	}
}

// O vigia de memória precisa MATAR de verdade, não só existir. Aqui um
// filho real sobe com o teto rebaixado a 1 MiB — que qualquer processo Go
// já ultrapassa só de existir — e o teste confere que o processo principal
// vê o canal fechar sozinho, sem ninguém mandar nada.
//
// É o mesmo que a aba veria: canal fechou, mostra "CAIU", religa.
func TestVigiaDerrubaFilhoQuePassaDoTeto(t *testing.T) {
	t.Setenv(VarLimiteSessao, "1")

	// O teste leva o intervalo de uma amostra (~2s): o vigia mora no
	// FILHO, que é outro processo e usa o próprio padrão — mexer na
	// variável aqui não o alcançaria.
	p, err := Iniciar("eco")
	if err != nil {
		t.Fatalf("Iniciar: %v", err)
	}
	defer p.Encerrar()

	fim := make(chan error, 1)
	go func() {
		for {
			if _, _, err := p.Ler(); err != nil {
				fim <- err
				return
			}
		}
	}()
	select {
	case <-fim:
		// O filho saiu sozinho: é exatamente o que se espera.
	case <-time.After(20 * time.Second):
		t.Fatal("o filho passou do teto e continuou vivo — o vigia não está derrubando nada")
	}
}
