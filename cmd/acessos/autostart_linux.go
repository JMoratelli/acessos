//go:build linux

package main

// Autostart do serviço, pelo portal org.freedesktop.portal.Background.
//
// O serviço sobe sozinho quando o app parte e continua vivo depois que a
// janela fecha — mas só até o fim da sessão. Para o Ctrl+Shift+F12
// existir depois de um logout ou de um reinício SEM ninguém abrir o app
// antes, é preciso que o sistema suba o serviço no login, e no Flatpak a
// porta de entrada para isso é o portal (o sandbox não escreve em
// ~/.config/autostart).
//
// A chave `[geral] atalho_autostart` no conexoes.ini manda aqui:
//
//	(vazio)  nunca foi pedido — pede uma vez e grava a resposta
//	1        o sistema já sobe o serviço no login
//	0        recusado (pelo usuário ou pelo desktop); não pergunta de novo
//
// A pergunta é feita UMA vez de propósito: o portal abre um diálogo
// ("Acessos quer rodar em segundo plano"), e um app que repete essa
// pergunta a cada login é um app que a pessoa aprende a recusar.

import (
	"fmt"
	"os"
	"time"

	"acessos-go/internal/conexoes"

	"github.com/godbus/dbus/v5"
)

const portalFundo = "org.freedesktop.portal.Background"

// garantirAutostart pede ao sistema para subir o serviço no login, se
// isso ainda não foi decidido. Erro nunca é fatal: sem autostart o atalho
// continua funcionando na sessão corrente, que é o comportamento de
// antes.
func garantirAutostart(caminhoINI string) {
	arq, err := conexoes.Carregar(caminhoINI)
	if err != nil {
		return
	}
	switch arq.Geral["atalho_autostart"] {
	case "1", "0":
		return // já decidido; ver o cabeçalho
	}

	ok, err := pedirAutostart()
	if err != nil {
		// Desktop sem o portal Background: nada a gravar. Tentar de novo
		// no próximo login é barato e pode ser um desktop diferente.
		fmt.Fprintf(os.Stderr, "autostart: %v\n", err)
		return
	}
	valor := "0"
	if ok {
		valor = "1"
		fmt.Println("autostart: o sistema passa a subir o atalho do Acessos no login")
	} else {
		fmt.Fprintln(os.Stderr, "autostart: recusado — o atalho global vai existir só "+
			"depois de abrir o Acessos uma vez por sessão. Para mudar de ideia, apague "+
			"a chave [geral] atalho_autostart do conexoes.ini")
	}
	if err := conexoes.SalvarGeral(caminhoINI, map[string]string{"atalho_autostart": valor}); err != nil {
		fmt.Fprintf(os.Stderr, "autostart: %v\n", err)
	}
}

func pedirAutostart() (bool, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false, err
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return false, err
	}
	sinais := make(chan *dbus.Signal, 8)
	conn.Signal(sinais)
	defer conn.RemoveSignal(sinais)

	portal := conn.Object(portalDestino, portalCaminho)
	if _, err := portal.GetProperty(portalFundo + ".version"); err != nil {
		return false, fmt.Errorf("este desktop não expõe Background: %w", err)
	}

	tk := fmt.Sprintf("acessosbg%d", time.Now().UnixNano()%100000)
	var req dbus.ObjectPath
	// commandline é o que o sistema vai executar no login. Dentro do
	// Flatpak o portal o traduz para o `flatpak run` equivalente; fora
	// dele vale como está.
	if err := portal.Call(portalFundo+".RequestBackground", 0, "",
		map[string]dbus.Variant{
			"handle_token": dbus.MakeVariant(tk),
			"reason": dbus.MakeVariant(
				"Manter o atalho de busca de máquinas funcionando com o app fechado"),
			"autostart":        dbus.MakeVariant(true),
			"commandline":      dbus.MakeVariant([]string{"acessos", ArgServico}),
			"dbus-activatable": dbus.MakeVariant(false),
		}).Store(&req); err != nil {
		return false, fmt.Errorf("RequestBackground: %w", err)
	}

	// Prazo longo: como no atalho, aqui pode haver um diálogo esperando a
	// pessoa ler e decidir.
	res, err := respostaDoPortal(sinais, req, 5*time.Minute)
	if err != nil {
		return false, err
	}
	auto, _ := res["autostart"].Value().(bool)
	return auto, nil
}
