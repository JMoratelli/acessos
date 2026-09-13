package main

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"acessos-go/internal/hostkey"
	"acessos-go/internal/massa/executor"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpTab é o navegador de arquivos: dois painéis lado a lado, LOCAL e
// REMOTO, com transferência nos dois sentidos.
//
// Duas decisões que vêm do app original e não são detalhe:
//   - conflito é verificado ANTES de começar a transferir, e o operador
//     escolhe sobrescrever, pular os existentes ou cancelar. Descobrir no
//     meio do caminho já custou arquivo sobrescrito.
//   - excluir no remoto levanta a árvore inteira primeiro, diz quantos
//     arquivos e pastas serão apagados e só então oferece o botão, com
//     contagem — não tem desfazer do outro lado.
type sftpTab struct {
	th     *material.Theme
	w      *app.Window
	titulo string
	host   string
	porta  int
	user   string
	senha  string

	mu      sync.Mutex
	cli     *sftp.Client
	ssh     *ssh.Client
	estado  string
	fechado bool

	local  painelArquivos
	remoto painelArquivos

	btnEnviar, btnBaixar   widget.Clickable
	btnExcluir, btnNovaDir widget.Clickable
	btnRec                 widget.Clickable
	btnHost                widget.Clickable
	trocarHost             func(pos image.Point)

	progresso string
	ocupado   bool
	msg       string
	msgErro   bool

	// Arrastar e soltar entre os painéis. O Gio não traz "arrastar item
	// de lista" pronto; o que existe é o evento de ponteiro. Guardamos
	// onde o arrasto começou e, ao soltar do outro lado, transferimos o
	// que estiver SELECIONADO — pegar a seleção inteira (e não o item sob
	// o cursor) é de propósito: arrastar 20 arquivos de uma vez é o caso
	// que faz o gesto valer a pena.
	tagArrasto *int
	arrastando bool
	origemDir  bool // o arrasto começou no painel local?
	posArrasto image.Point
	larguraEsq int // onde termina o painel local
}

// listagem é o resultado de ler uma pasta. Ela NÃO é aplicada por quem
// leu: fica aqui até o laço de quadro aplicar (ver aplicarListagens).
//
// Dois motivos, os dois vividos: a leitura remota roda em goroutine e
// escrevia itens/btnItem/sel enquanto a interface os lia, e a leitura
// local, por ser síncrona, trocava o btnItem NO MEIO do laço que estava
// percorrendo o btnItem antigo — entrar numa pasta com menos arquivos
// que a anterior derrubava o app com "index out of range".
type listagem struct {
	itens []itemArquivo
	erro  string
}

// painelArquivos é um lado do navegador.
type painelArquivos struct {
	remoto   bool
	pend     atomic.Pointer[listagem]
	caminho  widget.Editor
	filtro   widget.Editor // busca dentro da pasta atual (nunca recursiva)
	itens    []itemArquivo
	sel      map[string]bool
	lista    widget.List
	btnItem  []widget.Clickable
	btnAcima widget.Clickable
	btnAtual widget.Clickable
	erro     string
}

type itemArquivo struct {
	nome  string
	dir   bool
	tam   int64
	mtime time.Time
}

func newSFTPTab(w *app.Window, spec map[string]string) (Tab, error) {
	host := spec["host"]
	if host == "" {
		return nil, fmt.Errorf("sftp: falta host")
	}
	if spec["user"] == "" {
		return nil, fmt.Errorf("sftp: falta usuário")
	}
	porta := 22
	if p, err := strconv.Atoi(spec["port"]); err == nil && p > 0 {
		porta = p
	}
	t := &sftpTab{
		th: temaApp, w: w,
		titulo: rotuloAba(spec, "SFTP", host),
		host:   host, porta: porta,
		user: spec["user"], senha: spec["pass"],
		estado: "conectando…",
	}
	t.local.sel = map[string]bool{}
	t.remoto.sel = map[string]bool{}
	t.remoto.remoto = true
	t.local.caminho.SingleLine = true
	t.remoto.caminho.SingleLine = true
	t.local.filtro.SingleLine = true
	t.remoto.filtro.SingleLine = true
	if lar, err := os.UserHomeDir(); err == nil {
		t.local.caminho.SetText(lar)
	}
	t.local.lista.Axis = layout.Vertical
	t.remoto.lista.Axis = layout.Vertical
	t.tagArrasto = new(int)
	t.listarLocal()
	go t.conectar()
	return t, nil
}

