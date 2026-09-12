package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"sync"
	"time"

	"acessos-go/internal/conexoes"
	"acessos-go/internal/vida"

	"gio.tools/icons"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	cardLargura = unit.Dp(172)
	cardRaio    = unit.Dp(9)  // .card
	icoRaio     = unit.Dp(7)  // .card-ico
	icoAltura   = unit.Dp(36) // .card-ico (26px no CSS; aqui o card é quadrado e sobra altura)
)

// acaoAbrir é o que o painel pede quando alguém clica num ícone de
// protocolo do card: main.go abre a aba correspondente.
type acaoAbrir func(cx conexoes.Conexao, p conexoes.Protocolo)

// protocolo reúne o que muda entre VNC/SSH/RDP/SFTP num lugar só — ícone,
// cor e cor de fundo tênue. O MESMO glifo e a MESMA cor aparecem no card e
// na aba: um protocolo tem uma cor só em toda a interface (gtk.md §6).
type protoEstilo struct {
	p      conexoes.Protocolo
	ic     *widget.Icon
	cor    func() color.NRGBA
	fundo  func() color.NRGBA
	rotulo string
}

var protocolos = []protoEstilo{
	{conexoes.VNC, icons.HardwareDesktopWindows, func() color.NRGBA { return tema.Azul }, func() color.NRGBA { return tema.AzulFraco }, "tela"},
	{conexoes.SSH, icons.ActionCode, func() color.NRGBA { return tema.Verde }, func() color.NRGBA { return tema.VerdeFraco }, "shell"},
	{conexoes.RDP, icons.HardwareComputer, func() color.NRGBA { return tema.Roxo }, func() color.NRGBA { return tema.RoxoFraco }, "rdp"},
	// Glifo de TRAÇO, como os outros três: os cheios (a pasta sólida)
	// pintam o quadrado inteiro e encostam na borda da caixinha, tanto no
	// card quanto no selo da aba.
	{conexoes.SFTP, icons.ActionDescription, func() color.NRGBA { return tema.AtencaoFg }, func() color.NRGBA { return tema.AtencaoBg }, "sftp"},
}

func estiloDe(p conexoes.Protocolo) protoEstilo {
	for _, e := range protocolos {
		if e.p == p {
			return e
		}
	}
	return protocolos[0]
}

// dashTab é a aba FIXA "Painel": hero, filtro, árvore de grupos e os cards
// de vidro com um botão por protocolo.
type dashTab struct {
	th      *material.Theme
	arq     *conexoes.Arquivo
	arvore  []*conexoes.Grupo
	abrir   acaoAbrir
	caminho string
	abertas func() int

	// DOIS campos de busca, um na lateral e um no painel, com o texto
	// espelhado a cada frame: o Gio não deixa desenhar o MESMO
	// widget.Editor duas vezes no mesmo frame (o estado de foco/seleção é
	// um só), e a busca foi pedida nos dois lugares.
	filtro        widget.Editor
	filtroLateral widget.Editor
	ultimoTermo   string
	lista         widget.List
	expandir      map[string]bool
	cliques       map[string]*widget.Clickable
	btnRecolher   widget.Clickable
	btnExpandir   widget.Clickable
	btnNova       widget.Clickable
	btnMassa      widget.Clickable
	btnLimparSel  widget.Clickable

	// seleção para execução em massa. A chave é grupo|nome, a mesma dos
	// Clickables — o card é identificado pelo par, porque nome se repete
	// entre lojas.
	selecao     map[string]conexoes.Conexao
	aoExecMassa func([]conexoes.Conexao)
	recarregar  func()
	aoInvalidar func()

	// menu de contexto: onde o ponteiro está (para ancorar o menu) e a
	// tag de área do botão direito de cada card.
	ultimaPos      image.Point
	sobCursor      *conexoes.Conexao  // card sob o ponteiro (hover)
	protoSobCursor conexoes.Protocolo // ícone de protocolo sob o ponteiro
	grupoSobCursor string             // cabeçalho de grupo sob o ponteiro
	tagPainel      *int

	// Sonda de vida: trilho verde = respondeu, vermelho = não, cinza =
	// ainda não checado. Só os cards que estão SENDO DESENHADOS entram na
	// fila — sondar 274 máquinas de uma vez inunda a rede e não serve
	// para nada, porque o operador está olhando uma loja de cada vez.
	vidaMu        sync.Mutex
	vida          map[string]vida.Estado
	naFila        map[string]bool
	ultimoAviso   time.Time
	fila          chan conexoes.Conexao
	cliqueEmIcone bool // o clique deste quadro foi num ícone de protocolo
	travaAbrir    int  // quadros em que clique não abre (gesto do botão direito)
	aoMenuCard    func(cx conexoes.Conexao, pos image.Point)
	aoNova        func(grupo string)
	aoEfemera     func(destino string)
}

