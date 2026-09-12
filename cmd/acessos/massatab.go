package main

import (
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
	"sync"
	"time"

	"acessos-go/internal/conexoes"
	"acessos-go/internal/massa/model"
	"acessos-go/internal/massa/runner"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// massaTab é a execução em massa: pega as máquinas selecionadas no Painel,
// roda o mesmo comando em todas por SSH e mostra o resultado máquina a
// máquina.
//
// O motor é o do Mass SSH Executer (internal/massa/...), portado inteiro:
// executor por plataforma, runner com fase de canário para calibrar
// timeout, heurística de exit code. O que ficou de fora foi a interface
// dele — esta aqui é nova.
type massaTab struct {
	th  *material.Theme
	w   *app.Window
	cxs []conexoes.Conexao

	// A FILA é o que vai rodar, na ordem. Um comando só era o caso comum
	// no executor antigo, mas manutenção de PDV quase nunca é um comando:
	// é "para o serviço, troca o arquivo, sobe de novo, confere". O runner
	// já executa uma lista mantendo a MESMA sessão entre comandos, então
	// estado (cd, variável de ambiente) sobrevive de um para o outro.
	fila          []itemFila
	filaExecutada []itemFila // a fila da execução em curso, para rotular a saída
	snips         []model.Snippet
	escolhido     int // último snippet clicado, só para destacar na lista
	btnSnip       []widget.Clickable
	btnRemFila    []widget.Clickable
	btnAddCmd     widget.Clickable
	listaFila     widget.List
	comando       widget.Editor
	usuario       widget.Editor
	senha         widget.Editor
	root          widget.Clickable
	usarRoot      bool
	senhaRoot     widget.Editor

	btnLinha []widget.Clickable // uma por máquina: clicar mostra a saída
	// Fila ou paralelo. Acima de 1, "parar no erro" deixa de ser exato:
	// as outras máquinas já estão rodando quando a pergunta aparece —
	// por isso a escolha é explícita, e não um número escondido.
	btnRitmo   [2]widget.Clickable
	paralelo   bool
	btnRodar   widget.Clickable
	btnAbortar widget.Clickable
	lista      widget.List
	listaSnip  widget.List

	comCred   int    // quantas máquinas já têm credencial SSH salva
	pausa     *pausa // decisão pendente do runner (canário ou erro)
	mu        sync.Mutex
	rodando   bool
	estados   map[string]*estadoMaquina
	feitos    int
	ok, ruins int
	log       []string
	corrente  *runner.Runner
}

// itemFila é um comando da fila, com de onde ele veio (para o operador
// reconhecer sem reler o comando inteiro).
type itemFila struct {
	rotulo string
	texto  string
	root   bool
}

type estadoMaquina struct {
	status model.StatusExec
	motivo string
	// TODOS os comandos, não só o último: com uma fila de 4 comandos, o
	// que interessa quase sempre está no primeiro que falhou.
	cmds []model.ResultadoComando
	dur  time.Duration
}

func newMassaTab(w *app.Window, cxs []conexoes.Conexao, caminhoSnippets string) *massaTab {
	t := &massaTab{
		th:        temaApp,
		w:         w,
		cxs:       cxs,
		escolhido: -1,
		estados:   map[string]*estadoMaquina{},
	}
	t.lista.Axis = layout.Vertical
	t.listaSnip.Axis = layout.Vertical
	t.listaFila.Axis = layout.Vertical
	t.comando.Submit = false
	t.usuario.SingleLine = true
	t.senha.SingleLine = true
	t.senha.Mask = '•'
	t.senhaRoot.SingleLine = true
	t.senhaRoot.Mask = '•'

	// A credencial de cada máquina SAI DO .ini (e do cofre). Os campos da
	// tela são só o resto: máquina sem SSH salvo. Por isso eles nascem
	// preenchidos com a primeira credencial encontrada e ficam escondidos
	// quando todas as máquinas já têm a sua — na prática, o operador só
	// digita a senha de root.
	for _, cx := range cxs {
		if cx.Tem(conexoes.SSH) && cx.SSH.Usuario != "" {
			if _, err := segredo(cx.SSH.Senha); err == nil {
				t.comCred++
			}
		}
	}
	for _, cx := range cxs {
		if cx.Tem(conexoes.SSH) && cx.SSH.Usuario != "" {
			t.usuario.SetText(cx.SSH.Usuario)
			if s, err := segredo(cx.SSH.Senha); err == nil {
				t.senha.SetText(s)
			}
			break
		}
	}
	for _, cx := range cxs {
		t.estados[cx.Nome] = &estadoMaquina{}
	}
	t.btnLinha = make([]widget.Clickable, len(cxs))
	if s, err := carregarSnippets(caminhoSnippets); err == nil {
		t.snips = s
		t.btnSnip = make([]widget.Clickable, len(s))
	} else {
		t.registrar(fmt.Sprintf("snippets: %v", err))
	}
	return t
}

func (t *massaTab) Title() string { return fmt.Sprintf("Massa (%d)", len(t.cxs)) }
func (t *massaTab) SoIcone() bool { return false }
func (t *massaTab) Pinned() bool  { return false }
func (t *massaTab) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	return icons.ImageFlashOn, tema.AtencaoFg, tema.AtencaoBg
}
func (t *massaTab) HandleKey(_, _ uint32, _ bool)                {}
func (t *massaTab) HandlePointer(_ pointer.Event, _ image.Point) {}

