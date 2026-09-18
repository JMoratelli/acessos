package main

// Janela de busca: a caixinha que o atalho global pipoca por cima de
// tudo, com um campo só, para abrir uma máquina sem ir até o app.
//
// Ela é pipocada pelo atalho global (ver atalhoglobal_linux.go e
// servico_linux.go) e some ao escolher uma máquina, ao apertar Esc ou ao
// perder o foco.
//
// ----------------------------------------------------------------------
// TRÊS DEFEITOS QUE ESTAVAM AQUI (e por que o desenho é este)
// ----------------------------------------------------------------------
//
//  1. **A janela não crescia.** O tamanho era fixo em buscaAlt e nada
//     nunca o trocava — a lista de resultados era desenhada abaixo da
//     borda da janela, ou seja, invisível. Agora cada quadro calcula a
//     altura que a lista precisa e pede o tamanho novo (fora do quadro,
//     ver acoes abaixo).
//  2. **w.Perform de goroutine.** Fechar a janela e pedir o token saíam de
//     uma goroutine própria. No Wayland/X11 o Window.Run executa f() na
//     goroutine de quem chama (ver acaojanela.go), então isso mexia no
//     driver EM PARALELO com o laço que desenhava. Agora a goroutine só
//     ESPERA o token; tudo o que toca a janela volta pela fila `acoes`,
//     drenada pelo próprio laço.
//  3. **Sem foco, sem saída.** A janela fecha ao perder o foco, mas só
//     depois de tê-lo ganho uma vez. Se o compositor nunca desse o foco
//     (o serviço é um processo de segundo plano, e é justamente o caso em
//     que a prevenção de roubo de foco morde), ela ficava boiando sem
//     receber tecla nenhuma. Agora existe prazo: sem foco em
//     buscaPrazoFoco, ela se fecha em vez de virar lixo na tela.
//
// Duas coisas medidas em janela solta antes de escrever isto aqui, e que
// explicam o desenho: no Wayland/KWin a janela nasce com o foco do
// teclado sozinha, em ~60ms, sem precisar de token de ativação — mas ela
// nasce na tela da JANELA ATIVA, não na do ponteiro, porque o compositor
// não deixa cliente nenhum escolher onde aparecer nem saber onde o mouse
// está.
//
// A janela é SEM decoração e o fundo dela é transparente de propósito: o
// que se vê é um cartão de vidro flutuando, com sombra, igual aos cards
// do Painel. Quem limita o desenho é a margem em volta — é ela que deixa
// a sombra aparecer e os cantos arredondados recortarem o fundo.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"acessos-go/internal/conexoes"

	"gio.tools/icons"
	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Quantas linhas a lista mostra de uma vez. Não é rolagem: a busca é
// para ACHAR, e uma lista que passa de meia dúzia quer dizer que o termo
// ainda não discrimina o bastante — mais uma letra resolve melhor que
// mais scroll.
const buscaMaxLinhas = 6

const (
	buscaLinhaAlt = unit.Dp(38)
	// Margem transparente em volta do cartão: sem ela a sombra ficaria
	// recortada pela borda da janela e o vidro viraria um retângulo duro.
	buscaMargem = unit.Dp(14)
	buscaRaio   = unit.Dp(12)
	buscaLarg   = unit.Dp(640)
	// Altura SEM lista. A janela cresce conforme os resultados: nasce só
	// com o campo, como decidido, e não reserva espaço para uma lista
	// que talvez não venha.
	buscaAlt = unit.Dp(104)
	// Respiro entre o campo e a primeira linha da lista (o mesmo Spacer
	// que `lista` insere) — entra na conta da altura da janela.
	buscaRespiroLista = unit.Dp(10)
)

// buscaPrazoFoco: quanto se espera pelo foco antes de desistir. Medido em
// janela solta, o KWin dá o foco em ~60ms; três segundos é folga de
// sobra, e serve só para a caixa não ficar boiando sem receber tecla.
const buscaPrazoFoco = 3 * time.Second

