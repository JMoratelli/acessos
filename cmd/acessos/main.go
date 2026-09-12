// acessos é o app unificado: uma janela, várias abas (VNC/RDP/SSH/SFTP),
// reaproveitando internal/vnc, internal/rdp e internal/grab tal como os
// binários separados cmd/vncview e cmd/rdpview já validados.
package main

import (
	"flag"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"acessos-go/internal/chaveiro"
	"acessos-go/internal/conexoes"
	"acessos-go/internal/grab"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget/material"
)

// connSpecs é o valor de um -conn repetido: "chave=valor,chave=valor,...".
type connSpecs []map[string]string

func (c *connSpecs) String() string { return "" }
func (c *connSpecs) Set(s string) error {
	m := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("par inválido %q (esperado chave=valor)", kv)
		}
		m[k] = v
	}
	if m["type"] == "" {
		return fmt.Errorf("-conn sem type= : %q", s)
	}
	*c = append(*c, m)
	return nil
}

func specInt(spec map[string]string, key string, def int) int {
	v, ok := spec[key]
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func main() {
	var specs connSpecs
	flag.Var(&specs, "conn",
		"conexão (repetível): type=vnc|rdp|ssh|sftp,host=...,port=...,user=...,pass=...,domain=...")
	ini := flag.String("ini", "", "conexoes.ini a usar (padrão: o do diretório de configuração)")
	filtro := flag.String("filtro", "", "já abre o Painel filtrado por este termo")
	temaFlag := flag.String("tema", "", "claro|escuro — sobrepõe o [geral] tema do .ini")
	flag.BoolVar(&reguaLigada, "regua", false, "desenha a régua de alinhamento por cima da interface (Ctrl+G liga/desliga)")
	flag.Parse()

	// Sem argumento nenhum o app NÃO pede uso e sai: ele abre no
	// inventário padrão. Aberto pelo menu (ou pelo Flatpak, que não passa
	// argumento) era assim que ele morria antes de aparecer.
	*ini = caminhoINIPadrao(*ini)
	if err := garantirINI(*ini); err != nil {
		fmt.Fprintf(os.Stderr, "não consegui preparar %s: %v\n", *ini, err)
	}

	filtroInicial = *filtro

	w := new(app.Window)
	// Decoração PRÓPRIA: a barra do sistema gastaria uma faixa inteira de
	// altura só com o nome da janela. Aqui a mesma faixa leva identidade,
	// menu e botões de janela (ver topbar.go).
	// Nasce MAXIMIZADA: é uma ferramenta de operação, sempre com tela
	// remota ou lista grande dentro — abrir em janelinha só obrigava a
	// maximizar toda vez.
	// app_id do Wayland. É por ele que o ambiente casa a janela com o
	// arquivo .desktop — sem bater, o dock mostra o ícone genérico de
	// executável mesmo com o app aberto (a mesma armadilha documentada no
	// app original). Tem que ser igual ao StartupWMClass do .desktop.
	app.ID = "org.jj.Acessos"
	w.Option(app.Title("Acessos"), app.Decorated(false), app.Maximized.Option())

	th := material.NewTheme()
	// IBM Plex embutida (ver fontes.go) — e não a Go font nem as do
	// sistema: é a família que o tema.py pede, e o binário tem que sair
	// igual no Linux e no Windows.
	th.Shaper = shaperDoApp()
	// as abas são criadas por newTab(w, spec), sem tema no caminho — este
	// é o mesmo material.Theme do resto da janela.
	temaApp = th

	var tabs []Tab
	for _, spec := range specs {
		t, err := newTab(w, spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "conexão inválida (%v): %v\n", spec, err)
			continue
		}
		tabs = append(tabs, t)
	}

	bar := newTabBar(tabs)

	// recarregarIni relê o arquivo e troca o conteúdo do Painel sem
	// derrubar as abas de conexão já abertas.
	recarregarIni := func() {}
	var painelRef *dashTab

	// -tema vence o [geral] tema do .ini (serve pra conferir os dois temas
	// sem editar o arquivo do usuário).
	aplicarTemaFlag := func() {
		switch *temaFlag {
		case "escuro":
			tema = temaEscuro
		case "claro":
			tema = temaClaro
		}
	}
	aplicarTemaFlag()

	// Painel é sempre a primeira aba, fixa — evita o estado "zero abas" se
	// o operador fechar todas as conexões (ver gtk.md, padrão B).
	if *ini != "" {
		arq, err := conexoes.Carregar(*ini)
		if err != nil {
			fmt.Fprintf(os.Stderr, "não consegui ler %s: %v\n", *ini, err)
			os.Exit(1)
		}
		fmt.Printf("%s: %d conexões\n", *ini, len(arq.Conexoes))
		caminhoINI = *ini
		// chaveiro.ini mora ao lado do conexoes.ini; ausente = instalação
		// antiga, com o cofre dentro do próprio conexoes.ini.
		if ch, err := chaveiro.Carregar(filepath.Join(filepath.Dir(*ini), "chaveiro.ini")); err != nil {
			fmt.Fprintln(os.Stderr, err)
		} else if ch != nil {
			chaveiroAtual = ch
			fmt.Printf("chaveiro: %d credencial(is)\n", len(ch.Credenciais))
		}
		// [geral] tema=claro|escuro — o mesmo arquivo manda nos dois apps.
		if arq.Geral["tema"] == "escuro" {
			tema = temaEscuro
		}
		// [geral] fonte=0|1|2 — escala da interface (ver fonte.go).
		if n, err := strconv.Atoi(arq.Geral["fonte"]); err == nil {
			nivelFonte = n
		}
		aplicarTemaFlag()
		destrancarCofre(arq)
		abrir := func(cx conexoes.Conexao, p conexoes.Protocolo) {
			abrirConexao(w, bar, arq, cx, p)
		}
		abertas := func() int { return len(bar.tabs) - 1 }
		painel := newDashTab(th, arq, *ini, abrir, abertas)
		// Execução em massa: o Painel só junta a seleção; quem sabe rodar
		// é a aba, com o motor portado do Mass SSH Executer.
		// Botão direito no card: editar, duplicar e remover a conexão.
		painel.aoMenuCard = func(cx conexoes.Conexao, pos image.Point) {
			abrirMenu(pos.Add(offsetConteudo), []*itemMenu{
				{rotulo: "Editar…", acao: func() {
					editarConexao(w, *ini, cx, recarregarIni)
				}},
				// Duplicar CADASTRA A PRÓXIMA: nome e IP incrementados,
				// pulando o que já existe, e abre o editor — copiar
				// idêntico nunca é o que se quer num parque sequencial.
				{rotulo: "Duplicar…", acao: func() {
					duplicarConexao(w, *ini, painel.arq, cx, recarregarIni)
					w.Invalidate()
				}},
				{rotulo: "Detectar plataforma", acao: func() {
					detectarPlataforma(w, []conexoes.Conexao{cx}, recarregarIni)
				}},
				{rotulo: "Copiar host", acao: func() {
					gtxClipboard(w, cx.Host)
				}},
				{rotulo: "Remover…", perigo: true, acao: func() {
					confirmarDestrutivo(w, "Remover conexão",
						[]string{cx.Nome + "  (" + cx.Host + ")"},
						"Remover definitivamente", func() {
							if err := conexoes.Remover(*ini, cx.Nome); err != nil {
								fmt.Fprintln(os.Stderr, err)
								return
							}
							recarregarIni()
							w.Invalidate()
						})
				}},
			})
			w.Invalidate()
		}
		painel.aoInvalidar = w.Invalidate
		// Conexão efêmera: destino digitado vira sessão sem passar pelo
		// cadastro. Nada é gravado — vive enquanto a aba existir.
		painel.aoEfemera = func(destino string) {
			cx, p, ok := interpretarAlvo(destino)
			if !ok {
				return
			}
			for _, j := range arq.Conexoes {
				if strings.EqualFold(j.Nome, strings.TrimSpace(destino)) {
					return // é uma máquina cadastrada: o filtro já resolve
				}
			}
			abrirConexao(w, bar, arq, cx, p)
		}
		painel.aoNova = func(grupo string) {
			novaConexao(w, *ini, grupo, recarregarIni)
			w.Invalidate()
		}
		painel.aoExecMassa = func(sel []conexoes.Conexao) {
			if len(sel) == 0 {
				return
			}
			t := newMassaTab(w, sel, caminhoSnippets(*ini))
			bar.appendCom(t, fmt.Sprintf("massa|%d|%s", len(sel), sel[0].Nome))
			w.Invalidate()
		}
		painelRef = painel
		bar.prependPinned(painel)
		recarregarINI = func() { recarregarIni() }
		// Ajustes: apontar para outro conexoes.ini sem reiniciar. O cofre
		// volta a ficar trancado de propósito — senha mestra de um arquivo
		// não abre o outro.
		trocarArquivoINI = func(caminho string) error {
			novo, err := conexoes.Carregar(caminho)
			if err != nil {
				return err
			}
			*ini = caminho
			caminhoINI = caminho
			cofreAberto = nil
			chaveiroAtual = nil
			if ch, err := chaveiro.Carregar(filepath.Join(filepath.Dir(caminho), "chaveiro.ini")); err == nil && ch != nil {
				chaveiroAtual = ch
			}
			painel.recarregarCom(novo)
			return nil
		}
		recarregarIni = func() {
			novo, err := conexoes.Carregar(*ini)
			if err != nil {
				fmt.Fprintf(os.Stderr, "recarregar %s: %v\n", *ini, err)
				return
			}
			painel.recarregarCom(novo)
			fmt.Printf("%s recarregado: %d conexões\n", *ini, len(novo.Conexoes))
		}
	} else {
		painelRef = newDashTab(th, &conexoes.Arquivo{}, "(sem conexoes.ini)",
			func(conexoes.Conexao, conexoes.Protocolo) {}, func() int { return len(bar.tabs) - 1 })
		bar.prependPinned(painelRef)
	}

	// Quem abriu o app com -conn quer ver a conexão, não o painel: foca a
	// primeira aba de conexão (o Painel continua ali, fixo, no índice 0).
	if len(bar.tabs) > 1 {
		bar.selectIndex(1)
	}

	checarAtualizacao(w)

	go func() {
		if err := runApp(w, th, bar, recarregarIni, painelRef); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		for _, t := range bar.tabs {
			t.Close()
		}
		os.Exit(0)
	}()

	app.Main()
}

