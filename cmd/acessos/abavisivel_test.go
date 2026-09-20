package main

import (
	"image"
	"image/color"
	"testing"

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

// A salvaguarda do arranque é o que impede o travamento: antes do primeiro
// quadro ninguém foi marcado como ativo, e se abaVisivel dissesse "não"
// nenhuma sessão pediria quadro — sem quadro não há marca, sem marca não
// há quadro, e a primeira imagem nunca apareceria.
func TestAbaVisivelPassaAntesDoPrimeiroQuadro(t *testing.T) {
	anterior := abaAtivaRef.Load()
	abaAtivaRef.Store(nil)
	defer abaAtivaRef.Store(anterior)

	if !abaVisivel(&abaDeTeste{"rdp"}) {
		t.Fatal("sem nenhum quadro ainda, o pedido tem de passar")
	}
}

// Depois que há quadro, só a aba à vista pede.
func TestAbaVisivelSoParaAAtiva(t *testing.T) {
	anterior := abaAtivaRef.Load()
	defer abaAtivaRef.Store(anterior)

	frente, fundo := &abaDeTeste{"frente"}, &abaDeTeste{"fundo"}
	var ativa Tab = frente
	marcarAbaAtiva(ativa)

	if !abaVisivel(frente) {
		t.Error("a aba à vista tem de pedir quadro")
	}
	if abaVisivel(fundo) {
		t.Error("a aba escondida não pode pedir quadro da janela inteira")
	}

	// e a troca de aba muda quem pede
	ativa = fundo
	marcarAbaAtiva(ativa)
	if abaVisivel(frente) || !abaVisivel(fundo) {
		t.Error("depois da troca, quem pede é a nova aba à vista")
	}
}
