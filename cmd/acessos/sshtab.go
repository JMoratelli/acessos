package main

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"acessos-go/internal/hostkey"
	"acessos-go/internal/massa/executor"
	"acessos-go/internal/massa/model"

	"github.com/hinshun/vt10x"
	"golang.org/x/crypto/ssh"
)

// spTerminal: o terminal usa a Plex Mono embutida, um ponto acima do corpo
// — texto de shell é para ser lido por muito tempo seguido.
const spTerminal = unit.Sp(13)

// sshTab é o terminal embutido: sessão SSH + emulador VT (vt10x) + uma
// grade desenhada célula a célula. O vt10x cuida do parsing ANSI (cores,
// cursor, rolagem); daqui pra frente o trabalho é só teclado -> bytes e
// células -> pixels.
//
// Limites assumidos nesta versão, de propósito: sem histórico de rolagem
// (o que saiu da tela saiu), sem relatório de mouse e sem seleção com o
// mouse. Shell, logs e edição em tela cheia funcionam.
type sshTab struct {
	th     *material.Theme
	w      *app.Window
	titulo string
	host   string
	porta  int
	user   string
	senha  string

	term vt10x.Terminal

	mu      sync.Mutex
	entrada io.WriteCloser // stdin da sessão remota
	sess    *ssh.Session
	cli     *ssh.Client
	estado  string // mensagem mostrada enquanto não há sessão viva
	fechado bool

	cols, rows int
	mods       modificadores
	foco       widget.Clickable

	// painel lateral de snippets
	painelSnips bool
	snips       []model.Snippet
	btnSnips    []widget.Clickable
	listaSnips  widget.List
	filtroSnip  widget.Editor

	// corpo da fonte (Ctrl+roda) e último texto copiado no sistema, para
	// o Ctrl+Shift+V ter o que colar.
	corpo     float32
	clipLocal string

	religar     chan struct{}
	auto        atomic.Bool
	nomeConexao string
	btnRec      widget.Clickable
	btnAuto     widget.Clickable
	btnSnip     widget.Clickable
}

func newSSHTab(w *app.Window, spec map[string]string) (Tab, error) {
	host := spec["host"]
	if host == "" {
		return nil, fmt.Errorf("ssh: falta host")
	}
	porta := 22
	if p, err := strconv.Atoi(spec["port"]); err == nil && p > 0 {
		porta = p
	}
	if spec["user"] == "" {
		return nil, fmt.Errorf("ssh: falta usuário")
	}

	t := &sshTab{
		th:      temaApp,
		w:       w,
		titulo:  rotuloAba(spec, "SSH", host),
		host:    host,
		porta:   porta,
		user:    spec["user"],
		senha:   spec["pass"],
		cols:    80,
		rows:    24,
		estado:  "conectando…",
		religar: make(chan struct{}, 1),
	}
	t.auto.Store(true)
	t.nomeConexao = spec["rotulo"]
	if spec["auto"] == "0" {
		t.auto.Store(false)
	}
	t.term = vt10x.New(vt10x.WithSize(t.cols, t.rows), vt10x.WithWriter(escritorEntrada{t}))
	go t.laco()
	return t, nil
}

// escritorEntrada deixa o próprio vt10x responder sequências que pedem
// resposta (consulta de posição do cursor, por exemplo) mandando os bytes
// de volta pelo stdin da sessão.
type escritorEntrada struct{ t *sshTab }

func (e escritorEntrada) Write(p []byte) (int, error) {
	e.t.enviar(p)
	return len(p), nil
}

func (t *sshTab) Title() string { return t.titulo }
func (t *sshTab) SoIcone() bool { return false }
func (t *sshTab) Pinned() bool  { return false }
func (t *sshTab) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	return icons.ActionCode, tema.Verde, tema.VerdeFraco
}

// laco conecta e reconecta com espera crescente, igual às abas VNC/RDP:
// queda de rede não deveria exigir fechar e reabrir a aba.
func (t *sshTab) laco() {
	espera := time.Second
	for {
		if t.encerrada() {
			return
		}
		err := t.sessao()
		if t.encerrada() {
			return
		}
		msg := "sessão encerrada"
		if err != nil {
			msg = err.Error()
			reg("[%s] ssh falhou: %v", t.titulo, err)
		}
		if !t.auto.Load() {
			// reconexão automática desligada: espera o botão.
			t.setEstado(msg + " — parada (clique em Reconectar)")
			select {
			case <-t.religar:
				espera = time.Second
				continue
			}
		}
		t.setEstado(fmt.Sprintf("%s — reconectando em %s", msg, espera))
		select {
		case <-time.After(espera):
		case <-t.religar:
			espera = time.Second
			continue
		}
		if espera < 8*time.Second {
			espera *= 2
		}
	}
}