func (t *sftpTab) Title() string { return t.titulo }
func (t *sftpTab) SoIcone() bool { return false }
func (t *sftpTab) Pinned() bool  { return false }
func (t *sftpTab) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	return icons.ActionDescription, tema.AtencaoFg, tema.AtencaoBg
}
func (t *sftpTab) HandleKey(_, _ uint32, _ bool)                {}
func (t *sftpTab) HandlePointer(_ pointer.Event, _ image.Point) {}

func (t *sftpTab) Close() {
	t.mu.Lock()
	t.fechado = true
	cli, sc := t.cli, t.ssh
	t.cli, t.ssh = nil, nil
	t.mu.Unlock()
	if cli != nil {
		cli.Close()
	}
	if sc != nil {
		sc.Close()
	}
}

func (t *sftpTab) conectar() {
	t.setEstado("conectando…")
	cfg := &ssh.ClientConfig{
		User: t.user,
		Auth: []ssh.AuthMethod{ssh.Password(t.senha)},
		// mesmo motivo do sshtab: PDV antigo só oferece algoritmo que o
		// Go corta por padrão, e o aperto de mão falha com a rede boa.
		Config:          executor.AlgoritmosLegado(),
		HostKeyCallback: hostkey.Callback(),
		Timeout:         8 * time.Second,
	}
	sc, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", t.host, t.porta), cfg)
	if err != nil {
		if ec := erroDeChave(err); ec != nil {
			t.setEstado(ec.Error())
			pedirConfiancaHostKey(t.w, ec, func() { go t.conectar() })
			return
		}
		t.setEstado(err.Error())
		return
	}
	cli, err := sftp.NewClient(sc)
	if err != nil {
		sc.Close()
		t.setEstado(err.Error())
		return
	}
	t.mu.Lock()
	if t.fechado {
		t.mu.Unlock()
		cli.Close()
		sc.Close()
		return
	}
	t.cli, t.ssh, t.estado = cli, sc, ""
	t.mu.Unlock()

	inicio := "."
	if lar, err := cli.Getwd(); err == nil {
		inicio = lar
	}
	t.remoto.caminho.SetText(inicio)
	t.listarRemoto()
	t.invalidar()
}

func (t *sftpTab) setEstado(s string) {
	t.mu.Lock()
	t.estado = s
	t.mu.Unlock()
	t.invalidar()
}

func (t *sftpTab) encerrada() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fechado
}

func (t *sftpTab) cliente() *sftp.Client {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cli
}

// ------------------------------------------------------------ listagem

func ordenar(itens []itemArquivo) {
	// pastas primeiro, depois alfabético — é como todo navegador de
	// arquivos ordena, e procurar pasta no meio de 300 arquivos é pior.
	sort.Slice(itens, func(i, j int) bool {
		if itens[i].dir != itens[j].dir {
			return itens[i].dir
		}
		return strings.ToLower(itens[i].nome) < strings.ToLower(itens[j].nome)
	})
}

func (t *sftpTab) listarLocal() {
	dir := t.local.caminho.Text()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.local.pend.Store(&listagem{erro: err.Error()})
		return
	}
	var itens []itemArquivo
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		itens = append(itens, itemArquivo{nome: e.Name(), dir: e.IsDir(), tam: info.Size(), mtime: info.ModTime()})
	}
	ordenar(itens)
	t.local.pend.Store(&listagem{itens: itens})
}

func (t *sftpTab) listarRemoto() {
	cli := t.cliente()
	if cli == nil {
		return
	}
	dir := t.remoto.caminho.Text()
	ents, err := cli.ReadDir(dir)
	if err != nil {
		t.remoto.pend.Store(&listagem{erro: err.Error()})
		t.invalidar()
		return
	}
	var itens []itemArquivo
	for _, e := range ents {
		itens = append(itens, itemArquivo{nome: e.Name(), dir: e.IsDir(), tam: e.Size(), mtime: e.ModTime()})
	}
	ordenar(itens)
	t.remoto.pend.Store(&listagem{itens: itens})
	t.invalidar()
}

