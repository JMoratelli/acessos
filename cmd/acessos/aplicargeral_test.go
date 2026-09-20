package main

import (
	"testing"

	"gioui.org/unit"
)

// O [geral] volta INTEIRO: tema e escala da interface.
//
// A fonte é o caso que já falhou. O serviço do atalho global
// (servico_linux.go) é um processo à parte e montava o tema lendo o .ini
// na mão, sem a fonte — então quem usava o app com A+ abria a caixa de
// busca e ela vinha em tamanho base, justamente para quem aumentou a letra
// por precisar dela. Com os dois caminhos chamando aplicarGeral, o defeito
// só volta se alguém tirar uma linha de lá, e aí isto aqui quebra.
func TestAplicarGeralAplicaTemaEFonte(t *testing.T) {
	anteriorTema, anteriorFonte := tema, nivelFonte
	defer func() { tema, nivelFonte = anteriorTema, anteriorFonte }()

	tema, nivelFonte = temaClaro, 0
	aplicarGeral(map[string]string{"tema": "escuro", "fonte": "2"})
	if !tema.Escuro {
		t.Error("tema = escuro não foi aplicado")
	}
	if nivelFonte != 2 {
		t.Errorf("fonte = 2 não foi aplicada: nível %d", nivelFonte)
	}
	if f := fatorFonte(); f != fatoresFonte[2] {
		t.Errorf("o fator não acompanhou o nível: %v", f)
	}

	// E VOLTA: o serviço relê a cada abertura da caixa, então trocar para
	// o claro no app tem de chegar lá. Só o sentido escuro funcionava.
	aplicarGeral(map[string]string{"tema": "claro"})
	if tema.Escuro {
		t.Error("tema = claro não desfez o escuro")
	}

	// Chave ausente não mexe no que já está de pé: o serviço monta o tema
	// ANTES de ler o arquivo, e um .ini sem a chave não pode desfazer isso.
	tema, nivelFonte = temaEscuro, 1
	aplicarGeral(map[string]string{})
	if !tema.Escuro || nivelFonte != 1 {
		t.Errorf("chave ausente mexeu no estado: escuro=%v nível=%d", tema.Escuro, nivelFonte)
	}

	// Lixo na chave não pode zerar a preferência de quem enxerga mal.
	aplicarGeral(map[string]string{"fonte": "abacaxi"})
	if nivelFonte != 1 {
		t.Errorf("valor inválido mexeu no nível: %d", nivelFonte)
	}
}

// escalaDp e escalaFonte TÊM de andar juntas: a janela que se pede ao
// sistema cresce na mesma proporção do conteúdo desenhado dentro dela. Se
// uma mudar sem a outra, o que cresceu fica do lado de fora da janela — que
// é o defeito do rodapé cortado da caixa de busca, por outro caminho.
func TestEscalaDpAcompanhaAEscalaDoQuadro(t *testing.T) {
	anterior := nivelFonte
	defer func() { nivelFonte = anterior }()
	for nivel := 0; nivel <= nivelFonteMax; nivel++ {
		nivelFonte = nivel
		m := escalaFonte(unit.Metric{PxPerDp: 1, PxPerSp: 1})
		if got, want := float32(escalaDp(100)), 100*m.PxPerDp; got != want {
			t.Errorf("nível %d: escalaDp(100) = %v, mas o quadro escalaria para %v",
				nivel, got, want)
		}
	}
}
