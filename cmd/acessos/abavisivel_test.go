package main

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/app"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/widget"
)

// abaDeTeste é o mínimo que satisfaz Tab — nada aqui desenha.
type abaDeTeste struct{ nome string }

func (a *abaDeTeste) Title() string { return a.nome }
func (a *abaDeTeste) SoIcone() bool { return false }
func (a *abaDeTeste) Pinned() bool  { return false }
func (a *abaDeTeste) Close()        {}
func (a *abaDeTeste) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	return nil, color.NRGBA{}, color.NRGBA{}
}
func (a *abaDeTeste) Layout(gtx layout.Context) layout.Dimensions {
	return layout.Dimensions{}
}
func (a *abaDeTeste) HandleKey(keysym, keycodeX11 uint32, pressed bool) {}
func (a *abaDeTeste) HandlePointer(ev pointer.Event, size image.Point)  {}

// limparAbasAtivas zera o mapa entre casos. As janelas são só chaves aqui:
// nenhum Gio é aberto, e nada no caminho sob teste desreferencia o
// ponteiro.
func limparAbasAtivas(t *testing.T) {
	t.Helper()
	abasAtivas.mu.Lock()
	anterior := abasAtivas.por
	abasAtivas.por = nil
	abasAtivas.mu.Unlock()
	t.Cleanup(func() {
		abasAtivas.mu.Lock()
		abasAtivas.por = anterior
		abasAtivas.mu.Unlock()
	})
}

// A salvaguarda do arranque é o que impede o travamento: antes do primeiro
// quadro ninguém foi marcado, e se abaVisivel dissesse "não" nenhuma
// sessão pediria quadro — sem quadro não há marca, sem marca não há
// quadro, e a primeira imagem nunca apareceria.
func TestAbaVisivelPassaAntesDoPrimeiroQuadro(t *testing.T) {
	limparAbasAtivas(t)
	if !abaVisivel(&abaDeTeste{"rdp"}) {
		t.Fatal("sem nenhum quadro ainda, o pedido tem de passar")
	}
}

// Depois que há quadro, só a aba à vista pede.
func TestAbaVisivelSoParaAAtiva(t *testing.T) {
	limparAbasAtivas(t)
	w := new(app.Window)
	frente, fundo := &abaDeTeste{"frente"}, &abaDeTeste{"fundo"}

	marcarAbaAtiva(w, frente)
	if !abaVisivel(frente) {
		t.Error("a aba à vista tem de pedir quadro")
	}
	if abaVisivel(fundo) {
		t.Error("a aba escondida não pode pedir quadro da janela inteira")
	}

	marcarAbaAtiva(w, fundo)
	if abaVisivel(frente) || !abaVisivel(fundo) {
		t.Error("depois da troca, quem pede é a nova aba à vista")
	}
}

// O caso que a marca ÚNICA não sabia responder: com a sessão remota em
// janela própria, duas abas estão à vista ao mesmo tempo — uma por janela.
// Com um ponteiro só, marcar a segunda apagava a primeira, e a aba
// destacada deixava de pedir quadro e de receber clipboard mesmo estando
// na cara do operador.
func TestCadaJanelaTemSuaAbaAVista(t *testing.T) {
	limparAbasAtivas(t)
	principal, destacada := new(app.Window), new(app.Window)
	painel := &abaDeTeste{"painel"}
	sessao := &abaDeTeste{"rdp-destacado"}
	escondida := &abaDeTeste{"outra-aba"}

	marcarAbaAtiva(principal, painel)
	marcarAbaAtiva(destacada, sessao)

	if !abaVisivel(painel) {
		t.Error("a aba da janela principal continua à vista")
	}
	if !abaVisivel(sessao) {
		t.Error("a aba da janela destacada também está à vista")
	}
	if abaVisivel(escondida) {
		t.Error("aba de nenhuma janela não está à vista")
	}
}

// Janela fechada tem de sair do mapa: senão a aba que morreu com ela
// seguiria respondendo "estou à vista" para sempre, e pediria quadro de
// uma janela que não existe mais.
func TestJanelaFechadaDeixaDeContar(t *testing.T) {
	limparAbasAtivas(t)
	principal, destacada := new(app.Window), new(app.Window)
	painel, sessao := &abaDeTeste{"painel"}, &abaDeTeste{"rdp"}

	marcarAbaAtiva(principal, painel)
	marcarAbaAtiva(destacada, sessao)

	esquecerJanela(destacada)
	if abaVisivel(sessao) {
		t.Error("a aba da janela fechada não pode continuar à vista")
	}
	if !abaVisivel(painel) {
		t.Error("fechar uma janela não pode afetar a outra")
	}
	if nenhumaAbaMarcada() {
		t.Error("ainda há uma janela marcada")
	}

	esquecerJanela(principal)
	if !nenhumaAbaMarcada() {
		t.Error("sem nenhuma janela, volta a salvaguarda do arranque")
	}
}

// abaAtivaDe responde POR JANELA — é o que o callback de clipboard do grab
// usa, e entregar o texto para a aba da outra janela seria colar no lugar
// errado.
func TestAbaAtivaDeRespondePorJanela(t *testing.T) {
	limparAbasAtivas(t)
	principal, destacada := new(app.Window), new(app.Window)
	painel, sessao := &abaDeTeste{"painel"}, &abaDeTeste{"rdp"}

	marcarAbaAtiva(principal, painel)
	marcarAbaAtiva(destacada, sessao)

	if got := abaAtivaDe(principal); got != Tab(painel) {
		t.Errorf("janela principal devolveu %v", got)
	}
	if got := abaAtivaDe(destacada); got != Tab(sessao) {
		t.Errorf("janela destacada devolveu %v", got)
	}
	if got := abaAtivaDe(new(app.Window)); got != nil {
		t.Errorf("janela sem quadro ainda devolveu %v, esperado nil", got)
	}
}