func (t *sftpTab) navegar(p *painelArquivos, nome string) {
	base := p.caminho.Text()
	var novo string
	if p.remoto {
		novo = path.Join(base, nome)
	} else {
		novo = filepath.Join(base, nome)
	}
	p.caminho.SetText(novo)
	if p.remoto {
		go t.listarRemoto()
	} else {
		t.listarLocal()
	}
}

func (t *sftpTab) subir(p *painelArquivos) {
	base := p.caminho.Text()
	var novo string
	if p.remoto {
		novo = path.Dir(base)
	} else {
		novo = filepath.Dir(base)
	}
	if novo == base {
		return
	}
	p.caminho.SetText(novo)
	if p.remoto {
		go t.listarRemoto()
	} else {
		t.listarLocal()
	}
}

// visiveis aplica o filtro da pasta ATUAL. Nunca recursivo: procurar
// dentro de subpasta por acidente, num diretório remoto grande, custa uma
// eternidade de rede.
func (p *painelArquivos) visiveis() []itemArquivo {
	termo := strings.ToLower(strings.TrimSpace(p.filtro.Text()))
	if termo == "" {
		return p.itens
	}
	var out []itemArquivo
	for _, it := range p.itens {
		if strings.Contains(strings.ToLower(it.nome), termo) {
			out = append(out, it)
		}
	}
	return out
}

func selecionados(p *painelArquivos) []itemArquivo {
	var out []itemArquivo
	for _, it := range p.itens {
		if p.sel[it.nome] {
			out = append(out, it)
		}
	}
	return out
}

// ------------------------------------------------------- transferência

// conflitos devolve os nomes que já existem no destino. Verificar ANTES é
// o ponto: no meio da fila, "já existe" vira ou sobrescrita silenciosa ou
// uma pergunta por arquivo, e as duas coisas são ruins.
func (t *sftpTab) conflitos(itens []itemArquivo, destino string, paraRemoto bool) []string {
	var out []string
	cli := t.cliente()
	for _, it := range itens {
		if it.dir {
			// pasta é merge: o conflito que importa é arquivo a arquivo,
			// e ele é resolvido pela mesma pergunta, com a lista do que
			// vai ser substituído.
			continue
		}
		if paraRemoto {
			if cli == nil {
				continue
			}
			if _, err := cli.Stat(path.Join(destino, it.nome)); err == nil {
				out = append(out, it.nome)
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(destino, it.nome)); err == nil {
			out = append(out, it.nome)
		}
	}
	return out
}

// transferir roda numa goroutine própria: copiar 200MB não pode segurar a
// interface, e o progresso precisa aparecer enquanto anda.
func (t *sftpTab) transferir(itens []itemArquivo, paraRemoto bool, pular map[string]bool) {
	cli := t.cliente()
	if cli == nil {
		return
	}
	origem, destino := t.local.caminho.Text(), t.remoto.caminho.Text()
	if !paraRemoto {
		origem, destino = t.remoto.caminho.Text(), t.local.caminho.Text()
	}

	t.mu.Lock()
	t.ocupado = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.ocupado, t.progresso = false, ""
		t.mu.Unlock()
		if paraRemoto {
			t.listarRemoto()
		} else {
			t.listarLocal()
		}
		t.invalidar()
	}()

	// Total a transferir, para a barra saber a fração. Pasta entra
	// inteira: a transferência de pasta é MERGE — cria o que falta e
	// mantém o que já existe, sem apagar a estrutura do destino.
	var total, feito int64
	arquivos := t.planejar(itens, origem, destino, paraRemoto, pular)
	for _, a := range arquivos {
		total += a.tam
	}

	enviados := 0
	for _, a := range arquivos {
		if t.encerrada() {
			return
		}
		t.mu.Lock()
		t.progresso = fmt.Sprintf("%s — %s de %s", a.rel, tamanhoBytes(feito), tamanhoBytes(total))
		t.mu.Unlock()
		t.invalidar()

		var err error
		andou := func(n int64) {
			feito += n
			t.mu.Lock()
			t.progresso = fmt.Sprintf("%s — %s de %s", a.rel, tamanhoBytes(feito), tamanhoBytes(total))
			t.mu.Unlock()
		}
		if paraRemoto {
			err = copiarParaRemoto(cli, a.origem, a.destino, andou)
		} else {
			err = copiarParaLocal(cli, a.origem, a.destino, andou)
		}
		if err != nil {
			t.definirMsg(fmt.Sprintf("%s: %v", a.rel, err), true)
			return
		}
		enviados++
	}
	t.definirMsg(fmt.Sprintf("%d arquivo(s) transferido(s)", enviados), false)
}

