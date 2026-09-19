//go:build linux

package main

// O socket do serviço e a lógica de "quem sobe primeiro manda".
//
// Mora no XDG_RUNTIME_DIR de propósito: é um diretório por usuário e por
// sessão, apagado no logout, com permissão 0700 dada pelo sistema. Dentro
// do Flatpak ele é privado do aplicativo, então duas instâncias do Acessos
// se acham e nenhum outro programa esbarra nele.

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// ArgServico é o argumento que transforma este binário no serviço.
const ArgServico = "-servico"

func dirRuntime() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "acessos")
	}
	// Sem XDG_RUNTIME_DIR (sessão estranha, cron, contêiner): /tmp com o
	// uid no nome, para dois usuários na mesma máquina não colidirem.
	return filepath.Join(os.TempDir(), fmt.Sprintf("acessos-%d", os.Getuid()))
}

func caminhoSocket() string { return filepath.Join(dirRuntime(), "servico.sock") }

// escutarServico abre o socket. Devolve erro quando JÁ HÁ um serviço vivo
// atendendo — é assim que o segundo serviço descobre que não é preciso.
//
// Socket órfão (o processo morreu sem apagar o arquivo) não é "ocupado":
// o bind falha com "address already in use" mesmo sem ninguém do outro
// lado. Por isso a checagem é tentar FALAR com ele antes de apagar.
func escutarServico() (net.Listener, error) {
	dir := dirRuntime()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	caminho := caminhoSocket()
	ln, err := net.Listen("unix", caminho)
	if err == nil {
		return ln, nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		return nil, err
	}
	if c, derr := net.DialTimeout("unix", caminho, 300*time.Millisecond); derr == nil {
		c.Close()
		return nil, fmt.Errorf("já há um serviço do Acessos atendendo em %s", caminho)
	}
	// ninguém atende: o arquivo é restolho de um processo morto
	if err := os.Remove(caminho); err != nil {
		return nil, err
	}
	return net.Listen("unix", caminho)
}

// conectarServico fala com o serviço, se houver um. Prazo curto: um
// serviço que não responde em meio segundo é um serviço travado, e
// esperar por ele seria segurar a partida do app.
func conectarServico() (net.Conn, error) {
	return net.DialTimeout("unix", caminhoSocket(), 500*time.Millisecond)
}

// subirServico lança este mesmo binário em modo serviço e espera ele
// abrir o socket. É o app quem faz isso na partida: assim, quem nunca
// mexeu em autostart ganha o atalho global do mesmo jeito, e ele
// continua valendo depois que a janela grande fecha.
func subirServico(caminhoINI string) (net.Conn, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// O -ini vai junto: quem abriu o app pode ter apontado outro
	// inventário, e um serviço lendo o arquivo padrão mostraria na busca
	// um parque de máquinas diferente do que está na tela.
	cmd := exec.Command(exe, ArgServico, "-ini", caminhoINI)
	// Sem herdar a entrada e com o processo desligado do grupo deste: o
	// serviço tem de sobreviver ao app que o subiu, inclusive quando o
	// app é fechado por Ctrl+C no terminal.
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Não esperamos o processo: ele é para viver sozinho. Liberar o
	// registro dele é tarefa do init quando o app sair.
	go func() { _ = cmd.Process.Release() }()

	// O socket aparece assim que o serviço escuta. 2s é folga: medido,
	// ele sobe em menos de 100ms (não abre janela nem lê tela).
	prazo := time.Now().Add(2 * time.Second)
	for time.Now().Before(prazo) {
		if c, err := conectarServico(); err == nil {
			return c, nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil, fmt.Errorf("o serviço do atalho não abriu %s a tempo", caminhoSocket())
}

// clienteServico é a ponta do APP no canal com o serviço.
type clienteServico struct {
	c net.Conn
	r *bufio.Reader
	// Msgs entrega o que o serviço mandar (abrir uma máquina, pipocar a
	// caixa de busca). Quem consome é main.go.
	Msgs chan mensagem
	// mu serializa a escrita — ver Enviar.
	mu sync.Mutex
}

// ligarNoServico é o aperto de mão do app. Devolve:
//
//	cli != nil            -> este processo é a janela grande
//	cli == nil, ja == true -> JÁ existe uma janela grande; o chamador deve
//	                          encaminhar o que tem e sair
func ligarNoServico(caminhoINI string, specs []map[string]string) (cli *clienteServico, ja bool) {
	c, err := conectarServico()
	if err != nil {
		if c, err = subirServico(caminhoINI); err != nil {
			fmt.Fprintf(os.Stderr, "atalho global: sem serviço (%v)\n", err)
			return nil, false
		}
	}
	r := bufio.NewReader(c)

	// Versão diferente = binário atualizado com um serviço velho ainda no
	// ar. Pedimos que ele saia e subimos o nosso: um serviço velho
	// falando um protocolo velho é pior que nenhum.
	if resp, err := pedirResposta(c, r, mensagem{Tipo: msgPing}); err == nil &&
		resp.Tipo == msgPong && resp.Versao != versaoInstalada() {
		_ = escrever(c, mensagem{Tipo: msgSair})
		c.Close()
		time.Sleep(200 * time.Millisecond)
		if c, err = subirServico(caminhoINI); err != nil {
			fmt.Fprintf(os.Stderr, "atalho global: sem serviço (%v)\n", err)
			return nil, false
		}
		r = bufio.NewReader(c)
	}

	resp, err := pedirResposta(c, r, mensagem{
		Tipo: msgOlaApp, Versao: versaoInstalada()})
	if err != nil {
		c.Close()
		fmt.Fprintf(os.Stderr, "atalho global: %v\n", err)
		return nil, false
	}
	if resp.Tipo == msgOcupado {
		// Segunda instância: encaminha o que veio na linha de comando
		// para a janela que já existe e sai. Não abrimos uma segunda
		// janela sobre o mesmo inventário.
		_ = escrever(c, mensagem{Tipo: msgAbrir, Specs: specs})
		c.Close()
		return nil, true
	}

	cli = &clienteServico{c: c, r: r, Msgs: make(chan mensagem, 8)}
	go cli.ler()
	return cli, false
}

func (cli *clienteServico) ler() {
	defer close(cli.Msgs)
	for {
		m, err := lerMensagem(cli.r)
		if err != nil {
			return
		}
		if m.Tipo != msgAbrir && m.Tipo != msgBusca && m.Tipo != msgAtivar {
			continue // mensagem que esta versão não conhece: ignora
		}
		select {
		case cli.Msgs <- m:
		default: // fila cheia: janela travada, descartar é melhor que travar aqui
		}
	}
}

func (cli *clienteServico) Fechar() { _ = cli.c.Close() }

// Enviar manda uma mensagem ao serviço. Depois do aperto de mão o app só
// escreve daqui (hoje, o aviso de que o atalho global mudou nos Ajustes),
// e sempre da goroutine do laço de quadro — mas a trava fica porque o
// caminho de escrita do serviço já ensinou que confiar em "só um escreve"
// custa caro: ver o comentário do tipo canal, em instancia.go.
func (cli *clienteServico) Enviar(m mensagem) error {
	if cli == nil {
		return nil
	}
	cli.mu.Lock()
	defer cli.mu.Unlock()
	return escrever(cli.c, m)
}