// janelaBusca é o estado da caixinha. Vive enquanto a janela existir.
type janelaBusca struct {
	th    *material.Theme
	campo widget.Editor
	arq   *conexoes.Arquivo

	// achados é o resultado da busca do quadro anterior; sel é a linha
	// escolhida, que o Enter abre.
	achados []conexoes.Conexao
	sel     int
	termo   string
	// rascunho é o destino não cadastrado ("fc52002-lj06", "10.1.1.99"),
	// oferecido quando a busca não casa com nada. Mesma regra do card do
	// Painel — ver efemera.go.
	rascunho    conexoes.Conexao
	temRascunho bool

	cliques []widget.Clickable
	// um clique por (linha, protocolo): o ícone no fim da linha abre
	// AQUELE protocolo, sem passar pelo preferido do Enter.
	cliquesProto [][]widget.Clickable

	// jaFocou evita fechar no primeiro quadro: a janela nasce sem foco e
	// só ganha uns 60ms depois (medido; ver cmd/janelatest). Fechar ao
	// perder o foco sem esperar o primeiro ganho mataria a janela antes
	// de ela existir para o usuário.
	jaFocou bool

	// aoEscolher recebe o token de ativação junto: é com ele que a
	// janela principal se traz para a frente (ver AtivarCom). O bool diz
	// se a linha era o DESTINO AVULSO (digitado, sem cadastro) — quem
	// abre precisa saber para pedir credencial em vez de procurar uma
	// máquina que não existe no inventário.
	aoEscolher func(cx conexoes.Conexao, p conexoes.Protocolo, avulso bool, token string)
	// tokenAtivacao é o que veio do portal junto com o acionamento do
	// atalho, quando o desktop manda um. É com ele que esta janela, que
	// nasce de um processo de segundo plano, consegue o foco.
	tokenAtivacao string
	// ativou marca que o token já foi gasto: ele vale uma vez só.
	ativou bool
	// escolhendo evita disparar duas vezes enquanto o token não volta.
	escolhendo bool

	// acoes é o que goroutines (a espera do token) querem que aconteça NA
	// JANELA. Drenada no topo do laço, fora de qualquer quadro — mesma
	// regra e mesmo motivo de acaojanela.go.
	acoes chan func()
	// alturaAtual é a última altura pedida ao sistema, em Dp. Guardada
	// para não repetir o pedido a cada quadro.
	alturaAtual unit.Dp
}

// naJanela agenda f para rodar no laço DESTA janela e acorda o laço. Não
// bloqueia: fila cheia significa janela que não fecha quadro, e nesse
// estado pendurar quem pede não ajudaria ninguém.
func (j *janelaBusca) naJanela(w *app.Window, f func()) {
	select {
	case j.acoes <- f:
		w.Invalidate()
	default:
	}
}

func (j *janelaBusca) drenar() {
	for {
		select {
		case f := <-j.acoes:
			f()
		default:
			return
		}
	}
}

// alturaDesejada é o tamanho que a janela precisa ter para caber o que
// está desenhado agora. Sem isto a lista era desenhada abaixo da borda da
// janela — ou seja, não aparecia.
func (j *janelaBusca) alturaDesejada() unit.Dp {
	n := j.linhas()
	if n == 0 {
		return buscaAlt
	}
	if n > buscaMaxLinhas+1 {
		n = buscaMaxLinhas + 1 // +1: a linha do destino avulso
	}
	return buscaAlt + buscaRespiroLista + unit.Dp(n)*buscaLinhaAlt
}

// protocoloPreferido é o que o Enter abre quando o host tem mais de um.
// A ordem é a da fileira de ícones do Painel (tela, shell, rdp, sftp) —
// mesma da variável protocolos, para o olho e a tecla concordarem.
func protocoloPreferido(cx conexoes.Conexao) (conexoes.Protocolo, bool) {
	for _, e := range protocolos {
		if e.p == conexoes.SFTP {
			continue // SFTP divide o "ligado" com o SSH; nunca é o padrão
		}
		if cx.Tem(e.p) {
			return e.p, true
		}
	}
	return "", false
}