func newDashTab(th *material.Theme, arq *conexoes.Arquivo, caminho string, abrir acaoAbrir, abertas func() int) *dashTab {
	d := &dashTab{
		th:        th,
		arq:       arq,
		arvore:    arq.Arvore(),
		abrir:     abrir,
		caminho:   caminho,
		abertas:   abertas,
		expandir:  map[string]bool{},
		cliques:   map[string]*widget.Clickable{},
		selecao:   map[string]conexoes.Conexao{},
		tagPainel: new(int),
		vida:      map[string]vida.Estado{},
		naFila:    map[string]bool{},
		fila:      make(chan conexoes.Conexao, 512),
	}
	d.filtro.SingleLine = true
	d.filtro.Submit = true
	d.filtroLateral.SingleLine = true
	d.filtroLateral.Submit = true
	d.filtro.SetText(filtroInicial)
	d.filtroLateral.SetText(filtroInicial)
	d.ultimoTermo = filtroInicial
	d.lista.Axis = layout.Vertical
	// Poucos trabalhadores e prazo curto: isto é diagnóstico de fundo e
	// não pode competir com a sessão remota que o operador está usando —
	// nem com a CPU do próprio desenho.
	for i := 0; i < 3; i++ {
		go d.sondador()
	}
	return d
}

func (d *dashTab) sondador() {
	for cx := range d.fila {
		porta := 0
		if cx.Tem(conexoes.VNC) {
			porta = cx.VNC.Porta
		} else if cx.Tem(conexoes.SSH) {
			porta = cx.SSH.Porta
		}
		e := vida.Checar(cx.Host, porta, 400*time.Millisecond)
		d.vidaMu.Lock()
		d.vida[cx.Host] = e
		delete(d.naFila, cx.Host)
		// Um Invalidate por resposta fazia a janela redesenhar dezenas de
		// vezes por segundo com o parque inteiro na tela — e redesenhar
		// 40 cards com sombra não é de graça. Agrupa: no máximo um pedido
		// de quadro a cada 250ms.
		agora := time.Now()
		pedir := agora.Sub(d.ultimoAviso) > 250*time.Millisecond
		if pedir {
			d.ultimoAviso = agora
		}
		d.vidaMu.Unlock()
		if pedir && d.aoInvalidar != nil {
			d.aoInvalidar()
		}
	}
}

// estadoVida devolve o que se sabe do host e enfileira a checagem quando
// ainda não se sabe nada.
func (d *dashTab) estadoVida(cx conexoes.Conexao) vida.Estado {
	d.vidaMu.Lock()
	e, sabido := d.vida[cx.Host]
	pendente := d.naFila[cx.Host]
	if !sabido && !pendente {
		d.naFila[cx.Host] = true
		pendente = true
		defer func() {
			select {
			case d.fila <- cx:
			default: // fila cheia: fica para o próximo desenho
				d.vidaMu.Lock()
				delete(d.naFila, cx.Host)
				d.vidaMu.Unlock()
			}
		}()
	}
	d.vidaMu.Unlock()
	if !sabido {
		return vida.Desconhecido
	}
	return e
}

// recarregarCom troca o arquivo exibido mantendo o que está expandido —
// recarregar não deveria fechar tudo que o operador abriu.
func (d *dashTab) recarregarCom(arq *conexoes.Arquivo) {
	d.arq = arq
	d.arvore = arq.Arvore()
}

func (d *dashTab) Title() string { return "Painel" }
func (d *dashTab) SoIcone() bool { return true }
func (d *dashTab) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	return icons.ActionHome, tema.TopoTxt, tema.Vidro
}
func (d *dashTab) Pinned() bool                  { return true }
func (d *dashTab) Close()                        {}
func (d *dashTab) HandleKey(_, _ uint32, _ bool) {}

// HandlePointer não é usado pelo Painel: os widgets do Gio ficam POR CIMA
// da área que main.go registra, então esses eventos nunca chegam aqui. A
// posição do ponteiro vem de rastrearPonteiro, com PassOp.
func (d *dashTab) HandlePointer(_ pointer.Event, _ image.Point) {}

// rastrearPonteiro é uma área do tamanho do painel, com PassOp, registrada
// POR CIMA de tudo (daí o defer em Layout). Ela faz duas coisas: guarda
// onde o ponteiro está, para ancorar o menu, e detecta o clique com o
// botão direito.
//
// Por que não uma área por card: no hit-test do Gio, ao encontrar uma área
// SEM pass o percurso salta para o nó PAI, e com isso as áreas irmãs
// registradas antes ficam inalcançáveis. Uma área por card, por baixo dos
// widgets do card, nunca recebia nada — foram duas tentativas. Aqui a
// área é uma só, fica por cima, e QUAL card está sob o cursor sai do
// hover, que o Gio já mantém.
func (d *dashTab) rastrearPonteiro(gtx layout.Context) {
	for {
		ev, ok := gtx.Source.Event(pointer.Filter{
			Target: d.tagPainel,
			Kinds:  pointer.Move | pointer.Press | pointer.Drag,
		})
		if !ok {
			break
		}
		pe, isP := ev.(pointer.Event)
		if !isP {
			continue
		}
		d.ultimaPos = image.Pt(int(pe.Position.X), int(pe.Position.Y))
		if pe.Kind != pointer.Press {
			continue
		}
		switch {
		case pe.Buttons.Contain(pointer.ButtonSecondary):
			// trava o gesto: nenhum clique de abertura vale enquanto o
			// menu está sendo aberto por este mesmo movimento.
			d.travaAbrir = 2
			if d.sobCursor != nil && d.aoMenuCard != nil {
				d.aoMenuCard(*d.sobCursor, d.ultimaPos)
			} else if d.grupoSobCursor != "" {
				d.aoNovaGrupo(d.grupoSobCursor)
			}
		case pe.Buttons.Contain(pointer.ButtonTertiary):
			// Botão do meio abre em segundo plano. Se o cursor está sobre
			// um ÍCONE de protocolo, é aquele protocolo que abre — antes
			// ele caía sempre no padrão do card, então clicar no shell
			// abria a tela.
			if d.sobCursor != nil {
				p, ok := d.protoSobCursor, d.protoSobCursor != ""
				if !ok {
					p, ok = protocoloPadrao(*d.sobCursor)
				}
				if ok {
					d.abrirEm(*d.sobCursor, p, false)
				}
			}
		}
	}
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, d.tagPainel)
	area.Pop()
	pass.Pop()
}

