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
go mod vendor

echo ">> compilando o Flatpak ($APPID $VERSAO)"
mkdir -p build
flatpak-builder --force-clean --user --install-deps-from=flathub \
    --repo=build/repo build/dir "$MANIFESTO"

echo ">> gerando o bundle"
flatpak build-bundle build/repo "build/$APPID-$VERSAO.flatpak" "$APPID"
echo "bundle: build/$APPID-$VERSAO.flatpak"

if [ "${1:-}" = "--instalar" ]; then
    flatpak install --user --reinstall -y "build/$APPID-$VERSAO.flatpak"
    echo "instalado. rode com: flatpak run $APPID"
fi
