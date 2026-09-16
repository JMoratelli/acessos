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

// O teto existe para o app não virar um problema de memória da máquina
// inteira (ver MaxSessoes). Aqui ele é exercitado mexendo no contador, e
// não subindo MaxSessoes processos de verdade: o que precisa estar certo é
// a contabilidade — recusar no limite, não vazar a reserva na recusa, e
// devolver a vaga quando a sessão encerra.
func TestTetoDeSessoes(t *testing.T) {
	antes := SessoesVivas()
	t.Cleanup(func() { vivas.Store(int32(antes)) })

	vivas.Store(int32(MaxSessoes))
	if _, err := Iniciar("eco"); err == nil {
		t.Fatal("Iniciar passou por cima do teto")
	} else if !errors.Is(err, ErrLotado) {
		t.Fatalf("recusou com %v, esperava ErrLotado", err)
	}
	if v := SessoesVivas(); v != MaxSessoes {
		t.Fatalf("a recusa deixou o contador em %d, esperava %d", v, MaxSessoes)
	}

	// Com uma vaga livre, entra — e devolve a vaga ao encerrar.
	vivas.Store(int32(MaxSessoes - 1))
	p, err := Iniciar("eco")
	if err != nil {
		t.Fatalf("Iniciar com vaga livre: %v", err)
	}
	if v := SessoesVivas(); v != MaxSessoes {
		t.Fatalf("contador ficou em %d depois de subir uma sessão", v)
	}
	p.Encerrar()
	if v := SessoesVivas(); v != MaxSessoes-1 {
		t.Fatalf("Encerrar deixou o contador em %d, esperava %d", v, MaxSessoes-1)
	}
	// Encerrar duas vezes não pode descontar duas vagas.
	p.Encerrar()
	if v := SessoesVivas(); v != MaxSessoes-1 {
		t.Fatalf("Encerrar repetido vazou vaga: contador em %d", v)
	}
}