// clique devolve (criando na primeira vez) o Clickable daquela chave —
// recriar a cada frame zeraria o clique antes de ele ser lido.
func (d *dashTab) clique(chave string) *widget.Clickable {
	c, ok := d.cliques[chave]
	if !ok {
		c = &widget.Clickable{}
		d.cliques[chave] = c
	}
	return c
}

// sincronizarBusca copia o texto do campo que mudou para o outro, pra os
// dois espelharem o mesmo filtro.
func (d *dashTab) sincronizarBusca() string {
	painel, lateral := d.filtro.Text(), d.filtroLateral.Text()
	switch {
	case painel != d.ultimoTermo:
		d.ultimoTermo = painel
		d.filtroLateral.SetText(painel)
	case lateral != d.ultimoTermo:
		d.ultimoTermo = lateral
		d.filtro.SetText(lateral)
	}
	return strings.ToLower(strings.TrimSpace(d.ultimoTermo))
}

func (d *dashTab) Layout(gtx layout.Context) layout.Dimensions {
	termo := d.sincronizarBusca()
	// quem está sob o cursor é recalculado a cada quadro pelo hover
	d.sobCursor, d.grupoSobCursor, d.protoSobCursor = nil, "", ""
	if d.travaAbrir > 0 {
		d.travaAbrir--
	}
	defer d.rastrearPonteiro(gtx)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(d.hero),
		layout.Rigid(d.barraFiltro),
		layout.Rigid(d.barraSelecao),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return d.lista.Layout(gtx, len(d.arvore), func(gtx layout.Context, i int) layout.Dimensions {
				return d.grupo(gtx, d.arvore[i], 0, termo)
			})
		}),
		layout.Rigid(d.rodape),
	)
}

// hero: faixa de topo sem raio e sem margem, encostando nas bordas — um
// cartão flutuante ali deixaria a página sem âncora (tema.py).
func (d *dashTab) hero(gtx layout.Context) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			fundoHero(gtx, gtx.Constraints.Min)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 16, Bottom: 16, Left: 22, Right: 22}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(negrito(txt(d.th, fonteMono, spHeroEyebrow, "PAINEL DE ACESSOS", tema.HeroSec)).Layout),
							layout.Rigid(layout.Spacer{Height: 2}.Layout),
							layout.Rigid(negrito(txt(d.th, fonteCond, spHeroTitulo, "Acessos", tema.HeroTxt)).Layout),
							layout.Rigid(layout.Spacer{Height: 2}.Layout),
							layout.Rigid(rotulo(d.th, fonteMono, spHeroSub, d.caminho, tema.HeroSec)),
							layout.Rigid(layout.Spacer{Height: 1}.Layout),
							layout.Rigid(rotulo(d.th, fonteMono, spHeroSub, "Desenvolvido por @JJMoratelli", tema.Fraco)),
						)
					}),
					layout.Rigid(d.numeros),
				)
			})
		}),
	)
}

func (d *dashTab) numeros(gtx layout.Context) layout.Dimensions {
	var maquinas, tela, shell, rdp int
	for _, cx := range d.arq.Conexoes {
		maquinas++
		if cx.Tem(conexoes.VNC) {
			tela++
		}
		if cx.Tem(conexoes.SSH) {
			shell++
		}
		if cx.Tem(conexoes.RDP) {
			rdp++
		}
	}
	grupos := 0
	for _, g := range d.arvore {
		grupos += 1 + len(g.Filhos)
	}

	col := func(n int, cap string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 13, Right: 13}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(negrito(txt(d.th, fonteCond, spHeroNum, fmt.Sprint(n), tema.HeroTxt)).Layout),
					layout.Rigid(rotulo(d.th, fonteMono, spHeroCap, cap, tema.HeroSec)),
				)
			})
		})
	}
	// risco vertical separando os números, bem apagado (.hero-risco)
	risco := layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(34)
		c := tema.HeroTxt
		c.A = 36 // opacity 0.14
		paint.FillShape(gtx.Ops, c, clip.Rect{Max: image.Pt(1, h)}.Op())
		return layout.Dimensions{Size: image.Pt(1, h)}
	})

	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		col(maquinas, "MÁQUINAS"), risco,
		col(tela, "TELA"), risco,
		col(shell, "SHELL"), risco,
		col(rdp, "RDP"), risco,
		col(grupos, "GRUPOS"), risco,
		col(d.abertas(), "ABERTAS"),
	)
}