func copiarParaRemoto(cli *sftp.Client, origem, destino string, andou func(int64)) error {
	f, err := os.Open(origem)
	if err != nil {
		return err
	}
	defer f.Close()
	d, err := cli.Create(destino)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.Copy(d, &leitorContado{r: f, andou: andou})
	return err
}

func copiarParaLocal(cli *sftp.Client, origem, destino string, andou func(int64)) error {
	f, err := cli.Open(origem)
	if err != nil {
		return err
	}
	defer f.Close()
	d, err := os.Create(destino)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.Copy(d, &leitorContado{r: f, andou: andou})
	return err
}

// leitorContado avisa quanto já passou. Transferir 200MB sem dizer em que
// pé está é o mesmo que travar, do ponto de vista de quem espera.
type leitorContado struct {
	r     io.Reader
	andou func(int64)
}

func (l *leitorContado) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	if n > 0 && l.andou != nil {
		l.andou(int64(n))
	}
	return n, err
}

func (t *sftpTab) definirMsg(s string, erro bool) {
	t.mu.Lock()
	t.msg, t.msgErro = s, erro
	t.mu.Unlock()
	t.invalidar()
}

// levantar conta o que será apagado, recursivamente. O número é o que a
// confirmação mostra: "3 arquivos" e "1 pasta com 400 dentro" são coisas
// muito diferentes para quem vai clicar.
func (t *sftpTab) levantar(itens []itemArquivo) (arquivos, pastas int) {
	cli := t.cliente()
	base := t.remoto.caminho.Text()
	var anda func(p string)
	anda = func(p string) {
		ents, err := cli.ReadDir(p)
		if err != nil {
			return
		}
		for _, e := range ents {
			if e.IsDir() {
				pastas++
				anda(path.Join(p, e.Name()))
				continue
			}
			arquivos++
		}
	}
	for _, it := range itens {
		if it.dir {
			pastas++
			anda(path.Join(base, it.nome))
			continue
		}
		arquivos++
	}
	return
}

func (t *sftpTab) excluirRemoto(itens []itemArquivo) {
	cli := t.cliente()
	if cli == nil {
		return
	}
	base := t.remoto.caminho.Text()
	var apagarArvore func(p string) error
	apagarArvore = func(p string) error {
		ents, err := cli.ReadDir(p)
		if err != nil {
			return err
		}
		for _, e := range ents {
			alvo := path.Join(p, e.Name())
			if e.IsDir() {
				if err := apagarArvore(alvo); err != nil {
					return err
				}
				continue
			}
			if err := cli.Remove(alvo); err != nil {
				return err
			}
		}
		// de baixo para cima: pasta só sai vazia
		return cli.RemoveDirectory(p)
	}
	for _, it := range itens {
		alvo := path.Join(base, it.nome)
		var err error
		if it.dir {
			err = apagarArvore(alvo)
		} else {
			err = cli.Remove(alvo)
		}
		if err != nil {
			t.definirMsg(err.Error(), true)
			break
		}
	}
	t.listarRemoto()
}

// ------------------------------------------------------------- desenho

func (t *sftpTab) EstadoSessao() estadoSessao {
	t.mu.Lock()
	viva, estado, prog := t.cli != nil, t.estado, t.progresso
	t.mu.Unlock()
	e := estadoSessao{
		Chip: "AGUARDE", Tipo: "neutro",
		Texto: fmt.Sprintf("%s@%s:%d", t.user, t.host, t.porta),
		Geo:   prog,
	}
	switch {
	case viva:
		e.Chip, e.Tipo = "ATIVO", "ok"
	case estado != "" && estado != "conectando…":
		e.Chip, e.Tipo = "ERRO", "erro"
	}
	return e
}

func (t *sftpTab) ControlesSessao(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if t.btnRec.Clicked(gtx) {
		go t.conectar()
	}
	if t.btnHost.Clicked(gtx) && t.trocarHost != nil {
		t.trocarHost(ultimaPosPonteiro())
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// trocar de máquina sem fechar a aba: o painel local (caminho,
			// filtro, seleção) continua onde estava, que é o ponto de
			// mandar o mesmo arquivo para várias caixas.
			return botaoSessao(gtx, th, &t.btnHost, "Trocar máquina")
		}),
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return botaoSessao(gtx, th, &t.btnRec, "Reconectar")
		}),
	)
}

