package main

import (
	"fmt"
	"image"
	"os"
	"strconv"

	"acessos-go/internal/chaveiro"
	"acessos-go/internal/cofre"
	"acessos-go/internal/conexoes"

	"gioui.org/app"
)

// caminhoINI e recarregarINI são preenchidos no main: o diálogo do cofre
// precisa saber QUAL arquivo regravar ao trocar a senha mestra, e o painel
// precisa recarregar depois disso.
// abrirEmSegundoPlano vale só durante a chamada de abrirConexao (botão do
// meio no card): a aba entra na tira sem roubar o foco de quem está
// olhando a lista.
var abrirEmSegundoPlano bool

var (
	caminhoINI    string
	recarregarINI func()

	// chaveiroAtual é o chaveiro.ini lido no start (nil quando o arquivo
	// ainda não existe — instalação antiga, com o cofre dentro do
	// conexoes.ini). É dele que saem as credenciais por alias "!nome".
	chaveiroAtual *chaveiro.Arquivo
)

// paramsCofre devolve de onde vêm salt/kdf/verificador: o chaveiro tem
// precedência, porque é para lá que o app original move o cofre; o
// conexoes.ini continua valendo para quem ainda não migrou.
func paramsCofre(arq *conexoes.Arquivo) map[string]string {
	if chaveiroAtual != nil && len(chaveiroAtual.Cofre) > 0 {
		return chaveiroAtual.Cofre
	}
	return arq.Cofre
}

// cofreAberto é o cofre destrancado desta sessão (nil enquanto trancado).
// Hoje só destranca pela variável de ambiente ACESSOS_SENHA_MESTRA, a
// mesma escapatória não-interativa do app original; o diálogo de senha
// mestra é o próximo passo.
var cofreAberto *cofre.Cofre

// destrancarCofre tenta abrir o cofre com a senha do ambiente. Sem senha,
// segue trancado — conexões com senha em claro continuam funcionando.
func destrancarCofre(arq *conexoes.Arquivo) {
	senha := os.Getenv("ACESSOS_SENHA_MESTRA")
	if senha == "" || len(paramsCofre(arq)) == 0 {
		return
	}
	p := paramsCofre(arq)
	c, err := cofre.Abrir(cofre.Parametros{
		KDF:         p["kdf"],
		Salt:        p["salt"],
		Verificador: p["verificador"],
	}, senha)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cofre: %v\n", err)
		return
	}
	cofreAberto = c
	fmt.Println("cofre destrancado (ACESSOS_SENHA_MESTRA)")
	migrarChaveiro()
}

func segredo(valor string) (string, error) {
	// "!nome" aponta para uma credencial do chaveiro: resolve primeiro, e
	// o que voltar ainda pode estar cifrado.
	if chaveiro.EhAlias(valor) {
		resolvido := chaveiroAtual.Resolver(valor, "senha")
		if resolvido == valor {
			// alias quebrado: a credencial não existe (mais). Dizer isso
			// é melhor que mandar "!nome" como senha e ver o servidor
			// recusar sem explicação.
			return "", fmt.Errorf("a credencial %q não existe no chaveiro", valor[1:])
		}
		valor = resolvido
	}
	// Conserto de dano antigo: houve versão que CIFRAVA o alias. O
	// resultado é um segredo cujo conteúdo é "!nome", que ia como senha
	// para o servidor e falhava a autenticação sem explicação — ver o
	// desfecho lá embaixo, depois de decifrar.
	if !cofre.Cifrado(valor) {
		return valor, nil
	}
	if cofreAberto == nil {
		return "", fmt.Errorf("senha guardada no cofre e o cofre está trancado " +
			"(defina ACESSOS_SENHA_MESTRA)")
	}
	claro, err := cofreAberto.Decifrar(valor)
	if err != nil {
		return "", err
	}
	if chaveiro.EhAlias(claro) {
		return chaveiroAtual.Resolver(claro, "senha"), nil
	}
	return claro, nil
}

