//go:build linux

package main

// O SERVIÇO: o processo sem janela que segura o atalho global e pipoca a
// caixa de busca, com ou sem o app aberto.
//
// Sobe sozinho quando o app parte (ver ligarNoServico) e continua vivo
// depois que a janela grande fecha — é isso que faz o Ctrl+Shift+F12
// funcionar com o app fechado. Para ele continuar existindo depois de um
// logout, há o autostart pelo portal (ver definirAutostart).
//
// O porquê da arquitetura, e o problema das duas caixas, está em
// instancia.go.

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"acessos-go/internal/conexoes"

	"gioui.org/app"
	"gioui.org/widget/material"
)

type servico struct {
	ini string
	th  *material.Theme

	mu sync.Mutex
	// app é o canal com a janela grande, quando há uma. nil = app fechado.
	// É um *canal, e não o bufio.Writer cru, porque duas goroutines
	// escrevem nele: a conversa do próprio app e quem empurra pedidos de
	// fora (outra instância, ou a caixa de busca). Ver instancia.go.
	app *canal
	// appPronto separa "a vaga é dele" de "o canal já aceita empurrão".
	// Entre uma coisa e outra existe o msgOK do aperto de mão, e do outro
	// lado pedirResposta lê EXATAMENTE uma mensagem: qualquer coisa
	// empurrada nessa janela seria consumida COMO a resposta do
	// handshake. Ver msgOlaApp.
	appPronto bool
	// appProntoCh fecha quando appPronto vira true. Existe porque
	// DESISTIR na janela do aperto de mão também está errado: um "abrir"
	// vindo de uma segunda instância chega exatamente quando a primeira
	// janela acabou de nascer, e descartá-lo perde o pedido de quem
	// clicou. Com o canal, quem empurra espera o handshake terminar em
	// vez de falhar — ver mandarParaApp.
	appProntoCh chan struct{}
	// buscaAberta evita empilhar caixas: apertar o atalho de novo com uma
	// na tela não abre a segunda.
	buscaAberta bool
	// gatilho guarda a tecla que o sistema amarrou, e gatilhoSabido diz
	// se já houve registro. São guardados porque o app costuma se
	// apresentar DEPOIS do registro: sem isto, a janela que abre mais
	// tarde nunca ficaria sabendo que o atalho está sem tecla — só a que
	// estivesse aberta no instante exato do registro. Ver msgGatilho, em
	// instancia.go.
	gatilho       string
	gatilhoSabido bool

	// chegouApp avisa garantirApp na hora em que uma janela grande se
	// apresenta. Antes ele só perguntava de 100 em 100ms, e essa espera
	// caía inteira em cima de quem acabou de escolher a máquina.
	chegouApp chan struct{}

	// atalhoMudou é só a BATIDA NA PORTA avisando que a chave
	// `[geral] atalho_global` mudou (msgAtalho, vindo dos Ajustes). O
	// valor não viaja aqui de propósito: quem manda é o .ini, e
	// manterAtalho o relê a cada acordada. Assim um aviso descartado por
	// fila cheia não deixa o serviço num estado que não bate com o
	// arquivo — o aviso que sobrou na fila releva o mesmo arquivo e
	// chega na mesma conclusão.
	atalhoMudou chan struct{}
}

