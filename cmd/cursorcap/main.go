//go:build linux

// cursorcap captura MÁSCARAS DE CURSOR de uma sessão remota de verdade
// (RDP ou VNC), para virarem caso de teste de cursorforma.go.
//
// ----------------------------------------------------------------------
// POR QUE EXISTE
// ----------------------------------------------------------------------
//
// As silhuetas de cursorforma_test.go são desenhadas à mão: '#' vira 255,
// o resto vira 0. As PROPORÇÕES são dos cursores reais, mas os pixels são
// inventados — e é aí que mora o buraco. O cursor do Windows moderno vem
// em 32bpp com borda suavizada e, em alguns temas, SOMBRA: uma mancha de
// alfa baixo bem maior que o desenho. É por causa dela que existe o corte
// `alfaOpaco = 128` em cursorforma.go, e é justamente esse corte que arte
// 0/255 NUNCA exercita. O limiar mais delicado do classificador é o único
// que teste nenhum encosta.
//
// Sem amostra de verdade os limiares seguem calibrados por proporção, não
// por medida.
//
// ----------------------------------------------------------------------
// O QUE FAZ
// ----------------------------------------------------------------------
//
// Conecta, passeia o ponteiro por uma grade sobre a área remota e guarda
// cada máscara DISTINTA que o servidor mandar. Grava em PGM (P5, 1 byte
// por pixel — exatamente o que o callback entrega), com o ponto quente
// num comentário do cabeçalho. PGM porque preserva o byte exato, abre em
// qualquer visualizador de imagem e se lê em vinte linhas de Go, sem
// dependência nenhuma.
//
// NÃO CLICA EM NADA, de propósito: só move o ponteiro. A máquina do outro
// lado é de alguém e clique às cegas abre, fecha ou apaga coisa.
//
// ----------------------------------------------------------------------
// COMO NÃO CONGELAR A SESSÃO
// ----------------------------------------------------------------------
//
// Duas regras, as duas saindo de ONDE o callback roda:
//
//   - OnCursor é chamado de dentro do processamento do protocolo, na
//     goroutine da bomba (ver goRdpAoCursor em internal/rdp/rdp.go e
//     goAoCursor em internal/vnc/vnc.go). I/O ali dentro segura o
//     protocolo inteiro, e a sessão trava de verdade. Por isso o callback
//     aqui só copia para memória — gravar em disco é no fim, com a bomba
//     já parada.
//   - mover o ponteiro pega o RLock da sessão e entra na biblioteca C.
//     Chamado de dentro do callback, seria reentrar na lib no meio do
//     processamento dela. Por isso o passeio mora na goroutine principal,
//     e a bomba sozinha na dela.
//
// E ritmo: cada parada espera a resposta do servidor. Enxurrada de
// movimento não acelera nada, porque a forma nova só chega no quadro
// seguinte — só aumenta a chance de encher a fila de entrada.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// amostra é uma forma de cursor distinta, do jeito que chegou.
type amostra struct {
	xhot, yhot int
	w, h       int
	mask       []byte
	vezes      int // quantas vezes o servidor repetiu esta forma
	ordem      int // ordem de aparição, só para nomear em sequência
	onde       image.Point
}

// coletor junta as formas distintas. registrar é chamado da goroutine da
// bomba: rápido, sem I/O, sem alocação grande.
type coletor struct {
	mu   sync.Mutex
	por  map[string]*amostra
	seq  int
	nulo int // vezes que o servidor pediu "volta pro cursor padrão"

	// ondeEstou é o último ponto pedido pelo passeio. Serve para anotar
	// em que canto da tela a forma apareceu — ajuda a saber depois que
	// uma amostra veio da borda de janela, do menu, do campo de texto.
	ondeEstou image.Point
}

func novoColetor() *coletor { return &coletor{por: map[string]*amostra{}} }

func (c *coletor) marcarPonto(p image.Point) {
	c.mu.Lock()
	c.ondeEstou = p
	c.mu.Unlock()
}

