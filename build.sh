#!/usr/bin/env bash
# build.sh — constrói o Flatpak do Acessos (porte Go).
#
#   ./build.sh              constrói e gera o bundle .flatpak
#   ./build.sh --instalar   constrói, gera o bundle e instala/atualiza
#   ./build.sh --limpar     apaga build/ inteiro e sai
#
# Tudo o que é gerado vai para ./build — nada é escrito na raiz.
#
# O build é OFFLINE: as dependências Go vêm de vendor/, e as duas
# bibliotecas C (libvncserver e FreeRDP3) são baixadas pelo flatpak-builder
# a partir dos tarballs com sha256 fixo no manifesto.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

APPID=org.jj.Acessos
MANIFESTO=flatpak/$APPID.yml
VERSAO=$(sed -n 's/.*<release version="\([^"]*\)".*/\1/p' flatpak/$APPID.metainfo.xml | head -1)

case "${1:-}" in
    --limpar)
        rm -rf build
        echo "build/ apagado"
        exit 0
        ;;
esac

# vendor/ é o que permite o build sem rede. Regenerar aqui evita o erro
# clássico de empacotar com dependência nova e vendor velho.
# O binário embute o metainfo para saber a própria versão (ver versao.go).
# A cópia é feita aqui para não haver duas fontes de verdade: quem manda é
# o arquivo de flatpak/.
echo ">> sincronizando metainfo embutido"
cp "flatpak/$APPID.metainfo.xml" cmd/acessos/dados/metainfo.xml

echo ">> atualizando vendor/"
# GOWORK=off pelo mesmo motivo que o manifesto ignora go.work (veja a lista
# de arquivos removidos em flatpak/org.jj.Acessos.yml): o go.work é
# conveniência local do editor e não é versionado, mas basta ele existir no
# diretório para o Go entrar em modo workspace e recusar o `go mod vendor`
# com "cannot be run in workspace mode" — o build quebra na máquina de quem
# tem o arquivo e funciona na de quem não tem, que é o pior tipo de defeito
# de build. Quem manda aqui é o go.mod, como no Flatpak.
GOWORK=off go mod vendor

echo ">> compilando o Flatpak ($APPID $VERSAO)"
mkdir -p build
flatpak-builder --force-clean --user --install-deps-from=flathub \
    --repo=build/repo build/dir "$MANIFESTO"

echo ">> gerando o bundle"
flatpak build-bundle build/repo "build/$APPID-$VERSAO.flatpak" "$APPID"
echo "bundle: build/$APPID-$VERSAO.flatpak"

# Padrão de publicação: ao subir a release no GitHub, o nome do asset
# pode variar (ex.: com a versão embutida, como aqui) — o atualizador
# (internal/atualizador) só exige que termine em ".flatpak", não um nome
# fixo. Isso é de propósito, para não repetir o bug do lado Windows, onde
# o nome fixo "SHA256SUMS.txt" divergiu do publicado numa release
# (SHA256SUMS-2.1.0.txt) e a atualização nunca foi oferecida. Não crie
# aqui uma dependência de nome exato sem essa mesma tolerância.

if [ "${1:-}" = "--instalar" ]; then
    flatpak install --user --reinstall -y "build/$APPID-$VERSAO.flatpak"
    echo "instalado. rode com: flatpak run $APPID"
fi