func (t *massaTab) Close() {
	t.mu.Lock()
	r := t.corrente
	t.mu.Unlock()
	if r != nil {
		r.Abortar()
	}
}

func (t *massaTab) registrar(s string) {
	t.mu.Lock()
	t.log = append(t.log, s)
	if len(t.log) > 500 {
		t.log = t.log[len(t.log)-500:]
	}
	t.mu.Unlock()
}

// executar monta a Config do runner e dispara. O canário (fase 1) roda em
// UMA máquina para calibrar os timeouts antes de soltar o resto — é o que
// evita matar um comando lento por timeout chutado.
func (t *massaTab) executar() {
	// O que está digitado e ainda não foi somado à fila conta como o
	// último comando: esquecer de clicar em "+" não pode significar
	// executar coisa diferente do que está na tela.
	fila := append([]itemFila{}, t.fila...)
	if texto := strings.TrimSpace(t.comando.Text()); texto != "" {
		fila = append(fila, itemFila{rotulo: "digitado", texto: texto})
	}
	if len(fila) == 0 {
		t.registrar("nada a executar: escolha um snippet ou digite um comando")
		return
	}
	t.mu.Lock()
	if t.rodando {
		t.mu.Unlock()
		return
	}
	t.rodando = true
	t.filaExecutada = fila
	t.feitos, t.ok, t.ruins = 0, 0, 0
	for _, e := range t.estados {
		*e = estadoMaquina{}
	}
	t.mu.Unlock()

	// Plataforma por máquina: o que a sonda gravou no .ini. Windows usa
	// PowerShell e não eleva; Linux usa PTY + su. Mandar um para o outro
	// não dá erro bonito, dá comando estranho rodando.
	var hosts []model.Host
	nomePorIP := map[string]string{}
	credPorIP := map[string]model.Credencial{}
	for _, cx := range t.cxs {
		plat := model.Linux
		if cx.Windows {
			plat = model.Windows
		}
		hosts = append(hosts, model.Host{IP: cx.Host, Plataforma: plat, Filial: cx.GrupoStr()})
		nomePorIP[cx.Host] = cx.Nome
		// Credencial SALVA da máquina (conexoes.ini), decifrada pelo cofre
		// se for o caso. O campo da tela vira só o padrão de quem não tem
		// nada salvo — parque grande costuma ter exceção em uma loja ou
		// outra, e digitar uma senha só quebraria justo nessas.
		if cx.Tem(conexoes.SSH) && cx.SSH.Usuario != "" {
			senha, err := segredo(cx.SSH.Senha)
			if err != nil {
				t.registrar(fmt.Sprintf("%s: %v (vai usar a credencial da tela)", cx.Nome, err))
				continue
			}
			credPorIP[cx.Host] = model.Credencial{Usuario: cx.SSH.Usuario, Senha: senha}
		}
	}

	cfg := runner.Config{
		Hosts:      hosts,
		Comandos:   comandosDaFila(fila),
		Cred:       model.Credencial{Usuario: t.usuario.Text(), Senha: t.senha.Text()},
		CredDeHost: func(h model.Host) model.Credencial { return credPorIP[h.IP] },
		UsarRoot:   t.usarRoot || filaPedeRoot(fila),
		RootCred:   model.Credencial{Usuario: "root", Senha: t.senhaRoot.Text()},
		Plataforma: model.Linux,
		Workers:    8,
		// TRÊS canários, sempre: um só não distingue "o comando está
		// errado" de "esta máquina está ruim". Rodam em sequência, e a
		// fase 2 só começa depois que alguém olhar o resultado.
		TamCanario:     3,
		TimeoutConexao: 8 * time.Second,
	}
	cb := runner.Callbacks{
		OnLog: func(s string) { t.registrar(s); t.w.Invalidate() },
		OnStatus: func(r model.ResultadoHost) {
			t.mu.Lock()
			if e := t.estados[nomePorIP[r.Host.IP]]; e != nil {
				e.status, e.motivo = r.Status, r.Motivo
			}
			t.mu.Unlock()
			t.w.Invalidate()
		},
		OnHostConcluido: func(r model.ResultadoHost) {
			t.mu.Lock()
			e := t.estados[nomePorIP[r.Host.IP]]
			if e != nil {
				e.status, e.motivo, e.dur = r.Status, r.Motivo, r.Duracao
				e.cmds = r.Comandos
			}
			t.feitos++
			if r.Status == model.StatusOK {
				t.ok++
			} else {
				t.ruins++
			}
			t.mu.Unlock()
			t.w.Invalidate()
		},
		// Canário e erro BLOQUEIAM a goroutine do runner até alguém
		// responder. É isso que faz a trava existir: sem a parada, o
		// comando errado vai para as 200 máquinas antes de alguém ver a
		// primeira saída.
		OnCanario: func(r runner.ResumoCanario) bool {
			return t.esperarCanario(r, len(hosts)-len(r.Resultados))
		},
		OnErro: func(c runner.ErroCtx) runner.Decisao {
			return t.esperarErro(c)
		},
		OnFim: func([]model.ResultadoHost) {
			t.mu.Lock()
			t.rodando = false
			t.corrente = nil
			t.mu.Unlock()
			t.w.Invalidate()
		},
	}

	r := runner.Novo(cfg, cb)
	t.mu.Lock()
	t.corrente = r
	t.mu.Unlock()
	go r.Executar()
}

