//go:build linux || windows

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"sync"
	"sync/atomic"
	"time"

	"acessos-go/internal/conexoes"
	"acessos-go/internal/telaproc"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"gio.tools/icons"
)

// A sessão VNC NÃO roda dentro deste processo: ela vive num processo-filho
// (ver internal/telaproc e telaworker_vnc.go), e o que existe aqui é a
// ponta que manda entrada e recebe retângulos de tela.
//
// O motivo é o mesmo do RDP (BACKLOG.md §7), mais um específico do VNC: a
// libvncclient tem CVEs abertas de escrita fora dos limites no decodificador
// Tight, disparadas pelo servidor, sem release corrigida — as correções
// estão aplicadas como patch local (ver flatpak/patches/libvncserver/).
// Corrigir fecha o que se conhece; rodar em processo próprio limita o
// estrago do que ainda não se conhece. Isto aqui é a segunda metade.

// backoffSchedule espelha o ESPERAS do app original (acessos.py).
var backoffSchedule = []time.Duration{
	3 * time.Second, 5 * time.Second, 8 * time.Second,
	13 * time.Second, 21 * time.Second, 30 * time.Second,
}

// vncView descreve como o framebuffer remoto está mapeado na área da aba
// (escala + letterbox) — mesma lógica de cmd/vncview.
type vncView struct {
	scale      float32
	offX, offY float32
}

func computeVNCView(size image.Point, fw, fh int, modo int32) vncView {
	if fw == 0 || fh == 0 {
		return vncView{scale: 1}
	}
	sx := float32(size.X) / float32(fw)
	sy := float32(size.Y) / float32(fh)
	scale := sx
	if sy < scale {
		scale = sy
	}
	// nunca AMPLIA: esticar o framebuffer de um PDV borra o texto, que é
	// justamente o que se vai ler. E no modo 1:1 não escala de jeito
	// nenhum, mesmo que sobre espaço.
	if scale > 1 || modo == modo1x1 {
		scale = 1
	}
	ow, oh := float32(fw)*scale, float32(fh)*scale
	return vncView{
		scale: scale,
		offX:  (float32(size.X) - ow) / 2,
		offY:  (float32(size.Y) - oh) / 2,
	}
}

type vncTab struct {
	w         *app.Window
	title     string
	host      string
	port      int
	proc      atomic.Pointer[telaproc.Processo]
	stop      chan struct{}
	religar   chan struct{}
	closeOnce sync.Once
	viewMu    sync.Mutex
	view      vncView
	clip      clipboardSync

	// tela é o último quadro PRONTO para desenhar. Quem monta troca o
	// ponteiro por uma imagem nova e nunca mexe na anterior — é o que
	// permite entregá-la ao Gio sem trava e sem risco de ela mudar
	// debaixo do upload da textura.
	tela atomic.Pointer[image.NRGBA]
	// opCache guarda a ImageOp da última tela publicada: sem isto o Gio
	// remontaria (e reenviaria à GPU) a textura a cada quadro DA
	// INTERFACE, mesmo sem nada ter mudado do lado remoto.
	opCache  paint.ImageOp
	opDaTela *image.NRGBA

	// estado mostrado e controlado pela barra de sessão
	auto   atomic.Bool // reconectar sozinho ao cair
	clipOn atomic.Bool // sincronizar área de transferência
	modo   atomic.Int32
	caiu   atomic.Bool
	fw, fh atomic.Int32
	// nomeConexao é a seção do .ini (vazio quando a conexão veio da linha
	// de comando: aí não há onde gravar preferência).
	nomeConexao string
	ronly       atomic.Bool
	cursorAtual atomic.Uint32
	btnOlho     widget.Clickable
	btnRec      widget.Clickable
	btnTeclas   widget.Clickable
	btnAuto     widget.Clickable
	btnClip     widget.Clickable
	btnModo     [2]widget.Clickable
}

// modos da tela remota. "encaixar" reduz só quando não cabe (nunca
// amplia — ampliar borra o texto do PDV); "1:1" mostra pixel a pixel,
// centralizado, cortando o que não couber.
const (
	modoEncaixar = 0
	modo1x1      = 1
)

var rotulosModo = []string{"Encaixar", "1:1"}