func newTab(w *app.Window, spec map[string]string) (Tab, error) {
	switch spec["type"] {
	case "vnc", "rdp":
		// VNC e RDP existem só onde as bibliotecas C estão resolvidas
		// (hoje, o build de Linux) — ver protocolos_*.go.
		return novaAbaTela(w, spec)
	case "ssh":
		return newSSHTab(w, spec)
	case "sftp":
		return newSFTPTab(w, spec)
	default:
		return nil, fmt.Errorf("type=%q desconhecido", spec["type"])
	}
}

// currentGrab é a captura Wayland ativa (teclado + clipboard + atalhos) —
// uma só pra janela inteira, compartilhada por todas as abas. Os callbacks
// de teclado/clipboard são roteados pra aba ATIVA no momento (ver
// runApp/activeTab), não pra uma sessão fixa.
var currentGrab atomic.Pointer[grab.Handle]

var contentTag = new(int)

// filtroInicial preenche o campo de filtro na abertura (flag -filtro).
var filtroInicial string

// ctrlDown, pendingToggleSidebar e pendingCloseActive existem porque o
// callback de teclado (grab.Start) roda numa goroutine diferente da que
// processa FrameEvent/desenha: em vez de mexer direto em bar/sb dali (uma
// corrida de dados de verdade — duas goroutines lendo/escrevendo os mesmos
// campos sem lock), ele só marca a intenção; o frame seguinte aplica.
var (
	// ctrlDown vem do teclado cru do Wayland (internal/grab), que é o
	// único lugar desta pilha onde o estado dos modificadores é
	// confiável — o key.Event do Gio não preenche Modifiers aqui.
	ctrlDown             atomic.Bool
	pendingToggleSidebar atomic.Bool
	pendingCloseActive   atomic.Bool
)

