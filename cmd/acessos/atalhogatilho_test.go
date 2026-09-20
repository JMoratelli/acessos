package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// limparGatilho devolve o estado global ao zero. É global porque o estado
// vem de outro PROCESSO e não tem dono no laço de quadro (ver
// atalhogatilho.go); em teste, isso obriga a limpar entre um caso e outro.
func limparGatilho(t *testing.T) {
	t.Helper()
	anterior := gatilhoAtual.Load()
	gatilhoAtual.Store(nil)
	t.Cleanup(func() { gatilhoAtual.Store(anterior) })
}

// A invariante que evita alarme falso: enquanto o serviço não respondeu,
// NÃO se acusa "sem tecla". Sem isso, todo Ajustes aberto no primeiro
// segundo mostraria o aviso e ele sumiria sozinho depois — o pior tipo de
// aviso, o que aparece quando não devia.
func TestSemNoticiaNaoAcusaSemTecla(t *testing.T) {
	limparGatilho(t)
	if atalhoSemTecla() {
		t.Fatal("sem notícia do serviço não pode acusar sem tecla")
	}
	if e := lerEstadoGatilho(); e.Sabido {
		t.Fatal("estado zerado não pode vir como sabido")
	}
}

func TestGatilhoVazioAcusaSemTecla(t *testing.T) {
	limparGatilho(t)
	definirGatilho("")
	if !atalhoSemTecla() {
		t.Fatal("tecla vazia com notícia recebida é exatamente o caso do aviso")
	}
}

func TestGatilhoComTeclaNaoAcusa(t *testing.T) {
	limparGatilho(t)
	definirGatilho("Ctrl+Shift+F12")
	if atalhoSemTecla() {
		t.Fatal("com tecla amarrada não há o que avisar")
	}
	if e := lerEstadoGatilho(); e.Tecla != "Ctrl+Shift+F12" {
		t.Fatalf("tecla veio %q", e.Tecla)
	}
}

// A notícia chega tarde: o serviço registra o atalho antes de a janela se
// apresentar. Um aviso que só valesse no instante do registro nunca
// apareceria.
func TestGatilhoPodeChegarDepois(t *testing.T) {
	limparGatilho(t)
	if atalhoSemTecla() {
		t.Fatal("não devia acusar antes da notícia")
	}
	definirGatilho("")
	if !atalhoSemTecla() {
		t.Fatal("devia acusar depois da notícia")
	}
}

// O campo Gatilho NÃO pode ganhar omitempty: vazio é justamente a notícia
// que a mensagem existe para dar. Com omitempty, "registrado sem tecla"
// viajaria como uma mensagem sem o campo, indistinguível de uma versão
// velha que não o conhece — e o aviso nunca apareceria.
func TestMensagemGatilhoVaziaSobreviveAoJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := escrever(&buf, mensagem{Tipo: msgGatilho, Gatilho: ""}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"gatilho"`) {
		t.Fatalf("o campo sumiu do JSON (omitempty?): %s", buf.String())
	}
	m, err := lerMensagem(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if m.Tipo != msgGatilho {
		t.Fatalf("tipo veio %q", m.Tipo)
	}
	if m.Gatilho != "" {
		t.Fatalf("gatilho veio %q, esperado vazio", m.Gatilho)
	}
}

func TestMensagemGatilhoComTeclaSobreviveAoJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := escrever(&buf, mensagem{Tipo: msgGatilho, Gatilho: "Ctrl+Shift+F12"}); err != nil {
		t.Fatal(err)
	}
	m, err := lerMensagem(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if m.Gatilho != "Ctrl+Shift+F12" {
		t.Fatalf("gatilho veio %q", m.Gatilho)
	}
}