// sessao abre uma sessão e só volta quando ela morre.
func (t *sshTab) sessao() error {
	t.setEstado("conectando…")
	cfg := &ssh.ClientConfig{
		User: t.user,
		Auth: []ssh.AuthMethod{ssh.Password(t.senha)},
		// PDV antigo só oferece algoritmo que o Go corta por padrão, e o
		// aperto de mão falha com a rede perfeita. Em rede interna, com
		// alvo conhecido, aceitar o algoritmo velho custa menos que não
		// conseguir administrar a máquina (a mesma decisão do Mass SSH
		// Executer — ver internal/massa/executor.AlgoritmosLegado).
		Config: executor.AlgoritmosLegado(),
		// A identidade do servidor é conferida contra o known_hosts do
		// usuário (ver internal/hostkey). Sem isso, uma máquina no meio
		// do caminho recebe a senha de suporte inteira.
		HostKeyCallback: hostkey.Callback(),
		Timeout:         10 * time.Second,
	}
	inicio := time.Now()
	cli, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", t.host, t.porta), cfg)
	if err != nil {
		// Chave desconhecida ou trocada: a sessão PARA e pergunta. Ao
		// aceitar, tenta de novo na hora (o laço de reconexão faria isso
		// depois, mas com espera — e quem acabou de responder está
		// olhando a tela).
		if ec := erroDeChave(err); ec != nil {
			// PARA e espera a resposta. Sem isso o laço tentava de novo a
			// cada segundo e reabria o diálogo por cima do anterior.
			t.setEstado(ec.Error() + " — aguardando sua decisão")
			pedirConfiancaHostKey(t.w, ec, func() { t.Reconectar() })
			select {
			case <-t.religar:
			case <-t.paradaPorChave():
			}
			return nil
		}
		return fmt.Errorf("%w", err)
	}
	defer cli.Close()

	sess, err := cli.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	t.mu.Lock()
	cols, rows := t.cols, t.rows
	t.mu.Unlock()

	modos := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modos); err != nil {
		return err
	}
	entrada, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	saida, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	sess.Stderr = escritorTerminal{t}

	t.mu.Lock()
	if t.fechado {
		t.mu.Unlock()
		return nil
	}
	t.cli, t.sess, t.entrada = cli, sess, entrada
	t.estado = ""
	reg("[%s] ssh pronto em %s", t.titulo, time.Since(inicio).Truncate(time.Millisecond))
	t.mu.Unlock()
	t.w.Invalidate()

	if err := sess.Shell(); err != nil {
		return err
	}

	// Parse bloqueia lendo o stdout e alimenta o emulador; cada pedaço
	// lido pede um quadro novo.
	go func() {
		br := bufio.NewReader(leitorAvisado{r: saida, t: t})
		for {
			if err := t.term.Parse(br); err != nil {
				return
			}
		}
	}()

	err = sess.Wait()

	t.mu.Lock()
	t.cli, t.sess, t.entrada = nil, nil, nil
	t.mu.Unlock()
	t.w.Invalidate()
	if err != nil {
		if _, ok := err.(*ssh.ExitError); ok {
			return nil // saiu com exit != 0: é o shell terminando, não erro de rede
		}
	}
	return err
}

// leitorAvisado pede um quadro novo a cada pedaço de saída que chega.
type leitorAvisado struct {
	r io.Reader
	t *sshTab
}

func (l leitorAvisado) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	if n > 0 {
		l.t.w.Invalidate()
	}
	return n, err
}

// escritorTerminal manda o stderr da sessão para o mesmo emulador.
type escritorTerminal struct{ t *sshTab }

func (e escritorTerminal) Write(p []byte) (int, error) {
	n, err := e.t.term.Write(p)
	e.t.w.Invalidate()
	return n, err
}

func (t *sshTab) setEstado(s string) {
	t.mu.Lock()
	t.estado = s
	t.mu.Unlock()
	t.w.Invalidate()
}

func (t *sshTab) encerrada() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fechado
}