func (t *massaTab) Layout(gtx layout.Context) layout.Dimensions {
	if t.btnRodar.Clicked(gtx) {
		t.executar()
	}
	if t.btnAbortar.Clicked(gtx) {
		t.mu.Lock()
		r := t.corrente
		t.mu.Unlock()
		if r != nil {
			r.Abortar()
		}
	}
	if t.root.Clicked(gtx) {
		t.usarRoot = !t.usarRoot
	}
	for i := range t.btnRitmo {
		if t.btnRitmo[i].Clicked(gtx) {
			t.paralelo = i == 1
		}
	}
	for i := range t.btnSnip {
		if t.btnSnip[i].Clicked(gtx) {
			t.escolhido = i
			t.adicionarFila(itemFila{
				rotulo: t.snips[i].Descricao,
				texto:  t.snips[i].Comando,
				root:   t.snips[i].Root,
			})
		}
	}
	if t.btnAddCmd.Clicked(gtx) {
		if texto := strings.TrimSpace(t.comando.Text()); texto != "" {
			t.adicionarFila(itemFila{rotulo: "digitado", texto: texto})
			t.comando.SetText("")
		}
	}
	for i := range t.btnRemFila {
		if i < len(t.fila) && t.btnRemFila[i].Clicked(gtx) {
			t.fila = append(t.fila[:i], t.fila[i+1:]...)
			break
		}
	}

	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		// esquerda: o que rodar
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			larg := gtx.Dp(unit.Dp(330))
			gtx.Constraints.Min.X = larg
			gtx.Constraints.Max.X = larg
			return t.painelComando(gtx)
		}),
		// direita: máquinas e resultado
		layout.Flexed(1, t.painelMaquinas),
	)
}