// rodarServico é o modo -servico. Não retorna: ou o socket já tem dono (e
// aí sai na hora, porque não faltava serviço nenhum), ou fica de pé até
// alguém matá-lo.
func rodarServico(caminhoINI string) {
	ln, err := escutarServico()
	if err != nil {
		// Não é erro: é o caso normal de dois apps subindo quase juntos,
		// os dois tentando garantir que existe serviço.
		fmt.Fprintf(os.Stderr, "serviço: %v\n", err)
		return
	}
	s := &servico{
		ini:         caminhoINI,
		chegouApp:   make(chan struct{}, 1),
		atalhoMudou: make(chan struct{}, 1),
	}

	// O tema tem de estar pronto ANTES da primeira caixa: ela nasce de um
	// sinal do D-Bus, e montar tema/fonte ali dentro atrasaria justo o que
	// tem de ser instantâneo.
	s.th = material.NewTheme()
	s.th.Shaper = shaperDoApp()
	temaApp = s.th
	// Tema E escala da interface — as duas preferências da pessoa, pelo
	// mesmo aplicarGeral que o app usa (persistir.go). A fonte faltava
	// aqui: este processo lia só o tema, e a caixa nascia em tamanho base
	// para quem tinha o A+ ligado.
	if arq, err := conexoes.Carregar(caminhoINI); err == nil {
		aplicarGeral(arq.Geral)
	}

	go s.atender(ln)
	go s.manterAtalho()

	fmt.Printf("serviço do Acessos %s de pé em %s\n", versaoInstalada(), caminhoSocket())
	// A caixa de busca é uma janela do Gio; o laço dela precisa que o
	// programa tenha chamado app.Main() na goroutine principal.
	app.Main()
}

// ---------------------------------------------------------------- socket

func (s *servico) atender(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "serviço: %v\n", err)
			return
		}
		go s.conversa(c)
	}
}

func (s *servico) conversa(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	k := novoCanal(c)
	souOApp := false
	defer func() {
		if souOApp {
			s.mu.Lock()
			if s.app == k {
				s.app, s.appPronto, s.appProntoCh = nil, false, nil
			}
			s.mu.Unlock()
		}
	}()

	responder := func(m mensagem) bool { return k.enviar(m) == nil }

	for {
		m, err := lerMensagem(r)
		if err != nil {
			return
		}
		switch m.Tipo {
		case msgPing:
			if !responder(mensagem{Tipo: msgPong, Versao: versaoInstalada()}) {
				return
			}
		case msgSair:
			// Um app mais novo pediu a vaga. Sair é a resposta certa: dois
			// serviços de versões diferentes no mesmo socket é pior que
			// uma troca de guarda com meio segundo sem atalho.
			fmt.Fprintln(os.Stderr, "serviço: versão nova pediu a vaga; saindo")
			os.Exit(0)
		case msgOlaApp:
			// A vaga é tomada AQUI (ninguém mais entra), mas o canal só
			// passa a aceitar empurrão depois do msgOK — ver appPronto.
			s.mu.Lock()
			ocupado := s.app != nil
			if !ocupado {
				s.app, s.appPronto = k, false
				s.appProntoCh = make(chan struct{})
				souOApp = true
			}
			pronto := s.appProntoCh
			s.mu.Unlock()
			if ocupado {
				if !responder(mensagem{Tipo: msgOcupado}) {
					return
				}
				continue
			}
			if !responder(mensagem{Tipo: msgOK}) {
				return
			}

			// Só agora o canal é de mão única. Antes desta linha, um
			// guardarGatilho disparado por manterAtalho no mesmo instante
			// escrevia a notícia do gatilho no socket ANTES do msgOK: o
			// pedirResposta do app lia essa mensagem como a resposta do
			// aperto de mão, o msgOK caía depois como tipo desconhecido
			// no cli.ler() e a notícia de "sem tecla" se perdia para
			// sempre — calada. Um msgAbrir ou msgBusca no mesmo instante
			// sumia do mesmo jeito.
			s.mu.Lock()
			s.appPronto = true
			sabido, tecla := s.gatilhoSabido, s.gatilho
			s.mu.Unlock()
			close(pronto) // solta quem estiver esperando em mandarParaApp

			// Quem espera por uma janela só é acordado com o canal já
			// pronto, senão garantirApp devolveria true para um app que
			// ainda não pode receber.
			select {
			case s.chegouApp <- struct{}{}:
			default:
			}

			// O atalho normalmente já foi registrado quando a janela
			// aparece: conta a ela o que se sabe, senão a notícia de
			// "sem tecla" só existiria para quem estivesse aberto no
			// instante exato do registro.
			if sabido && !responder(mensagem{Tipo: msgGatilho, Gatilho: tecla}) {
				return
			}
		case msgAbrir, msgAtivar:
			// Veio de uma SEGUNDA instância do app: encaminha para a
			// janela que já existe.
			if !s.mandarParaApp(m) {
				fmt.Fprintln(os.Stderr, "serviço: ninguém para abrir a conexão")
			}
		case msgAtalho:
			// Os Ajustes ligaram/desligaram o atalho. Quem lê a chave e
			// registra (ou solta) é manterAtalho; aqui só batemos na
			// porta. Sem bloquear, e sem o valor: ver atalhoMudou.
			select {
			case s.atalhoMudou <- struct{}{}:
			default:
			}
		}
	}
}

