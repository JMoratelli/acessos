package main

// Conversa entre os processos do Acessos: o SERVIÇO (que segura o atalho
// global e pipoca a caixa de busca) e o APP (a janela grande).
//
// ----------------------------------------------------------------------
// POR QUE EXISTE UM SEGUNDO PROCESSO
// ----------------------------------------------------------------------
//
// O atalho global do Wayland vive numa SESSÃO do portal
// (org.freedesktop.portal.GlobalShortcuts), e essa sessão morre junto com
// o processo que a abriu. Enquanto quem registrava o atalho era o app, o
// Ctrl+Shift+F12 só existia com o app aberto — e, pior, abrir o app duas
// vezes registrava o atalho duas vezes: um aperto de tecla, duas caixas
// de busca na tela.
//
// O serviço resolve as duas de uma vez: ele é o único a registrar o
// atalho, vive independente da janela grande e atende o app pelo socket.
// É um processo sem janela até a caixa de busca aparecer.
//
// ----------------------------------------------------------------------
// QUEM FALA COM QUEM
// ----------------------------------------------------------------------
//
//	app (1ª instância)  --ola-app-->  serviço      "eu sou a janela grande"
//	serviço             --abrir--->   app          "abre esta máquina"
//	app (2ª instância)  --abrir--->   serviço --> app já aberto
//
// O socket também dá INSTÂNCIA ÚNICA de graça: a segunda invocação do app
// descobre que já há uma janela, manda para ela o que veio na linha de
// comando e sai, em vez de abrir uma segunda janela com o mesmo
// inventário.
//
// O formato é JSON por linha. Não é protocolo de rede — é um socket de
// usuário, dentro do XDG_RUNTIME_DIR, que só o próprio usuário abre.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"

	"acessos-go/internal/conexoes"
)

// Tipos de mensagem. Strings, e não números: o que este canal precisa é
// sobreviver a uma versão nova conversando com uma velha, e um tipo
// desconhecido tem de ser ignorável — ver lerMensagem.
const (
	msgOlaApp  = "ola-app" // o app se apresenta ao serviço
	msgAbrir   = "abrir"   // abra estas conexões na janela grande
	msgBusca   = "busca"   // pipoque a caixa de busca (o app tem foco, o serviço não)
	msgAtivar  = "ativar"  // traga a janela grande para a frente com este token
	msgPing    = "ping"
	msgPong    = "pong"
	msgOK      = "ok"
	msgOcupado = "ocupado" // já há um app registrado
	msgSemApp  = "sem-app" // ninguém para abrir a conexão
	msgSair    = "sair"    // serviço de versão antiga, pode encerrar
)

// mensagem é tudo o que trafega no socket. Campos opcionais em vez de um
// tipo por mensagem: são quatro mensagens curtas, e uma struct só deixa a
// leitura em um lugar.
type mensagem struct {
	Tipo   string              `json:"tipo"`
	Versao string              `json:"versao,omitempty"`
	Specs  []map[string]string `json:"specs,omitempty"`
	// Token é o de ativação do Wayland: a caixa de busca o pede enquanto
	// TEM o foco e o entrega junto, para a janela grande poder se trazer
	// para a frente. Ver buscapop.go.
	Token string `json:"token,omitempty"`
	// Alvo é o que a CAIXA DE BUSCA escolheu. Vem separado de Specs
	// porque é outra coisa: Specs é linha de comando (-conn), já resolvida
	// por quem digitou; Alvo é uma escolha no inventário, que a janela
	// grande ainda vai resolver com as credenciais dela.
	Alvo *alvoAbrir `json:"alvo,omitempty"`
}

// alvoAbrir é uma máquina escolhida na caixa de busca.
//
// De propósito, a máquina CADASTRADA viaja só pelo nome: quem resolve
// usuário, senha e cofre é a janela grande, com o inventário dela, e
// assim nenhuma senha passa pelo socket. Só o destino avulso (digitado na
// hora, sem cadastro em lugar nenhum) viaja inteiro — ele não existe do
// outro lado para ser procurado, e não tem senha guardada por definição.
type alvoAbrir struct {
	Nome      string            `json:"nome"`
	Protocolo string            `json:"protocolo"`
	Avulso    bool              `json:"avulso,omitempty"`
	Conexao   *conexoes.Conexao `json:"conexao,omitempty"`
}

func escrever(w io.Writer, m mensagem) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// lerMensagem devolve a próxima mensagem. Linha ilegível é ERRO, não
// mensagem vazia: continuar lendo um fluxo que já saiu de sincronia é
// pior que fechar e deixar quem fala reconectar.
func lerMensagem(r *bufio.Reader) (mensagem, error) {
	linha, err := r.ReadBytes('\n')
	if err != nil {
		return mensagem{}, err
	}
	var m mensagem
	if err := json.Unmarshal(linha, &m); err != nil {
		return mensagem{}, fmt.Errorf("mensagem inválida: %w", err)
	}
	return m, nil
}

// canal é uma conexão com a escrita SERIALIZADA.
//
// Não é zelo preventivo: sem isto, a resposta que a conversa do app está
// escrevendo e um pedido empurrado por OUTRA conexão ("abra esta máquina")
// mexiam no mesmo bufio.Writer ao mesmo tempo. O sintoma é feio e não
// aponta para a causa — `short write` no flush, conexão derrubada, e o app
// perdendo o canal com o serviço bem no meio de um aperto de mão (pego por
// TestUmAppPorVez).
type canal struct {
	c  net.Conn
	mu sync.Mutex
	w  *bufio.Writer
}

func novoCanal(c net.Conn) *canal {
	return &canal{c: c, w: bufio.NewWriter(c)}
}

func (k *canal) enviar(m mensagem) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := escrever(k.w, m); err != nil {
		return err
	}
	return k.w.Flush()
}

func (k *canal) fechar() { _ = k.c.Close() }

// pedirResposta manda m e espera UMA resposta. Usado só no aperto de mão,
// onde a conversa é pergunta-resposta; depois dele o canal vira mão única
// (o serviço empurra "abrir" e ninguém responde).
func pedirResposta(c net.Conn, r *bufio.Reader, m mensagem) (mensagem, error) {
	if err := escrever(c, m); err != nil {
		return mensagem{}, err
	}
	return lerMensagem(r)
}