func (t *sshTab) enviar(b []byte) {
	if len(b) == 0 {
		return
	}
	t.mu.Lock()
	e := t.entrada
	t.mu.Unlock()
	if e != nil {
		e.Write(b)
	}
}

func (t *sshTab) Close() {
	t.mu.Lock()
	t.fechado = true
	sess, cli := t.sess, t.cli
	t.sess, t.cli, t.entrada = nil, nil, nil
	t.mu.Unlock()
	if sess != nil {
		sess.Close()
	}
	if cli != nil {
		cli.Close()
	}
}

// HandleKey recebe a tecla física já traduzida pelo Wayland (ver
// internal/grab) — é o mesmo caminho do VNC/RDP, e por isso Ctrl, Alt e
// Shift chegam com apertar E soltar, que é o que o terminal precisa.
func (t *sshTab) HandleKey(keysym, _ uint32, pressed bool) {
	if t.mods.anotar(keysym, pressed) {
		return
	}
	if !pressed {
		return
	}
	// Ctrl+Shift+C / Ctrl+Shift+V: copiar e colar. No terminal, Ctrl+C é
	// SIGINT e Ctrl+V é literal-next — por isso o mundo inteiro usa
	// Shift junto aqui, e é o que o app original faz.
	if t.mods.ctrl && t.mods.shift {
		switch keysym {
		case 'C', 'c':
			t.copiarSelecao()
			return
		case 'V', 'v':
			t.colarDoSistema()
			return
		}
	}
	t.enviar(bytesDaTecla(keysym, t.mods))
}

// copiarSelecao manda a tela inteira quando não há seleção de mouse
// (ainda não implementada): é o texto visível do terminal, que é o que
// serve para colar num chamado.
func (t *sshTab) copiarSelecao() {
	t.term.Lock()
	texto := t.term.String()
	t.term.Unlock()
	publicarClipboard(t.w, strings.TrimRight(texto, "\n \t"))
	t.setEstado("tela copiada")
}

// colarDoSistema escreve o clipboard no stdin. Sem Enter: quem confirma é
// quem está olhando — colar comando que executa sozinho já derrubou PDV.
func (t *sshTab) colarDoSistema() {
	t.mu.Lock()
	texto := t.clipLocal
	t.mu.Unlock()
	if texto == "" {
		return
	}
	t.enviar([]byte(texto))
}

// OnLocalClipboard guarda o que foi copiado no sistema, para o
// Ctrl+Shift+V ter o que colar.
func (t *sshTab) OnLocalClipboardTexto(texto string) {
	t.mu.Lock()
	t.clipLocal = texto
	t.mu.Unlock()
}

// HandlePointer: Ctrl+roda muda o corpo da fonte, 6 a 32, como no VTE do
// app original. Sem Ctrl a roda não faz nada (ainda não há scrollback).
func (t *sshTab) HandlePointer(ev pointer.Event, _ image.Point) {
	if ev.Kind != pointer.Scroll || !(t.mods.ctrl || ctrlPressionado()) {
		return
	}
	t.mu.Lock()
	corpo := t.corpo
	if corpo == 0 {
		corpo = float32(spTerminal)
	}
	if ev.Scroll.Y < 0 {
		corpo++
	} else if ev.Scroll.Y > 0 {
		corpo--
	}
	if corpo < 6 {
		corpo = 6
	}
	if corpo > 32 {
		corpo = 32
	}
	t.corpo = corpo
	t.mu.Unlock()
	t.w.Invalidate()
}

// corpoAtual é o tamanho da fonte em uso.
func (t *sshTab) corpoAtual() unit.Sp {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.corpo == 0 {
		return spTerminal
	}
	return unit.Sp(t.corpo)
}

// OnLocalClipboard é o gancho do clipboard do sistema (mesmo de VNC/RDP).
func (t *sshTab) OnLocalClipboard(texto string) { t.OnLocalClipboardTexto(texto) }

// ------------------------------------------------------------- desenho

func (t *sshTab) Layout(gtx layout.Context) layout.Dimensions {
	if t.painelSnips {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Flexed(1, t.layoutTerminal),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				larg := gtx.Dp(320)
				if larg > gtx.Constraints.Max.X/2 {
					larg = gtx.Constraints.Max.X / 2
				}
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
				return t.layoutSnips(gtx)
			}),
		)
	}
	return t.layoutTerminal(gtx)
}

