//go:build linux

package main

// O SERVIÇO: o processo sem janela que segura o atalho global e pipoca a
// caixa de busca, com ou sem o app aberto.
//
// Sobe sozinho quando o app parte (ver ligarNoServico) e continua vivo
// depois que a janela grande fecha — é isso que faz o Ctrl+Shift+F12
// funcionar com o app fechado. Para ele continuar existindo depois de um
// logout, há o autostart pelo portal (ver pedirAutostart).
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
	// buscaAberta evita empilhar caixas: apertar o atalho de novo com uma
	// na tela não abre a segunda.
	buscaAberta bool
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
	s := &servico{ini: caminhoINI}

	// O tema tem de estar pronto ANTES da primeira caixa: ela nasce de um
	// sinal do D-Bus, e montar tema/fonte ali dentro atrasaria justo o que
	// tem de ser instantâneo.
	s.th = material.NewTheme()
	s.th.Shaper = shaperDoApp()
	temaApp = s.th
	if arq, err := conexoes.Carregar(caminhoINI); err == nil && arq.Geral["tema"] == "escuro" {
		tema = temaEscuro
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
				s.app = nil
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
			s.mu.Lock()
			ocupado := s.app != nil
			if !ocupado {
				s.app = k
				souOApp = true
			}
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
		case msgAbrir:
			// Veio de uma SEGUNDA instância do app: encaminha para a
			// janela que já existe.
			if !s.mandarParaApp(m) {
				fmt.Fprintln(os.Stderr, "serviço: ninguém para abrir a conexão")
			}
		}
	}
}

// mandarParaApp entrega m à janela grande. Falso quando não há nenhuma.
func (s *servico) mandarParaApp(m mensagem) bool {
	s.mu.Lock()
	k := s.app
	s.mu.Unlock()
	if k == nil {
		return false
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
	tem := s.app != nil
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

	// Espera ele se apresentar. O teto é generoso porque aqui nasce uma
	// janela de verdade — contexto gráfico, fontes, inventário — e falhar
	// por pressa deixaria a máquina escolhida sem abrir, que é o pior
	// desfecho possível para quem apertou o atalho.
	prazo := time.Now().Add(20 * time.Second)
	for time.Now().Before(prazo) {
		time.Sleep(100 * time.Millisecond)
		s.mu.Lock()
		tem = s.app != nil
		s.mu.Unlock()
		if tem {
			return true
		}
	}
	fmt.Fprintln(os.Stderr, "serviço: o app não se apresentou a tempo")
	return false
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
		a, err := registrarAtalhoGlobal("abrir-busca",
			"Abrir a busca de máquinas do Acessos", "CTRL+SHIFT+F12",
			func(token string) { s.abrirBusca(token) })
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "atalho global: %v (nova tentativa em %s)\n", err, espera)
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
		// Com o atalho de pé, vale pedir para o sistema subir o serviço
		// no login — é o que faz o Ctrl+Shift+F12 existir numa sessão em
		// que ninguém abriu o app ainda. Pergunta uma vez só; ver
		// autostart_linux.go.
		go garantirAutostart(s.ini)

		// Registrado. A goroutine de sinais do portal fica com ele; só
		// voltamos aqui se a sessão do portal cair (portal reiniciado,
		// logout parcial), e aí registramos de novo.
		<-a.Caiu
		fmt.Fprintln(os.Stderr, "atalho global: a sessão do portal caiu; registrando de novo")
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
		func() {
			s.mu.Lock()
			s.buscaAberta = false
			s.mu.Unlock()
		})
}
