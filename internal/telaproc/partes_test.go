package telaproc

import (
	"bytes"
	"net"
	"testing"
)

// Mandar o corpo em PEDAÇOS tem de sair no fio byte a byte igual a mandar
// o corpo inteiro. É a invariante que permite EnviarQuadro não copiar a
// tela só para pôr 24 bytes de cabeçalho na frente dela.
func TestEnviarEmPartesSaiIgualAoCorpoInteiro(t *testing.T) {
	q := Quadro{X: 3, Y: 2, W: 2, H: 1, TotalW: 8, TotalH: 4}
	pix := []byte{1, 2, 3, 4, 5, 6, 7, 8}

	emPartes := naFita(t, func(c *Conn) error { return c.EnviarQuadro(q, pix) })
	inteiro := naFita(t, func(c *Conn) error { return c.Enviar(EvtQuadro, q.Codificar(pix)) })

	if !bytes.Equal(emPartes, inteiro) {
		t.Fatalf("os bytes divergiram:\nem partes: % x\ninteiro:   % x", emPartes, inteiro)
	}
}

// Corpo vazio e pedaço vazio no meio não podem mudar o enquadramento: o
// tamanho anunciado no cabeçalho é a SOMA, e um pedaço de zero bytes só
// não escreve nada.
func TestEnviarPartesComPedacoVazio(t *testing.T) {
	comVazio := naFita(t, func(c *Conn) error {
		return c.enviarPartes(EvtQuadro, []byte{9, 9}, nil, []byte{7})
	})
	inteiro := naFita(t, func(c *Conn) error {
		return c.Enviar(EvtQuadro, []byte{9, 9, 7})
	})
	if !bytes.Equal(comVazio, inteiro) {
		t.Fatalf("com pedaço vazio: % x, inteiro: % x", comVazio, inteiro)
	}
}

// naFita roda um envio contra um socket de mentira e devolve tudo o que
// foi para o fio.
func naFita(t *testing.T, enviar func(*Conn) error) []byte {
	t.Helper()
	meu, seu := net.Pipe()
	lido := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		b := make([]byte, 4096)
		for {
			n, err := seu.Read(b)
			buf.Write(b[:n])
			if err != nil {
				break
			}
		}
		lido <- buf.Bytes()
	}()

	c := novaConn(meu)
	if err := enviar(c); err != nil {
		t.Fatalf("enviar: %v", err)
	}
	_ = meu.Close()
	return <-lido
}