// buscar refaz a lista quando o texto muda.
func (j *janelaBusca) buscar() {
	termo := strings.ToLower(strings.TrimSpace(j.campo.Text()))
	if termo == j.termo {
		return
	}
	j.termo, j.sel = termo, 0
	j.achados, j.temRascunho = nil, false
	if termo == "" || j.arq == nil {
		return
	}
	todos := filtrar(j.arq.Conexoes, termo)
	if len(todos) > buscaMaxLinhas {
		todos = todos[:buscaMaxLinhas]
	}
	j.achados = todos
	// Destino avulso pela MESMA regra do Painel: nome cru só vira destino
	// quando a busca não achou nada (ver ehDestinoPlausivel).
	j.rascunho, j.temRascunho = alvoRascunho(j.campo.Text(), len(filtrar(j.arq.Conexoes, termo)) == 0)
	for len(j.cliques) < len(j.achados)+1 {
		j.cliques = append(j.cliques, widget.Clickable{})
		j.cliquesProto = append(j.cliquesProto, make([]widget.Clickable, len(protocolos)))
	}
}

// linhas é quantas linhas a lista tem, contando a do destino avulso.
func (j *janelaBusca) linhas() int {
	n := len(j.achados)
	if j.temRascunho {
		n++
	}
	return n
}

// conexaoDa devolve a conexão da linha i (a última é o destino avulso).
func (j *janelaBusca) conexaoDa(i int) (conexoes.Conexao, bool) {
	switch {
	case i >= 0 && i < len(j.achados):
		return j.achados[i], true
	case j.temRascunho && i == len(j.achados):
		return j.rascunho, true
	}
	return conexoes.Conexao{}, false
}

// escolher abre a linha i. Protocolo vazio = o preferido, que é o que o
// Enter usa; clicar num ícone manda o protocolo daquele ícone.
func (j *janelaBusca) escolher(i int, p conexoes.Protocolo, w *app.Window) {
	cx, ok := j.conexaoDa(i)
	if !ok {
		return
	}
	avulso := j.temRascunho && i == len(j.achados)
	if p == "" {
		p, ok = protocoloPreferido(cx)
		if !ok {
			return
		}
	}
	if j.aoEscolher == nil || j.escolhendo {
		w.Perform(system.ActionClose)
		return
	}
	j.escolhendo = true

	// Três exigências que não cabem no mesmo lugar, e é por isso que este
	// trecho tem esta forma:
	//
	//  1. o token tem de ser pedido ANTES de fechar — quem autoriza a
	//     troca de foco é a janela que TEM o foco;
	//  2. o PEDIDO não pode sair de dentro do quadro nem de uma goroutine
	//     qualquer: ele passa pelo Window.Run, que no Windows espera o
	//     laço (de dentro do quadro, isso trava) e no Wayland/X11 executa
	//     na goroutine de quem chama (de fora, isso corre com o desenho).
	//     O lugar certo é a fila, drenada no topo do laço;
	//  3. a ESPERA não pode ficar no laço, porque a resposta do
	//     compositor chega justamente por ele. Essa vai para a goroutine.
	j.naJanela(w, func() {
		ch, err := w.PedirTokenAtivacao()
		if err != nil {
			// Sem token a aba abre do mesmo jeito; o que se perde é a
			// janela principal vir para a frente. Degradar assim é bem
			// melhor que não abrir.
			fmt.Fprintf(os.Stderr, "busca: sem token de ativação (%v)\n", err)
			j.aoEscolher(cx, p, avulso, "")
			w.Perform(system.ActionClose)
			return
		}
		go func() {
			var token string
			select {
			case token = <-ch:
			case <-time.After(2 * time.Second):
				fmt.Fprintln(os.Stderr, "busca: o compositor não devolveu o token de ativação")
			}
			j.naJanela(w, func() {
				j.aoEscolher(cx, p, avulso, token)
				w.Perform(system.ActionClose)
			})
		}()
	})
}