// ApontarPara troca o destino desta aba e reconecta.
func (t *sftpTab) ApontarPara(nome, host string, porta int, usuario, senha string) {
	t.Close()
	t.mu.Lock()
	t.fechado = false
	t.host, t.porta, t.user, t.senha = host, porta, usuario, senha
	t.titulo = nome
	t.estado = "conectando…"
	t.mu.Unlock()
	go t.conectar()
}

// aplicarListagens troca o conteúdo dos painéis, no laço de quadro e só
// aqui. Depois disto, nada mais mexe em itens/btnItem/sel durante o
// quadro — que é a condição para o laço de cliques poder confiar nos
// índices que está percorrendo.
// invalidar existe para a aba poder ser exercitada sem janela (testes).
func (t *sftpTab) invalidar() {
	if t.w != nil {
		t.w.Invalidate()
	}
}

func (t *sftpTab) aplicarListagens() {
	for _, p := range []*painelArquivos{&t.local, &t.remoto} {
		l := p.pend.Swap(nil)
		if l == nil {
			continue
		}
		p.erro = l.erro
		if l.erro != "" {
			continue // erro de leitura não apaga o que estava na tela
		}
		p.itens = l.itens
		p.btnItem = make([]widget.Clickable, len(l.itens))
		p.sel = map[string]bool{}
	}
}

func (t *sftpTab) Layout(gtx layout.Context) layout.Dimensions {
	t.aplicarListagens()
	t.tratarBotoes(gtx)
	defer t.arrasto(gtx)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					d := t.painel(gtx, &t.local, "ESTA MÁQUINA")
					t.larguraEsq = d.Size.X
					return d
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return t.colunaBotoes(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return t.painel(gtx, &t.remoto, "MÁQUINA REMOTA")
				}),
			)
		}),
		layout.Rigid(t.rodape),
	)
}

// comCtrl diz se o clique veio com Ctrl. São DUAS fontes de propósito: no
// Linux o teclado vem do Wayland cru (internal/grab), e o Modifiers do
// Gio chega vazio nesta pilha; no Windows não há grab nenhum, e o
// Modifiers do Gio é a única fonte. Perguntar aos dois faz o gesto
// funcionar nos dois lugares.
func comCtrl(ev widget.Click) bool {
	return ctrlPressionado() || ev.Modifiers.Contain(key.ModCtrl)
}

func (t *sftpTab) tratarBotoes(gtx layout.Context) {
	for _, p := range []*painelArquivos{&t.local, &t.remoto} {
		p := p
		itensVisiveis := p.visiveis()
		if p.btnAcima.Clicked(gtx) {
			t.subir(p)
		}
		if p.btnAtual.Clicked(gtx) {
			if p.remoto {
				go t.listarRemoto()
			} else {
				t.listarLocal()
			}
		}
		botoes := p.btnItem
		for i := range botoes {
			if i >= len(itensVisiveis) {
				continue
			}
			// Gesto de gerenciador de arquivos: clique simples marca SÓ
			// aquele item (o resto desmarca), Ctrl+clique soma e tira da
			// marcação, e dois cliques numa pasta entram nela.
			//
			// Antes o clique simples não fazia nada, e o resultado era
			// clicar num arquivo e a tela não responder — sem pista de
			// que faltava o Ctrl. Marcar um item com um clique não custa
			// nada (o que abre a pasta são DOIS cliques) e dá o retorno
			// que faltava.
			for {
				ev, ok := botoes[i].Update(gtx)
				if !ok {
					break
				}
				it := itensVisiveis[i]
				if it.dir && ev.NumClicks >= 2 {
					t.navegar(p, it.nome)
					break
				}
				if comCtrl(ev) {
					p.sel[it.nome] = !p.sel[it.nome]
					continue
				}
				clear(p.sel)
				p.sel[it.nome] = true
			}
		}
	}

	if t.btnEnviar.Clicked(gtx) {
		t.iniciarTransferencia(selecionados(&t.local), true)
	}
	if t.btnBaixar.Clicked(gtx) {
		t.iniciarTransferencia(selecionados(&t.remoto), false)
	}
	if t.btnNovaDir.Clicked(gtx) {
		t.criarPasta()
	}
	if t.btnExcluir.Clicked(gtx) {
		itens := selecionados(&t.remoto)
		if len(itens) == 0 {
			return
		}
		arquivos, pastas := t.levantar(itens)
		var lista []string
		for _, it := range itens {
			lista = append(lista, it.nome)
		}
		lista = append(lista, fmt.Sprintf("total: %d arquivo(s) e %d pasta(s) — na MÁQUINA REMOTA (%s)",
			arquivos, pastas, t.host))
		// A contagem regressiva é para LOTE: apagar um arquivo solto com
		// três segundos de espera irrita sem proteger nada. Ela entra
		// quando há vários itens ou quando tem pasta no meio — aí o
		// estrago é recursivo e o operador precisa do freio.
		emLote := len(itens) > 1 || pastas > 0
		confirmarDestrutivoEm(t.w, "Excluir no remoto", lista, "Excluir definitivamente",
			emLote, func() { go t.excluirRemoto(itens) })
	}
}

