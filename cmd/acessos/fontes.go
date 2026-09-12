package main

import (
	_ "embed"
	"fmt"
	"os"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/text"
)

// As fontes do app vão DENTRO do binário. Duas razões:
//
//  1. O objetivo do porte é um executável autocontido pra Linux e Windows:
//     depender de fonte instalada faz o mesmo binário ter uma cara em cada
//     máquina, e no Windows a IBM Plex simplesmente não existe.
//  2. Nesta máquina o app Python cai na Cantarell (substituta da Plex), e o
//     shaper do Gio não consegue carregar a Cantarell — devolve texto de
//     largura zero. Embutindo a Plex de verdade, Go e GTK ficam idênticos,
//     que é o que o tema.py pede: IBM Plex Sans/Sans Condensed/Mono.
//
// ~930 KB no binário. Cada família traz Regular e SemiBold porque o tema usa
// peso 600 em nome de card, título e marca.
//
//go:embed fontes/IBMPlexSans-Regular.ttf
var plexSansRegular []byte

//go:embed fontes/IBMPlexSans-SemiBold.ttf
var plexSansSemiBold []byte

//go:embed fontes/IBMPlexSansCondensed-Regular.ttf
var plexCondRegular []byte

//go:embed fontes/IBMPlexSansCondensed-SemiBold.ttf
var plexCondSemiBold []byte

//go:embed fontes/IBMPlexMono-Regular.ttf
var plexMonoRegular []byte

//go:embed fontes/IBMPlexMono-SemiBold.ttf
var plexMonoSemiBold []byte

// colecaoFontes monta a coleção que o shaper usa. Os nomes aqui têm que
// bater com os Typeface de tema.go.
func colecaoFontes() []font.FontFace {
	var col []font.FontFace
	add := func(nome string, peso font.Weight, ttf []byte) {
		face, err := opentype.Parse(ttf)
		if err != nil {
			// Fonte embutida que não carrega é erro de build, não de
			// ambiente: avisa e segue com o que deu certo.
			fmt.Fprintf(os.Stderr, "fonte %s %v: %v\n", nome, peso, err)
			return
		}
		col = append(col, font.FontFace{
			Font: font.Font{Typeface: font.Typeface(nome), Weight: peso},
			Face: face,
		})
	}
	add("IBM Plex Sans", font.Normal, plexSansRegular)
	add("IBM Plex Sans", font.SemiBold, plexSansSemiBold)
	add("IBM Plex Sans Condensed", font.Normal, plexCondRegular)
	add("IBM Plex Sans Condensed", font.SemiBold, plexCondSemiBold)
	add("IBM Plex Mono", font.Normal, plexMonoRegular)
	add("IBM Plex Mono", font.SemiBold, plexMonoSemiBold)
	return col
}

// shaperDoApp: a coleção embutida primeiro, e as fontes do sistema seguem
// valendo como último recurso (glifos que a Plex não tem, tipo emoji).
func shaperDoApp() *text.Shaper {
	return text.NewShaper(text.WithCollection(colecaoFontes()))
}