// abrirJanelaBusca cria a janela e roda o laço dela numa goroutine
// própria. Devolve na hora — quem chama continua o que estava fazendo.
//
// tokenAtivacao é o token do portal, quando o desktop manda um junto com o
// atalho: sem ele, uma janela criada por processo de segundo plano pode
// nascer sem foco (prevenção de roubo de foco do compositor). Vazio é
// aceitável — no KDE medido, a janela ganha o foco sozinha.
func abrirJanelaBusca(th *material.Theme, arq *conexoes.Arquivo, tokenAtivacao string,
	aoEscolher func(conexoes.Conexao, conexoes.Protocolo, bool, string), aoFechar func()) {
	j := &janelaBusca{th: th, arq: arq, aoEscolher: aoEscolher, tokenAtivacao: tokenAtivacao,
		acoes: make(chan func(), 8), alturaAtual: buscaAlt}
	j.campo.SingleLine = true
	j.campo.Submit = true

	w := new(app.Window)
	w.Option(
		app.Title("Acessos — busca"),
		app.Decorated(false),
		app.Size(buscaLarg, buscaAlt),
		// Mínimo = tamanho fechado, e nada de máximo: é por app.Size que
		// a janela cresce quando a lista aparece (ver alturaDesejada).
		app.MinSize(buscaLarg, buscaAlt),
		// Sem isto o Gio declara a superfície inteira opaca e o
		// compositor pula a composição: a margem transparente em volta
		// do cartão vira lixo de memória e os cantos arredondados saem
		// quebrados. Ver o patch em third_party/gio (PATCH.md).
		app.Translucent(true),
	)

	// Sem foco em buscaPrazoFoco a janela se fecha sozinha: uma caixa que
	// não recebe tecla não serve para nada, e deixá-la na tela é pior que
	// não tê-la aberto — dá a impressão de que o atalho travou o
	// aplicativo. Acorda o laço para o prazo ser conferido mesmo sem
	// nenhum evento chegando.
	go func() {
		time.Sleep(buscaPrazoFoco)
		j.naJanela(w, func() {
			if !j.jaFocou {
				fmt.Fprintln(os.Stderr,
					"busca: o compositor não deu foco à caixa; fechando")
				w.Perform(system.ActionClose)
			}
		})
	}()

	go func() {
		defer func() {
			if aoFechar != nil {
				aoFechar()
			}
		}()
		var ops op.Ops
		for {
			// Fora de qualquer quadro: aqui o FrameEvent anterior já
			// retornou e o próximo ainda não começou. É deste ponto que
			// saem os w.Option/w.Perform pedidos por goroutines e pelo
			// próprio quadro (ver acaojanela.go, mesma regra).
			j.drenar()
			switch e := w.Event().(type) {
			case app.DestroyEvent:
				return
			case app.ConfigEvent:
				// Fecha ao perder o foco, como o lançador do sistema:
				// uma caixa de busca esquecida boiando vira lixo na
				// tela, e reabrir custa um atalho. O jaFocou é o que
				// impede de morrer no primeiro quadro, ainda sem foco.
				if e.Config.Focused {
					j.jaFocou = true
				} else if j.jaFocou {
					w.Perform(system.ActionClose)
				}
			case app.FrameEvent:
				gtx := app.NewContext(&ops, e)
				j.quadro(gtx, w)
				e.Frame(gtx.Ops)
				// O token de ativação só pode ser gasto com o driver de
				// pé, e sai pela fila para não ser mais um w.Run de
				// dentro de um quadro (ver acaojanela.go).
				if !j.ativou && j.tokenAtivacao != "" {
					j.ativou = true
					tk := j.tokenAtivacao
					j.naJanela(w, func() {
						if err := w.AtivarCom(tk); err != nil {
							fmt.Fprintf(os.Stderr, "busca: ativação (%v)\n", err)
						}
					})
				}
			}
		}
	}()
}