// barraFiltro fica sobre a lista rolando, então usa VIDRO e não a cor do
// fundo — com fundo chapado ela sumiria dentro da página (tema.py).
// CampoBusca é o campo de filtro, desenhado ou na lateral ou no próprio
// painel — nunca nos dois ao mesmo tempo (um widget.Editor só pode ser
// desenhado uma vez por frame). Quem decide é main.go, olhando o estado
// da lateral.
func (d *dashTab) CampoBusca(gtx layout.Context) layout.Dimensions {
	return d.caixaBusca(gtx, &d.filtroLateral)
}

func (d *dashTab) barraFiltro(gtx layout.Context) layout.Dimensions {
	for d.btnRecolher.Clicked(gtx) {
		d.expandir = map[string]bool{}
	}
	for d.btnExpandir.Clicked(gtx) {
		d.expandirTudo()
	}
	if d.btnNova.Clicked(gtx) && d.aoNova != nil {
		d.aoNova("")
	}
	return layout.Inset{Top: 10, Bottom: 6, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, d.campoFiltro),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoBarra(gtx, d.th, &d.btnRecolher, "recolher tudo")
			}),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoBarra(gtx, d.th, &d.btnExpandir, "expandir tudo")
			}),
		)
	})
}

// expandirTudo abre todos os grupos e subgrupos de uma vez.
func (d *dashTab) expandirTudo() {
	var caminhar func(g *conexoes.Grupo)
	caminhar = func(g *conexoes.Grupo) {
		d.expandir[strings.Join(g.Caminho, ";")] = true
		for _, f := range g.Filhos {
			caminhar(f)
		}
	}
	for _, g := range d.arvore {
		caminhar(g)
	}
}

func (d *dashTab) campoFiltro(gtx layout.Context) layout.Dimensions {
	return d.caixaBusca(gtx, &d.filtro)
}

func (d *dashTab) caixaBusca(gtx layout.Context, ed *widget.Editor) layout.Dimensions {
	marcarFoco(gtx.Focused(ed))
	// Enter no campo: se o que está escrito não achou nada, trata como
	// DESTINO e conecta sem cadastrar (ver efemera.go). É o caminho de
	// "me passaram um IP no chat".
	for {
		ev, ok := ed.Update(gtx)
		if !ok {
			break
		}
		if _, sub := ev.(widget.SubmitEvent); sub && d.aoEfemera != nil {
			d.aoEfemera(ed.Text())
		}
	}
	return layout.Inset{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				superficie(gtx, gtx.Constraints.Min, tema.Vidro2, tema.LuzB, 8)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: 7, Bottom: 7, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return icone(gtx, icons.ActionSearch, tema.Fraco, 16)
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							es := material.Editor(d.th, ed, "filtrar máquinas, hosts ou grupos...")
							es.Font = fonteMono
							es.TextSize = spCorpo
							es.Color = tema.Texto
							es.HintColor = tema.Fraco
							return es.Layout(gtx)
						}),
					)
				})
			}),
		)
	})
}

func (d *dashTab) rodape(gtx layout.Context) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			size := gtx.Constraints.Min
			paint.FillShape(gtx.Ops, tema.Barra, clip.Rect{Max: size}.Op())
			paint.FillShape(gtx.Ops, tema.Borda, clip.Rect{Max: image.Pt(size.X, 1)}.Op())
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 5, Bottom: 5, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(rotulo(d.th, fonteMono, spRodape,
						"Acessos "+versaoInstalada(), tema.Fraco)),
					layout.Rigid(layout.Spacer{Width: 12}.Layout),
					layout.Flexed(1, rotulo(d.th, fonteMono, spRodape, d.caminho, tema.Fraco)),
					layout.Rigid(rotulo(d.th, fonteMono, spRodape,
						fmt.Sprintf("%d máquinas", len(d.arq.Conexoes)), tema.Fraco)),
				)
			})
		}),
	)
}

// grupo desenha a linha do grupo (trilho + nome + contagem) e, se aberto,
// os filhos e os cards.
func (d *dashTab) grupo(gtx layout.Context, g *conexoes.Grupo, nivel int, termo string) layout.Dimensions {
	chave := strings.Join(g.Caminho, ";")
	visiveis := filtrar(g.Conexoes, termo)

	aberto := d.expandir[chave]
	if termo != "" {
		// Filtro ativo: grupo sem nada que case some, e o que sobra já
		// aparece aberto — procurar não deveria exigir mais um clique.
		if len(visiveis) == 0 && !temDescendente(g, termo) {
			return layout.Dimensions{}
		}
		aberto = true
	}

	btn := d.clique("grupo:" + chave)
	if btn.Clicked(gtx) {
		d.expandir[chave] = !d.expandir[chave]
	}
	if btn.Hovered() {
		d.grupoSobCursor = chave
	}

	filhos := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return d.linhaGrupo(gtx, g, nivel, aberto, btn.Hovered())
			})
		}),
	}
	if aberto {
		for _, f := range g.Filhos {
			f := f
			filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return d.grupo(gtx, f, nivel+1, termo)
			}))
		}
		if len(visiveis) > 0 {
			filhos = append(filhos, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{
					Left: unit.Dp(14 + 16*float32(nivel)), Right: 14, Top: 4, Bottom: 12,
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return d.grade(gtx, visiveis)
				})
			}))
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

