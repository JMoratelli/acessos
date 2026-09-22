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
DIST=$SAIDA/dist
CACHE=$SAIDA/.cache
WINEPREFIX_LOCAL=$PWD/$SAIDA/wine
INNO_URL=https://github.com/jrsoftware/issrc/releases/download/is-6_7_3/innosetup-6.7.3.exe
CC=${CC:-x86_64-w64-mingw32-gcc}

VERSAO=$(sed -n 's/.*<release version="\([^"]*\)".*/\1/p' flatpak/$APPID.metainfo.xml | head -1)

if [ "${1:-}" = "--limpar" ]; then
    rm -rf "$SAIDA" cmd/acessos/recurso_windows.syso
    echo "$SAIDA apagado"
    exit 0
fi

# 0. RUNTIME C: o .exe e as DLLs que viajam com ele TÊM de usar o mesmo.
#
# No Linux esta etapa não tem irmã e nem poderia ter: lá o app e as
# bibliotecas falam com a MESMA libc, a do sistema (ou a do runtime do
# Flatpak), e não existe escolha a fazer. No Windows existem duas — a
# antiga msvcrt.dll e a UCRT —, cada uma com o SEU heap. Memória alocada
# dentro de uma DLL de um sabor e liberada pelo .exe do outro é violação de
# acesso na certa.
#
# ISSO JÁ ACONTECEU, e caro. O .exe da v2.7.1 saiu do cross-compiler ligado
# em msvcrt.dll enquanto o sysroot vinha do repositório ucrt64 do MSYS2.
# Resultado: toda sessão VNC morria na conexão — a primeira linha do
# vs_conectar libera o serverHost que a libvncclient tinha alocado, e o
# free() caía no heap errado. Compilava limpo, ligava limpo, instalava,
# abria a janela, e só morria na hora de usar.
#
# O script anterior escolhia o repositório por uma AFIRMAÇÃO em comentário
# ("o gcc do Arch gera UCRT"). Era verdade quando foi escrita e deixou de
# ser sem avisar ninguém. Agora ninguém afirma nada: mede-se o compilador,
# compilando um programa de uma linha e olhando o que ele importa.
sonda=$(mktemp -d)
printf 'int main(void){return 0;}\n' > "$sonda/sonda.c"
if "$CC" "$sonda/sonda.c" -o "$sonda/sonda.exe" >/dev/null 2>&1; then
    CRT_CC=$(scripts/crt-windows.sh "$sonda/sonda.exe" || echo desconhecido)
else
    CRT_CC=desconhecido
fi
rm -rf "$sonda"

case "$CRT_CC" in
    ucrt)   REPO=ucrt64 ;;
    msvcrt) REPO=mingw64 ;;
    *)
        REPO=ucrt64
        echo "AVISO: não consegui medir o runtime C de $CC (deu '$CRT_CC')." >&2
        echo "       Seguindo com $REPO; a trava depois do link confere." >&2
        ;;
esac
echo ">> runtime C do $CC: $CRT_CC — sysroot do repositório $REPO"

# O sysroot mora num diretório POR SABOR: trocar de compilador não pode
# reaproveitar em silêncio o sysroot do outro, que é exatamente o caminho
# de volta para o defeito acima.
SYSROOT_REAL=$PWD/$SAIDA/sysroot/$REPO
# cgo corta CGO_LDFLAGS/CGO_CFLAGS/PKG_CONFIG no primeiro espaço do valor,
# sem suporte a aspas — limite conhecido do Go. Se o próprio repositório
# estiver num caminho com espaço (pasta sincronizada tipo "Google Drive",
# como aqui), qualquer flag que aponte direto pro sysroot quebra o link
# ("cannot find Drive/Machado/..."). Um link simbólico fixo fora do
# repositório contorna isso sem mudar onde o sysroot de fato mora (continua
# em build/win, como todo o resto).
SYSROOT=/tmp/acessos-win-sysroot
ln -sfn "$SYSROOT_REAL" "$SYSROOT"

# 1. sysroot: as bibliotecas C que o Arch não empacota para MinGW.
if [ ! -d "$SYSROOT/lib/pkgconfig" ]; then
    echo ">> montando o sysroot MinGW (MSYS2)"
    python3 scripts/sysroot-msys2.py --repo "$REPO" "$SAIDA" freerdp libvncserver
fi