func (t *sftpTab) iniciarTransferencia(itens []itemArquivo, paraRemoto bool) {
	// Nada aqui pode falhar calado: transferência que "não vai" sem dizer
	// por quê é pior que erro na cara.
	if len(itens) == 0 {
		lado := "nesta máquina"
		if !paraRemoto {
			lado = "no remoto"
		}
		t.definirMsg("selecione os arquivos "+lado+" (clique no nome)", true)
		return
	}
	if t.cliente() == nil {
		t.definirMsg("sem conexão SFTP", true)
		return
	}

	t.mu.Lock()
	ocupado := t.ocupado
	t.mu.Unlock()
	if ocupado {
		t.definirMsg("aguarde a transferência em andamento", true)
		return
	}
	destino := t.remoto.caminho.Text()
	if !paraRemoto {
		destino = t.local.caminho.Text()
	}
	conf := t.conflitos(itens, destino, paraRemoto)
	if len(conf) == 0 {
		go t.transferir(itens, paraRemoto, nil)
		return
	}
	// três saídas, como no original: sobrescrever, pular os existentes
	// (os demais vão assim mesmo) ou cancelar tudo.
	abrirDialogo(&dlgConflito{
		w: t.w, nomes: conf,
		sobrescrever: func() { go t.transferir(itens, paraRemoto, nil) },
		pular: func() {
			pular := map[string]bool{}
			for _, n := range conf {
				pular[n] = true
			}
			go t.transferir(itens, paraRemoto, pular)
		},
	})
}

func (t *sftpTab) criarPasta() {
	cli := t.cliente()
	if cli == nil {
		return
	}
	abrirDialogo(&dlgTexto{
		w: t.w, titulo: "Nova pasta", dica: "nome da pasta",
		ok: func(nome string) error {
			if strings.TrimSpace(nome) == "" {
				return fmt.Errorf("o nome não pode ficar vazio")
			}
			if err := cli.Mkdir(path.Join(t.remoto.caminho.Text(), nome)); err != nil {
				return err
			}
			go t.listarRemoto()
			return nil
		},
	})
}

func (t *sftpTab) colunaBotoes(gtx layout.Context) layout.Dimensions {
	larg := gtx.Dp(120)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
	return layout.Inset{Top: 40, Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoPrimario(gtx, t.th, &t.btnEnviar, "enviar →")
			}),
			espaco(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoPrimario(gtx, t.th, &t.btnBaixar, "← baixar")
			}),
			espaco(16),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoSutil(gtx, t.th, &t.btnNovaDir, "+ pasta")
			}),
			espaco(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoPerigo(gtx, t.th, &t.btnExcluir, "excluir")
			}),
		)
	})
}