// guardarGatilho registra a tecla amarrada e avisa a janela, se houver
// uma. Guardar antes de mandar é o que cobre os dois tempos possíveis: a
// janela já aberta recebe agora, e a que abrir depois recebe no aperto
// de mão (ver msgOlaApp).
func (s *servico) guardarGatilho(tecla string) {
	s.mu.Lock()
	s.gatilho, s.gatilhoSabido = tecla, true
	s.mu.Unlock()
	s.mandarParaApp(mensagem{Tipo: msgGatilho, Gatilho: tecla})
}

// mandarParaApp entrega m à janela grande. Falso quando não há nenhuma.
func (s *servico) mandarParaApp(m mensagem) bool {
	s.mu.Lock()
	k, pronto, ch := s.app, s.appPronto, s.appProntoCh
	s.mu.Unlock()
	if k == nil {
		return false
	}
	if !pronto {
		// Aperto de mão em curso. Esperar é o certo: esta é a janela em
		// que um "abrir" de segunda instância mais aparece — a pessoa
		// clicou no atalho, o app está subindo — e desistir aqui perderia
		// o pedido dela. Com prazo, porque um app que trava no meio do
		// handshake não pode prender quem empurra para sempre.
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			return false
		}
		s.mu.Lock()
		k, pronto = s.app, s.appPronto
		s.mu.Unlock()
		if k == nil || !pronto {
			return false
		}
	}
	if err := k.enviar(m); err != nil {
		// Escrita que falha é canal fora de sincronia: fechar libera a
		// vaga (o laço de leitura vê o EOF) e o próximo pedido sobe um
		// app novo em vez de falar com um fantasma.
		fmt.Fprintf(os.Stderr, "serviço: perdi o canal com o app (%v)\n", err)
		k.fechar()
		return false
	}
	return true
}

// garantirApp devolve true quando há uma janela grande pronta para
// receber. Sem nenhuma, SOBE o app e espera ele se apresentar — é o
// caminho do "atalho com o app fechado": a caixa aparece na hora, o app
// grande só nasce quando uma máquina é escolhida.
func (s *servico) garantirApp() bool {
	s.mu.Lock()
	tem := s.app != nil && s.appPronto
	s.mu.Unlock()
	if tem {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serviço: %v\n", err)
		return false
	}
	// Mesmo inventário do serviço: ver o -ini em subirServico.
	cmd := exec.Command(exe, "-ini", s.ini)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "serviço: não subi o app (%v)\n", err)
		return false
	}
	go func() { _ = cmd.Wait() }()
	partiu := time.Now()

	// Espera ele se apresentar, acordando no INSTANTE em que isso
	// acontece (ver chegouApp). O teto é generoso porque aqui nasce uma
	// janela de verdade — contexto gráfico, fontes, inventário — e falhar
	// por pressa deixaria a máquina escolhida sem abrir, que é o pior
	// desfecho possível para quem apertou o atalho.
	temApp := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.app != nil
	}
	prazo := time.After(20 * time.Second)
	for {
		select {
		case <-s.chegouApp:
			if temApp() {
				fmt.Fprintf(os.Stderr, "serviço: o app subiu em %s\n",
					time.Since(partiu).Round(time.Millisecond))
				return true
			}
		case <-prazo:
			// Última conferência antes de desistir: o aviso é um canal de
			// um lugar só, e uma segunda espera em paralelo poderia ter
			// consumido o daqui.
			if temApp() {
				return true
			}
			fmt.Fprintln(os.Stderr, "serviço: o app não se apresentou a tempo")
			return false
		}
	}
}

