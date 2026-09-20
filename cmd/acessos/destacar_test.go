package main

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/widget"
)

// abaContaFecha registra se Close foi chamado — é a diferença entre
// destacar e fechar.
type abaContaFecha struct {
	nome    string
	fixa    bool
	fechada int
}

func (a *abaContaFecha) Title() string { return a.nome }
func (a *abaContaFecha) SoIcone() bool { return false }
func (a *abaContaFecha) Pinned() bool  { return a.fixa }
func (a *abaContaFecha) Close()        { a.fechada++ }
func (a *abaContaFecha) Selo() (*widget.Icon, color.NRGBA, color.NRGBA) {
	return nil, color.NRGBA{}, color.NRGBA{}
}
func (a *abaContaFecha) Layout(gtx layout.Context) layout.Dimensions {
	return layout.Dimensions{}
}
func (a *abaContaFecha) HandleKey(keysym, keycodeX11 uint32, pressed bool) {}
func (a *abaContaFecha) HandlePointer(ev pointer.Event, size image.Point)  {}

// Destacar NÃO é fechar. Fechar chama Close(), que derruba a sessão e mata
// o processo-filho; destacar só muda quem desenha a aba. Trocar um pelo
// outro faria o botão de tela cheia encerrar a sessão do operador.
func TestRetirarNaoFechaAAba(t *testing.T) {
	b := &tabBar{}
	painel := &abaContaFecha{nome: "painel", fixa: true}
	sessao := &abaContaFecha{nome: "PDV-01"}
	b.prependPinned(painel)
	b.appendCom(sessao, "rdp:10.0.0.9")

	i := b.indiceDe(sessao)
	if i < 0 {
		t.Fatal("a aba deveria estar na tira")
	}
	aba, chave, ok := b.retirar(i)
	if !ok {
		t.Fatal("retirar devolveu falso para uma aba comum")
	}
	if aba != Tab(sessao) {
		t.Fatal("retirar devolveu outra aba")
	}
	if sessao.fechada != 0 {
		t.Fatalf("retirar chamou Close %dx — isso derrubaria a sessão", sessao.fechada)
	}
	if chave != "rdp:10.0.0.9" {
		t.Fatalf("a chave veio %q", chave)
	}
	if b.indiceDe(sessao) >= 0 {
		t.Fatal("a aba continuou na tira depois de retirada")
	}

	// e fechar, esse sim, fecha
	b.appendCom(sessao, chave)
	b.fechar(b.indiceDe(sessao))
	if sessao.fechada != 1 {
		t.Fatalf("fechar devia chamar Close uma vez, chamou %d", sessao.fechada)
	}
}

// A aba fixa (Painel) não sai: ela não tem sessão para destacar, e tirá-la
// deixaria a tira no estado "zero abas" que ela existe para impedir.
func TestAbaFixaNaoSeDestaca(t *testing.T) {
	b := &tabBar{}
	painel := &abaContaFecha{nome: "painel", fixa: true}
	b.prependPinned(painel)

	if _, _, ok := b.retirar(0); ok {
		t.Fatal("a aba fixa não pode ser retirada")
	}
	if b.indiceDe(painel) != 0 {
		t.Fatal("a aba fixa tem de continuar onde estava")
	}
}

// A CHAVE tem de sobreviver à ida e volta. É ela que identifica "o que a
// aba é" (protocolo + máquina); voltar sem ela faria o Painel achar que
// não há sessão aberta para aquela máquina e abrir uma segunda.
func TestChaveSobreviveAoDestacarEVoltar(t *testing.T) {
	b := &tabBar{}
	b.prependPinned(&abaContaFecha{nome: "painel", fixa: true})
	sessao := &abaContaFecha{nome: "PDV-01"}
	b.appendCom(sessao, "rdp:10.0.0.9")

	_, chave, _ := b.retirar(b.indiceDe(sessao))
	b.appendCom(sessao, chave)

	if got := b.chave(sessao); got != "rdp:10.0.0.9" {
		t.Fatalf("depois da volta a chave é %q — o Painel abriria uma segunda sessão", got)
	}
}

// retirar com índice inválido não pode estourar nem mexer na tira.
func TestRetirarIndiceInvalido(t *testing.T) {
	b := &tabBar{}
	b.prependPinned(&abaContaFecha{nome: "painel", fixa: true})
	antes := len(b.tabs)

	for _, i := range []int{-1, 99} {
		if _, _, ok := b.retirar(i); ok {
			t.Errorf("retirar(%d) devolveu verdadeiro", i)
		}
	}
	if len(b.tabs) != antes {
		t.Fatal("a tira mudou de tamanho com índice inválido")
	}
}

// Destacar a aba selecionada não pode deixar o índice apontando para fora
// da fatia — o quadro seguinte lê active() e estouraria.
func TestIndiceContinuaValidoDepoisDeRetirar(t *testing.T) {
	b := &tabBar{}
	b.prependPinned(&abaContaFecha{nome: "painel", fixa: true})
	a1 := &abaContaFecha{nome: "PDV-01"}
	a2 := &abaContaFecha{nome: "PDV-02"}
	b.appendCom(a1, "rdp:1")
	b.appendCom(a2, "rdp:2")

	// a2 é a selecionada (append seleciona); destaca ela
	if b.active() != Tab(a2) {
		t.Fatal("a última adicionada deveria estar selecionada")
	}
	b.retirar(b.indiceDe(a2))

	if b.idx < 0 || b.idx >= len(b.tabs) {
		t.Fatalf("idx=%d fora da fatia de %d abas", b.idx, len(b.tabs))
	}
	if b.active() == nil {
		t.Fatal("active() devolveu nil depois de destacar a selecionada")
	}
}