func (t *sshTab) layoutTerminal(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, tema.TermBg, clip.Rect{Max: size}.Op())

	avanco, alturaCel := t.medidaCelula(gtx)
	if avanco <= 0 || alturaCel <= 0 {
		return layout.Dimensions{Size: size}
	}
	margem := gtx.Dp(6)
	cols := max(20, int(float64(size.X-2*margem)/avanco))
	rows := max(4, (size.Y-2*margem)/alturaCel)
	t.redimensionar(cols, rows)

	defer op.Offset(image.Pt(margem, margem)).Push(gtx.Ops).Pop()
	defer clip.Rect{Max: image.Pt(size.X-margem, size.Y-margem)}.Push(gtx.Ops).Pop()
	t.desenharGrade(gtx, cols, rows, avanco, alturaCel)

	t.mu.Lock()
	estado := t.estado
	t.mu.Unlock()
	if estado != "" {
		txt(t.th, fonteMono, spCorpo, estado, tema.AtencaoFg).Layout(gtx)
	}
	return layout.Dimensions{Size: size}
}

// medidaCelula mede o avanço da monoespaçada uma vez por quadro: é a
// única forma honesta de saber o tamanho da célula, já que ele depende da
// fonte embutida e da escala da tela.
//
// O avanço volta em FLOAT de propósito. Arredondar aqui e posicionar cada
// coluna em col*int(avanço) desalinha o desenho: dentro de um trecho de
// texto o shaper usa o avanço real, então o erro de arredondamento se
// acumula letra a letra e, lá pela coluna 30, o cursor já aparece
// quatro caracteres fora do lugar (visto na prática contra um cmd.exe).
func (t *sshTab) medidaCelula(gtx layout.Context) (float64, int) {
	const amostra = 40
	gtxM := gtx
	gtxM.Constraints.Min = image.Point{}
	gtxM.Constraints.Max = image.Pt(1<<20, 1<<20)
	macro := op.Record(gtx.Ops)
	d := txt(t.th, fonteMono, t.corpoAtual(), strings.Repeat("M", amostra), tema.TermFg).Layout(gtxM)
	macro.Stop() // medição: nada disso vai pra tela
	return float64(d.Size.X) / amostra, d.Size.Y
}

func (t *sshTab) redimensionar(cols, rows int) {
	t.mu.Lock()
	mudou := cols != t.cols || rows != t.rows
	t.cols, t.rows = cols, rows
	sess := t.sess
	t.mu.Unlock()
	if !mudou {
		return
	}
	t.term.Resize(cols, rows)
	if sess != nil {
		sess.WindowChange(rows, cols)
	}
}

func (t *sshTab) desenharGrade(gtx layout.Context, cols, rows int, avanco float64, ch int) {
	// px converte coluna em pixel com o avanço REAL: o texto de um trecho
	// é desenhado a partir de px(col) e daí em diante o próprio shaper
	// avança, então fundo, glifo e cursor caem sempre na mesma grade.
	px := func(col int) int { return int(math.Round(float64(col) * avanco)) }
	t.term.Lock()
	defer t.term.Unlock()

	tcols, trows := t.term.Size()
	if tcols < cols {
		cols = tcols
	}
	if trows < rows {
		rows = trows
	}

	for y := 0; y < rows; y++ {
		// primeiro os fundos da linha, depois o texto: assim uma célula
		// com fundo não apaga o glifo da vizinha.
		x := 0
		for x < cols {
			g := t.term.Cell(x, y)
			bg, temBg := corDeFundo(g.BG)
			if !temBg {
				x++
				continue
			}
			fim := x + 1
			for fim < cols {
				g2 := t.term.Cell(fim, y)
				if c2, ok := corDeFundo(g2.BG); !ok || c2 != bg {
					break
				}
				fim++
			}
			r := clip.Rect{Min: image.Pt(px(x), y*ch), Max: image.Pt(px(fim), (y+1)*ch)}
			paint.FillShape(gtx.Ops, bg, r.Op())
			x = fim
		}

		x = 0
		for x < cols {
			g := t.term.Cell(x, y)
			fg := corDeTexto(g.FG)
			var sb strings.Builder
			inicio := x
			for x < cols {
				g2 := t.term.Cell(x, y)
				if corDeTexto(g2.FG) != fg {
					break
				}
				c := g2.Char
				if c == 0 {
					c = ' '
				}
				sb.WriteRune(c)
				x++
			}
			s := strings.TrimRight(sb.String(), " ")
			if s == "" {
				continue
			}
			st := op.Offset(image.Pt(px(inicio), y*ch)).Push(gtx.Ops)
			gtxL := gtx
			gtxL.Constraints.Min = image.Point{}
			gtxL.Constraints.Max = image.Pt(1<<20, ch)
			txt(t.th, fonteMono, t.corpoAtual(), s, fg).Layout(gtxL)
			st.Pop()
		}
	}

	if t.term.CursorVisible() {
		c := t.term.Cursor()
		if c.X < cols && c.Y < rows {
			r := clip.Rect{
				Min: image.Pt(px(c.X), c.Y*ch),
				Max: image.Pt(px(c.X+1), (c.Y+1)*ch),
			}
			cur := tema.Azul
			cur.A = 150
			paint.FillShape(gtx.Ops, cur, r.Op())
		}
	}
}

