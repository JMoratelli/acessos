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

	"gio.tools/icons"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/explorer"
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
	// Antes de tudo, inclusive do worker: no Windows é aqui que o
	// OPENSSL_MODULES passa a apontar para os providers que vão junto do
	// .exe. Sem isso a libcrypto embarcada procura num caminho do MSYS2
	// que não existe na máquina do usuário, fica sem MD4/RC4, e o FreeRDP
	// perde NTLM e reconexão automática — ver ossl_windows.go. Quem
	// conecta de verdade é o FILHO, então tem de valer para ele também.
	ajustarOpenSSL()

	// Este processo pode ser um FILHO hospedando uma sessão remota, e não
	// o app. Ele não tem janela, não lê .ini e não mexe no log — só abre
	// o canal com quem o criou. Ver internal/telaproc e telaworker.go.
	if modoWorker() {
		return
	}

	var specs connSpecs
	flag.Var(&specs, "conn",
		"conexão (repetível): type=vnc|rdp|ssh|sftp,host=...,port=...,user=...,pass=...,domain=...")
	ini := flag.String("ini", "", "conexoes.ini a usar (padrão: o do diretório de configuração)")
	filtro := flag.String("filtro", "", "já abre o Painel filtrado por este termo")
	temaFlag := flag.String("tema", "", "claro|escuro — sobrepõe o [geral] tema do .ini")
	flag.BoolVar(&reguaLigada, "regua", false, "desenha a régua de alinhamento por cima da interface (Ctrl+G liga/desliga)")
	servico := flag.Bool("servico", false,
		"roda sem janela, só segurando o atalho global e a caixa de busca (ver servico_linux.go)")
	flag.Parse()

	// Sem argumento nenhum o app NÃO pede uso e sai: ele abre no
	// inventário padrão. Aberto pelo menu (ou pelo Flatpak, que não passa
	// argumento) era assim que ele morria antes de aparecer.
	*ini = caminhoINIPadrao(*ini)
	if err := garantirINI(*ini); err != nil {
		fmt.Fprintf(os.Stderr, "não consegui preparar %s: %v\n", *ini, err)
	}

	// Log em arquivo: no Windows o app não tem stdout (ver log_windows.go).
	// Vem antes de tudo o que pode falhar e depois do caminho do .ini,
	// porque é ao lado dele que o arquivo mora.
	iniciarLog(filepath.Dir(*ini))

	// -servico: este processo não tem janela. Ele segura o atalho global,
	// atende o socket e pipoca a caixa de busca. Ver servico_linux.go.
	if *servico {
		rodarServico(*ini)
		return
	}

	// Instância única, e o serviço do atalho de quebra. Antes daqui não
	// há nada caro montado: se já existe uma janela grande, esta invocação
	// só entrega o que veio na linha de comando e sai — e não fica uma
	// segunda janela sobre o mesmo inventário, nem um segundo registro do
	// mesmo atalho global (que fazia UM aperto de tecla abrir DUAS caixas).
	cliServico, jaTemApp := ligarNoServico(*ini, specs)
	if jaTemApp {
		fmt.Println("já há um Acessos aberto; mandei o pedido para ele")
		return
	}
	if cliServico != nil {
		defer cliServico.Fechar()
	}

	filtroInicial = *filtro

	w := new(app.Window)
	// A janela do app, para quem precisa dela sem tê-la em mão: menus de
	// contexto abertos de dentro de diálogos, e o pedido de confiança de
	// certificado, que é desenhado só aqui (ver certificadodlg.go).
	janelaPrincipal = w
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
	// Theme próprio da caixa de busca, montado AQUI e não na abertura
	// dela: colecaoFontes() reparseia as seis fontes embutidas a cada
	// chamada (fontes.go), e a caixa do atalho global tem de nascer
	// instantânea. O porquê de não reaproveitar o th está no temaBusca,
	// em tema.go.
	temaBusca = material.NewTheme()
	temaBusca.Shaper = shaperDoApp()

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
		// [geral] tema=claro|escuro e fonte=0|1|2 — o mesmo arquivo manda
		// nos dois apps, e quem aplica é o aplicarGeral de persistir.go,
		// que o serviço do atalho global também chama.
		aplicarGeral(arq.Geral)
		// [geral] lateral=0|1 — a lateral volta como estava (ver sidebar.go).
		// Fora do aplicarGeral de propósito: o serviço não tem lateral.
		lateralInicialOculta = arq.Geral["lateral"] == "0"
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
			abrirMenu(janelaPrincipal, pos.Add(offsetConteudo), []*itemMenu{
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
		// Destino avulso: pergunta as credenciais e abre. Um destino fora
		// do inventário não tem senha guardada em lugar nenhum — abrir
		// direto só produzia "autenticação recusada".
		abrirRascunho := func(cx conexoes.Conexao, p conexoes.Protocolo) {
			pedirCredenciaisEfemeras(w, cx, p, func(cx conexoes.Conexao) {
				abrirConexao(w, bar, arq, cx, p)
			})
			w.Invalidate()
		}
		painel.aoRascunho = abrirRascunho
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
			abrirRascunho(cx, p)
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
		painel.aoExcluirMassa = func(sel []conexoes.Conexao) {
			if len(sel) == 0 {
				return
			}
			itens := make([]string, len(sel))
			for i, cx := range sel {
				itens[i] = cx.Nome + "  (" + cx.Host + ")"
			}
			confirmarDestrutivo(w, fmt.Sprintf("Remover %d conexões", len(sel)), itens,
				"Remover definitivamente", func() {
					for _, cx := range sel {
						if err := conexoes.Remover(caminhoINI, cx.Nome); err != nil {
							fmt.Fprintln(os.Stderr, err)
						}
					}
					painel.selecao = map[string]conexoes.Conexao{}
					recarregarINI()
				})
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

			// Grava a escolha em [geral] caminho, no arquivo PADRÃO: sem
			// isto, "Usar este arquivo" só valia pra sessão atual — na
			// próxima vez que o app abrisse, caminhoINIPadrao() voltava a
			// resolver pro padrão, como se nada tivesse sido escolhido.
			padrao := filepath.Join(dirPadrao(), "conexoes.ini")
			apontar := ""
			if caminho != padrao {
				apontar = filepath.Dir(caminho)
			}
			if err := garantirINI(padrao); err != nil {
				fmt.Fprintln(os.Stderr, "não foi possível lembrar o novo caminho:", err)
			} else if err := conexoes.SalvarGeral(padrao, map[string]string{"caminho": apontar}); err != nil {
				fmt.Fprintln(os.Stderr, "não foi possível lembrar o novo caminho:", err)
			}
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
		if err := runApp(w, th, bar, recarregarIni, painelRef, cliServico); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		for _, t := range bar.tabs {
			t.Close()
		}
		// As destacadas não estão mais na tira: sem isto a sessão em
		// janela própria nunca receberia Close, e o processo-filho dela
		// ficaria órfão.
		for _, t := range abasForaDaTira() {
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
	// pendingLimparFocoGio: o grab do Wayland entrega teclado cru a uma
	// aba remota por FORA do roteador do Gio, mas o Wayland ainda manda o
	// MESMO evento pro wl_keyboard interno do Gio também (não existe
	// exclusividade entre dois listeners do mesmo wl_seat). Se algum
	// widget (o "+" de nova conexão, o "x" de fechar aba, etc.) ficou com
	// o FOCO DE TECLADO do Gio de antes de trocar pra aba remota, ele
	// continua "ouvindo" esse fluxo paralelo — e Enter/Espaço digitados
	// dentro da sessão (confirmar `nano arquivo`, salvar e sair) reativam
	// aquele botão como se tivesse sido clicado de novo. Limpar o foco
	// aqui é o mesmo remédio que ControlesSessao já usa por botão
	// (sshtab.go), só que geral: sem foco nenhum, o fluxo paralelo não
	// tem em quem cair.
	pendingLimparFocoGio atomic.Bool
)

func runApp(w *app.Window, th *material.Theme, bar *tabBar, recarregar func(),
	painel *dashTab, cli *clienteServico) error {
	var ops op.Ops
	explorerAcessos = explorer.NewExplorer(w)
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
			abrirMenu(janelaPrincipal, pos, itens)
			w.Invalidate()
		}
		// Menu do GRUPO: abrir tudo em lote, por protocolo.
		sb.aoMenuGrupo = func(g *conexoes.Grupo, pos image.Point) {
			abrirMenu(janelaPrincipal, pos, []*itemMenu{
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

	// ------------------------------------------- caixa de busca global
	// A caixa é uma SEGUNDA janela. Quem segura o atalho do sistema não é
	// mais este processo: é o serviço (ver instancia.go e
	// servico_linux.go). Com o app aberto ele manda o pedido para cá — a
	// janela que acabou de ser usada é a que o compositor deixa tomar o
	// foco; com o app fechado ele abre a caixa sozinho.
	//
	// Foi assim que duas coisas se resolveram de uma vez: abrir o Acessos
	// duas vezes deixou de registrar o atalho duas vezes (um aperto, duas
	// caixas), e o atalho passou a existir sem o app aberto.
	var buscaAberta atomic.Bool
	abrirBusca := func(token string) {
		if !buscaAberta.CompareAndSwap(false, true) {
			return // já há uma na tela; apertar de novo não empilha
		}
		// O inventário é RELIDO aqui, e não tirado de painel.arq: esta
		// função roda na goroutine do socket e painel.arq é do laço
		// principal. Reler um .ini é barato, e de quebra a máquina
		// cadastrada depois de o app abrir já aparece na busca.
		arq, err := conexoes.Carregar(caminhoINI)
		if err != nil {
			fmt.Fprintf(os.Stderr, "busca: %v\n", err)
			buscaAberta.Store(false)
			return
		}
		abrirJanelaBusca(temaBusca, arq, token,
			func(cx conexoes.Conexao, p conexoes.Protocolo, avulso bool, tk string) {
				naJanelaPrincipal(w, func() {
					abrirEscolhaDaBusca(w, bar, painel, cx, p, avulso, tk)
				})
			},
			// A vinda para a frente chega depois, quando (e se) o
			// compositor devolver o token — a aba já começou a abrir.
			func(tk string) {
				naJanelaPrincipal(w, func() { trazerParaFrente(w, tk) })
			},
			func() { buscaAberta.Store(false) })
	}

	if cli != nil {
		go func() {
			for m := range cli.Msgs {
				switch m.Tipo {
				case msgBusca:
					// Direto, sem passar pela fila do laço principal: a
					// caixa tem janela e goroutine próprias. Depender de um
					// quadro da janela grande era parte do "às vezes abre,
					// às vezes não" — com a janela minimizada o pedido
					// ficava na fila sem ninguém para drená-la.
					abrirBusca(m.Token)
				case msgAbrir, msgAtivar:
					pedido := m
					naJanelaPrincipal(w, func() {
						aplicarPedidoDeAbrir(w, bar, painel, pedido)
					})
				}
			}
		}()
	}

	if cli != nil {
		// No Linux quem segura o atalho é o serviço: os Ajustes só avisam
		// pelo socket, e quem relê a chave e registra (ou solta) é o
		// manterAtalho de lá.
		aplicarAtalhoGlobal = func(ligado bool) {
			if err := cli.Enviar(mensagem{Tipo: msgAtalho, Ligado: ligado}); err != nil {
				fmt.Fprintf(os.Stderr, "atalho global: %v\n", err)
			}
		}
	} else {
		// Sem serviço à parte (Windows e as demais plataformas fora do
		// Linux — ver atalhoglobal_windows.go e atalhoglobal_outros.go):
		// o atalho global mora aqui dentro, no próprio app.
		var atalhoVivo *AtalhoGlobal
		ligarAtalho := func() {
			if atalhoVivo != nil {
				return
			}
			a, err := registrarAtalhoGlobal("abrir-busca",
				"Abrir a busca de máquinas do Acessos", "CTRL+SHIFT+F12",
				abrirBusca)
			if err != nil {
				fmt.Fprintf(os.Stderr, "atalho global: %v\n", err)
				return
			}
			atalhoVivo = a
			// Aqui o registro é do próprio app, então a notícia não
			// precisa de socket.
			//
			// E ela PODE ser vazia: este ramo não é só do Windows. Ele
			// vale sempre que cli == nil, o que inclui o Linux quando
			// não houve como falar com o serviço (ver ligarNoServico) —
			// e aí quem registra é o portal, que devolve gatilho vazio
			// se o diálogo do KDE for recusado. No Windows é que o caso
			// não existe: RegisterHotKey ou amarra ou devolve erro, que
			// o if acima já tratou. Ver atalhogatilho.go.
			definirGatilho(a.Gatilho)
		}
		if atalhoGlobalLigado(caminhoINI) {
			ligarAtalho()
		}
		// Só é chamado do laço de quadro (o clique nos Ajustes), sempre
		// na mesma goroutine — daí atalhoVivo não precisar de trava.
		aplicarAtalhoGlobal = func(ligado bool) {
			if ligado {
				ligarAtalho()
				return
			}
			atalhoVivo.Fechar()
			atalhoVivo = nil
		}
	}

	activeTab := func() Tab { return bar.active() }

	// O que era global quando havia uma janela só: captura, inibição e a
	// marca de aba à vista. Ver janela.go.
	est := novoEstadoJanela(w)
	defer esquecerJanela(w)

	// Botão de tela cheia da barra de sessão. Mora aqui, e não na aba,
	// porque a ação é sobre a JANELA — a aba só é levada junto.
	var btnTelaCheia widget.Clickable

	// devolverAba recoloca na tira uma sessão que estava destacada.
	// Enfileirado para rodar no laço DESTA janela: quem chama é a
	// goroutine da janela de sessão, e mexer na tira de fora daqui seria
	// corrida de dados com o desenho.
	// A CHAVE viaja junto: é ela que identifica "o que a aba é" (protocolo
	// + máquina) e impede abrir a mesma coisa duas vezes. retirar a tira do
	// mapa, e devolver sem ela faria a aba voltar identificada só pelo
	// rótulo — e o Painel passaria a abrir uma segunda sessão para a mesma
	// máquina achando que não havia nenhuma.
	devolverAba := func(t abaDestacavel, chave string) {
		naJanelaPrincipalInsistindo(w, func() {
			t.TrocarJanela(w, th)
			bar.appendCom(t, chave)
			w.Invalidate()
		})
	}

	// destacar tira a aba da tira e a abre em janela própria, já em tela
	// cheia. Roda de dentro do quadro, na goroutine do laço — é quem pode
	// mexer na tira.
	destacar := func(t abaDestacavel) {
		i := bar.indiceDe(t)
		if i < 0 {
			return
		}
		aba, chave, ok := bar.retirar(i)
		if !ok {
			return
		}
		abrirJanelaSessao(aba.(abaDestacavel), true,
			func(v abaDestacavel) { devolverAba(v, chave) })
	}

	for {
		// Trabalho vindo de outras janelas (a caixa de busca) e do socket
		// (segunda invocação do app) roda AQUI, e não mais no começo do
		// quadro: JANELA MINIMIZADA NÃO TEM QUADRO — nem no Windows nem
		// no Wayland —, e ali a fila ficava parada. O efeito era escolher
		// uma máquina no atalho global com o app minimizado e não
		// acontecer nada: a caixa fechava, a aba não nascia e a janela
		// não voltava. O w.Invalidate() de naJanelaPrincipal acorda o
		// laço de qualquer jeito (o Gio entrega um wakeup mesmo sem
		// quadro), e é este ponto que o recebe. Ver filajanela.go.
		drenarFilaJanela()
		// Ações de janela (minimizar/maximizar/raise) pedidas durante o
		// quadro anterior saem AQUI: o FrameEvent que as pediu já retornou
		// por completo e o w.Event() abaixo ainda não foi chamado, então
		// não há quadro em voo. Roda na goroutine do laço de propósito —
		// no Wayland/X11 o Window.Run executa f() na goroutine de quem
		// chama, e despachar isso de uma goroutine própria criava corrida
		// com o desenho. Ver acaojanela.go.
		//
		// Vem DEPOIS da fila acima de propósito: o que ela enfileira (o
		// trazerParaFrente da escolha) sai na mesma volta, e não na
		// seguinte.
		drenarAcoesJanela()
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
			tratarEventoPlataforma(est, w, e, activeTab)
			// O explorer de arquivos (botão "procurar…" dos Ajustes)
			// precisa do handle nativo da janela para abrir o diálogo
			// já ancorado nela — ele mesmo ignora o que não reconhece.
			explorerAcessos.ListenEvents(e)

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
			est.atualizarInibicao(activeTab())
			gtx := app.NewContext(&ops, e)
			if btnTelaCheia.Clicked(gtx) {
				if t, ok := activeTab().(abaDestacavel); ok {
					destacar(t)
				}
			}
			// Teclado e clipboard fora do Linux (Windows) passam pelo
			// próprio Gio, não pelo grab — ver entrada_outros.go. No
			// Linux estas duas só repassam pro caminho de sempre.
			tratarTecladoFrame(w, gtx, activeTab)
			tratarClipboardFrame(est, gtx, activeTab)
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
			if pendingLimparFocoGio.Swap(false) {
				gtx.Execute(key.FocusCmd{Tag: nil})
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
							lembrarGeral("fonte", strconv.Itoa(proximoNivelFonte()))
						},
						trocarTema: alternarTema,
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
									// Só quem sabe trocar de janela ganha o
									// botão de tela cheia: destacar uma aba
									// que continuasse pedindo quadro para
									// esta janela abriria uma janela que
									// nunca repinta.
									if _, pode := activeTab().(abaDestacavel); !pode {
										return layoutBarraSessao(gtx, th, a)
									}
									return layoutBarraSessao(gtx, th, a,
										func(gtx layout.Context) layout.Dimensions {
											return botaoIcone(gtx, &btnTelaCheia,
												icons.NavigationFullscreen, tema.Sec, tema.Texto)
										})
								}),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									size := gtx.Constraints.Max
									// De onde a área de conteúdo começa na
									// janela: o menu de contexto recebe do
									// Painel a posição do ponteiro relativa
									// a ela, e precisa somar isto.
									offsetConteudo = image.Pt(larguraLateralAtual, alturaTopoAtual)
									return rotearPonteiroEDesenhar(gtx, activeTab(), contentTag, size)
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
			layoutMenu(gtx, th, w)
			layoutTrocarHost(gtx, th)
			// A dica por último de todos: ela é a única coisa na tela que
			// ninguém clica, então cobrir o menu ou o diálogo seria só
			// atrapalhar. Desenhada depois, some por cima de tudo — e é
			// exatamente aí que ela não esconde nada que importe.
			layoutDica(gtx, th)

			regua(gtx, sb.largura(gtx))
			recorte.Pop()

			e.Frame(gtx.Ops)

			// A marca da aba ativa é gravada no FIM do quadro, e não no
			// começo, porque a TROCA de aba acontece durante ele: o
			// clique é lido dentro de bar.layout (tabbar.go, b.idx = i) e
			// o conteúdo já desenha a aba nova. Marcando no começo, o
			// quadro inteiro em que a pessoa troca de aba ficava
			// apontando para a ANTERIOR.
			//
			// Isso não era visível enquanto a marca só arbitrava o
			// clipboard (ver clipboard.go), mas passou a ser: quem decide
			// pedir quadro por ela — invalidarSeVisivel — travaria a aba
			// recém-aberta, porque sem quadro a marca nunca se corrigiria
			// e sem marca certa não sai quadro. Aqui ela reflete o que
			// acabou de ser DESENHADO, que é a definição certa de "aba
			// à vista".
			//
			// Continua valendo o de sempre: quem publica no clipboard do
			// sistema é ESTE laço, nunca as goroutines das sessões.
			marcarAbaAtiva(w, activeTab())
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
	publicarClipboard(w, texto)
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