func (d *dashTab) linhaGrupo(gtx layout.Context, g *conexoes.Grupo, nivel int, aberto, hover bool) layout.Dimensions {
	seta := icons.NavigationChevronRight
	if aberto {
		seta = icons.NavigationExpandMore
	}
	cor := corCategoria(g.Caminho[0])
	tamNome := spGrupoTitulo
	corNome := tema.Texto
	if nivel > 0 {
		tamNome = spSubgrupo
		corNome = tema.Sec
	}

	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			if hover {
				superficie(gtx, gtx.Constraints.Min, tema.Vidro1, transparente, 7)
			}
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{
				Top: 5, Bottom: 5,
				Left:  unit.Dp(12 + 16*float32(nivel)),
				Right: 14,
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					// Selecionar o grupo inteiro de uma vez: com 40 caixas
					// por loja, marcar uma a uma não é opção. O estado
					// PARCIAL existe porque "algumas selecionadas" não é
					// nem marcado nem desmarcado.
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.caixaGrupo(gtx, g)
					}),
					layout.Rigid(layout.Spacer{Width: 7}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return icone(gtx, seta, tema.Sec, 18)
					}),
					layout.Rigid(layout.Spacer{Width: 4}.Layout),
					// trilho da categoria: 3px SEMPRE presentes (transparente
					// quando não se aplica), senão o texto desloca entre
					// estados — regra dura da skill.
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						w, h := gtx.Dp(3), gtx.Dp(18)
						rr := clip.UniformRRect(image.Rectangle{Max: image.Pt(w, h)}, gtx.Dp(2))
						paint.FillShape(gtx.Ops, cor, rr.Op(gtx.Ops))
						return layout.Dimensions{Size: image.Pt(w, h)}
					}),
					layout.Rigid(layout.Spacer{Width: 8}.Layout),
					layout.Rigid(negrito(txt(d.th, fonteCond, tamNome, g.Nome, corNome)).Layout),
					layout.Rigid(layout.Spacer{Width: 7}.Layout),
					layout.Rigid(rotulo(d.th, fonteMono, spGrupoCont, fmt.Sprint(g.Total()), tema.Fraco)),
				)
			})
		}),
	)
}

// grade quebra os cards em linhas conforme a largura disponível.
func (d *dashTab) grade(gtx layout.Context, lista []conexoes.Conexao) layout.Dimensions {
	larg := gtx.Dp(cardLargura)
	esp := gtx.Dp(10)
	porLinha := max(1, (gtx.Constraints.Max.X+esp)/(larg+esp))

	var linhas []layout.FlexChild
	for i := 0; i < len(lista); i += porLinha {
		fatia := lista[i:min(i+porLinha, len(lista))]
		linhas = append(linhas,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				var cols []layout.FlexChild
				for _, cx := range fatia {
					cx := cx
					cols = append(cols,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							// card QUADRADO: mesma medida nos dois eixos, então
							// a grade fica regular independente de quantas
							// linhas de meta cada conexão tem.
							gtx.Constraints.Min = image.Pt(larg, larg)
							gtx.Constraints.Max = image.Pt(larg, larg)
							return d.card(gtx, cx)
						}),
						layout.Rigid(layout.Spacer{Width: 10}.Layout),
					)
				}
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, cols[:len(cols)-1]...)
			}),
			layout.Rigid(layout.Spacer{Height: 10}.Layout),
		)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, linhas...)
}