func runApp(w *app.Window, th *material.Theme, bar *tabBar, recarregar func(), painel *dashTab) error {
	var ops op.Ops
	sb := newSidebar()
	sb.painel = painel // a lateral mostra a MESMA árvore do painel
	if painel != nil {
		// Menu da máquina na lateral: as mesmas ações do card, mais abrir
		// cada protocolo direto (na lateral não há os quatro ícones).
		sb.aoMenuHost = func(cx conexoes.Conexao, pos image.Point) {
			itens := []*itemMenu{}
			for _, e := range protocolos {
				e := e
				if !cx.Tem(e.p) {
					continue
				}
				itens = append(itens, &itemMenu{
					rotulo: "Abrir " + e.rotulo,
					acao:   func() { painel.abrirEm(cx, e.p, true) },
				})
			}
			itens = append(itens,
				&itemMenu{rotulo: "Abrir em segundo plano", acao: func() {
					if p, ok := protocoloPadrao(cx); ok {
						painel.abrirEm(cx, p, false)
					}
				}},
				&itemMenu{rotulo: "Copiar host", acao: func() { gtxClipboard(w, cx.Host) }},
				&itemMenu{rotulo: "Detectar plataforma", acao: func() {
					detectarPlataforma(w, []conexoes.Conexao{cx}, recarregarINI)
				}},
				&itemMenu{rotulo: "Editar…", acao: func() { editarConexao(w, caminhoINI, cx, recarregarINI) }},
				&itemMenu{rotulo: "Duplicar…", acao: func() {
					duplicarConexao(w, caminhoINI, painel.arq, cx, recarregarINI)
				}},
				&itemMenu{rotulo: "Remover…", perigo: true, acao: func() {
					confirmarDestrutivo(w, "Remover conexão",
						[]string{cx.Nome + "  (" + cx.Host + ")"}, "Remover definitivamente",
						func() {
							if err := conexoes.Remover(caminhoINI, cx.Nome); err != nil {
								fmt.Fprintln(os.Stderr, err)
								return
							}
							recarregarINI()
						})
				}},
			)
			abrirMenu(pos, itens)
			w.Invalidate()
		}
		// Menu do GRUPO: abrir tudo em lote, por protocolo.
		sb.aoMenuGrupo = func(g *conexoes.Grupo, pos image.Point) {
			abrirMenu(pos, []*itemMenu{
				{rotulo: fmt.Sprintf("Abrir grupo (%d máquinas)…", g.Total()), acao: func() {
					abrirGrupo(w, painel, g)
				}},
				{rotulo: "Nova conexão aqui…", acao: func() {
					novaConexao(w, caminhoINI, strings.Join(g.Caminho, ";"), recarregarINI)
				}},
				{rotulo: "Detectar plataforma do grupo", acao: func() {
					detectarPlataforma(w, todasDoGrupo(g), recarregarINI)
				}},
			})
			w.Invalidate()
		}
	}
	tb := &topBar{}

	activeTab := func() Tab { return bar.active() }

	for {
		e := w.Event()
		switch e := e.(type) {
		case app.DestroyEvent:
			// Não soltar o grab aqui — mesma razão documentada em
			// cmd/vncview: o Gio pode já estar desmontando a conexão
			// Wayland, e mexer nela nesse ponto derrubava o processo.
			return e.Err

		default:
			// Entrada específica de plataforma (no Linux, o grab de
			// teclado/clipboard por Wayland) — ver entrada_*.go.
			tratarEventoPlataforma(w, e, activeTab)

		case app.ConfigEvent:
			// O Gio nunca manda zxdg_toplevel_decoration_v1.set_mode, então
			// app.Decorated(false) NÃO tira a barra do KWin: o compositor
			// responde SERVER_SIDE e é isso que chega aqui. Em vez de
			// empilhar a nossa barra debaixo da dele, a nossa se adapta e
			// esconde os botões de janela (que já existem logo acima).
			sistemaDecora = e.Config.Decorated
			janelaMaximizada = e.Config.Mode != app.Windowed

		case app.FrameEvent:
			// a marca é recalculada a cada quadro pelos campos de texto
			focoEmCampo.Store(false)
			atualizarInibicao(activeTab())
			gtx := app.NewContext(&ops, e)
			// A escala da interface entra AQUI, antes de qualquer layout:
			// tudo o que é medido em Dp ou Sp no quadro já nasce no
			// tamanho escolhido, sem cada widget precisar saber disso.
			gtx.Metric = escalaFonte(gtx.Metric)

			// UM gradiente só, na janela inteira — lateral, abas e cards
			// são translúcidos POR CIMA dele. Sem fundo variável não existe
			// vidro: translucidez sobre cor chapada resolve para outra cor
			// chapada (tema.py). Por isso o fundo não vive dentro de cada
			// painel, senão cada um ganharia a própria cópia das camadas e
			// as emendas apareceriam.
			// Cantos arredondados: com a decoração do sistema fora (CSD),
			// a moldura é NOSSA. Recortamos a janela inteira num retângulo
			// arredondado e não pintamos os cantos — eles saem do surface
			// com alfa 0 e o compositor compõe o que está atrás. Em
			// maximizado/tela cheia o canto volta a ser reto, senão sobra
			// uma falha contra a borda da tela.
			raio := 0
			if !janelaMaximizada && !sistemaDecora {
				raio = gtx.Dp(janelaRaio)
			}
			recorte := clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max}, raio).Push(gtx.Ops)

			// UM gradiente só, na janela inteira — lateral, abas e cards
			// são translúcidos POR CIMA dele. Sem fundo variável não existe
			// vidro: translucidez sobre cor chapada resolve para outra cor
			// chapada (tema.py). Por isso o fundo não vive dentro de cada
			// painel, senão cada um ganharia a própria cópia das camadas e
			// as emendas apareceriam.
			fundoJanela(gtx, gtx.Constraints.Max)

			if pendingToggleSidebar.Swap(false) {
				sb.ciclar()
			}
			if pendingCloseActive.Swap(false) {
				bar.closeActive()
			}

			layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return tb.layout(gtx, w, th, sb.largura(gtx), acoesTopo{
						menu:       sb.ciclar,
						recarregar: recarregar,
						nova: func() {
							if caminhoINI != "" {
								novaConexao(w, caminhoINI, "", recarregarINI)
							}
						},
						chaveiro: func() { abrirChaveiro(w) },
						snippets: func() {
							abrirSnippets(w, caminhoSnippets(caminhoINI), nil)
						},
						ajustes: func() {
							abrirAjustes(w, caminhoINI)
						},
						abrirCofre: func() {
							// cadeado aberto tranca de volta; fechado pede a senha.
							if cofreAberto != nil {
								cofreAberto = nil
								return
							}
							if painel != nil && painel.arq != nil {
								pedirCofre(w, painel.arq, caminhoINI, recarregarINI, nil)
							}
						},
						// A+ cicla 0 → 1 → 2 → 0 e grava no .ini na hora:
						// é preferência de quem usa, não da sessão.
						trocarFonte: func() {
							n := proximoNivelFonte()
							if caminhoINI == "" {
								return
							}
							if err := conexoes.SalvarGeral(caminhoINI,
								map[string]string{"fonte": strconv.Itoa(n)}); err != nil {
								fmt.Fprintln(os.Stderr, err)
							}
						},
						trocarTema: func() {
							// tema é um dicionário só: trocar a variável
							// troca o app inteiro (ver tema.go).
							if tema.Fundo == temaClaro.Fundo {
								tema = temaEscuro
							} else {
								tema = temaClaro
							}
						},
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							var busca layout.Widget
							if painel != nil {
								busca = painel.CampoBusca
							}
							return sb.layout(gtx, th, "painel", func(string) { bar.selectIndex(0) }, busca)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return bar.layout(gtx, th)
								}),
								// Barra de sessão: só nas abas de conexão, entre a tira
								// de abas e o conteúdo. É onde o operador vê, sem abrir
								// nada, se a sessão está viva, para onde ela aponta e em
								// que resolução — a mesma barra do app original.
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									a, ok := activeTab().(abaSessao)
									if !ok {
										return layout.Dimensions{}
									}
									return layoutBarraSessao(gtx, th, a)
								}),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									size := gtx.Constraints.Max
									// De onde a área de conteúdo começa na
									// janela: o menu de contexto recebe do
									// Painel a posição do ponteiro relativa
									// a ela, e precisa somar isto.
									offsetConteudo = image.Pt(larguraLateralAtual, alturaTopoAtual)
									area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
									event.Op(gtx.Ops, contentTag)
									area.Pop()

									t := activeTab()

									for {
										ev, ok := gtx.Source.Event(
											pointer.Filter{Target: contentTag, Kinds: pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Scroll},
										)
										if !ok {
											break
										}
										if pe, ok := ev.(pointer.Event); ok && t != nil {
											t.HandlePointer(pe, size)
										}
									}

									if t == nil {
										return layout.Dimensions{Size: size}
									}
									return t.Layout(gtx)
								}),
							)
						}),
					)
				}),
			)

			// O modal vem primeiro e o MENU por cima: o menu também é
			// aberto de dentro de diálogos (escolher credencial do
			// chaveiro no editor de conexão), e ali ele precisa ficar
			// acima, não atrás.
			// Ordem: modal, DEPOIS o rastreio do ponteiro, DEPOIS o menu.
			// O véu do modal engole os eventos de ponteiro (é o que
			// impede clicar no que está atrás), então o rastreio tinha de
			// subir para cima dele — senão, com um diálogo aberto, a
			// posição do ponteiro congelava na última de antes e o menu
			// do chaveiro nascia longe do botão que o abriu.
			layoutModal(gtx, th)
			rastrearPonteiroGlobal(gtx)
			layoutMenu(gtx, th)

			regua(gtx, sb.largura(gtx))
			recorte.Pop()

			e.Frame(gtx.Ops)
		}
	}
}

