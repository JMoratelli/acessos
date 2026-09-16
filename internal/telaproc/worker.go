package telaproc

import (
	"encoding/binary"
	"fmt"
	"net"
)

// Atender é o outro lado de Iniciar: roda DENTRO do processo-filho, liga de
// volta no endereço que veio por argumento e se apresenta com o token.
func Atender(endereco, token string) (*Conn, error) {
	c, err := net.DialTimeout("tcp", endereco, prazoHandshake)
	if err != nil {
		return nil, fmt.Errorf("não consegui ligar de volta em %s: %w", endereco, err)
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	conn := novaConn(c)
	if err := conn.Enviar(EvtOla, []byte(token)); err != nil {
		c.Close()
		return nil, fmt.Errorf("não consegui me apresentar: %w", err)
	}
	return conn, nil
}

// ------------------------------------------- leitura dos comandos, tipada
//
// Cada helper devolve ok=false quando o corpo não bate com o esperado, para
// o filho poder ignorar a mensagem em vez de confiar em bytes estragados.

func LerPonteiroMover(corpo []byte) (x, y int, ok bool) {
	v, ok := lerI32(corpo, 2)
	if !ok {
		return 0, 0, false
	}
	return v[0], v[1], true
}

func LerPonteiroBotao(corpo []byte) (x, y, botao int, pressionado, ok bool) {
	v, bom := lerI32(corpo, 3)
	if !bom || len(corpo) < 13 {
		return 0, 0, 0, false, false
	}
	return v[0], v[1], v[2], corpo[12] != 0, true
}

func LerPonteiroRoda(corpo []byte) (eixo, passos int, ok bool) {
	v, ok := lerI32(corpo, 2)
	if !ok {
		return 0, 0, false
	}
	return v[0], v[1], true
}

func LerTecla(corpo []byte) (keycode uint32, pressionada, ok bool) {
	v, bom := lerI32(corpo, 1)
	if !bom || len(corpo) < 5 {
		return 0, false, false
	}
	return uint32(v[0]), corpo[4] != 0, true
}

func LerResize(corpo []byte) (w, h int, ok bool) {
	v, ok := lerI32(corpo, 2)
	if !ok {
		return 0, 0, false
	}
	return v[0], v[1], true
}

// ------------------------------------------------------- eventos tipados

// EnviarQuadro manda um retângulo da tela. pix é NRGBA empacotado, W*H*4.
func (c *Conn) EnviarQuadro(q Quadro, pix []byte) error {
	return c.Enviar(EvtQuadro, q.Codificar(pix))
}

// EnviarCursor manda o cursor LOCAL já escolhido pelo filho (o valor de
// pointer.Cursor do Gio). Classificar no filho evita mandar a máscara do
// bitmap por socket a cada troca de cursor — o processo principal só
// precisa do resultado.
func (c *Conn) EnviarCursor(cursor uint32) error {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, cursor)
	return c.Enviar(EvtCursor, b)
}

func LerCursor(corpo []byte) (uint32, bool) {
	if len(corpo) < 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(corpo), true
}
