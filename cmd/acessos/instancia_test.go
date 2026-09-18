//go:build linux

package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pontas devolve as duas pontas de um canal em memória com um serviço já
// atendendo de um lado. net.Pipe basta: o que está sob teste é a conversa,
// não o socket.
func pontas(t *testing.T, s *servico) (net.Conn, *bufio.Reader) {
	t.Helper()
	cliente, servidor := net.Pipe()
	go s.conversa(servidor)
	t.Cleanup(func() { cliente.Close() })
	_ = cliente.SetDeadline(time.Now().Add(5 * time.Second))
	return cliente, bufio.NewReader(cliente)
}

func TestServicoResponde(t *testing.T) {
	s := &servico{}
	c, r := pontas(t, s)

	resp, err := pedirResposta(c, r, mensagem{Tipo: msgPing})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if resp.Tipo != msgPong {
		t.Fatalf("resposta ao ping: %q", resp.Tipo)
	}
	if resp.Versao != versaoInstalada() {
		t.Errorf("versão %q, esperado %q", resp.Versao, versaoInstalada())
	}
}

// O app que chega primeiro fica com a vaga; o segundo é mandado embora
// com "ocupado" — é isso que faz a segunda invocação do Acessos
// encaminhar em vez de abrir outra janela.
func TestUmAppPorVez(t *testing.T) {
	s := &servico{}

	c1, r1 := pontas(t, s)
	resp, err := pedirResposta(c1, r1, mensagem{Tipo: msgOlaApp})
	if err != nil {
		t.Fatalf("primeiro ola-app: %v", err)
	}
	if resp.Tipo != msgOK {
		t.Fatalf("primeiro app recebeu %q, esperado %q", resp.Tipo, msgOK)
	}

	c2, r2 := pontas(t, s)
	resp, err = pedirResposta(c2, r2, mensagem{Tipo: msgOlaApp})
	if err != nil {
		t.Fatalf("segundo ola-app: %v", err)
	}
	if resp.Tipo != msgOcupado {
		t.Fatalf("segundo app recebeu %q, esperado %q", resp.Tipo, msgOcupado)
	}

	// O que o segundo mandar tem de sair na conexão do PRIMEIRO.
	if err := escrever(c2, mensagem{Tipo: msgAbrir,
		Specs: []map[string]string{{"type": "ssh", "host": "10.0.0.9"}}}); err != nil {
		t.Fatalf("encaminhar: %v", err)
	}
	m, err := lerMensagem(r1)
	if err != nil {
		t.Fatalf("o primeiro app não recebeu: %v", err)
	}
	if m.Tipo != msgAbrir || len(m.Specs) != 1 || m.Specs[0]["host"] != "10.0.0.9" {
		t.Fatalf("chegou %+v", m)
	}

	// O "traga-se para a frente" viaja separado da abertura: é o que
	// tira a espera pelo token de ativação da frente de quem escolheu a
	// máquina. Ele tem de chegar pelo mesmo caminho.
	if err := escrever(c2, mensagem{Tipo: msgAtivar, Token: "tk-123"}); err != nil {
		t.Fatalf("encaminhar ativar: %v", err)
	}
	if m, err = lerMensagem(r1); err != nil {
		t.Fatalf("o primeiro app não recebeu o ativar: %v", err)
	}
	if m.Tipo != msgAtivar || m.Token != "tk-123" {
		t.Fatalf("chegou %+v", m)
	}
}

// App fechado libera a vaga: senão o serviço ficaria para sempre achando
// que há uma janela grande, e a busca nunca mais subiria o app.
func TestVagaLiberaQuandoAppCai(t *testing.T) {
	s := &servico{}
	c1, r1 := pontas(t, s)
	if _, err := pedirResposta(c1, r1, mensagem{Tipo: msgOlaApp}); err != nil {
		t.Fatalf("ola-app: %v", err)
	}
	c1.Close()

	// A limpeza acontece na goroutine da conversa, ao ler o EOF.
	prazo := time.Now().Add(2 * time.Second)
	for time.Now().Before(prazo) {
		s.mu.Lock()
		vago := s.app == nil
		s.mu.Unlock()
		if vago {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a vaga do app continuou ocupada depois de a conexão cair")
}

// Socket de um processo que morreu sem apagar o arquivo NÃO é serviço
// vivo: quem sobe depois tem de conseguir a vaga, senão o atalho fica
// morto até alguém apagar o arquivo à mão.
func TestSocketOrfaoNaoBloqueia(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	if err := os.MkdirAll(filepath.Join(dir, "acessos"), 0o700); err != nil {
		t.Fatal(err)
	}
	// um socket de verdade, abandonado: cria e fecha sem remover
	ln, err := net.Listen("unix", caminhoSocket())
	if err != nil {
		t.Fatal(err)
	}
	lnUnix := ln.(*net.UnixListener)
	lnUnix.SetUnlinkOnClose(false)
	ln.Close()

	ln2, err := escutarServico()
	if err != nil {
		t.Fatalf("não consegui assumir o socket órfão: %v", err)
	}
	ln2.Close()
}

// Serviço vivo, ao contrário, tem de recusar o segundo — é o que impede
// dois registros do mesmo atalho global.
func TestServicoVivoRecusaSegundo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	ln, err := escutarServico()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	if ln2, err := escutarServico(); err == nil {
		ln2.Close()
		t.Fatal("dois serviços conseguiram o mesmo socket")
	}
}