// ---------------------------------------------------------------- atalho

// manterAtalho registra o atalho e o mantém registrado.
//
// Com repetição: o portal pode não estar de pé quando o serviço sobe
// (autostart no login é exatamente esse instante), e uma única tentativa
// que falha deixava o atalho morto até o próximo reinício do app — sem
// nada na tela dizendo por quê.
func (s *servico) manterAtalho() {
	espera := 2 * time.Second
	for {
		// Desligado pelos Ajustes (ver atalhopref.go): fica parado aqui,
		// sem registrar nada, até alguém religar. O serviço continua de
		// pé — ele também é instância única e ponte para abrir conexão
		// vinda de outra janela.
		if !atalhoGlobalLigado(s.ini) {
			fmt.Fprintln(os.Stderr, "atalho global: desligado nos Ajustes")
			for {
				<-s.atalhoMudou
				if atalhoGlobalLigado(s.ini) {
					break
				}
			}
			espera = 2 * time.Second
		}

		a, err := registrarAtalhoGlobal("abrir-busca",
			"Abrir a busca de máquinas do Acessos", "CTRL+SHIFT+F12",
			func(token string) { s.abrirBusca(token) })
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "atalho global: %v (nova tentativa em %s)\n", err, espera)
			// Conta à janela que NÃO há tecla. Sem isto, um registro que
			// falha (portal ainda não de pé no login, que é exatamente
			// quando o autostart nos sobe) deixava a caixa marcada, sem
			// aviso nenhum, e o Ctrl+Shift+F12 morto por até dois
			// minutos de backoff — o mesmo silêncio que este aviso
			// existe para acabar.
			s.guardarGatilho("")
			time.Sleep(espera)
			if espera < 2*time.Minute {
				espera *= 2
			}
			continue
		case a.Gatilho == "":
			fmt.Fprintln(os.Stderr, "atalho global registrado SEM TECLA — "+
				"amarre em Preferências do Sistema → Atalhos → Acessos")
		default:
			fmt.Printf("atalho global: %s\n", a.Gatilho)
		}
		// O stderr acima é para quem roda pelo terminal; quem abriu pelo
		// menu não o vê. A notícia que chega na TELA sai daqui.
		s.guardarGatilho(a.Gatilho)
		// Com o atalho de pé, vale pedir para o sistema subir o serviço
		// no login — é o que faz o Ctrl+Shift+F12 existir numa sessão em
		// que ninguém abriu o app ainda. Pergunta uma vez só; ver
		// autostart_linux.go.
		go garantirAutostart(s.ini)

		// Registrado. A goroutine de sinais do portal fica com ele.
		// Voltamos aqui por dois motivos: a sessão do portal caiu (portal
		// reiniciado, logout parcial) e é preciso registrar de novo, ou o
		// app desligou o atalho pelos Ajustes e é preciso SOLTAR a tecla.
		// O laço aqui dentro é o que impede registrar DUAS vezes: um
		// aviso de "mudou" que no fim das contas manteve o atalho ligado
		// (desligar e religar antes de chegarmos aqui) tem de voltar a
		// esperar, não cair no registrarAtalhoGlobal lá de cima. Dois
		// registros vivos é o bug de "um aperto, duas caixas de busca"
		// que motivou o processo separado — ver instancia.go.
		for soltou := false; !soltou; {
			select {
			case <-a.Caiu:
				fmt.Fprintln(os.Stderr, "atalho global: a sessão do portal caiu; registrando de novo")
				// A tecla foi embora com a sessão: manter a anterior
				// guardada faria a janela seguir anunciando um atalho
				// que não existe mais.
				s.guardarGatilho("")
				soltou = true
			case <-s.atalhoMudou:
				if atalhoGlobalLigado(s.ini) {
					continue // religado antes de soltarmos: segue de pé
				}
				fmt.Fprintln(os.Stderr, "atalho global: desligado nos Ajustes; soltando a tecla")
				a.Fechar()
				s.guardarGatilho("")
				// Espera o portal confirmar pelo Session.Closed, mas COM
				// PRAZO: a especificação não garante esse sinal para quem
				// fechou a própria sessão, e esperar sem prazo prenderia
				// o serviço aqui para sempre — atalho solto e sem jeito
				// de religar. O que importa (tecla liberada) já aconteceu
				// no Fechar; isto é só para não deixar a goroutine de
				// sinais da sessão velha de pé junto com um registro novo.
				select {
				case <-a.Caiu:
				case <-time.After(5 * time.Second):
					fmt.Fprintln(os.Stderr, "atalho global: o portal não confirmou o fim da sessão")
				}
				soltou = true
			}
		}
		espera = 2 * time.Second
	}
}