// paleta ANSI padrão (as 16 primeiras), com as claras um pouco puxadas
// pro tema: são as cores que qualquer prompt colorido usa.
var paletaANSI = [16]color.NRGBA{
	hex(0x1b1f24), hex(0xd2414f), hex(0x2fa87c), hex(0xd79a28),
	hex(0x4c6ef5), hex(0x9a6ee0), hex(0x1ba8a0), hex(0xc6ccd4),
	hex(0x5c6570), hex(0xe05561), hex(0x38d9a9), hex(0xe0b458),
	hex(0x748ffc), hex(0xb197fc), hex(0x3bc9db), hex(0xffffff),
}

func corVT(c vt10x.Color) (color.NRGBA, bool) {
	switch {
	case c == vt10x.DefaultFG || c == vt10x.DefaultBG:
		return color.NRGBA{}, false
	case c < 16:
		return paletaANSI[c], true
	case c < 256:
		return corXterm(int(c)), true
	}
	// acima de 256 o vt10x guarda cor de 24 bits crua
	v := uint32(c)
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}, true
}

func corDeTexto(c vt10x.Color) color.NRGBA {
	if got, ok := corVT(c); ok {
		return got
	}
	return tema.TermFg
}

// corDeFundo devolve (cor, precisa pintar). O fundo padrão não é pintado:
// o retângulo do terminal inteiro já foi preenchido com ele.
func corDeFundo(c vt10x.Color) (color.NRGBA, bool) {
	return corVT(c)
}

// corXterm converte um índice 16..255 da paleta xterm: 6x6x6 de cor e 24
// de cinza.
func corXterm(i int) color.NRGBA {
	if i >= 232 {
		v := uint8(8 + (i-232)*10)
		return color.NRGBA{R: v, G: v, B: v, A: 0xff}
	}
	i -= 16
	nivel := func(n int) uint8 {
		if n == 0 {
			return 0
		}
		return uint8(55 + n*40)
	}
	return color.NRGBA{R: nivel(i / 36), G: nivel((i / 6) % 6), B: nivel(i % 6), A: 0xff}
}

// ------------------------------------------------ barra de sessão

func (t *sshTab) EstadoSessao() estadoSessao {
	t.mu.Lock()
	viva := t.sess != nil
	estado := t.estado
	cols, rows := t.cols, t.rows
	t.mu.Unlock()

	e := estadoSessao{
		Chip: "AGUARDE", Tipo: "neutro",
		Texto: fmt.Sprintf("%s@%s:%d", t.user, t.host, t.porta),
		Geo:   fmt.Sprintf("%dx%d", cols, rows),
	}
	switch {
	case viva:
		e.Chip, e.Tipo = "ATIVO", "ok"
	case estado != "" && strings.Contains(estado, "reconectando"):
		e.Chip, e.Tipo = "CAIU", "erro"
	}
	return e
}

func (t *sshTab) ControlesSessao(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if t.btnRec.Clicked(gtx) {
		t.Reconectar()
	}
	if t.btnAuto.Clicked(gtx) {
		t.auto.Store(!t.auto.Load())
		gravarPreferencia(t.nomeConexao, "ssh_auto", simNao(t.auto.Load()))
	}
	if t.btnSnip.Clicked(gtx) {
		// Painel LATERAL, não janela: um modal em cima do terminal tapa
		// justamente a saída que se quer consultar antes de escolher o
		// comando. Aqui a lista fica ao lado e o terminal continua vivo.
		t.painelSnips = !t.painelSnips
		if t.painelSnips && t.snips == nil {
			t.recarregarSnips()
		}
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoSessao(gtx, th, &t.btnSnip, "Snippets")
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoSessao(gtx, th, &t.btnRec, "Reconectar")
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return toggleSessao(gtx, th, &t.btnAuto, icons.NavigationRefresh, t.auto.Load(), tema.Azul)
		}),
	)
}