func (t *sftpTab) painel(gtx layout.Context, p *painelArquivos, titulo string) layout.Dimensions {
	th := t.th
	return layout.Inset{Top: 8, Bottom: 8, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(negrito(txt(th, fonteMono, spCardMeta, titulo, tema.Sec)).Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return botaoSutil(gtx, th, &p.btnAcima, "↑")
					}),
					layout.Rigid(layout.Spacer{Width: 4}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return botaoSutil(gtx, th, &p.btnAtual, "⟳")
					}),
					layout.Rigid(layout.Spacer{Width: 6}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						n, total := len(p.visiveis()), len(p.itens)
						texto := fmt.Sprintf("%d", total)
						if n != total {
							texto = fmt.Sprintf("%d de %d", n, total)
						}
						return rotulo(th, fonteMono, spCardMeta, texto, tema.Fraco)(gtx)
					}),
				)
			}),
			espaco(4),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return caixaEditor(gtx, th, &p.caminho, "caminho", 0)
			}),
			espaco(4),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return caixaEditor(gtx, th, &p.filtro, "filtrar nesta pasta…", 0)
			}),
			espaco(6),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Background{}.Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						superficie(gtx, gtx.Constraints.Min, tema.Vidro1, tema.Borda, 8)
						return layout.Dimensions{Size: gtx.Constraints.Min}
					},
					func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = gtx.Constraints.Max
						return layout.UniformInset(4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if p.erro != "" {
								return layout.Center.Layout(gtx, rotulo(th, fonteMono, spCardMeta, p.erro, tema.ErroFg))
							}
							return t.listaArquivos(gtx, p)
						})
					},
				)
			}),
		)
	})
}

func (t *sftpTab) listaArquivos(gtx layout.Context, p *painelArquivos) layout.Dimensions {
	th := t.th
	itens := p.visiveis()
	return material.List(th, &p.lista).Layout(gtx, len(itens), func(gtx layout.Context, i int) layout.Dimensions {
		it := itens[i]
		marcado := p.sel[it.nome]
		return p.btnItem[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					fundo, borda := transparente, transparente
					switch {
					case marcado:
						fundo, borda = tema.AzulFraco, tema.Azul
					case p.btnItem[i].Hovered():
						fundo = tema.Vidro2
					}
					superficie(gtx, gtx.Constraints.Min, fundo, borda, 5)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				},
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 3, Bottom: 3, Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						ic, cor := icons.ActionDescription, tema.Sec
						if it.dir {
							ic, cor = icons.FileFolder, tema.AtencaoFg
						}
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return icone(gtx, ic, cor, 14)
							}),
							layout.Rigid(layout.Spacer{Width: 6}.Layout),
							layout.Flexed(1, rotuloLinha(th, fonteMono, spCorpo, it.nome, tema.Texto)),
							layout.Rigid(rotuloLinha(th, fonteMono, spCardMeta, tamanhoHumano(it), tema.Fraco)),
							layout.Rigid(layout.Spacer{Width: 8}.Layout),
							layout.Rigid(rotuloLinha(th, fonteMono, spCardMeta, it.mtime.Format("02/01/06 15:04"), tema.Fraco)),
						)
					})
				},
			)
		})
	})
}

func (t *sftpTab) rodape(gtx layout.Context) layout.Dimensions {
	t.mu.Lock()
	msg, erro, prog := t.msg, t.msgErro, t.progresso
	t.mu.Unlock()
	texto, cor := msg, tema.Sec
	if erro {
		cor = tema.ErroFg
	}
	if prog != "" {
		texto, cor = "transferindo "+prog, tema.AtencaoFg
	}
	if t.arrastando {
		texto, cor = "solte do outro lado para transferir", tema.AtencaoFg
	}
	if texto == "" {
		texto = fmt.Sprintf("%d selecionado(s) aqui · %d no remoto · arraste entre os painéis para transferir",
			len(selecionados(&t.local)), len(selecionados(&t.remoto)))
		cor = tema.Fraco
	}
	return layout.Inset{Top: 4, Bottom: 8, Left: 12, Right: 12}.Layout(gtx,
		rotuloLinha(t.th, fonteMono, spSecundario, texto, cor))
}

// tamanhoHumano: pasta não mostra tamanho (o do inode não diz nada).
func tamanhoHumano(it itemArquivo) string {
	if it.dir {
		return ""
	}
	n := float64(it.tam)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if n < 1024 {
			return fmt.Sprintf("%.0f %s", n, u)
		}
		n /= 1024
	}
	return fmt.Sprintf("%.1f TB", n)
}

var _ = unit.Dp(0)