// ---------------------------------------------------------------- busca

// abrirBusca é o que o atalho dispara.
//
// Com o app ABERTO, quem mostra a caixa é ele, não o serviço. Não é
// detalhe: o compositor decide quem pode tomar o foco pelo histórico de
// interação do processo, e um serviço de segundo plano é o caso clássico
// de janela que nasce atrás de tudo. O app acabou de ser usado; o
// serviço, não. Só com o app fechado — quando não há alternativa — é que
// a caixa nasce aqui.
func (s *servico) abrirBusca(token string) {
	if s.mandarParaApp(mensagem{Tipo: msgBusca, Token: token}) {
		return
	}
	s.abrirBuscaAqui(token)
}

func (s *servico) abrirBuscaAqui(token string) {
	s.mu.Lock()
	if s.buscaAberta {
		s.mu.Unlock()
		return // já há uma na tela; apertar de novo não empilha
	}
	s.buscaAberta = true
	s.mu.Unlock()

	// O inventário é lido AGORA, a cada abertura. É barato (um .ini) e
	// evita a classe inteira de bugs de "a máquina que acabei de cadastrar
	// não aparece na busca": o serviço é longevo, e uma cópia guardada na
	// partida envelheceria junto com ele.
	arq, err := conexoes.Carregar(s.ini)
	if err != nil {
		fmt.Fprintf(os.Stderr, "busca: não consegui ler %s: %v\n", s.ini, err)
		s.mu.Lock()
		s.buscaAberta = false
		s.mu.Unlock()
		return
	}

	// E as preferências junto, pelo mesmo motivo do parágrafo acima: o
	// tema e o A+ que valem são os que a pessoa deixou por último no app,
	// não os que estavam no .ini quando este serviço subiu — ele pode
	// estar de pé há dias. O arquivo já está aqui na mão; custa nada.
	aplicarGeral(arq.Geral)

	abrirJanelaBusca(s.th, arq, token,
		func(cx conexoes.Conexao, p conexoes.Protocolo, avulso bool, tk string) {
			m := mensagem{Tipo: msgAbrir, Token: tk, Alvo: &alvoAbrir{
				Nome: cx.Nome, Protocolo: string(p), Avulso: avulso}}
			if avulso {
				// Destino avulso não existe no inventário do app; vai
				// inteiro. Não carrega senha — ver alvoAbrir.
				copia := cx
				m.Alvo.Conexao = &copia
			}
			// Em goroutine: garantirApp pode esperar o app inteiro subir,
			// e isto é chamado do laço da janela da busca.
			go func() {
				if !s.garantirApp() {
					return
				}
				if !s.mandarParaApp(m) {
					fmt.Fprintln(os.Stderr, "busca: não consegui entregar ao app")
				}
			}()
		},
		// A ativação vai numa mensagem própria, depois da abertura: o
		// token só serve para trazer a janela grande para a frente, e
		// segurar a abertura à espera dele era o que fazia o app demorar
		// a aparecer.
		func(tk string) {
			s.mandarParaApp(mensagem{Tipo: msgAtivar, Token: tk})
		},
		func() {
			s.mu.Lock()
			s.buscaAberta = false
			s.mu.Unlock()
		})
}
