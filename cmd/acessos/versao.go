package main

import (
	_ "embed"
	"regexp"
	"strings"
)

// A versão vem do metainfo.xml EMBUTIDO, não de uma constante no código:
// é o mesmo arquivo que o Flatpak instala e que a loja lê, então não há
// como o número exibido divergir do número empacotado — que foi o motivo
// de o app original também tirar a versão de lá.
//
// O go:embed não sobe diretórios, então o metainfo é COPIADO para cá pelo
// build.sh antes de compilar — o arquivo em flatpak/ continua sendo o
// original, e é dele que sai a cópia.
//
//go:embed dados/metainfo.xml
var metainfoXML string

var reRelease = regexp.MustCompile(`<release[^>]*version="([^"]+)"`)

// versaoInstalada devolve a versão da release mais recente listada no
// metainfo. Vazio se o arquivo não listar nenhuma — a interface apenas
// deixa de mostrar o número nesse caso; não vale travar o app por isso.
func versaoInstalada() string {
	m := reRelease.FindStringSubmatch(metainfoXML)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}