// card: elevação por LUZ, não por cor — repouso vidro2, hover vidro3.
// Como o fundo por baixo tem gradiente, o mesmo card não fica idêntico no
// topo e na base da lista, e é isso que o olho lê como vidro. A borda é de
// luz (LuzB): borda cinza sobre translúcido denuncia o truque.
func (d *dashTab) card(gtx layout.Context, cx conexoes.Conexao) layout.Dimensions {
	cor := corCategoria(cx.Grupo[0])
	hov := d.clique("card:" + cx.GrupoStr() + "|" + cx.Nome)

	if hov.Hovered() {
		cx := cx
		d.sobCursor = &cx
	}

	// Clique no corpo do card (nome, host, o vazio) abre no protocolo
	// padrão. É lido ANTES do layout, mas só decidido DEPOIS: o Clickable
	// do card cobre o card inteiro, inclusive os quatro ícones, então
	// clicar em "shell" disparava os dois e a aba que abria era a do
	// protocolo padrão — não a do ícone clicado.
	clicouCorpo := hov.Clicked(gtx)
	d.cliqueEmIcone = false

	dims := hov.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				fundo, borda := tema.Vidro2, tema.LuzB
				if hov.Hovered() {
					fundo, borda = tema.Vidro3, tema.Borda2
				}
				sombra(gtx, size, cardRaio)
				superficie(gtx, size, fundo, borda, cardRaio)
				// Trilho da categoria na borda esquerda, recortado pelo
				// MOLDE do card: sem esse recorte a faixa passava por fora
				// do canto arredondado — era o vazamento da borda.
				molde := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(cardRaio)).Push(gtx.Ops)
				paint.FillShape(gtx.Ops, cor, clip.Rect{Max: image.Pt(gtx.Dp(3), size.Y)}.Op())
				molde.Pop()
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				// O conteúdo OCUPA o quadrado inteiro. Sem isto o card
				// desenhava só a altura do texto enquanto a linha da grade
				// continuava com a altura do quadrado — era daí que vinha
				// aquele rasgo vazio entre uma fileira e a seguinte.
				gtx.Constraints.Min = gtx.Constraints.Max
				return layout.Inset{Top: 11, Bottom: 10, Left: 12, Right: 11}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						// Dois blocos ancorados: identidade no topo,
						// meta + protocolos no rodapé. O card é quadrado e
						// quase sempre sobra altura; deixar a folga SOLTA no
						// meio faz cada card parecer de um tamanho, e é isso
						// que dava a sensação de desproporção.
						// nome + marca de lote na MESMA linha: a caixinha
						// entra à esquerda do nome, como no app original, e
						// o host fica alinhado com o nome logo abaixo.
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return d.caixaSelecao(gtx, cx)
								}),
								layout.Rigid(layout.Spacer{Width: 7}.Layout),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return negrito(txt(d.th, fonteCond, spCardNome, cx.Nome, tema.Texto)).Layout(gtx)
								}),
							)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: 22}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								// O estado de vida SUBLINHA o host: é o dado
								// a que ele se refere. Verde respondeu,
								// vermelho não; sem linha = ainda não
								// perguntei, que é diferente de "está fora".
								dims := rotulo(d.th, fonteMono, spCardHost, cx.Host, tema.Sec)(gtx)
								if cv, mostra := corDaVida(d.estadoVida(cx)); mostra {
									alt := gtx.Dp(2)
									y := dims.Size.Y - alt
									rr := clip.UniformRRect(image.Rect(0, y, dims.Size.X, y+alt), gtx.Dp(1))
									paint.FillShape(gtx.Ops, cv, rr.Op(gtx.Ops))
								}
								return dims
							})
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, gtx.Constraints.Min.Y)}
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return d.meta(gtx, cx)
						}),
						layout.Rigid(layout.Spacer{Height: 7}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return d.botoes(gtx, cx)
						}),
					)
				})
			}),
		)
	})

	if clicouCorpo && !d.cliqueEmIcone && d.travaAbrir == 0 {
		if p, ok := protocoloPadrao(cx); ok {
			d.abrirEm(cx, p, true)
		}
	}
	return dims
}

// meta são as linhas apagadas com o resumo de cada protocolo configurado
// ("tela 5900 · encaixar · auto"), como no card do app original.
func (d *dashTab) meta(gtx layout.Context, cx conexoes.Conexao) layout.Dimensions {
	var linhas []layout.FlexChild
	add := func(s string) {
		linhas = append(linhas, layout.Rigid(rotulo(d.th, fonteMono, spCardMeta, s, tema.Fraco)))
	}
	if cx.Tem(conexoes.VNC) {
		s := fmt.Sprintf("tela %d · %s", cx.VNC.Porta, cx.VNC.Modo)
		if cx.VNC.Auto {
			s += " · auto"
		}
		if cx.VNC.Ronly {
			s += " · só ver"
		}
		add(s)
	}
	if cx.Tem(conexoes.SSH) {
		s := fmt.Sprintf("ssh %s@%d", cx.SSH.Usuario, cx.SSH.Porta)
		if cx.SSH.Auto {
			s += " · auto"
		}
		add(s)
	}
	if cx.Tem(conexoes.RDP) {
		s := fmt.Sprintf("rdp %s@%d · %s", cx.RDP.Usuario, cx.RDP.Porta, cx.RDP.Tela)
		add(s)
	}
	if len(linhas) == 0 {
		add("sem protocolo configurado")
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, linhas...)
}

// botoes é a fileira de protocolos do card: indicador e botão no mesmo
// controle. Slots SEMPRE presentes, mesmo desligados (apagados e inertes):
// ícone que aparece e some obriga a reler o cartão toda vez; posição fixa
// se aprende e para de ser lida (gtk.md §6).
func (d *dashTab) botoes(gtx layout.Context, cx conexoes.Conexao) layout.Dimensions {
	var filhos []layout.FlexChild
	for _, e := range protocolos {
		e := e
		ligado := cx.Tem(e.p)
		btn := d.clique(fmt.Sprintf("%s|%s|%s", cx.GrupoStr(), cx.Nome, e.p))
		if ligado && btn.Hovered() {
			d.protoSobCursor = e.p
		}
		if ligado && btn.Clicked(gtx) {
			// AVISA que o clique foi num ícone: o Clickable do card cobre
			// o card inteiro, inclusive os quatro ícones, e sem esta marca
			// os dois disparavam — clicar em "shell" abria o shell E a
			// tela (o protocolo padrão do corpo).
			d.cliqueEmIcone = true
			if d.travaAbrir == 0 {
				d.abrirEm(cx, e.p, true)
			}
		}

		conteudo := func(gtx layout.Context) layout.Dimensions {
			// largura = a fatia inteira que o Flexed deu, pra os quatro
			// slots cobrirem o comprimento da linha do card.
			size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(icoAltura))
			gtx.Constraints.Min = size

			// Caixa SEMPRE do mesmo tamanho, em qualquer estado: o que muda
			// é só cor. Configurado ganha o fundo tênue e o glifo na cor do
			// protocolo; não configurado fica apagado e inerte, mas continua
			// ocupando o slot — ícone que aparece e some obriga a reler o
			// cartão toda vez (gtk.md §6).
			fundo, borda, cor := tema.Vidro2, tema.LuzB, tema.Fraco
			if ligado {
				fundo, cor, borda = e.fundo(), e.cor(), transparente
				if btn.Hovered() {
					borda = e.cor()
				}
			}
			superficie(gtx, size, fundo, borda, icoRaio)
			layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return icone(gtx, e.ic, cor, 19)
			})
			return layout.Dimensions{Size: size}
		}

		filhos = append(filhos,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if !ligado {
					return conteudo(gtx)
				}
				return btn.Layout(gtx, conteudo)
			}),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
		)
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, filhos[:len(filhos)-1]...)
}

