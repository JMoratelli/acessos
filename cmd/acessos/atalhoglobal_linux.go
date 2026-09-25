//go:build linux

package main

// Atalho global no Linux: pelo portal org.freedesktop.portal.GlobalShortcuts.
//
// No Wayland nenhum cliente pode capturar tecla fora da própria janela —
// é o compositor que escuta, e o portal é a porta de entrada suportada.
// O que foi medido em KDE Plasma 6.7 / xdg-desktop-portal 1.22 (ver o
// espinho em cmd/atalhotest):
//
//   - o preferred_trigger é honrado: pedimos Ctrl+Shift+F12 e é isso que
//     o sistema amarra;
//   - o diálogo de confirmação aparece UMA VEZ por aplicativo. Quem
//     fechar sem querer fica com o atalho existindo e sem tecla, e nada
//     na tela explica por quê — daí a checagem de trigger_description
//     vazio, que o app usa para avisar em vez de parecer quebrado;
//   - a identidade vem do app id, então isso só funciona direito no
//     Flatpak. Binário solto herda a identidade de quem o lançou;
//   - o Activated NÃO trazia activation_token no KDE quando isto foi
//     medido. Ele passou a ser LIDO assim mesmo (é opcional no protocolo
//     e outros desktops mandam): quem abre a caixa de busca agora é o
//     serviço, um processo de segundo plano, e é exatamente aí que a
//     prevenção de roubo de foco do compositor morde. Com token, a janela
//     nasce à frente; sem ele, seguimos dependendo de o compositor dar o
//     foco sozinho, como antes.

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalDestino = "org.freedesktop.portal.Desktop"
	portalCaminho = "/org/freedesktop/portal/desktop"
	portalAtalhos = "org.freedesktop.portal.GlobalShortcuts"
)

// AtalhoGlobal é o resultado do registro.
//
// A conexão e o caminho da sessão ficam guardados por causa do Fechar:
// antes a sessão do portal vivia enquanto o processo vivesse, porque nada
// desregistrava o atalho em vida. Com a chave `[geral] atalho_global`
// (ver atalhopref.go) isso deixou de ser verdade — desligar o atalho pelos
// Ajustes precisa SOLTAR a tecla, não só ignorar o disparo.
type AtalhoGlobal struct {
	// Gatilho é o que o sistema amarrou NO REGISTRO; vazio = sem tecla.
	//
	// Só de leitura depois que registrarAtalhoGlobal devolve, e por isso
	// não precisa de trava: a troca de tecla feita em Preferências do
	// Sistema chega pelo canal Mudou, abaixo, NÃO reescrevendo este
	// campo. Antes era reescrito da goroutine de sinais e lido daqui, o
	// que era corrida — e pior, inútil, porque ninguém o relia.
	Gatilho string

	// Mudou entrega a tecla nova quando o usuário a troca em Preferências
	// do Sistema (ShortcutsChanged do portal).
	//
	// Existe porque a promessa não estava sendo cumprida: o sinal era
	// tratado e guardado num campo que ninguém lia de novo, então
	// reamarrar a tecla atualizava uma variável e a janela seguia
	// anunciando a tecla ANTIGA até a sessão do portal cair ou o serviço
	// reiniciar. Quem escuta é o manterAtalho (servico_linux.go) e, sem
	// serviço, o próprio app (main.go).
	Mudou chan string

	// Caiu fecha quando a sessão do portal acaba (portal reiniciado,
	// sessão encerrada pelo desktop, ou Fechar daqui). Sem isto o atalho
	// morria calado e só voltava reiniciando o app: quem escuta registra
	// de novo.
	Caiu chan struct{}

	conn     *dbus.Conn
	sessao   dbus.ObjectPath
	parar    chan struct{}
	fecharUm sync.Once
}

// Fechar solta o atalho: encerra a sessão do portal, o que libera a tecla
// para o resto do sistema, e manda a goroutine de sinais embora — é ela
// que desregistra o canal e as match rules ao sair.
//
// Idempotente — pode ser chamado junto com a sessão caindo por conta
// própria, que é justamente quando as duas coisas correm ao mesmo tempo.
func (a *AtalhoGlobal) Fechar() {
	if a == nil {
		return
	}
	a.fecharUm.Do(func() {
		if a.conn != nil && a.sessao != "" {
			// Erro aqui não tem a quem interessar: se a sessão já morreu,
			// o objetivo (tecla livre) está cumprido do mesmo jeito.
			_ = a.conn.Object(portalDestino, a.sessao).
				Call("org.freedesktop.portal.Session.Close", 0).Err
		}
		// Independente do Closed chegar: a especificação NÃO garante esse
		// sinal para quem fechou a própria sessão, e sem esta parada a
		// goroutine ficaria de pé com o canal registrado para sempre.
		if a.parar != nil {
			close(a.parar)
		}
	})
}

