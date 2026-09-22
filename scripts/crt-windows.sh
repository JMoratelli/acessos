#!/usr/bin/env bash
# crt-windows.sh — mede o runtime C de um binário PE, e confere se dois
# binários usam o mesmo.
#
#   scripts/crt-windows.sh <arquivo>
#        -> ucrt | msvcrt | misto | desconhecido | ilegivel
#   scripts/crt-windows.sh --conferir <exe> <dll> -> sai 1 se forem diferentes
#   scripts/crt-windows.sh --conferir-payload <dir>
#                                                 -> mede TODO .exe/.dll de
#                                                    dir; sai 1 se houver
#                                                    mais de um sabor
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
    if ! importadas=$("$objdump" -p "$bin" 2>/dev/null | sed -n 's/.*DLL Name: //p' \
        | tr 'A-Z' 'a-z' | tr -d '\r'); then
        echo ilegivel
        return 0
    fi
    # Tabela de importação VAZIA não é "não usa runtime C": todo PE de
    # verdade importa ao menos a kernel32. Vazio aqui é arquivo truncado,
    # cópia pela metade ou coisa que nem PE é — e isso PRECISA ser distinto
    # de "desconhecido", senão o portão do payload conta como "sem runtime
    # próprio, o que é normal" e deixa passar.
    if [ -z "$importadas" ]; then
        echo ilegivel
        return 0
    fi

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

# --conferir-payload: o ÚLTIMO portão, sobre o que vai de fato no pacote.
#
# O --conferir abaixo mede uma amostra (o .exe contra uma DLL do sysroot) e
# roda antes de as DLLs serem coletadas. Serve para falhar cedo, não para
# dar garantia: entre ele e o .zip ainda passam o coletor de DLLs, o
# provider do OpenSSL e qualquer sobra de um build anterior. Aqui se mede
# ARQUIVO POR ARQUIVO o diretório pronto.
#
# É o mais perto que dá para chegar da garantia que o Linux tem de graça.
# Lá o app e as bibliotecas saem da mesma passada de compilação contra a
# mesma libc, e misturar runtime é impossível por construção; aqui as
# bibliotecas vêm prontas do MSYS2, então a garantia tem de ser medida —
# em tudo, não por amostragem.
if [ "${1:-}" = "--conferir-payload" ]; then
    dir=${2:-}
    if [ -z "$dir" ] || [ ! -d "$dir" ]; then
        echo "uso: $0 --conferir-payload <diretório>" >&2
        exit 2
    fi

    # Sem objdump não há medição — e aqui isso é ERRO, não aviso. "Não
    # consegui medir" é exatamente o estado em que a v2.7.1 foi publicada
    # com todo o VNC morto; este portão falha FECHADO de propósito.
    if ! achar_objdump >/dev/null 2>&1; then
        cat >&2 <<'MSG'

ERRO: nenhum objdump encontrado, e sem ele não há como medir o runtime C.

Instale o mingw-w64-binutils (traz o x86_64-w64-mingw32-objdump), ou
aponte OBJDUMP= para um que leia PE.

Este portão não deixa passar "não conferido": publicar sem esta medição já
custou uma release inteira com todas as sessões VNC morrendo na conexão.
MSG
        exit 1
    fi

    ucrt=() msvcrt=() misto=() sem=() ilegivel=()
    while IFS= read -r -d '' arq; do
        case "$(crt_de "$arq")" in
            ucrt)     ucrt+=("$arq") ;;
            msvcrt)   msvcrt+=("$arq") ;;
            misto)    misto+=("$arq") ;;
            ilegivel) ilegivel+=("$arq") ;;
            *)        sem+=("$arq") ;;
        esac
    done < <(find "$dir" -type f \( -iname '*.exe' -o -iname '*.dll' \) -print0 | sort -z)

    total=$(( ${#ucrt[@]} + ${#msvcrt[@]} + ${#misto[@]} + ${#sem[@]} + ${#ilegivel[@]} ))
    if [ "$total" = 0 ]; then
        echo "ERRO: nenhum .exe ou .dll em $dir — não há o que empacotar." >&2
        exit 1
    fi

    listar() {
        local n=0 a
        for a in "$@"; do
            n=$((n + 1))
            [ "$n" -gt 8 ] && { echo "        ... e mais $(($# - 8))" >&2; break; }
            echo "        $(basename "$a")" >&2
        done
    }

    # Arquivo que não dá para medir não passa como "não opina": num pacote
    # deste projeto todo .exe/.dll é PE de verdade, então ilegível aqui é
    # cópia truncada, download pela metade ou disco cheio — defeito que
    # chegaria ao usuário como "o app não abre".
    if [ ${#ilegivel[@]} -gt 0 ]; then
        echo >&2
        echo "ERRO: arquivo que o objdump não conseguiu ler (truncado? não é PE?):" >&2
        listar "${ilegivel[@]}"
        echo >&2
        exit 1
    fi

    # misto é defeito em si: um binário só, importando os dois runtimes,
    # já carrega os dois heaps para dentro do processo.
    if [ ${#misto[@]} -gt 0 ]; then
        echo >&2
        echo "ERRO: binário importando OS DOIS runtimes C:" >&2
        listar "${misto[@]}"
        echo >&2
        exit 1
    fi

    if [ ${#ucrt[@]} -gt 0 ] && [ ${#msvcrt[@]} -gt 0 ]; then
        cat >&2 <<MSG

ERRO: o pacote mistura runtimes C. Ele NÃO pode ser publicado.

    ucrt   (${#ucrt[@]} arquivo(s))
MSG
        listar "${ucrt[@]}"
        echo "    msvcrt (${#msvcrt[@]} arquivo(s))" >&2
        listar "${msvcrt[@]}"
        cat >&2 <<MSG

Cada runtime tem o seu heap: memória alocada dentro de uma DLL e liberada
pelo .exe vira violação de acesso, e o app só quebra na hora de USAR.

Normalmente é sobra de um build do outro sabor no diretório de saída.
Rode "scripts/build-windows.sh --limpar" e refaça.
MSG
        exit 1
    fi

    sabor=msvcrt
    [ ${#ucrt[@]} -gt 0 ] && sabor=ucrt
    echo "runtime C do pacote: $sabor em $total binário(s)" \
         "(${#sem[@]} sem runtime próprio, o que é normal)"
    exit 0
fi

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
    if [ "$crt_exe" = desconhecido ] || [ "$crt_dll" = desconhecido ] \
       || [ "$crt_exe" = ilegivel ] || [ "$crt_dll" = ilegivel ]; then
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
