#!/usr/bin/env bash
# build-windows.sh — gera acessos.exe e o instalador, a partir do Linux.
#
#   scripts/build-windows.sh              compila e monta build/win/dist
#   scripts/build-windows.sh --instalador compila e gera o instalador .exe
#   scripts/build-windows.sh --limpar     apaga build/win e sai
#
# Uma passada só, do zero à entrega, sem nenhum passo manual: o script
# monta o sysroot MinGW (pacotes do MSYS2), gera o ícone, compila com cgo
# (VNC e RDP ligados — sem eles o app perde o motivo de existir), resolve
# RECURSIVAMENTE as DLLs de que o .exe depende e, se pedido, compila o
# instalador com o Inno Setup rodando no Wine.
#
# Tudo o que é baixado ou gerado fica em build/win — nada é instalado no
# sistema, nada precisa de root.
set -euo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)"

APPID=org.jj.Acessos
SAIDA=build/win
SYSROOT=$PWD/$SAIDA/sysroot/ucrt64
DIST=$SAIDA/dist
CACHE=$SAIDA/.cache
WINEPREFIX_LOCAL=$PWD/$SAIDA/wine
INNO_URL=https://github.com/jrsoftware/issrc/releases/download/is-6_7_3/innosetup-6.7.3.exe

VERSAO=$(sed -n 's/.*<release version="\([^"]*\)".*/\1/p' flatpak/$APPID.metainfo.xml | head -1)

if [ "${1:-}" = "--limpar" ]; then
    rm -rf "$SAIDA" cmd/acessos/recurso_windows.syso
    echo "$SAIDA apagado"
    exit 0
fi

# 1. sysroot: as bibliotecas C que o Arch não empacota para MinGW.
if [ ! -d "$SYSROOT/lib/pkgconfig" ]; then
    echo ">> montando o sysroot MinGW (MSYS2)"
    python3 scripts/sysroot-msys2.py "$SAIDA" freerdp libvncserver
fi

# 2. ícone: um .ico multi-resolução a partir do mesmo SVG do Linux, para
#    não haver duas artes divergindo com o tempo.
ICO=$SAIDA/acessos.ico
if [ ! -f "$ICO" ] || [ icones/acessos.svg -nt "$ICO" ]; then
    echo ">> gerando o ícone"
    tmp=$(mktemp -d)
    for lado in 16 24 32 48 64 128 256; do
        rsvg-convert -w $lado -h $lado icones/acessos.svg -o "$tmp/$lado.png"
    done
    magick "$tmp"/*.png "$ICO"
    rm -rf "$tmp"
fi

# 3. recurso do Windows: ícone no Explorer e aba "Detalhes" com a versão.
#    O sufixo _windows no .syso mantém o build de Linux intocado.
echo ">> gerando o recurso (ícone + versão $VERSAO)"
cp "flatpak/$APPID.metainfo.xml" cmd/acessos/dados/metainfo.xml
IFS=. read -r v1 v2 v3 <<< "${VERSAO%%-*}"
rc=$(mktemp --suffix=.rc)
cat > "$rc" <<RC
1 ICON "$PWD/$ICO"
1 VERSIONINFO
FILEVERSION ${v1:-0},${v2:-0},${v3:-0},0
PRODUCTVERSION ${v1:-0},${v2:-0},${v3:-0},0
BEGIN
  BLOCK "StringFileInfo"
  BEGIN
    BLOCK "040904b0"
    BEGIN
      VALUE "CompanyName", "Machadao"
      VALUE "FileDescription", "Acessos - gerenciador de acesso remoto"
      VALUE "FileVersion", "$VERSAO"
      VALUE "InternalName", "acessos"
      VALUE "OriginalFilename", "acessos.exe"
      VALUE "ProductName", "Acessos"
      VALUE "ProductVersion", "$VERSAO"
    END
  END
  BLOCK "VarFileInfo"
  BEGIN
    VALUE "Translation", 0x409, 1200
  END
END
RC
x86_64-w64-mingw32-windres -O coff -i "$rc" -o cmd/acessos/recurso_windows.syso
rm -f "$rc"

# 4. compilação. O CGO_LDFLAGS repete o -L do sysroot de propósito: o Go
#    não inclui o PKG_CONFIG_PATH na chave do cache de build, então sem
#    isto uma troca de sysroot reaproveita silenciosamente o link antigo.
echo ">> compilando acessos.exe"
export PKG_CONFIG_PATH="$SYSROOT/lib/pkgconfig"
export PKG_CONFIG_LIBDIR="$SYSROOT/lib/pkgconfig"
export PKG_CONFIG="$PWD/scripts/pkg-config-mingw"
export CGO_ENABLED=1 GOOS=windows GOARCH=amd64
export CC=x86_64-w64-mingw32-gcc
export CGO_LDFLAGS="-O2 -g -L$SYSROOT/lib"
mkdir -p "$DIST"
# -H=windowsgui: sem isto o Windows abre um console preto atrás da janela.
go build -ldflags "-H=windowsgui" -o "$DIST/acessos.exe" ./cmd/acessos

# 5. DLLs: o linker grava só as dependências diretas; o resto da cadeia
#    vem daqui.
echo ">> resolvendo DLLs"
python3 scripts/dlls-windows.py "$DIST/acessos.exe" "$SYSROOT" "$DIST"
echo "pronto: $DIST ($(du -sh "$DIST" | cut -f1))"

[ "${1:-}" = "--instalador" ] || exit 0

# 6. instalador. O Inno Setup é um .exe: instalamos num prefixo Wine
#    próprio (build/win/wine), que não mexe no ~/.wine do usuário.
ISCC="$WINEPREFIX_LOCAL/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe"
export WINEPREFIX=$WINEPREFIX_LOCAL WINEDEBUG=-all
if [ ! -f "$ISCC" ]; then
    echo ">> instalando o Inno Setup no prefixo Wine local"
    mkdir -p "$CACHE"
    [ -f "$CACHE/innosetup.exe" ] || curl -L -o "$CACHE/innosetup.exe" "$INNO_URL"
    wineboot -u >/dev/null 2>&1
    wine "$CACHE/innosetup.exe" /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- >/dev/null 2>&1
fi
echo ">> compilando o instalador"
wine "$ISCC" "/DAppVersion=$VERSAO" "/O$PWD/$SAIDA" scripts/instalador.iss >"$SAIDA/iscc.log" 2>&1 \
    || { tail -20 "$SAIDA/iscc.log"; exit 1; }
echo "instalador: $SAIDA/AcessosSetup-$VERSAO.exe"