func (c *coletor) registrar(xhot, yhot, w, h int, mask []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if mask == nil || w <= 0 || h <= 0 {
		c.nulo++
		return
	}
	// A chave inclui geometria e ponto quente: duas máscaras iguais com
	// hotspot diferente SÃO cursores diferentes para o classificador, que
	// usa o hotspot como sinal principal de família.
	soma := sha256.New()
	fmt.Fprintf(soma, "%d|%d|%d|%d|", w, h, xhot, yhot)
	soma.Write(mask)
	chave := hex.EncodeToString(soma.Sum(nil))[:12]

	if a, ok := c.por[chave]; ok {
		a.vezes++
		return
	}
	c.seq++
	c.por[chave] = &amostra{
		xhot: xhot, yhot: yhot, w: w, h: h, mask: mask,
		vezes: 1, ordem: c.seq, onde: c.ondeEstou,
	}
}

func (c *coletor) instantaneo() ([]*amostra, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fora := make([]*amostra, 0, len(c.por))
	for _, a := range c.por {
		fora = append(fora, a)
	}
	sort.Slice(fora, func(i, j int) bool { return fora[i].ordem < fora[j].ordem })
	return fora, c.nulo
}

func (c *coletor) quantas() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.por)
}

func main() {
	proto := flag.String("proto", "rdp", "protocolo: rdp ou vnc")
	host := flag.String("host", "", "endereço do servidor")
	porta := flag.Int("porta", 0, "porta (padrão: 3389 no rdp, 5900 no vnc)")
	usuario := flag.String("usuario", "", "usuário (pode ser vazio no vnc clássico)")
	dominio := flag.String("dominio", "", "domínio (só rdp, opcional)")
	saida := flag.String("saida", "", "diretório onde gravar as máscaras")
	passo := flag.Int("passo", 80, "distância entre paradas da grade, em pixels")
	// Passo por eixo, porque borda de janela tem 4 a 8 pixels de largura e
	// grade quadrada grossa passa por cima sem encostar. Com -passo-x fino
	// e -passo-y grosso, cada linha horizontal cruza TODA borda vertical da
	// tela — que é onde moram os cursores de redimensionar. Trocando os
	// dois, o mesmo para as bordas horizontais.
	passoX := flag.Int("passo-x", 0, "passo no eixo x (0 = usa -passo)")
	passoY := flag.Int("passo-y", 0, "passo no eixo y (0 = usa -passo)")
	// Recorte, porque varrer a tela inteira fino custa minutos e a maior
	// parte dela é área vazia que só devolve a seta. Cursor de campo de
	// texto, por exemplo, mora numa faixa de ~20 pixels de altura: achar
	// pede densidade em cima DELA, não da tela.
	regiao := flag.String("regiao", "", "recorte `x0,y0,x1,y1` (vazio = tela inteira)")
	pausa := flag.Duration("pausa", 180*time.Millisecond, "espera em cada parada")
	limite := flag.Duration("limite", 5*time.Minute, "tempo máximo total")
	certMudouOK := flag.Bool("aceitar-cert-mudado", false,
		"aceitar certificado que MUDOU (o padrão é recusar e sair)")
	flag.Parse()

	// A senha vem do ambiente, não da linha de comando: argumento aparece
	// em `ps` para qualquer usuário da máquina e no histórico do shell.
	senha := os.Getenv("REMOTO_SENHA")
	if *host == "" || senha == "" || *saida == "" {
		fmt.Fprintln(os.Stderr,
			"uso: REMOTO_SENHA=... cursorcap -proto rdp|vnc -host H [-usuario U] -saida DIR")
		os.Exit(2)
	}
	if *porta == 0 {
		if *proto == "vnc" {
			*porta = 5900
		} else {
			*porta = 3389
		}
	}
	if err := os.MkdirAll(*saida, 0o755); err != nil {
		morrer("criando %s: %v", *saida, err)
	}

	desconectou := make(chan string, 1)
	var r remoto
	switch *proto {
	case "rdp":
		r = novoRDP(*certMudouOK, desconectou)
	case "vnc":
		r = novoVNC()
	default:
		morrer("protocolo desconhecido: %q (use rdp ou vnc)", *proto)
	}

	col := novoColetor()
	r.aoCursor(col.registrar)
	r.credenciais(*usuario, senha, *dominio)

	fmt.Fprintf(os.Stderr, ">> conectando em %s://%s:%d\n", *proto, *host, *porta)
	if err := r.conectar(*host, *porta); err != nil {
		morrer("conectando: %v", err)
	}
	defer r.fechar()

	// A bomba sozinha na goroutine dela. Nada mais entra na lib C por
	// aqui — ver o cabeçalho.
	parar := make(chan struct{})
	fimBomba := make(chan error, 1)
	go func() { fimBomba <- r.bombear(parar) }()

	larg, alt, ok := esperarTela(r, 15*time.Second)
	if !ok {
		close(parar)
		morrer("a sessão não entregou nenhum quadro em 15s")
	}
	fmt.Fprintf(os.Stderr, ">> área remota: %dx%d\n", larg, alt)

	if *passoX == 0 {
		*passoX = *passo
	}
	if *passoY == 0 {
		*passoY = *passo
	}
	area, err := lerRegiao(*regiao, larg, alt)
	if err != nil {
		close(parar)
		morrer("-regiao: %v", err)
	}
	if area != image.Rect(0, 0, larg, alt) {
		fmt.Fprintf(os.Stderr, ">> recorte: %d,%d até %d,%d\n",
			area.Min.X, area.Min.Y, area.Max.X, area.Max.Y)
	}

	prazo := time.After(*limite)
	interrompido := passear(r, col, area, *passoX, *passoY, *pausa, prazo, fimBomba, desconectou)

	// A foto da tela sai ANTES de parar a bomba, que é enquanto o quadro
	// ainda está vivo: sem ela não dá para saber depois sobre o que o
	// ponteiro estava passando quando cada forma apareceu.
	erroTela := gravarTela(r, filepath.Join(*saida, "tela.png"))

	// Só agora, com a bomba parada, é que se grava o resto em disco.
	close(parar)
	select {
	case <-fimBomba:
	case <-time.After(2 * time.Second):
	}

	formas, nulos := col.instantaneo()
	fmt.Fprintf(os.Stderr, "\n>> %d formas distintas, %d pedidos de cursor padrão\n",
		len(formas), nulos)
	if interrompido != "" {
		fmt.Fprintf(os.Stderr, ">> passeio interrompido: %s\n", interrompido)
	}
	if erroTela != nil {
		fmt.Fprintf(os.Stderr, "   (sem foto da tela: %v)\n", erroTela)
	}

	for _, a := range formas {
		nome := fmt.Sprintf("%02d-%dx%d-hot%dx%d.pgm", a.ordem, a.w, a.h, a.xhot, a.yhot)
		if err := gravarPGM(filepath.Join(*saida, nome), a, *proto, *host); err != nil {
			fmt.Fprintf(os.Stderr, "   !! %s: %v\n", nome, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "   %-28s  %3dx%-3d hot=(%d,%d)  %dx  alfa: %s\n",
			nome, a.w, a.h, a.xhot, a.yhot, a.vezes, perfilAlfa(a.mask))
	}
}

// esperarTela espera o primeiro quadro com geometria. Antes dele não há
// área para passear.
func esperarTela(r remoto, limite time.Duration) (w, h int, ok bool) {
	fim := time.Now().Add(limite)
	for time.Now().Before(fim) {
		if _, lw, lh, _ := r.quadro(); lw > 0 && lh > 0 {
			return lw, lh, true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return 0, 0, false
}

// passear varre a área remota em serpentina. Serpentina e não varredura da
// esquerda toda vez porque o que faz o servidor mandar forma nova é a
// TRANSIÇÃO entre elementos; indo e voltando, cada linha entra na seguinte
// pela borda mais próxima e cruza mais bordas de janela por segundo de
// sessão.
//
// Devolve o motivo de ter parado antes da hora, ou "" se varreu tudo.
func passear(r remoto, col *coletor, area image.Rectangle, passoX, passoY int,
	pausa time.Duration, prazo <-chan time.Time,
	fimBomba <-chan error, desconectou <-chan string) string {

	if passoX < 1 {
		passoX = 1
	}
	if passoY < 1 {
		passoY = 1
	}
	totalLinhas := (area.Dy() + passoY - 1) / passoY
	linhas := 0
	for y, desc := area.Min.Y, false; y < area.Max.Y; y, desc = y+passoY, !desc {
		xs := make([]int, 0, area.Dx()/passoX+1)
		for x := area.Min.X; x < area.Max.X; x += passoX {
			xs = append(xs, x)
		}
		if desc {
			for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
				xs[i], xs[j] = xs[j], xs[i]
			}
		}
		for _, x := range xs {
			select {
			case <-prazo:
				return "tempo limite"
			case err := <-fimBomba:
				return fmt.Sprintf("a sessão caiu (%v)", err)
			case motivo := <-desconectou:
				return "o servidor desconectou: " + motivo
			default:
			}
			col.marcarPonto(image.Pt(x, y))
			r.moverPonteiro(x, y)
			time.Sleep(pausa)
		}
		linhas++
		fmt.Fprintf(os.Stderr, "\r   linha %d/%d — %d formas até aqui   ",
			linhas, totalLinhas, col.quantas())
	}
	return ""
}

// lerRegiao converte "x0,y0,x1,y1" no recorte, já cortado pela tela. Vazio
// devolve a tela inteira. Recorte que não sobrepõe a tela é erro, e não um
// passeio de zero pontos que terminaria "sem formas" sem explicar por quê.
func lerRegiao(s string, larg, alt int) (image.Rectangle, error) {
	tela := image.Rect(0, 0, larg, alt)
	if s == "" {
		return tela, nil
	}
	partes := strings.Split(s, ",")
	if len(partes) != 4 {
		return tela, fmt.Errorf("esperado x0,y0,x1,y1, veio %q", s)
	}
	var v [4]int
	for i, p := range partes {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return tela, fmt.Errorf("campo %d (%q) não é número", i+1, p)
		}
		v[i] = n
	}
	area := image.Rect(v[0], v[1], v[2], v[3]).Canon().Intersect(tela)
	if area.Empty() {
		return tela, fmt.Errorf("recorte %q não encosta na tela de %dx%d", s, larg, alt)
	}
	return area, nil
}

// perfilAlfa resume a distribuição de alfa da máscara. É o número que
// justifica esta ferramenta: se vier só "0/255", o servidor manda máscara
// dura e a arte ASCII do teste de hoje já representava bem. Se aparecer
// gente no meio, a borda suavizada (e a sombra) existem de verdade, e o
// corte alfaOpaco=128 passa a ter o que separar.
func perfilAlfa(m []byte) string {
	var zero, meio, cheio int
	for _, v := range m {
		switch {
		case v == 0:
			zero++
		case v == 255:
			cheio++
		default:
			meio++
		}
	}
	if meio == 0 {
		return "0/255 (duro)"
	}
	return fmt.Sprintf("%d nulos, %d parciais, %d cheios", zero, meio, cheio)
}

// gravarPGM escreve a máscara como PGM binário (P5). O ponto quente vai em
// comentário porque o formato não tem campo para ele — e é dado que não
// pode se perder: é o sinal mais forte do classificador.
func gravarPGM(caminho string, a *amostra, proto, host string) error {
	var cab strings.Builder
	cab.WriteString("P5\n")
	fmt.Fprintf(&cab, "# hot %d %d\n", a.xhot, a.yhot)
	fmt.Fprintf(&cab, "# origem %s %s\n", proto, host)
	fmt.Fprintf(&cab, "# ponteiro em %d,%d quando apareceu; repetiu %dx\n",
		a.onde.X, a.onde.Y, a.vezes)
	fmt.Fprintf(&cab, "%d %d\n255\n", a.w, a.h)

	f, err := os.Create(caminho)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(cab.String()); err != nil {
		return err
	}
	_, err = f.Write(a.mask)
	return err
}

// gravarTela salva o último quadro. O framebuffer é BGRX de 32 bits e o
// stride pode ser maior que w*4 (no RDP o gdi alinha a linha), então cada
// linha é lida pelo stride, nunca por w*4 — ver o comentário de
// Framebuffer em internal/rdp/rdp.go.
func gravarTela(r remoto, caminho string) error {
	buf, w, h, stride := r.quadro()
	if buf == nil || w == 0 || h == 0 {
		return fmt.Errorf("sem quadro")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		linha := buf[y*stride : y*stride+w*4]
		for x := 0; x < w; x++ {
			p := linha[x*4 : x*4+4]
			d := img.Pix[y*img.Stride+x*4 : y*img.Stride+x*4+4]
			d[0], d[1], d[2], d[3] = p[2], p[1], p[0], 255 // BGRX -> RGBA
		}
	}
	f, err := os.Create(caminho)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func morrer(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "cursorcap: "+f+"\n", a...)
	os.Exit(1)
}