func (j *janelaBusca) quadro(gtx layout.Context, w *app.Window) layout.Dimensions {
	// Esc fecha. Filtro COM Name preenchido consome só essa tecla — um
	// filtro de nome vazio engoliria o teclado inteiro e o campo perderia
	// backspace e setas (medido no espinho; ver cmd/janelatest).
	for _, nome := range []key.Name{key.NameEscape, key.NameUpArrow, key.NameDownArrow} {
		for {
			ev, ok := gtx.Event(key.Filter{Name: nome})
			if !ok {
				break
			}
			ke, ok := ev.(key.Event)
			if !ok || ke.State != key.Press {
				continue
			}
			switch ke.Name {
			case key.NameEscape:
				w.Perform(system.ActionClose)
			case key.NameUpArrow:
				if j.sel > 0 {
					j.sel--
				}
			case key.NameDownArrow:
				if j.sel < j.linhas()-1 {
					j.sel++
				}
			}
		}
	}

	// Enter no campo: o widget.Editor entrega como SubmitEvent, e não
	// como tecla — por isso não entra no laço acima.
	for {
		ev, ok := j.campo.Update(gtx)
		if !ok {
			break
		}
		if _, sub := ev.(widget.SubmitEvent); sub {
			j.escolher(j.sel, "", w)
		}
	}
	j.buscar()
	// A janela acompanha a lista. O pedido sai pela fila e não daqui: um
	// w.Option de dentro do quadro é a mesma reentrância documentada em
	// acaojanela.go.
	if alt := j.alturaDesejada(); alt != j.alturaAtual {
		j.alturaAtual = alt
		j.naJanela(w, func() { w.Option(app.Size(buscaLarg, alt)) })
	}
	for i := range j.cliques[:min(len(j.cliques), j.linhas())] {
		if j.cliques[i].Clicked(gtx) {
			j.escolher(i, "", w)
		}
		// Passar o mouse move a seleção: senão o Enter abriria uma linha
		// e o clique, outra — duas ideias de "a escolhida" na mesma
		// caixa. Mesma regra da lista do Painel.
		if j.cliques[i].Hovered() {
			j.sel = i
		}
		// os ícones vêm DEPOIS da linha: clicar num deles também conta
		// como clique na linha, e o protocolo do ícone é que manda.
		for k, e := range protocolos {
			if j.cliquesProto[i][k].Clicked(gtx) {
				j.escolher(i, e.p, w)
			}
		}
	}

	return layout.UniformInset(buscaMargem).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = gtx.Constraints.Max
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				sombra(gtx, size, buscaRaio)
				// Vidro, não sólido: é o mesmo tratamento dos cards, e
				// aqui ele tem função — a caixa aparece POR CIMA do que
				// a pessoa estava olhando, e deixar o fundo atravessar
				// diz sozinho que isto é passageiro, não uma janela que
				// veio para ficar.
				//
				// O alfa mora no tema (BuscaVidro) porque claro e escuro
				// não aguentam o mesmo valor: sobre um desktop qualquer,
				// o cartão escuro pode ser bem mais fino que o claro sem
				// o texto perder legibilidade.
				superficie(gtx, size, tema.BuscaVidro, tema.Borda2, buscaRaio)
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = gtx.Constraints.Max
				return layout.Inset{Top: 14, Bottom: 12, Left: 14, Right: 14}.Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(j.caixa),
							layout.Rigid(j.lista),
							layout.Rigid(layout.Spacer{Height: 8}.Layout),
							// Sec, e não CardFraco: o cinza fraco foi
							// calibrado para se apoiar no fundo OPACO de
							// um card. Aqui o texto flutua sobre vidro,
							// com o desktop atravessando por trás, e some.
							// Pelo mesmo motivo sobe de 9sp (tamanho de
							// metadado) para 11sp.
							layout.Rigid(rotulo(j.th, fonteMono, spSecundario,
								"Enter conecta · Esc fecha", tema.Sec)),
						)
					})
			}),
		)
	})
}