// clipboardReceiver é implementado pelas abas que participam do clipboard
// do sistema (hoje, VNC e RDP — SSH/SFTP não têm um "clipboard remoto").
type clipboardReceiver interface {
	OnLocalClipboard(text string)
}

// janelaMaximizada evita arredondar os cantos quando a janela está colada
// nas bordas da tela.
var janelaMaximizada bool

// sistemaDecora diz se o compositor está desenhando a decoração dele por
// cima da nossa (ver o app.ConfigEvent no laço de eventos).
var sistemaDecora bool

// querTeclado marca as abas que precisam do teclado cru do Wayland: as
// sessões remotas. As demais (painel, SFTP) usam os widgets do Gio e
// precisam que as teclas sigam o caminho normal.
func querTeclado(t Tab) bool {
	switch t.(type) {
	case telaRemota, *sshTab:
		return true
	}
	return false
}

// inibicaoLigada guarda o estado já aplicado, pra não refazer o pedido a
// cada frame.
var inibicaoLigada bool

// atualizarInibicao liga a captura dos atalhos do compositor só enquanto
// uma TELA remota está em foco. O SSH recebe o teclado cru (precisa de
// Ctrl+C, setas, Tab) mas NÃO inibe: num terminal ninguém espera perder o
// Alt+Tab do próprio desktop.
func atualizarInibicao(t Tab) {
	quer := false
	if _, ok := t.(telaRemota); ok {
		quer = true
	}
	if temDialogo() || focoEmCampo.Load() {
		// diálogo aberto ou campo de texto em foco devolve os atalhos ao
		// compositor: o foco está aqui, não na máquina remota.
		quer = false
	}
	if quer == inibicaoLigada {
		return
	}
	inibicaoLigada = quer
	currentGrab.Load().Inibir(quer)
}