// ---------------------------------------------------------------- apoio

func filtrar(lista []conexoes.Conexao, termo string) []conexoes.Conexao {
	if termo == "" {
		return lista
	}
	var out []conexoes.Conexao
	for _, cx := range lista {
		if strings.Contains(strings.ToLower(cx.Nome), termo) ||
			strings.Contains(strings.ToLower(cx.Host), termo) ||
			strings.Contains(strings.ToLower(cx.GrupoStr()), termo) {
			out = append(out, cx)
		}
	}
	return out
}

func temDescendente(g *conexoes.Grupo, termo string) bool {
	for _, f := range g.Filhos {
		if len(filtrar(f.Conexoes, termo)) > 0 || temDescendente(f, termo) {
			return true
		}
	}
	return false
}

// corCategoria dá uma cor estável por grupo de topo. Cor de categoria
// NUNCA significa erro — por isso a cor de erro fica fora da lista.
func corCategoria(nome string) color.NRGBA {
	paleta := []color.NRGBA{tema.Azul, tema.Verde, tema.Roxo, tema.AtencaoFg, tema.OkFg}
	var soma int
	for _, r := range nome {
		soma += int(r)
	}
	return paleta[soma%len(paleta)]
}

// ---------------------------------------------------- seleção em massa

// chaveCx identifica uma conexão: nome se repete entre lojas, o par
// grupo+nome não.
func chaveCx(cx conexoes.Conexao) string { return cx.GrupoStr() + "|" + cx.Nome }

func (d *dashTab) selecionada(cx conexoes.Conexao) bool {
	_, ok := d.selecao[chaveCx(cx)]
	return ok
}

func (d *dashTab) alternarSelecao(cx conexoes.Conexao) {
	k := chaveCx(cx)
	if _, ok := d.selecao[k]; ok {
		delete(d.selecao, k)
		return
	}
	d.selecao[k] = cx
}

// barraSelecao só existe quando há seleção — barra de ações em massa
// aparece com a seleção e traz o contador (regra da skill). Sem seleção
// ela não ocupa altura nenhuma.
func (d *dashTab) barraSelecao(gtx layout.Context) layout.Dimensions {
	if len(d.selecao) == 0 {
		return layout.Dimensions{}
	}
	if d.btnLimparSel.Clicked(gtx) {
		d.selecao = map[string]conexoes.Conexao{}
	}
	if d.btnMassa.Clicked(gtx) && d.aoExecMassa != nil {
		d.aoExecMassa(d.listaSelecionada())
	}

	return layout.Inset{Top: 2, Bottom: 6, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				superficie(gtx, gtx.Constraints.Min, tema.AzulFraco, tema.Azul, 8)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			},
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(negrito(txt(d.th, fonteMono, spSecundario,
							fmt.Sprintf("%d máquina(s) selecionada(s)", len(d.selecao)), tema.Texto)).Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: gtx.Constraints.Min}
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return botaoBarra(gtx, d.th, &d.btnLimparSel, "limpar seleção")
						}),
						layout.Rigid(layout.Spacer{Width: 6}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return botaoPrimario(gtx, d.th, &d.btnMassa,
								fmt.Sprintf("Executar em %d", len(d.selecao)))
						}),
					)
				})
			},
		)
	})
}

// listaSelecionada devolve a seleção em ordem estável (a do arquivo), pra
// a aba de massa não embaralhar a lista a cada execução.
func (d *dashTab) listaSelecionada() []conexoes.Conexao {
	var out []conexoes.Conexao
	for _, cx := range d.arq.Conexoes {
		if d.selecionada(cx) {
			out = append(out, cx)
		}
	}
	return out
}

// caixaSelecao é a marca de lote do card: discreta, some no fundo até ser
// marcada — não deve competir com o nome da máquina (tema.py).
func (d *dashTab) caixaSelecao(gtx layout.Context, cx conexoes.Conexao) layout.Dimensions {
	btn := d.clique("sel:" + chaveCx(cx))
	if btn.Clicked(gtx) {
		d.alternarSelecao(cx)
	}
	marcada := d.selecionada(cx)
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lado := gtx.Dp(15)
		gtx.Constraints.Min = image.Pt(lado, lado)
		gtx.Constraints.Max = image.Pt(lado, lado)
		fundo, borda := transparente, tema.LuzB
		if btn.Hovered() {
			borda = tema.Borda2
		}
		if marcada {
			fundo, borda = tema.Azul, tema.Azul
		}
		superficie(gtx, image.Pt(lado, lado), fundo, borda, 4)
		if marcada {
			layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return icone(gtx, icons.NavigationCheck, hex(0xffffff), 11)
			})
		}
		return layout.Dimensions{Size: image.Pt(lado, lado)}
	})
}

