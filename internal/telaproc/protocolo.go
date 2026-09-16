// Package telaproc carrega o protocolo entre o processo principal do app e
// o processo-filho que hospeda UMA sessão de tela remota (RDP hoje, VNC
// quando for portado).
//
// Por que um processo por sessão: libfreerdp3 e libvncclient são C rodando
// no MESMO heap que a interface. Um segmentation fault lá dentro — e existe
// pelo menos um reproduzido, na desconexão abrupta de RDP (ver BACKLOG.md
// §6) — mata o app inteiro, com todas as outras abas junto. Em processo
// separado, o mesmo crash mata só aquele filho: o processo principal vê o
// socket fechar, marca a aba como caída e o backoff de reconexão que já
// existia religa a sessão. Quem está usando vê uma reconexão, não a perda
// do trabalho nas outras abas.
//
// O transporte é um socket TCP em 127.0.0.1 com token, e não stdin/stdout,
// por dois motivos concretos:
//
//   - a libfreerdp escreve no stdout/stderr do processo (WLog), e no
//     Windows o próprio app redireciona os HANDLES padrão para o arquivo de
//     log (ver log_windows.go). Qualquer byte que ela resolvesse imprimir
//     cairia no meio do fluxo binário e corromperia o protocolo;
//   - herdar descritor extra (fd 3) é trivial no POSIX e um estorvo no
//     Windows; o loopback funciona igual nos dois, inclusive dentro do
//     Flatpak (o sandbox tem loopback mesmo sem --share=network).
//
// Este pacote é Go puro de propósito: nada de cgo aqui, para que o lado
// que só decodifica mensagens compile em qualquer plataforma.
package telaproc

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Tipo identifica a mensagem. Comandos vão do processo principal para o
// filho; eventos, do filho para o principal.
type Tipo byte

const (
	_ Tipo = iota

	// ---- processo principal -> filho ----

	CmdConectar      // JSON Ligacao
	CmdPonteiroMover // x, y (int32)
	CmdPonteiroBotao // x, y, botão (int32) + pressionado (1 byte)
	CmdPonteiroRoda  // eixo, passos (int32)
	CmdTecla         // keycode (uint32) + pressionada (1 byte)
	CmdClipboard     // texto UTF-8 cru
	CmdResize        // w, h (int32)
	CmdCredito       // vazio: libera o filho a mandar mais UM quadro
	CmdCertResposta  // decisão sobre o certificado (1 byte)

	// ---- filho -> processo principal ----

	EvtOla           // token de autenticação, cru (primeira mensagem)
	EvtConectado     // vazio
	EvtFalha         // JSON Falha
	EvtQuadro        // cabeçalho binário + pixels NRGBA (ver Quadro)
	EvtDesconectado  // motivo, texto cru
	EvtClipboard     // texto UTF-8 cru
	EvtCursor        // pointer.Cursor já classificado (uint32)
	EvtCertPedido    // JSON Certificado
	EvtDisplayPronto // vazio
)

// Ligacao são os parâmetros de conexão (CmdConectar).
type Ligacao struct {
	Host    string `json:"host"`
	Porta   int    `json:"porta"`
	Usuario string `json:"usuario"`
	Senha   string `json:"senha"`
	Dominio string `json:"dominio"`
}

// Falha diz por que a conexão não subiu (EvtFalha).
type Falha struct {
	Mensagem   string `json:"mensagem"`
	AuthFalhou bool   `json:"auth_falhou"`
}

// Certificado espelha rdp.Certificado sem depender do pacote rdp, que é
// cgo e só existe em Linux e Windows. Quem recebe converte.
type Certificado struct {
	Host            string `json:"host"`
	Porta           int    `json:"porta"`
	NomeComum       string `json:"nome_comum"`
	Assunto         string `json:"assunto"`
	Emissor         string `json:"emissor"`
	Digital         string `json:"digital"`
	DigitalAnterior string `json:"digital_anterior"`
	Mudou           bool   `json:"mudou"`
}

// Quadro é o cabeçalho de EvtQuadro: um RETÂNGULO da tela remota, não a
// tela toda. O filho acumula os retângulos sujos que a biblioteca reporta
// e manda só a caixa que os envolve — é o que faz valer a pena passar
// pixels por socket em vez de compartilhar memória.
//
// Os pixels vêm em NRGBA (o formato que o Gio consome direto), já
// convertidos do BGRX da biblioteca: a conversão é um laço por pixel, e
// ela roda no filho justamente para sair da thread que desenha.
type Quadro struct {
	X, Y, W, H     int32 // retângulo sujo, em pixels da tela remota
	TotalW, TotalH int32 // tamanho atual da tela remota inteira
}

// tamCabQuadro são os 6 int32 do cabeçalho.
const tamCabQuadro = 24

// Codificar escreve o cabeçalho na frente de pix e devolve o corpo pronto.
func (q Quadro) Codificar(pix []byte) []byte {
	buf := make([]byte, tamCabQuadro+len(pix))
	le := binary.LittleEndian
	le.PutUint32(buf[0:], uint32(q.X))
	le.PutUint32(buf[4:], uint32(q.Y))
	le.PutUint32(buf[8:], uint32(q.W))
	le.PutUint32(buf[12:], uint32(q.H))
	le.PutUint32(buf[16:], uint32(q.TotalW))
	le.PutUint32(buf[20:], uint32(q.TotalH))
	copy(buf[tamCabQuadro:], pix)
	return buf
}