// registrarAtalhoGlobal pede o atalho e começa a escutar. Devolve erro
// quando o desktop não tem o portal — e aí o app segue sem atalho, que é
// degradação aceitável: a busca continua existindo dentro da janela.
func registrarAtalhoGlobal(id, descricao, gatilho string, ao func(token string)) (*AtalhoGlobal, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("barramento de sessão: %w", err)
	}
	// As match rules ficam guardadas para poderem ser DESFEITAS depois.
	// Sem isso elas se acumulavam no barramento a cada re-registro — e há
	// três caminhos que re-registram (a sessão do portal cair, desligar e
	// religar nos Ajustes, e cada tentativa que falha, com backoff de até
	// dois minutos). No login, com o portal ainda subindo, são várias
	// seguidas antes do primeiro sucesso.
	regras := [][]dbus.MatchOption{
		{
			dbus.WithMatchInterface("org.freedesktop.portal.Request"),
			dbus.WithMatchMember("Response"),
		},
		{dbus.WithMatchInterface(portalAtalhos)},
		// A sessão do portal avisa a própria morte por este sinal. É o que
		// permite registrar de novo em vez de ficar com um atalho fantasma.
		{
			dbus.WithMatchInterface("org.freedesktop.portal.Session"),
			dbus.WithMatchMember("Closed"),
		},
	}
	for i, r := range regras {
		if err := conn.AddMatchSignal(r...); err != nil {
			// Desfaz as que já entraram: falhar no meio deixava as
			// anteriores instaladas no barramento, e o backoff chamaria
			// isto de novo em dois segundos.
			for _, feita := range regras[:i] {
				_ = conn.RemoveMatchSignal(feita...)
			}
			return nil, err
		}
	}
	sinais := make(chan *dbus.Signal, 32)
	conn.Signal(sinais)

	// soltar desfaz TUDO o que foi registrado no barramento compartilhado
	// (dbus.SessionBus é a conexão do processo inteiro, não uma nossa).
	// Todo caminho de saída daqui para baixo passa por ele: sem isso, uma
	// tentativa que falhasse deixava o canal registrado para sempre, sem
	// ninguém lendo — ele enchia até 32 e, a partir daí, cada sinal que
	// casasse abria uma goroutine bloqueada para sempre.
	soltar := func() {
		conn.RemoveSignal(sinais)
		for _, r := range regras {
			_ = conn.RemoveMatchSignal(r...)
		}
	}

	portal := conn.Object(portalDestino, portalCaminho)
	if _, err := portal.GetProperty(portalAtalhos + ".version"); err != nil {
		soltar()
		return nil, fmt.Errorf("este desktop não expõe GlobalShortcuts: %w", err)
	}

	tk := fmt.Sprintf("acessos%d", time.Now().UnixNano()%100000)
	var req dbus.ObjectPath
	if err := portal.Call(portalAtalhos+".CreateSession", 0, map[string]dbus.Variant{
		"handle_token":         dbus.MakeVariant(tk),
		"session_handle_token": dbus.MakeVariant(tk + "s"),
	}).Store(&req); err != nil {
		soltar()
		return nil, fmt.Errorf("CreateSession: %w", err)
	}
	res, err := respostaDoPortal(sinais, req, 30*time.Second)
	if err != nil {
		soltar()
		return nil, err
	}
	sessao, _ := res["session_handle"].Value().(string)
	if sessao == "" {
		soltar()
		return nil, fmt.Errorf("o portal não devolveu session_handle")
	}

	type atalho struct {
		ID    string
		Extra map[string]dbus.Variant
	}
	lista := []atalho{{ID: id, Extra: map[string]dbus.Variant{
		"description":       dbus.MakeVariant(descricao),
		"preferred_trigger": dbus.MakeVariant(gatilho),
	}}}
	if err := portal.Call(portalAtalhos+".BindShortcuts", 0,
		dbus.ObjectPath(sessao), lista, "",
		map[string]dbus.Variant{"handle_token": dbus.MakeVariant(tk + "b")},
	).Store(&req); err != nil {
		soltar()
		return nil, fmt.Errorf("BindShortcuts: %w", err)
	}
	// Sem prazo curto aqui de propósito: na PRIMEIRA vez o KDE abre um
	// diálogo e fica esperando a pessoa ler e confirmar.
	res, err = respostaDoPortal(sinais, req, 5*time.Minute)
	if err != nil {
		soltar()
		return nil, err
	}

	a := &AtalhoGlobal{
		Mudou:  make(chan string, 1),
		Caiu:   make(chan struct{}),
		conn:   conn,
		sessao: dbus.ObjectPath(sessao),
		parar:  make(chan struct{}),
	}
	a.Gatilho = gatilhoAmarrado(res, id)

	// O atendimento do atalho NÃO roda neste laço — ver atalhodisparo.go.
	// Enquanto ele rodava aqui, um aperto segurava o consumidor por até
	// cinco segundos mais a leitura do .ini, e nesse tempo ninguém drenava
	// o canal de sinais.
	disp := novoDisparador(ao)

	go func() {
		defer func() {
			disp.pararTudo()
			soltar()
			close(a.Caiu)
		}()
		for {
			select {
			case <-a.parar:
				return
			case s := <-sinais:
				if s == nil {
					return
				}
				switch s.Name {
				case portalAtalhos + ".Activated":
					if len(s.Body) > 1 {
						if quem, _ := s.Body[1].(string); quem == id {
							disp.disparar(tokenDeAtivacao(s.Body))
						}
					}
				case portalAtalhos + ".ShortcutsChanged":
					// O usuário mexeu na tecla em Preferências do Sistema.
					// Vai pelo canal porque quem mostra a tecla na janela
					// está em OUTRA goroutine (e, no Linux com serviço,
					// em outro processo): guardar num campo, como era
					// antes, não avisava ninguém.
					if len(s.Body) > 1 {
						avisarMudanca(a.Mudou, gatilhoDaLista(s.Body[1], id))
					}
				case "org.freedesktop.portal.Session.Closed":
					if string(s.Path) == sessao {
						return
					}
				}
			}
		}
	}()
	return a, nil
}