// todasDoGrupo devolve as conexões do grupo e de todos os subgrupos.
func todasDoGrupo(g *conexoes.Grupo) []conexoes.Conexao {
	out := append([]conexoes.Conexao{}, g.Conexoes...)
	for _, f := range g.Filhos {
		out = append(out, todasDoGrupo(f)...)
	}
	return out
}

// contarSelecao percorre a árvore SEM alocar: a caixa de cada grupo
// precisa desse número a cada quadro, e montar uma fatia com as 54
// conexões da loja 60 vezes por segundo era o "travadinho" ao marcar
// vários.
func (d *dashTab) contarSelecao(g *conexoes.Grupo) (marcadas, total int) {
	for _, cx := range g.Conexoes {
		total++
		if _, ok := d.selecao[chaveCx(cx)]; ok {
			marcadas++
		}
	}
	for _, f := range g.Filhos {
		m, t := d.contarSelecao(f)
		marcadas += m
		total += t
	}
	return
}

// caixaGrupo marca/desmarca o grupo inteiro. Três estados: nenhuma,
// algumas (traço) e todas — o traço é o "indeterminate" que a skill pede,
// sem ele um grupo meio selecionado parece não selecionado.
func (d *dashTab) caixaGrupo(gtx layout.Context, g *conexoes.Grupo) layout.Dimensions {
	marcadas, total := d.contarSelecao(g)
	btn := d.clique("selg:" + strings.Join(g.Caminho, ";"))
	if btn.Clicked(gtx) {
		// só aqui a fatia é montada: no clique, não a cada quadro.
		lista := todasDoGrupo(g)
		if marcadas == total {
			for _, cx := range lista {
				delete(d.selecao, chaveCx(cx))
			}
		} else {
			for _, cx := range lista {
				d.selecao[chaveCx(cx)] = cx
			}
		}
		marcadas, total = d.contarSelecao(g)
	}
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lado := gtx.Dp(15)
		gtx.Constraints.Min = image.Pt(lado, lado)
		gtx.Constraints.Max = image.Pt(lado, lado)
		fundo, borda := transparente, tema.LuzB
		if btn.Hovered() {
			borda = tema.Borda2
		}
		if marcadas > 0 {
			fundo, borda = tema.Azul, tema.Azul
		}
		superficie(gtx, image.Pt(lado, lado), fundo, borda, 4)
		switch {
		case marcadas == total && marcadas > 0:
			layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return icone(gtx, icons.NavigationCheck, hex(0xffffff), 11)
			})
		case marcadas > 0:
			// traço do estado parcial
			larg, alt := gtx.Dp(8), gtx.Dp(2)
			off := image.Pt((lado-larg)/2, (lado-alt)/2)
			defer op.Offset(off).Push(gtx.Ops).Pop()
			paint.FillShape(gtx.Ops, hex(0xffffff), clip.Rect{Max: image.Pt(larg, alt)}.Op())
		}
		return layout.Dimensions{Size: image.Pt(lado, lado)}
	})
}

// aoNovaGrupo abre o menu de um grupo: por enquanto, só criar conexão
// dentro dele (renomear/remover grupo mexe em N seções e fica para
// quando houver desfazer).
func (d *dashTab) aoNovaGrupo(caminhoGrupo string) {
	if d.aoNova == nil {
		return
	}
	abrirMenu(d.ultimaPos.Add(offsetConteudo), []*itemMenu{
		{rotulo: "Nova conexão aqui…", acao: func() { d.aoNova(caminhoGrupo) }},
	})
}

// protocoloPadrao é o que abre ao clicar no card sem mirar num ícone:
// tela primeiro, senão shell, senão RDP — a mesma ordem do app original.
func protocoloPadrao(cx conexoes.Conexao) (conexoes.Protocolo, bool) {
	for _, p := range []conexoes.Protocolo{conexoes.VNC, conexoes.SSH, conexoes.RDP} {
		if cx.Tem(p) {
			return p, true
		}
	}
	return "", false
}

// abrirEm abre a conexão; focar=false deixa a aba em segundo plano.
func (d *dashTab) abrirEm(cx conexoes.Conexao, p conexoes.Protocolo, focar bool) {
	if d.abrir == nil {
		return
	}
	abrirEmSegundoPlano = !focar
	d.abrir(cx, p)
	abrirEmSegundoPlano = false
}

// corDaVida: cinza não é pintado (fica só o trilho da categoria), porque
// "não sei" não deve gritar nada.
func corDaVida(e vida.Estado) (color.NRGBA, bool) {
	switch e {
	case vida.Viva:
		return tema.Verde, true
	case vida.Morta:
		return tema.ErroFg, true
	}
	return color.NRGBA{}, false
}