func newVNCTab(w *app.Window, spec map[string]string) *vncTab {
	host := spec["host"]
	port := specInt(spec, "port", 5900)
	t := &vncTab{
		w:       w,
		title:   rotuloAba(spec, "VNC", host),
		host:    host,
		port:    port,
		stop:    make(chan struct{}),
		religar: make(chan struct{}, 1),
	}
	t.auto.Store(true)
	t.clipOn.Store(true)
	t.nomeConexao = spec["rotulo"]
	t.ronly.Store(spec["ronly"] == "1")
	if spec["modo"] == "1x1" {
		t.modo.Store(modo1x1)
	}
	if spec["auto"] == "0" {
		t.auto.Store(false)
	}
	go t.manageSession(spec["user"], spec["pass"])
	return t
}

func (t *vncTab) Title() string { return t.title }
func (t *vncTab) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	e := estiloDe(conexoes.VNC)
	return e.ic, e.cor(), e.fundo()
}

// EhTelaRemota marca esta aba como tela remota (ver tab.go).
func (t *vncTab) EhTelaRemota() {}

func (t *vncTab) Pinned() bool  { return false }
func (t *vncTab) SoIcone() bool { return false }

func (t *vncTab) Close() {
	t.closeOnce.Do(func() { close(t.stop) })
}

// manageSession delega o laço de reconexão a gerenciarSessaoRemota (ver
// telatab.go), compartilhado com rdpTab. Sem antesDeConectar: o VNC não
// tem resolução dinâmica, então não há nada para esquecer entre
// tentativas. aoTerminar apaga a tela congelada ao perder a sessão — o
// mesmo motivo do RDP (ver o comentário lá): com o chip "CAIU" ao lado,
// uma imagem parada que continua parecendo viva é pior que preto.
func (t *vncTab) manageSession(user, pass string) {
	gerenciarSessaoRemota(sessaoRemotaCfg{
		title:      t.title,
		stop:       t.stop,
		religar:    t.religar,
		w:          t.w,
		proc:       &t.proc,
		caiu:       &t.caiu,
		auto:       &t.auto,
		rodar:      func() fimSessao { return t.rodarSessao(user, pass) },
		aoTerminar: func() { t.tela.Store(nil) },
	})
}

// rodarSessao delega a rodarSessaoRemota (ver telatab.go), compartilhado
// com rdpTab — só como conectar() monta a telaproc.Ligacao muda entre os
// dois protocolos (o VNC não tem domínio).
func (t *vncTab) rodarSessao(user, pass string) fimSessao {
	return rodarSessaoRemota("vnc", t.title, t.host, t.port, t.stop, t.religar,
		func(proc *telaproc.Processo) error {
			return proc.Conectar(telaproc.Ligacao{
				Host: t.host, Porta: t.port, Usuario: user, Senha: pass,
			})
		},
		t.lacoEventos,
	)
}

// lacoEventos é o único leitor do canal do filho. Sai quando o filho fecha
// ou morre — e "morre" inclui um SIGSEGV dentro da libvncclient, que daqui
// é indistinguível de uma desconexão limpa. Essa indistinção é o ponto.
//
// Devolve true quando a sessão terminou por RECUSA do servidor.
func (t *vncTab) lacoEventos(proc *telaproc.Processo, inicio time.Time) (falhou bool) {
	// acum é a tela remota inteira, montada retângulo a retângulo. Fica
	// nesta goroutine e nunca é entregue ao Gio: o que vai para a
	// interface é sempre uma cópia congelada (ver publicarTela).
	var acum *image.NRGBA

	for {
		tipo, corpo, err := proc.Ler()
		if err != nil {
			return false
		}
		switch tipo {
		case telaproc.EvtConectado:
			reg("[%s] conectado em %s", t.title, time.Since(inicio).Truncate(time.Millisecond))
			t.proc.Store(proc)
			t.caiu.Store(false)
			t.w.Invalidate()
			_ = proc.Credito()

		case telaproc.EvtFalha:
			var f telaproc.Falha
			_ = json.Unmarshal(corpo, &f)
			reg("[%s] falha: %s (auth=%v precisa_usuario=%v recusado=%v)",
				t.title, f.Mensagem, f.AuthFalhou, f.PrecisaUsuario, f.Recusado)
			return true

		case telaproc.EvtQuadro:
			q, pix, err := telaproc.DecodificarQuadro(corpo)
			if err != nil {
				reg("[%s] quadro inválido: %v", t.title, err)
				return false
			}
			acum = aplicarQuadro(acum, q, pix)
			t.fw.Store(q.TotalW)
			t.fh.Store(q.TotalH)
			publicarTela(&t.tela, acum)
			t.w.Invalidate()
			// O crédito do quadro SEGUINTE só sai agora: é o que impede o
			// filho de encher a fila do socket mais rápido do que isto
			// aqui consome. E sai devagar quando a aba não está à vista —
			// ver intervaloSegundoPlano.
			creditarConformeVisibilidade(proc, ehAbaAtiva(t), t.stop)

		case telaproc.EvtDesconectado:
			reg("[%s] sessão caiu: %s", t.title, string(corpo))
			return false

		case telaproc.EvtClipboard:
			texto := string(corpo)
			if !t.clipOn.Load() || !ehAbaAtiva(t) {
				continue
			}
			if !t.clip.checkAndSet(texto) {
				continue
			}
			publicarClipboard(t.w, texto)

		case telaproc.EvtCursor:
			if c, ok := telaproc.LerCursor(corpo); ok {
				t.cursorAtual.Store(c)
				t.w.Invalidate()
			}
		}
	}
}