// abrirConexao transforma um card do painel numa aba nova. Se já houver
// uma aba para a mesma conexão+protocolo, só troca pra ela — clicar duas
// vezes não deve abrir duas sessões pro mesmo lugar.
func abrirConexao(w *app.Window, bar *tabBar, arq *conexoes.Arquivo, cx conexoes.Conexao, p conexoes.Protocolo) {
	chave := string(p) + "|" + cx.GrupoStr() + "|" + cx.Nome
	titulo := tituloAba(cx, p)
	for i, t := range bar.tabs {
		if bar.chave(t) == chave {
			bar.selectIndex(i)
			return
		}
	}

	// "rotulo" é o que aparece na aba: só o nome da máquina, porque a
	// conexão veio do .ini e o protocolo já está no selo.
	spec := map[string]string{"host": cx.Host, "name": cx.Nome, "rotulo": cx.Nome}
	var err error
	switch p {
	case conexoes.VNC:
		spec["type"] = "vnc"
		spec["port"] = strconv.Itoa(cx.VNC.Porta)
		spec["user"] = chaveiroAtual.Resolver(cx.VNC.Usuario, "usuario")
		spec["pass"], err = segredo(cx.VNC.Senha)
		spec["ronly"] = simNao(cx.VNC.Ronly)
		spec["modo"] = cx.VNC.Modo
		spec["auto"] = simNao(cx.VNC.Auto)
	case conexoes.RDP:
		spec["type"] = "rdp"
		spec["port"] = strconv.Itoa(cx.RDP.Porta)
		spec["user"] = chaveiroAtual.Resolver(cx.RDP.Usuario, "usuario")
		spec["domain"] = cx.RDP.Dominio
		spec["pass"], err = segredo(cx.RDP.Senha)
		spec["auto"] = simNao(cx.RDP.Auto)
	case conexoes.SSH, conexoes.SFTP:
		spec["type"] = string(p)
		spec["port"] = strconv.Itoa(cx.SSH.Porta)
		spec["user"] = chaveiroAtual.Resolver(cx.SSH.Usuario, "usuario")
		spec["pass"], err = segredo(cx.SSH.Senha)
		spec["auto"] = simNao(cx.SSH.Auto)
	}
	if err != nil {
		// Senha no cofre e cofre trancado: em vez de falhar em silêncio no
		// stderr (o clique "não fazia nada"), pede a senha mestra e repete
		// ESTA mesma abertura assim que o cofre abrir.
		if cofreAberto == nil && len(arq.Cofre) > 0 {
			pedirCofre(w, arq, caminhoINI, recarregarINI, func() { abrirConexao(w, bar, arq, cx, p) })
			w.Invalidate()
			return
		}
		fmt.Fprintf(os.Stderr, "[%s] %v\n", titulo, err)
		return
	}

	t, err := newTab(w, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] %v\n", titulo, err)
		return
	}
	// A aba SFTP sabe trocar de máquina sem fechar: o menu vem daqui,
	// onde a lista de conexões está à mão.
	if sf, ok := t.(*sftpTab); ok {
		sf.trocarHost = func(pos image.Point) {
			var itens []*itemMenu
			for _, outra := range arq.Conexoes {
				outra := outra
				if !outra.Tem(conexoes.SSH) {
					continue
				}
				itens = append(itens, &itemMenu{
					rotulo: outra.Nome + "  (" + outra.Host + ")",
					acao: func() {
						senha, err := segredo(outra.SSH.Senha)
						if err != nil {
							fmt.Fprintln(os.Stderr, err)
							return
						}
						sf.ApontarPara(outra.Nome, outra.Host, outra.SSH.Porta,
							chaveiroAtual.Resolver(outra.SSH.Usuario, "usuario"), senha)
					},
				})
			}
			abrirMenu(pos, itens)
			w.Invalidate()
		}
	}

	if abrirEmSegundoPlano {
		bar.appendFundo(t, chave)
	} else {
		bar.appendCom(t, chave)
	}
	w.Invalidate()
}

// nomeOuHost: o card do painel manda o nome da máquina ("CAIXA5201");
// pela linha de comando só existe o host, e aí ele mesmo vira o rótulo.
func nomeOuHost(spec map[string]string, host string) string {
	if n := spec["name"]; n != "" {
		return n
	}
	return host
}

// rotuloAba é o texto da aba: o NOME da máquina (ou o host, quando a
// conexão veio da linha de comando e não tem nome). O protocolo nunca
// entra aqui — quem diz isso é o selo colorido ao lado, e escrever
// "SSH"/"VNC" de novo só rouba largura da tira de abas.
func rotuloAba(spec map[string]string, _, host string) string {
	if r := spec["rotulo"]; r != "" {
		return r
	}
	return nomeOuHost(spec, host)
}

func tituloAba(cx conexoes.Conexao, p conexoes.Protocolo) string {
	switch p {
	case conexoes.VNC:
		return "VNC " + cx.Nome
	case conexoes.RDP:
		return "RDP " + cx.Nome
	case conexoes.SSH:
		return "SSH " + cx.Nome
	case conexoes.SFTP:
		return "SFTP " + cx.Nome
	}
	return cx.Nome
}