// tokenDeAtivacao tira o activation_token das opções do Activated. O
// campo é OPCIONAL no protocolo: vazio não é erro, é desktop que não
// manda (ver o cabeçalho deste arquivo).
func tokenDeAtivacao(corpo []any) string {
	if len(corpo) < 4 {
		return ""
	}
	opcoes, ok := corpo[3].(map[string]dbus.Variant)
	if !ok {
		return ""
	}
	tk, _ := opcoes["activation_token"].Value().(string)
	return tk
}

// gatilhoAmarrado tira do Response a tecla que o sistema amarrou de
// fato. Vazio significa "registrado, mas sem tecla" — o caso de quem
// fechou o diálogo do KDE, que precisa virar aviso na interface.
func gatilhoAmarrado(res map[string]dbus.Variant, id string) string {
	v, ok := res["shortcuts"]
	if !ok {
		return ""
	}
	return gatilhoDaLista(v.Value(), id)
}

func gatilhoDaLista(v any, id string) string {
	lista, ok := v.([][]any)
	if !ok {
		// a assinatura é a(sa{sv}); o godbus entrega como slice de
		// structs anônimos quando o valor vem de um sinal
		return gatilhoPorTexto(fmt.Sprint(v), id)
	}
	for _, item := range lista {
		if len(item) < 2 {
			continue
		}
		if nome, _ := item[0].(string); nome != id {
			continue
		}
		if extra, ok := item[1].(map[string]dbus.Variant); ok {
			if t, ok := extra["trigger_description"]; ok {
				s, _ := t.Value().(string)
				return s
			}
		}
	}
	return ""
}

// gatilhoPorTexto é a saída de emergência para quando a forma exata do
// valor não bate com o esperado: a resposta ainda diz a tecla, e é
// melhor ler dela do que devolver "sem tecla" e mentir para o usuário.
func gatilhoPorTexto(s, id string) string {
	i := strings.Index(s, "trigger_description:")
	if i < 0 {
		return ""
	}
	resto := s[i+len("trigger_description:"):]
	resto = strings.TrimPrefix(strings.TrimSpace(resto), "\"")
	if j := strings.IndexAny(resto, "\"]"); j > 0 {
		return resto[:j]
	}
	return ""
}

func respostaDoPortal(sinais chan *dbus.Signal, req dbus.ObjectPath, limite time.Duration) (map[string]dbus.Variant, error) {
	prazo := time.After(limite)
	for {
		select {
		case <-prazo:
			return nil, fmt.Errorf("o portal não respondeu em %s", limite)
		case s := <-sinais:
			if s.Path != req || s.Name != "org.freedesktop.portal.Request.Response" {
				continue
			}
			codigo, _ := s.Body[0].(uint32)
			res, _ := s.Body[1].(map[string]dbus.Variant)
			switch codigo {
			case 0:
				return res, nil
			case 1:
				return nil, fmt.Errorf("o diálogo do atalho foi cancelado")
			default:
				return nil, fmt.Errorf("o portal recusou (código %d)", codigo)
			}
		}
	}
}

// avisarMudanca entrega a tecla nova sem bloquear, trocando um aviso
// ainda não lido pelo mais recente. Bloquear aqui seria voltar ao defeito
// que este canal existe para fechar — este código roda no consumidor de
// sinais, que não pode parar —, e uma tecla intermediária não interessa a
// ninguém: o que a janela mostra é a tecla de AGORA.
func avisarMudanca(ch chan string, tecla string) {
	for {
		select {
		case ch <- tecla:
			return
		default:
			select {
			case <-ch: // descarta o aviso velho e tenta de novo
			default:
				return
			}
		}
	}
}
