#!/usr/bin/env bash
# crt-windows.sh — mede o runtime C de um binário PE, e confere se dois
# binários usam o mesmo.
#
#   scripts/crt-windows.sh <arquivo>              -> ucrt | msvcrt | misto | desconhecido
#   scripts/crt-windows.sh --conferir <exe> <dll> -> sai 1 se forem diferentes
#
# POR QUE ISTO EXISTE. O .exe e as DLLs que viajam com ele TÊM de usar o
# mesmo runtime C. Runtimes diferentes são heaps diferentes: memória
# alocada dentro de uma DLL e liberada pelo .exe (ou o contrário) vira
# violação de acesso. Não é teoria — foi o que derrubou toda sessão VNC no
# Windows: o .exe da v2.7.1 saiu do cross-compiler ligado em msvcrt.dll, as
# DLLs vieram do repositório ucrt64 do MSYS2, e a PRIMEIRA linha do
# vs_conectar (free do serverHost que a libvncclient tinha alocado) matava
# o processo da sessão a cada tentativa de conexão.
#
# O que torna esse defeito caro é que ele não aparece em lugar nenhum antes
# de acontecer: compila limpo, liga limpo, instala, abre a janela, e só
# morre na hora de usar. Daí medir — e medir o BINÁRIO, não o nome do
# repositório, não o que o script acha que o compilador faz.
#
# Isto não tem irmão do lado Linux, e nem poderia: lá o app e as
# bibliotecas falam com a mesma libc e não há escolha a fazer.
#
# Usa o objdump do próprio cross-toolchain quando existe (é ele que lê PE
# em qualquer distro); cai no objdump do sistema, que também lê PE quando o
# binutils foi compilado com os alvos habituais.
set -euo pipefail

achar_objdump() {
    local cand
    for cand in ${OBJDUMP:-} x86_64-w64-mingw32-objdump objdump; do
        if command -v "$cand" >/dev/null 2>&1; then
            echo "$cand"
            return 0
        fi
    done
    return 1
}

crt_de() {
    local bin=$1 objdump importadas tem_ucrt=0 tem_msvcrt=0
    if ! objdump=$(achar_objdump); then
        echo desconhecido
        return 0
    fi
    # Só a tabela de importação: é o que o carregador vai de fato resolver.
    importadas=$("$objdump" -p "$bin" 2>/dev/null | sed -n 's/.*DLL Name: //p' \
        | tr 'A-Z' 'a-z' | tr -d '\r') || true

    # ucrtbase direto ou pelos stubs api-ms-win-crt-*, que é como o
    # mingw-w64 UCRT liga na prática.
    if printf '%s\n' "$importadas" | grep -qE '^(ucrtbase\.dll|api-ms-win-crt-)'; then
        tem_ucrt=1
    fi
    # msvcrt.dll é o runtime antigo do sistema; msvcr71/msvcr100... são os
    # redistribuíveis do Visual Studio, mesma família para o que importa.
    if printf '%s\n' "$importadas" | grep -qE '^(msvcrt(-os)?\.dll|msvcr[0-9]+\.dll)$'; then
        tem_msvcrt=1
    fi

    if [ "$tem_ucrt" = 1 ] && [ "$tem_msvcrt" = 1 ]; then
        echo misto
    elif [ "$tem_ucrt" = 1 ]; then
        echo ucrt
    elif [ "$tem_msvcrt" = 1 ]; then
        echo msvcrt
    else
        # Binário que não importa runtime C nenhum existe (Go puro, sem
        # cgo) e não é erro: para o que se decide aqui, ele não opina.
        echo desconhecido
    fi
}

if [ "${1:-}" = "--conferir" ]; then
    exe=${2:-}
    dll=${3:-}
    sysroot=${4:-}
    if [ -z "$exe" ] || [ -z "$dll" ]; then
        echo "uso: $0 --conferir <exe> <dll> [sysroot]" >&2
        exit 2
    fi
    for arq in "$exe" "$dll"; do
        if [ ! -r "$arq" ]; then
            echo "não consigo ler $arq" >&2
            exit 2
        fi
    done
    crt_exe=$(crt_de "$exe")
    crt_dll=$(crt_de "$dll")
    echo "runtime C: $(basename "$exe")=$crt_exe, $(basename "$dll")=$crt_dll"
    if [ "$crt_exe" = desconhecido ] || [ "$crt_dll" = desconhecido ]; then
        echo "AVISO: não deu para medir os dois lados; a conferência não vale." >&2
        exit 0
    fi
    [ "$crt_exe" = "$crt_dll" ] && exit 0
    cat >&2 <<MSG

ERRO: runtime C incompatível entre o executável e as DLLs que vão com ele.

    $(basename "$exe") -> $crt_exe
    $(basename "$dll") -> $crt_dll

Cada runtime tem o seu heap. Memória alocada dentro de uma DLL e liberada
pelo .exe (ou o contrário) vira violação de acesso — e o app só quebra na
hora de USAR, nunca no build. Foi assim que a v2.7.1 saiu com toda sessão
VNC morrendo na conexão.

Este pacote NÃO pode ser publicado. Normalmente o sysroot é de um sabor
diferente do compilador, ou ficou de um build anterior: apague-o e rode de
novo.
MSG
    # if, e não "[ ... ] && echo": com set -e, uma lista && que termina
    # falsa encerra o script ali e engole o resto da mensagem.
    if [ -n "$sysroot" ]; then
        echo "    rm -rf $sysroot" >&2
    fi
    echo >&2
    exit 1
fi

bin=${1:-}
if [ -z "$bin" ] || [ ! -r "$bin" ]; then
    echo "uso: $0 <arquivo .exe ou .dll>" >&2
    echo "     $0 --conferir <exe> <dll> [sysroot]" >&2
    exit 2
fi
crt_de "$bin"