// painelComando: biblioteca de snippets em cima, comando embaixo,
// credencial e o botão de rodar no rodapé.
func (t *massaTab) painelComando(gtx layout.Context) layout.Dimensions {
	t.mu.Lock()
	rodando := t.rodando
	feitos, ok, ruins := t.feitos, t.ok, t.ruins
	t.mu.Unlock()

	return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(negrito(txt(t.th, fonteCond, spSubgrupo, "Snippets", tema.Texto)).Layout),
			layout.Rigid(layout.Spacer{Height: 6}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.List(t.th, &t.listaSnip).Layout(gtx, len(t.snips), func(gtx layout.Context, i int) layout.Dimensions {
					return t.linhaSnippet(gtx, i)
				})
			}),
			layout.Rigid(layout.Spacer{Height: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(negrito(txt(t.th, fonteCond, spSubgrupo,
						fmt.Sprintf("Fila (%d)", len(t.fila)), tema.Texto)).Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}),
					layout.Rigid(rotulo(t.th, fonteMono, spCardMeta, "clique num snippet para somar", tema.Fraco)),
				)
			}),
			layout.Rigid(layout.Spacer{Height: 4}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.Y = gtx.Dp(110)
				return t.painelFila(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: 6}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return caixaEditor(gtx, t.th, &t.comando, "comando a executar (pode ter várias linhas)", 74)
			}),
			layout.Rigid(layout.Spacer{Height: 4}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return botaoBarra(gtx, t.th, &t.btnAddCmd, "+ somar à fila")
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Height: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				aviso := fmt.Sprintf("SSH: %d de %d usam a credencial salva no .ini", t.comCred, len(t.cxs))
				cor := tema.Sec
				if t.comCred < len(t.cxs) {
					cor = tema.AtencaoFg
				}
				return rotulo(t.th, fonteMono, spCardMeta, aviso, cor)(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// Campos de SSH só aparecem se ALGUMA máquina não tiver
				// credencial salva; com todas salvas, o operador digita
				// apenas a senha de root.
				if t.comCred >= len(t.cxs) {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return caixaEditor(gtx, t.th, &t.usuario, "usuário (para as sem credencial)", 0)
						}),
						layout.Rigid(layout.Spacer{Width: 6}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return caixaEditor(gtx, t.th, &t.senha, "senha", 0)
						}),
					)
				})
			}),
			layout.Rigid(layout.Spacer{Height: 6}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return caixaMarcar(gtx, t.th, &t.root, t.usarRoot, "elevar para root")
					}),
					layout.Rigid(layout.Spacer{Width: 6}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						if !t.usarRoot {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
						}
						return caixaEditor(gtx, t.th, &t.senhaRoot, "senha root", 0)
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Height: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return segmentado(gtx, t.th,
					[]*widget.Clickable{&t.btnRitmo[0], &t.btnRitmo[1]},
					[]string{"1 por vez", "paralelo"}, seSim(t.paralelo, 1, 0))
			}),
			layout.Rigid(layout.Spacer{Height: 10}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if rodando {
							// Abortar é destrutivo (mata execução em curso):
							// é o único botão vermelho desta tela.
							return botaoPerigo(gtx, t.th, &t.btnAbortar, "Abortar")
						}
						return botaoPrimario(gtx, t.th, &t.btnRodar,
							fmt.Sprintf("Executar em %d", len(t.cxs)))
					}),
					layout.Rigid(layout.Spacer{Width: 10}.Layout),
					layout.Rigid(rotulo(t.th, fonteMono, spSecundario,
						fmt.Sprintf("%d/%d · %d ok · %d falha", feitos, len(t.cxs), ok, ruins), tema.Sec)),
				)
			}),
		)
	})
}

func (t *massaTab) linhaSnippet(gtx layout.Context, i int) layout.Dimensions {
	s := t.snips[i]
	escolhido := t.escolhido == i
	return t.btnSnip[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				fundo, borda := transparente, transparente
				switch {
				case escolhido:
					fundo, borda = tema.AzulFraco, tema.Azul
				case t.btnSnip[i].Hovered():
					fundo = tema.Vidro2
				}
				superficie(gtx, gtx.Constraints.Min, fundo, borda, 7)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			},
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: 5, Bottom: 5, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(rotulo(t.th, fonteSans, spCorpo, s.Descricao, tema.Texto)),
						layout.Rigid(rotuloLinha(t.th, fonteMono, spCardMeta, primeiraLinha(s.Comando), tema.Fraco)),
					)
				})
			},
		)
	})
}

// painelMaquinas lista as máquinas com o estado de cada uma. Status é cor
// semântica: verde só quando terminou bem, vermelho só quando falhou.
func (t *massaTab) painelMaquinas(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: 10, Bottom: 10, Left: 8, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(t.faixaPausa),
			layout.Flexed(1, t.listaMaquinas),
		)
	})
}