// arrasto detecta o gesto de arrastar entre os painéis. A área é uma só,
// por cima de tudo e com PassOp — por baixo ela não receberia nada (o
// hit-test do Gio salta para o nó pai ao encontrar área sem passagem), e
// com PassOp os cliques normais das listas continuam funcionando.
func (t *sftpTab) arrasto(gtx layout.Context) {
	for {
		ev, ok := gtx.Source.Event(pointer.Filter{
			Target: t.tagArrasto,
			Kinds:  pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel,
		})
		if !ok {
			break
		}
		pe, isP := ev.(pointer.Event)
		if !isP {
			continue
		}
		pos := image.Pt(int(pe.Position.X), int(pe.Position.Y))
		esquerda := pos.X < t.larguraEsq

		switch pe.Kind {
		case pointer.Press:
			if !pe.Buttons.Contain(pointer.ButtonPrimary) {
				continue
			}
			t.arrastando = false
			t.origemDir = esquerda
			t.posArrasto = pos
		case pointer.Drag:
			// só vira arrasto depois de andar um tanto: sem essa folga,
			// todo clique com a mão trêmula viraria transferência.
			if !t.arrastando && abs(pos.X-t.posArrasto.X)+abs(pos.Y-t.posArrasto.Y) > gtx.Dp(12) {
				t.arrastando = true
			}
		case pointer.Release:
			if !t.arrastando {
				continue
			}
			t.arrastando = false
			if esquerda == t.origemDir {
				continue // soltou do mesmo lado: não é transferência
			}
			if t.origemDir {
				t.iniciarTransferencia(selecionados(&t.local), true)
			} else {
				t.iniciarTransferencia(selecionados(&t.remoto), false)
			}
		case pointer.Cancel:
			t.arrastando = false
		}
	}

	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, t.tagArrasto)
	area.Pop()
	pass.Pop()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// arquivoPlano é uma cópia a fazer, já com origem e destino resolvidos.
type arquivoPlano struct {
	origem, destino, rel string
	tam                  int64
}

// planejar percorre a seleção e devolve a lista de ARQUIVOS a copiar,
// criando as pastas do destino pelo caminho. Pasta é copiada inteira, em
// MERGE: o que já existe do outro lado fica, o que falta é criado — nunca
// se apaga a estrutura do destino para depois recriar.
func (t *sftpTab) planejar(itens []itemArquivo, origem, destino string, paraRemoto bool, pular map[string]bool) []arquivoPlano {
	cli := t.cliente()
	var plano []arquivoPlano

	criarDir := func(p string) {
		if paraRemoto {
			cli.MkdirAll(p)
			return
		}
		os.MkdirAll(p, 0o755)
	}

	var anda func(orig, dest, rel string)
	anda = func(orig, dest, rel string) {
		criarDir(dest)
		var nomes []itemArquivo
		if paraRemoto {
			ents, err := os.ReadDir(orig)
			if err != nil {
				return
			}
			for _, e := range ents {
				info, err := e.Info()
				if err != nil {
					continue
				}
				nomes = append(nomes, itemArquivo{nome: e.Name(), dir: e.IsDir(), tam: info.Size()})
			}
		} else {
			ents, err := cli.ReadDir(orig)
			if err != nil {
				return
			}
			for _, e := range ents {
				nomes = append(nomes, itemArquivo{nome: e.Name(), dir: e.IsDir(), tam: e.Size()})
			}
		}
		for _, it := range nomes {
			o, d := juntar(orig, it.nome, paraRemoto, true), juntar(dest, it.nome, paraRemoto, false)
			if it.dir {
				anda(o, d, rel+"/"+it.nome)
				continue
			}
			plano = append(plano, arquivoPlano{origem: o, destino: d, rel: rel + "/" + it.nome, tam: it.tam})
		}
	}

	for _, it := range itens {
		if pular[it.nome] {
			continue
		}
		o, d := juntar(origem, it.nome, paraRemoto, true), juntar(destino, it.nome, paraRemoto, false)
		if it.dir {
			anda(o, d, it.nome)
			continue
		}
		plano = append(plano, arquivoPlano{origem: o, destino: d, rel: it.nome, tam: it.tam})
	}
	return plano
}

// juntar monta caminho com a separação certa de cada lado: o remoto é
// sempre POSIX, o local segue o sistema.
func juntar(base, nome string, paraRemoto, ladoOrigem bool) string {
	remoto := paraRemoto != ladoOrigem // origem remota quando não é envio
	if remoto {
		return path.Join(base, nome)
	}
	return filepath.Join(base, nome)
}

func tamanhoBytes(n int64) string {
	v := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if v < 1024 {
			return fmt.Sprintf("%.0f %s", v, u)
		}
		v /= 1024
	}
	return fmt.Sprintf("%.1f TB", v)
}