# 2. ícone: um .ico multi-resolução a partir do mesmo SVG do Linux, para
#    não haver duas artes divergindo com o tempo.
ICO=$SAIDA/acessos.ico
if [ ! -f "$ICO" ] || [ icones/acessos.svg -nt "$ICO" ]; then
    echo ">> gerando o ícone"
    tmp=$(mktemp -d)
    # A ORDEM das imagens dentro do .ico importa: o Windows trata a
    # PRIMEIRA entrada como padrão em vários lugares (Explorer, Alt+Tab,
    # instalador), e a convenção é ascendente. A lista é montada à mão de
    # propósito — deixar o shell expandir "$tmp"/*.png ordenava por NOME,
    # e o .ico saía 128, 16, 24, 256, 32, 48, 64: o de 128 no papel de
    # padrão, com o Explorer escolhendo o tamanho errado conforme o caso.
    pngs=()
    for lado in 16 24 32 48 64 128 256; do
        rsvg-convert -w "$lado" -h "$lado" icones/acessos.svg -o "$tmp/$lado.png"
        pngs+=("$tmp/$lado.png")
    done
    magick "${pngs[@]}" "$ICO"
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
      VALUE "CompanyName", "Jurandir Moratelli"
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
# O cgo SEPARA o valor de PKG_CONFIG por espaço (é por isso que o wrapper
# existe: "pkg-config --define-prefix" direto vira um nome de programa só,
# com o --define-prefix ignorado). Se o próprio $PWD tiver espaço no
# caminho — caso comum com pastas sincronizadas tipo "Google Drive" —, o
# cgo corta o caminho do wrapper ali e tenta executar só o pedaço antes do
# espaço, com "arquivo não encontrado". Por isso o wrapper é copiado para
# um caminho fixo sem espaço antes de apontar PKG_CONFIG pra ele.
pkgConfigMingw=/tmp/acessos-pkg-config-mingw
cp scripts/pkg-config-mingw "$pkgConfigMingw"
chmod +x "$pkgConfigMingw"
export PKG_CONFIG="$pkgConfigMingw"
export CGO_ENABLED=1 GOOS=windows GOARCH=amd64
export CC   # medido no passo 0; o sysroot foi escolhido para casar com ele
export CGO_LDFLAGS="-O2 -g -L$SYSROOT/lib"
mkdir -p "$DIST"
# -H=windowsgui: sem isto o Windows abre um console preto atrás da janela.
go build -ldflags "-H=windowsgui" -o "$DIST/acessos.exe" ./cmd/acessos

# 4b. TRAVA do runtime C. O passo 0 escolhe o sysroot pelo compilador;
#     aqui se confere o que DE FATO saiu, medindo o .exe contra uma DLL que
#     vai junto dele. É a trava que faltava: sem ela, um sysroot velho de
#     outro sabor (ou um compilador trocado no meio do caminho) volta a
#     produzir um pacote que instala, abre e só morre na hora de conectar.
#
#     Falhar aqui é o ponto: um .exe destes não pode ser publicado.
echo ">> conferindo o runtime C do que vai ser empacotado"
scripts/crt-windows.sh --conferir "$DIST/acessos.exe" \
    "$SYSROOT/bin/libvncclient.dll" "$SYSROOT_REAL"

# 5. DLLs: o linker grava só as dependências diretas; o resto da cadeia
#    vem daqui.
echo ">> resolvendo DLLs"
python3 scripts/dlls-windows.py "$DIST/acessos.exe" "$SYSROOT" "$DIST"

# 5b. Provider "legacy" do OpenSSL. NÃO é pego pelo passo acima, e esta é
#     a armadilha: o dlls-windows.py percorre a TABELA DE IMPORTAÇÃO do
#     PE, e um provider do OpenSSL 3 não está nela — é carregado em tempo
#     de execução, pelo nome, de dentro do MODULESDIR compilado na
#     libcrypto. Na libcrypto do MSYS2 esse caminho é
#     /ucrt64/lib/ossl-modules, que não existe em máquina nenhuma sem o
#     MSYS2 instalado.
#
#     Sem o legacy não há MD4 nem RC4, e o FreeRDP diz no log o que isso
#     custa: "md4: NTLM support not available" e "rc4: ... NTLM and
#     autoreconnect cookies will not work". Na prática: login recusado
#     (ERRCONNECT_LOGON_FAILURE) contra servidor que não faz Kerberos, e
#     reconexão automática depois de queda passageira que não acontece.
#     Os dois apareceram no log de produção do Windows.
#
#     A pasta vai junto do .exe; quem aponta o OPENSSL_MODULES para ela é
#     o próprio app, no start (ver cmd/acessos/ossl_windows.go). O
#     instalador leva a pasta inteira (recursesubdirs no .iss).
echo ">> copiando o provider legacy do OpenSSL"
mkdir -p "$DIST/ossl-modules"
cp "$SYSROOT/lib/ossl-modules/legacy.dll" "$DIST/ossl-modules/"

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

# 7. sha256 do instalador: o atualizador do Windows (internal/atualizador)
#    baixa um asset SHA256SUMS*.txt anexado à release antes de confiar no
#    .exe. Mesmo formato de `sha256sum` já usado nas releases anteriores
#    (que hoje juntam ali a soma do bundle Flatpak também — junte as duas
#    linhas na hora de publicar).
#
#    Padrão de publicação: suba o asset SEMPRE como "SHA256SUMS.txt"
#    (sem sufixo de versão no nome) — é o nome usado em toda release já
#    publicada. Um nome diferente (ex.: "SHA256SUMS-2.1.0.txt") já causou
#    o atualizador nunca oferecer a versão nova no Windows; o código hoje
#    tolera qualquer "SHA256SUMS*.txt" como rede de segurança, mas manter
#    o nome fixo evita depender dessa tolerância.
INSTALADOR="$SAIDA/AcessosSetup-$VERSAO.exe"
(cd "$SAIDA" && sha256sum "$(basename "$INSTALADOR")") > "$SAIDA/SHA256SUMS.txt"

echo "instalador: $INSTALADOR"
echo "somas:      $SAIDA/SHA256SUMS.txt ($(cat "$SAIDA/SHA256SUMS.txt"))"
echo ">> ao publicar a release no GitHub, suba os assets com estes nomes exatos:"
echo "     $(basename "$INSTALADOR")"
echo "     SHA256SUMS.txt"