// OnLocalClipboard implementa clipboardReceiver: o clipboard do sistema
// mudou, manda pro servidor VNC se for a aba ativa (main.go só chama isto
// pra aba ativa no momento).
func (t *vncTab) OnLocalClipboard(text string) {
	if !t.clipOn.Load() {
		return
	}
	if !t.clip.checkAndSet(text) {
		return
	}
	if p := t.proc.Load(); p != nil {
		_ = p.Clipboard(text)
	}
}

func (t *vncTab) Layout(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	if depurarLayout {
		fmt.Printf("[dbg vnc] max=%v min=%v\n", gtx.Constraints.Max, gtx.Constraints.Min)
	}
	tela := t.tela.Load()

	fw, fh := 0, 0
	if tela != nil {
		fw, fh = tela.Rect.Dx(), tela.Rect.Dy()
	}
	v := computeVNCView(size, fw, fh, t.modo.Load())
	t.viewMu.Lock()
	t.view = v
	t.viewMu.Unlock()

	// Clipa à própria área: sem isto, o preenchimento preto de fundo
	// (sem clip nenhum) pinta por cima de tudo que já foi desenhado no
	// frame, inclusive a barra de abas logo acima.
	area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
	defer area.Pop()
	pointer.Cursor(t.cursorAtual.Load()).Add(gtx.Ops)

	paint.ColorOp{Color: color.NRGBA{A: 255}}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)

	if tela != nil {
		// Só remonta a ImageOp quando a TELA mudou: a comparação é de
		// ponteiro porque cada publicação é uma imagem nova (ver
		// publicarTela). Reaproveitar a op é o que faz o Gio reusar a
		// textura já na GPU em vez de reenviá-la a cada quadro da
		// interface.
		if t.opDaTela != tela {
			t.opCache = paint.NewImageOp(tela)
			t.opDaTela = tela
		}
		tr := op.Affine(f32.Affine2D{}.
			Scale(f32.Point{}, f32.Point{X: v.scale, Y: v.scale}).
			Offset(f32.Point{X: v.offX, Y: v.offY}),
		).Push(gtx.Ops)
		t.opCache.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		tr.Pop()
	}

	return layout.Dimensions{Size: size}
}

func (t *vncTab) HandlePointer(ev pointer.Event, _ image.Point) {
	proc := t.proc.Load()
	// Somente leitura: nada de ponteiro nem de teclado sai daqui. É o
	// modo de acompanhar o operador da loja sem esbarrar no que ele está
	// fazendo — e o clique acidental num PDV em venda custa caro. O filtro
	// fica AQUI, e não no processo-filho, de propósito: o que não é
	// enviado não pode ser entregue errado do outro lado.
	if proc == nil || t.ronly.Load() {
		return
	}
	t.viewMu.Lock()
	v := t.view
	t.viewMu.Unlock()
	if v.scale == 0 {
		return
	}
	x := (ev.Position.X - v.offX) / v.scale
	y := (ev.Position.Y - v.offY) / v.scale

	buttons := 0
	if ev.Buttons.Contain(pointer.ButtonPrimary) {
		buttons |= 1 << 0
	}
	if ev.Buttons.Contain(pointer.ButtonSecondary) {
		buttons |= 1 << 2
	}
	if ev.Buttons.Contain(pointer.ButtonTertiary) {
		buttons |= 1 << 1
	}
	_ = proc.PonteiroMascara(int(x), int(y), buttons)
}