// Posição da área de conteúdo na janela, atualizada a cada quadro. O menu
// de contexto é ancorado no ponteiro, e a posição que a aba conhece é
// relativa à área dela, não à janela.
var (
	offsetConteudo      image.Point
	larguraLateralAtual int
	alturaTopoAtual     int
)

// gtxClipboard põe texto no clipboard do sistema pelo mesmo caminho
// Wayland que as sessões remotas usam (internal/grab) — o clipboard do
// Gio não é confiável nesta pilha, é o mesmo motivo documentado lá.
func gtxClipboard(w *app.Window, texto string) {
	currentGrab.Load().SetClipboardText(texto)
	w.Invalidate()
}

// Posição do ponteiro na JANELA, para ancorar menus abertos de qualquer
// lugar (o "⌁ teclas" das abas remotas, por exemplo). Mesma técnica do
// Painel: uma área só, por cima de tudo, com PassOp — por baixo ela nunca
// receberia nada, porque o hit-test do Gio salta para o nó pai ao
// encontrar área sem passagem.
var (
	tagPonteiroGlobal = new(int)
	posPonteiroMu     sync.Mutex
	posPonteiro       image.Point
)

func rastrearPonteiroGlobal(gtx layout.Context) {
	for {
		ev, ok := gtx.Source.Event(pointer.Filter{
			Target: tagPonteiroGlobal,
			Kinds:  pointer.Move | pointer.Press | pointer.Drag,
		})
		if !ok {
			break
		}
		if pe, isP := ev.(pointer.Event); isP {
			posPonteiroMu.Lock()
			posPonteiro = image.Pt(int(pe.Position.X), int(pe.Position.Y))
			posPonteiroMu.Unlock()
		}
	}
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, tagPonteiroGlobal)
	area.Pop()
	pass.Pop()
}

func ultimaPosPonteiro() image.Point {
	posPonteiroMu.Lock()
	defer posPonteiroMu.Unlock()
	return posPonteiro
}

// ctrlPressionado diz se o Ctrl está segurado agora. Serve para gestos de
// mouse (Ctrl+clique para marcar arquivo no SFTP, Ctrl+roda no terminal).
func ctrlPressionado() bool { return ctrlDown.Load() }