// Reconectar derruba a sessão atual; o laço em laco() reabre em seguida.
// Fechar a sessão é o que faz o Wait() voltar — é o mesmo caminho de uma
// queda de rede, só que provocado.
func (t *sshTab) Reconectar() {
	t.mu.Lock()
	sess, cli := t.sess, t.cli
	t.mu.Unlock()
	if sess != nil {
		sess.Close()
	}
	if cli != nil {
		cli.Close()
	}
	select {
	case t.religar <- struct{}{}:
	default:
	}
}

// ------------------------------------------------ painel de snippets

func (t *sshTab) recarregarSnips() {
	s, err := carregarSnippets(caminhoSnippets(caminhoINI))
	if err != nil {
		t.setEstado(fmt.Sprintf("snippets: %v", err))
		return
	}
	t.snips = s
	t.btnSnips = make([]widget.Clickable, len(s))
	t.listaSnips.Axis = layout.Vertical
	t.filtroSnip.SingleLine = true
}

// layoutSnips é a coluna lateral: filtro em cima, lista embaixo. Clicar
// DIGITA o comando na sessão, sem Enter — quem confirma é quem está
// olhando a tela. Um comando de Windows num PDV Linux é erro caro, então a
// plataforma de cada snippet fica visível na linha.
func (t *sshTab) layoutSnips(gtx layout.Context) layout.Dimensions {
	th := t.th
	termo := strings.ToLower(strings.TrimSpace(t.filtroSnip.Text()))

	var visiveis []int
	for i, s := range t.snips {
		if termo == "" || strings.Contains(strings.ToLower(s.Descricao), termo) ||
			strings.Contains(strings.ToLower(s.Comando), termo) {
			visiveis = append(visiveis, i)
		}
	}

	for _, i := range visiveis {
		if t.btnSnips[i].Clicked(gtx) {
			t.enviar([]byte(t.snips[i].Comando))
		}
	}

	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			size := gtx.Constraints.Min
			paint.FillShape(gtx.Ops, tema.Abas2, clip.Rect{Max: size}.Op())
			paint.FillShape(gtx.Ops, tema.Borda2, clip.Rect{Max: image.Pt(1, size.Y)}.Op())
			return layout.Dimensions{Size: size}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return caixaEditor(gtx, th, &t.filtroSnip, "filtrar snippets…", 0)
					}),
					espaco(6),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return material.List(th, &t.listaSnips).Layout(gtx, len(visiveis), func(gtx layout.Context, k int) layout.Dimensions {
							i := visiveis[k]
							s := t.snips[i]
							return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return t.btnSnips[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Background{}.Layout(gtx,
										func(gtx layout.Context) layout.Dimensions {
											fundo, borda := tema.Cartao, transparente
											if t.btnSnips[i].Hovered() {
												fundo, borda = tema.Hover, tema.Borda2
											}
											superficie(gtx, gtx.Constraints.Min, fundo, borda, 6)
											return layout.Dimensions{Size: gtx.Constraints.Min}
										},
										func(gtx layout.Context) layout.Dimensions {
											gtx.Constraints.Min.X = gtx.Constraints.Max.X
											return layout.Inset{Top: 5, Bottom: 5, Left: 7, Right: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
												return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
													layout.Rigid(func(gtx layout.Context) layout.Dimensions {
														return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
															layout.Rigid(func(gtx layout.Context) layout.Dimensions {
																return selinhoPlataforma(gtx, th, s.Plataforma)
															}),
															layout.Rigid(layout.Spacer{Width: 5}.Layout),
															layout.Flexed(1, rotulo(th, fonteSans, spSecundario, s.Descricao, tema.Texto)),
														)
													}),
													layout.Rigid(rotuloLinha(th, fonteMono, spCardMeta, primeiraLinha(s.Comando), tema.Fraco)),
												)
											})
										},
									)
								})
							})
						})
					}),
				)
			})
		},
	)
}

// paradaPorChave devolve um canal que fecha quando a aba é encerrada —
// é a saída do bloqueio enquanto se espera a decisão sobre a host key.
func (t *sshTab) paradaPorChave() <-chan struct{} {
	c := make(chan struct{})
	go func() {
		for {
			if t.encerrada() {
				close(c)
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
	return c
}