func (t *vncTab) HandleKey(keysym, _ uint32, pressed bool) {
	if t.ronly.Load() {
		return
	}
	if p := t.proc.Load(); p != nil {
		_ = p.Tecla(keysym, pressed)
	}
}

// ------------------------------------------------ barra de sessão

func (t *vncTab) EstadoSessao() estadoSessao {
	e := estadoSessao{
		Chip: "AGUARDE", Tipo: "neutro",
		Texto: fmt.Sprintf("%s:%d", t.host, t.port),
	}
	switch {
	case t.proc.Load() != nil && t.ronly.Load():
		e.Chip, e.Tipo = "SÓ VER", "atencao"
	case t.proc.Load() != nil:
		e.Chip, e.Tipo = "ATIVO", "ok"
	case t.caiu.Load():
		e.Chip, e.Tipo = "CAIU", "erro"
	}
	if fw, fh := t.fw.Load(), t.fh.Load(); fw > 0 && fh > 0 {
		escala := "1:1"
		t.viewMu.Lock()
		if t.view.scale < 1 {
			escala = "reduzido"
		}
		t.viewMu.Unlock()
		e.Geo = fmt.Sprintf("%dx%d %s", fw, fh, escala)
	}
	return e
}

func (t *vncTab) ControlesSessao(gtx layout.Context, th *material.Theme) layout.Dimensions {
	for i := range t.btnModo {
		if t.btnModo[i].Clicked(gtx) {
			t.modo.Store(int32(i))
			gravarPreferencia(t.nomeConexao, "modo", []string{"encaixar", "1x1"}[i])
		}
	}
	if t.btnTeclas.Clicked(gtx) {
		menuTeclas(ultimaPosPonteiro(), t.ronly.Load(), func(ks uint32, pressionada bool) {
			if p := t.proc.Load(); p != nil {
				_ = p.Tecla(ks, pressionada)
			}
		})
	}
	if t.btnOlho.Clicked(gtx) {
		t.ronly.Store(!t.ronly.Load())
		gravarPreferencia(t.nomeConexao, "ronly", simNao(t.ronly.Load()))
	}
	if t.btnRec.Clicked(gtx) {
		t.Reconectar()
	}
	if t.btnAuto.Clicked(gtx) {
		t.auto.Store(!t.auto.Load())
		gravarPreferencia(t.nomeConexao, "auto", simNao(t.auto.Load()))
	}
	if t.btnClip.Clicked(gtx) {
		t.clipOn.Store(!t.clipOn.Load())
	}

	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return segmentado(gtx, th,
				[]*widget.Clickable{&t.btnModo[0], &t.btnModo[1]},
				rotulosModo, int(t.modo.Load()))
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// olho: fechado = somente leitura. Vermelho não, porque não
			// destrói nada; é estado, e estado usa a cor do assunto.
			ic := icons.ActionVisibility
			cor := tema.Verde
			if t.ronly.Load() {
				ic, cor = icons.ActionVisibilityOff, tema.AtencaoFg
			}
			return toggleSessao(gtx, th, &t.btnOlho, ic, true, cor)
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoTeclas(gtx, th, &t.btnTeclas)
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoSessao(gtx, th, &t.btnRec, "Reconectar")
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return toggleSessao(gtx, th, &t.btnClip, icons.ContentContentCopy, t.clipOn.Load(), tema.Verde)
		}),
		layout.Rigid(layout.Spacer{Width: 3}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return toggleSessao(gtx, th, &t.btnAuto, icons.NavigationRefresh, t.auto.Load(), tema.Azul)
		}),
	)
}

// Reconectar derruba a sessão viva e recomeça na hora. Sem efeito se a aba
// já está fechando.
func (t *vncTab) Reconectar() {
	select {
	case t.religar <- struct{}{}:
	default: // já há um pedido na fila
	}
}
