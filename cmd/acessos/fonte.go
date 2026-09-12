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
	f := fatoresFonte[nivelFonteValido()]
	m.PxPerDp *= f
	m.PxPerSp *= f
	return m
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