// DecodificarQuadro separa cabeçalho e pixels. Os pixels apontam PARA
// DENTRO de corpo — não guarde a fatia além do tratamento da mensagem.
func DecodificarQuadro(corpo []byte) (Quadro, []byte, error) {
	if len(corpo) < tamCabQuadro {
		return Quadro{}, nil, fmt.Errorf("quadro truncado (%d bytes)", len(corpo))
	}
	le := binary.LittleEndian
	q := Quadro{
		X:      int32(le.Uint32(corpo[0:])),
		Y:      int32(le.Uint32(corpo[4:])),
		W:      int32(le.Uint32(corpo[8:])),
		H:      int32(le.Uint32(corpo[12:])),
		TotalW: int32(le.Uint32(corpo[16:])),
		TotalH: int32(le.Uint32(corpo[20:])),
	}
	pix := corpo[tamCabQuadro:]
	if q.W < 0 || q.H < 0 || int(q.W)*int(q.H)*4 != len(pix) {
		return Quadro{}, nil, fmt.Errorf("quadro %dx%d não bate com %d bytes", q.W, q.H, len(pix))
	}
	return q, pix, nil
}

// ---------------------------------------------------------------- Conn

// prazoEscrita é o teto para uma mensagem sair. O socket é local e o outro
// lado lê sem parar, então estourar isto não significa "lento": significa
// filho travado (dentro da biblioteca C, por exemplo). Quebrar a conexão
// nesse caso é o certo — é o que faz a aba cair e religar em vez de a
// INTERFACE congelar esperando, já que entrada de teclado e ponteiro é
// escrita daqui mesmo, da thread que desenha.
const prazoEscrita = 5 * time.Second

// tamMax é o teto de uma mensagem. O maior corpo possível é um quadro de
// tela inteira: 4K em 32bpp dá ~34 MB, então 64 MB dá folga sem permitir
// que um cabeçalho corrompido peça uma alocação absurda.
const tamMax = 64 << 20

// Conn é uma ponta do canal. É segura para escrever de várias goroutines;
// a LEITURA é de uma só (o corpo devolvido é reaproveitado entre chamadas).
type Conn struct {
	c net.Conn
	r *bufio.Reader

	escMu   sync.Mutex
	w       *bufio.Writer
	quebrou bool // uma escrita falhou: o fluxo não é mais confiável

	buf []byte // reaproveitado pela leitura
}

func novaConn(c net.Conn) *Conn {
	return &Conn{
		c: c,
		// Buffers generosos: o caminho quente é um quadro de alguns MB, e
		// o padrão de 4 KiB picotaria cada um em milhares de write().
		r: bufio.NewReaderSize(c, 256<<10),
		w: bufio.NewWriterSize(c, 256<<10),
	}
}

// Enviar manda uma mensagem inteira. corpo pode ser nil.
func (c *Conn) Enviar(t Tipo, corpo []byte) error {
	if len(corpo) > tamMax {
		return fmt.Errorf("mensagem %d grande demais (%d bytes)", t, len(corpo))
	}
	var cab [5]byte
	cab[0] = byte(t)
	binary.LittleEndian.PutUint32(cab[1:], uint32(len(corpo)))

	c.escMu.Lock()
	defer c.escMu.Unlock()
	if c.quebrou {
		return errQuebrado
	}
	// Uma escrita que falha no meio deixa MEIA mensagem no fluxo, e daí em
	// diante o outro lado lê lixo alinhado errado. Por isso qualquer erro
	// aqui derruba a conexão inteira em vez de ser devolvido e esquecido
	// pelo chamador: a sessão cai e religa, que é um estado que o app
	// inteiro já sabe tratar.
	if err := c.escrever(cab[:], corpo); err != nil {
		c.quebrou = true
		_ = c.c.Close()
		return err
	}
	return nil
}

var errQuebrado = errors.New("canal da sessão já foi rompido")

func (c *Conn) escrever(cab, corpo []byte) error {
	if err := c.c.SetWriteDeadline(time.Now().Add(prazoEscrita)); err != nil {
		return err
	}
	defer func() { _ = c.c.SetWriteDeadline(time.Time{}) }()
	if _, err := c.w.Write(cab); err != nil {
		return err
	}
	if len(corpo) > 0 {
		if _, err := c.w.Write(corpo); err != nil {
			return err
		}
	}
	return c.w.Flush()
}

// EnviarJSON é o atalho para as mensagens estruturadas (as raras).
func (c *Conn) EnviarJSON(t Tipo, v any) error {
	corpo, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Enviar(t, corpo)
}

// Ler devolve a próxima mensagem. O corpo é válido só até a chamada
// seguinte — copie o que precisar guardar.
func (c *Conn) Ler() (Tipo, []byte, error) {
	var cab [5]byte
	if _, err := io.ReadFull(c.r, cab[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.LittleEndian.Uint32(cab[1:]))
	if n > tamMax {
		return 0, nil, fmt.Errorf("mensagem anuncia %d bytes, acima do teto", n)
	}
	if cap(c.buf) < n {
		c.buf = make([]byte, n)
	}
	corpo := c.buf[:n]
	if n > 0 {
		if _, err := io.ReadFull(c.r, corpo); err != nil {
			return 0, nil, err
		}
	}
	return Tipo(cab[0]), corpo, nil
}

// Fechar derruba o canal. Quem estiver em Ler() sai com erro — é assim que
// os dois lados são acordados para encerrar.
func (c *Conn) Fechar() error { return c.c.Close() }

// ------------------------------------------------- pequenos empacotadores

func i32(vs ...int) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(int32(v)))
	}
	return b
}

func lerI32(corpo []byte, n int) ([]int, bool) {
	if len(corpo) < 4*n {
		return nil, false
	}
	vs := make([]int, n)
	for i := range vs {
		vs[i] = int(int32(binary.LittleEndian.Uint32(corpo[4*i:])))
	}
	return vs, true
}

func bool1(b bool) byte {
	if b {
		return 1
	}
	return 0
}
