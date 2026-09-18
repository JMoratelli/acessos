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
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalDestino = "org.freedesktop.portal.Desktop"
	portalCaminho = "/org/freedesktop/portal/desktop"
	portalAtalhos = "org.freedesktop.portal.GlobalShortcuts"
)

// AtalhoGlobal é o resultado do registro. A sessão do portal vive
// enquanto o processo viver — não guardamos a conexão nem o caminho dela
// porque o app nunca desregistra o atalho em vida; quem o solta é o fim
// do processo, e quem o muda de tecla é o usuário, pelas Preferências do
// Sistema (e aí o ShortcutsChanged atualiza o Gatilho abaixo).
type AtalhoGlobal struct {
	Gatilho string // o que o SISTEMA amarrou; vazio = sem tecla

	// Caiu fecha quando a sessão do portal acaba (portal reiniciado,
	// sessão encerrada pelo desktop). Sem isto o atalho morria calado e
	// só voltava reiniciando o app: quem escuta registra de novo.
	Caiu chan struct{}
}

// registrarAtalhoGlobal pede o atalho e começa a escutar. Devolve erro
// quando o desktop não tem o portal — e aí o app segue sem atalho, que é
// degradação aceitável: a busca continua existindo dentro da janela.
func registrarAtalhoGlobal(id, descricao, gatilho string, ao func(token string)) (*AtalhoGlobal, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("barramento de sessão: %w", err)
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return nil, err
	}
	if err := conn.AddMatchSignal(dbus.WithMatchInterface(portalAtalhos)); err != nil {
		return nil, err
	}
	// A sessão do portal avisa a própria morte por este sinal. É o que
	// permite registrar de novo em vez de ficar com um atalho fantasma.
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.portal.Session"),
		dbus.WithMatchMember("Closed"),
	); err != nil {
		return nil, err
	}
	sinais := make(chan *dbus.Signal, 32)
	conn.Signal(sinais)

	portal := conn.Object(portalDestino, portalCaminho)
	if _, err := portal.GetProperty(portalAtalhos + ".version"); err != nil {
		return nil, fmt.Errorf("este desktop não expõe GlobalShortcuts: %w", err)
	}

	tk := fmt.Sprintf("acessos%d", time.Now().UnixNano()%100000)
	var req dbus.ObjectPath
	if err := portal.Call(portalAtalhos+".CreateSession", 0, map[string]dbus.Variant{
		"handle_token":         dbus.MakeVariant(tk),
		"session_handle_token": dbus.MakeVariant(tk + "s"),
	}).Store(&req); err != nil {
		return nil, fmt.Errorf("CreateSession: %w", err)
	}
	res, err := respostaDoPortal(sinais, req, 30*time.Second)
	if err != nil {
		return nil, err
	}
	sessao, _ := res["session_handle"].Value().(string)
	if sessao == "" {
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
		return nil, fmt.Errorf("BindShortcuts: %w", err)
	}
	// Sem prazo curto aqui de propósito: na PRIMEIRA vez o KDE abre um
	// diálogo e fica esperando a pessoa ler e confirmar.
	res, err = respostaDoPortal(sinais, req, 5*time.Minute)
	if err != nil {
		return nil, err
	}

	a := &AtalhoGlobal{Caiu: make(chan struct{})}
	a.Gatilho = gatilhoAmarrado(res, id)

	go func() {
		defer close(a.Caiu)
		for s := range sinais {
			switch s.Name {
			case portalAtalhos + ".Activated":
				if len(s.Body) > 1 {
					if quem, _ := s.Body[1].(string); quem == id && ao != nil {
						ao(tokenDeAtivacao(s.Body))
					}
				}
			case portalAtalhos + ".ShortcutsChanged":
				// o usuário mexeu na tecla em Preferências do Sistema
				if len(s.Body) > 1 {
					a.Gatilho = gatilhoDaLista(s.Body[1], id)
				}
			case "org.freedesktop.portal.Session.Closed":
				if string(s.Path) == sessao {
					return
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