// listaMaquinas é a lista de alvos com o estado de cada um. Clicar numa
// linha abre o retorno completo daquela máquina.
func (t *massaTab) listaMaquinas(gtx layout.Context) layout.Dimensions {
	return material.List(t.th, &t.lista).Layout(gtx, len(t.cxs), func(gtx layout.Context, i int) layout.Dimensions {
		cx := t.cxs[i]
		t.mu.Lock()
		e := *t.estados[cx.Nome]
		t.mu.Unlock()

		chip, tipo := "—", "neutro"
		switch e.status {
		case model.StatusOK:
			chip, tipo = "OK", "ok"
		case model.StatusErro, model.StatusFalhaConexao, model.StatusTimeout:
			chip, tipo = strings.ToUpper(e.status.String()), "erro"
		case model.StatusConectando, model.StatusExecutando:
			chip, tipo = strings.ToUpper(e.status.String()), "atencao"
		case model.StatusPulado:
			chip = "PULADO"
		}

		if t.btnLinha[i].Clicked(gtx) {
			abrirDialogo(&dlgSaida{titulo: cx.Nome + " · " + cx.Host, texto: textoCompleto(e, t.filaExecutada)})
		}
		return layout.Inset{Bottom: 5}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return t.btnLinha[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Background{}.Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						superficie(gtx, gtx.Constraints.Min, tema.Vidro2, tema.LuzB, 8)
						return layout.Dimensions{Size: gtx.Constraints.Min}
					},
					func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Inset{Top: 6, Bottom: 6, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return chipEstado(gtx, t.th, chip, tipo)
										}),
										layout.Rigid(layout.Spacer{Width: 8}.Layout),
										layout.Rigid(negrito(txt(t.th, fonteCond, spCardHost, cx.Nome, tema.Texto)).Layout),
										layout.Rigid(layout.Spacer{Width: 8}.Layout),
										layout.Flexed(1, rotulo(t.th, fonteMono, spCardMeta, cx.Host, tema.Fraco)),
										layout.Rigid(rotulo(t.th, fonteMono, spCardMeta, duracao(e.dur), tema.Fraco)),
									)
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									texto := resumoSaida(e)
									if e.motivo != "" {
										texto = e.motivo
									}
									if texto == "" {
										return layout.Dimensions{}
									}
									return layout.Inset{Top: 4}.Layout(gtx,
										rotulo(t.th, fonteMono, spCardMeta, recorte(texto, 400), tema.Sec))
								}),
							)
						})
					},
				)
			})
		})
	})
}

// textoCompleto monta o retorno de TODOS os comandos, cada um com um
// cabeçalho dizendo qual é, o exit code e quanto demorou. Sem o cabeçalho,
// duas saídas coladas viram uma coisa só e ninguém acha onde quebrou.
func textoCompleto(e estadoMaquina, fila []itemFila) string {
	var p []string
	if e.motivo != "" {
		p = append(p, e.motivo)
	}
	for _, c := range e.cmds {
		rot := fmt.Sprintf("comando %d", c.IndiceCmd+1)
		if c.IndiceCmd < len(fila) {
			rot += " · " + fila[c.IndiceCmd].rotulo
		}
		cab := fmt.Sprintf("── %s · exit %d · %s ──", rot, c.ExitCode, duracao(c.Duracao))
		if c.Erro != nil {
			cab += "  [" + c.Erro.Error() + "]"
		}
		saida := strings.TrimRight(c.Saida, "\n")
		if saida == "" {
			saida = "(sem saída)"
		}
		p = append(p, cab+"\n"+saida)
	}
	if len(p) == 0 {
		return "(sem saída)"
	}
	return strings.Join(p, "\n\n")
}

// resumoSaida é a linha única da lista: o primeiro comando que deu
// problema; não havendo, o último que rodou.
func resumoSaida(e estadoMaquina) string {
	for _, c := range e.cmds {
		if c.Erro != nil || c.ExitCode != 0 {
			return strings.TrimSpace(c.Saida)
		}
	}
	if n := len(e.cmds); n > 0 {
		return strings.TrimSpace(e.cmds[n-1].Saida)
	}
	return ""
}

