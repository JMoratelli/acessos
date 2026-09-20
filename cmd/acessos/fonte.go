package main

import "gioui.org/unit"

// Escala da interface, para quem enxerga mal.
//
// São três níveis fixos, e o botão cicla entre eles (0 → 1 → 2 → 0): é um
// clique só, sem submenu e sem número para ajustar, e o operador sempre
// consegue voltar ao padrão continuando a clicar. O nível vive no
// [geral] fonte do .ini, junto com o tema — a preferência é da pessoa, não
// da sessão, e refazer o ajuste a cada abertura seria o pior dos mundos
// justamente para quem precisa dele.
//
// O fator multiplica PxPerDp JUNTO com PxPerSp, isto é: aumenta a
// interface inteira, não só a letra. Mexer só no Sp faria o texto crescer
// dentro de caixas do mesmo tamanho — os rótulos das pílulas e dos cards
// seriam cortados, e o resultado ficaria menos legível, não mais.
const nivelFonteMax = 2

var nivelFonte int

// 15% por degrau: o suficiente para a diferença ser evidente sem que a
// dashboard deixe de caber em tela de 1366x768, que é o que tem nas lojas.
var fatoresFonte = [nivelFonteMax + 1]float32{1.0, 1.15, 1.32}

// escalaFonte aplica o nível corrente às métricas do quadro.
func escalaFonte(m unit.Metric) unit.Metric {
	f := fatorFonte()
	m.PxPerDp *= f
	m.PxPerSp *= f
	return m
}

// fatorFonte é o multiplicador do nível corrente para quem precisa do
// NÚMERO, e não da métrica de um quadro.
func fatorFonte() float32 {
	return fatoresFonte[nivelFonteValido()]
}

// escalaDp aplica o nível a uma medida que vai para o SISTEMA — o tamanho
// de uma janela, em app.Size —, e não para o layout.
//
// São duas réguas diferentes e é fácil confundir: o Dp de app.Size é
// convertido pela métrica DO SISTEMA, que nunca passou por escalaFonte. Se
// o conteúdo cresce 32% e a janela não, o que cresceu fica do lado de fora
// — foi exatamente assim que o rodapé da caixa de busca se perdeu, com a
// diferença de que lá a causa era constante errada e aqui seria a escala.
func escalaDp(d unit.Dp) unit.Dp {
	return unit.Dp(float32(d) * fatorFonte())
}

func nivelFonteValido() int {
	if nivelFonte < 0 || nivelFonte > nivelFonteMax {
		return 0
	}
	return nivelFonte
}

// proximoNivelFonte cicla e devolve o novo nível.
func proximoNivelFonte() int {
	nivelFonte = (nivelFonteValido() + 1) % (nivelFonteMax + 1)
	return nivelFonte
}

// rotuloFonte é o texto da pílula: "A+" no padrão, ganhando um "+" por
// degrau. Fica aqui, junto dos níveis, para o rótulo nunca desencontrar do
// que o botão realmente faz.
func rotuloFonte() string {
	switch nivelFonteValido() {
	case 1:
		return "A++"
	case 2:
		return "A+++"
	}
	return "A+"
}