// caixa é o campo em si — mesmo desenho do campo do Painel
// (dashtab.caixaBusca): sólido, com a lupa à esquerda, mono no texto.
// Sólido e não vidro pelo mesmo motivo de lá: texto digitado por cima de
// fundo translúcido fica difícil de ler.
func (j *janelaBusca) caixa(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			superficie(gtx, gtx.Constraints.Min, tema.Campo, tema.LuzB, 8)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return icone(gtx, icons.ActionSearch, tema.Fraco, 18)
						}),
						layout.Rigid(layout.Spacer{Width: 10}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							es := material.Editor(j.th, &j.campo, "máquina, host ou 10.1.1.99 ...")
							es.Font = fonteMono
							es.TextSize = unit.Sp(15)
							es.Color = tema.Texto
							es.HintColor = tema.Fraco
							dims := es.Layout(gtx)
							// O foco é pedido DEPOIS do layout do editor:
							// o alvo precisa estar registrado no mesmo
							// quadro, senão o roteador do Gio desfaz o
							// foco sozinho ao fim dele (ver BACKLOG.md).
							gtx.Execute(key.FocusCmd{Tag: &j.campo})
							return dims
						}),
					)
				})
		}),
	)
}

// lista desenha os resultados. Some inteira quando não há termo — a
// caixa nasce sendo só um campo, e cresce conforme a busca discrimina.
func (j *janelaBusca) lista(gtx layout.Context) layout.Dimensions {
	if j.linhas() == 0 {
		return layout.Dimensions{}
	}
	var filhos []layout.FlexChild
	filhos = append(filhos, layout.Rigid(layout.Spacer{Height: 10}.Layout))
	for i, cx := range j.achados {
		filhos = append(filhos, layout.Rigid(j.linha(i, cx, false)))
	}
	if j.temRascunho {
		filhos = append(filhos, layout.Rigid(j.linha(len(j.achados), j.rascunho, true)))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

// linha é um host: nome, grupo e a fileira de ícones de protocolo — a
// MESMA do card do Painel, para o mesmo host não ter duas aparências.
func (j *janelaBusca) linha(i int, cx conexoes.Conexao, avulso bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		gtx.Constraints.Min.Y = gtx.Dp(buscaLinhaAlt)
		return j.cliques[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					size := gtx.Constraints.Min
					if i == j.sel {
						// A linha escolhida é a que o Enter abre: ela
						// precisa de um realce que sobreviva ao vidro,
						// por isso o token de seleção e não o de hover.
						superficie(gtx, size, tema.TermSel, tema.Borda2, 7)
					}
					return layout.Dimensions{Size: size}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 7, Bottom: 7, Left: 10, Right: 10}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(negrito(txt(j.th, fonteCond, spCardHost, cx.Nome, tema.Texto)).Layout),
								layout.Rigid(layout.Spacer{Width: 10}.Layout),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									meta := cx.GrupoStr()
									if avulso {
										meta = "não cadastrado · sessão temporária"
									}
									return rotulo(j.th, fonteMono, spCardMeta, meta, tema.Sec)(gtx)
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return j.selos(gtx, i, cx)
								}),
							)
						})
				}),
			)
		})
	}
}

// selos é a fileira de ícones de protocolo do host, e cada ícone abre o
// SEU protocolo. Diferente do card do Painel, aqui só aparecem os que o
// host tem: a linha da busca é lida de relance, e um slot apagado só
// ocupa largura. O destino avulso não precisa de caso especial — ele
// nasce com todos os protocolos ligados (ver alvoRascunho), então os
// quatro ícones aparecem por consequência da mesma regra.
func (j *janelaBusca) selos(gtx layout.Context, i int, cx conexoes.Conexao) layout.Dimensions {
	var filhos []layout.FlexChild
	for k, e := range protocolos {
		if !cx.Tem(e.p) {
			continue
		}
		est, btn := e, &j.cliquesProto[i][k]
		filhos = append(filhos,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					cor := est.cor()
					return layout.UniformInset(5).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return icone(gtx, est.ic, cor, 16)
					})
				})
			}),
			layout.Rigid(layout.Spacer{Width: 4}.Layout))
	}
	if len(filhos) == 0 {
		return layout.Dimensions{}
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.
		Layout(gtx, filhos[:len(filhos)-1]...)
}