func primeiraLinha(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func recorte(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func duracao(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.Truncate(100 * time.Millisecond).String()
}

// carregarSnippets lê o snippets.ini do app original (mesmo formato do
// comandos.ini do Mass SSH Executer) e devolve em ordem de descrição.
func carregarSnippets(caminho string) ([]model.Snippet, error) {
	s, err := snippetsDoArquivo(caminho)
	if err != nil {
		return nil, err
	}
	sort.Slice(s, func(i, j int) bool { return s[i].Descricao < s[j].Descricao })
	return s, nil
}

// caixaEditor é o campo de texto padrão desta aba. altura 0 = uma linha.
func caixaEditor(gtx layout.Context, th *material.Theme, ed *widget.Editor, dica string, altura unit.Dp) layout.Dimensions {
	marcarFoco(gtx.Focused(ed))
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			superficie(gtx, gtx.Constraints.Min, tema.Campo, tema.Borda2, 7)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			if altura > 0 {
				h := gtx.Dp(altura)
				gtx.Constraints.Min.Y = h
				gtx.Constraints.Max.Y = h
			}
			return layout.Inset{Top: 6, Bottom: 6, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				e := material.Editor(th, ed, dica)
				e.Font = fonteMono
				e.TextSize = spCorpo
				e.Color = tema.Texto
				e.HintColor = tema.Fraco
				return e.Layout(gtx)
			})
		},
	)
}

// caixaMarcar é um checkbox com rótulo.
func caixaMarcar(gtx layout.Context, th *material.Theme, btn *widget.Clickable, marcado bool, rot string) layout.Dimensions {
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lado := gtx.Dp(15)
				gtx.Constraints.Min = image.Pt(lado, lado)
				fundo, borda := transparente, tema.LuzB
				if marcado {
					fundo, borda = tema.Azul, tema.Azul
				}
				superficie(gtx, image.Pt(lado, lado), fundo, borda, 4)
				if marcado {
					layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return icone(gtx, icons.NavigationCheck, hex(0xffffff), 11)
					})
				}
				return layout.Dimensions{Size: image.Pt(lado, lado)}
			}),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Rigid(txt(th, fonteMono, spSecundario, rot, tema.Sec).Layout),
		)
	})
}

// adicionarFila põe o comando no fim da fila (e cria o botão de remover
// correspondente — um por item, senão o clique de remover se perde ao
// mudar o tamanho da lista).
func (t *massaTab) adicionarFila(it itemFila) {
	t.fila = append(t.fila, it)
	t.btnRemFila = append(t.btnRemFila, widget.Clickable{})
	if it.root {
		t.usarRoot = true
	}
}

func comandosDaFila(fila []itemFila) []*model.Comando {
	out := make([]*model.Comando, 0, len(fila))
	for _, it := range fila {
		out = append(out, &model.Comando{Texto: it.texto, Plataforma: model.Linux})
	}
	return out
}

// filaPedeRoot: basta um snippet marcado como root para a sessão inteira
// precisar de elevação — o runner eleva uma vez, no começo.
func filaPedeRoot(fila []itemFila) bool {
	for _, it := range fila {
		if it.root {
			return true
		}
	}
	return false
}

// painelFila lista o que vai rodar, na ordem, com o "×" para tirar.
func (t *massaTab) painelFila(gtx layout.Context) layout.Dimensions {
	if len(t.fila) == 0 {
		return layout.Dimensions{}
	}
	return material.List(t.th, &t.listaFila).Layout(gtx, len(t.fila), func(gtx layout.Context, i int) layout.Dimensions {
		it := t.fila[i]
		return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					superficie(gtx, gtx.Constraints.Min, tema.Vidro2, tema.LuzB, 6)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				},
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 4, Bottom: 4, Left: 8, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(rotulo(t.th, fonteMono, spCardMeta, fmt.Sprintf("%d.", i+1), tema.Fraco)),
							layout.Rigid(layout.Spacer{Width: 6}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(rotulo(t.th, fonteSans, spSecundario, it.rotulo, tema.Texto)),
									layout.Rigid(rotuloLinha(t.th, fonteMono, spCardMeta, primeiraLinha(it.texto), tema.Fraco)),
								)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return t.btnRemFila[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									c := tema.Fraco
									if t.btnRemFila[i].Hovered() {
										c = tema.ErroFg // tirar da fila é a ação que desfaz
									}
									return layout.UniformInset(2).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return icone(gtx, iconeFechar, c, 14)
									})
								})
							}),
						)
					})
				},
			)
		})
	})
}

func seSim(cond bool, sim, nao int) int {
	if cond {
		return sim
	}
	return nao
}
