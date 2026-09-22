#!/usr/bin/env python3
"""Monta um sysroot MinGW-w64 a partir dos pacotes binários do MSYS2.

Por que isto existe: o cgo deste projeto liga em libvncclient e
libfreerdp3, e o Arch não empacota nenhuma das duas para MinGW (nem o
repositório oficial, nem o AUR). Compilá-las do zero significaria também
compilar OpenSSL, zlib, libjpeg e mais meia dúzia de dependências — meia
hora de build para chegar exatamente no que o MSYS2 já publica pronto.

Então: baixamos os pacotes do MSYS2, resolvemos as dependências
RECURSIVAMENTE pelo banco de dados oficial e extraímos tudo num prefixo
local. Nenhuma instalação no sistema, nenhum root, e o resultado é o mesmo
em qualquer máquina.

QUAL REPOSITÓRIO não é preferência. O MSYS2 publica o mesmo pacote em dois
sabores de runtime C — "ucrt64" (UCRT) e "mingw64" (MSVCRT) — e o que vem
daqui tem de casar com o que o CROSS-COMPILER gera. Misturar põe dois
runtimes C no mesmo processo, com um heap cada: memória alocada dentro de
uma DLL e liberada pelo .exe vira violação de acesso. Quem mede e decide é
o scripts/build-windows.sh, com o scripts/crt-windows.sh; o padrão abaixo
só serve para quem rodar este script na mão.

Uso:
    scripts/sysroot-msys2.py [--repo ucrt64|mingw64] <diretório> pacote [pacote...]
"""

import os
import shutil
import subprocess
import sys
import tarfile
import urllib.request

# Os dois sabores, com o prefixo de nome de pacote de cada um. As versões
# publicadas são as MESMAS nos dois (conferido em 2026-09-22: freerdp
# 3.31.1-1 e libvncserver 0.9.15-3 em ambos) — o que muda é o runtime C
# contra o qual foram compilados.
#
# O comentário que morava aqui afirmava que o mingw-w64-gcc do Arch gera
# binários UCRT, "as importações api-ms-win-crt-* no .exe provam". Deixou
# de ser verdade em algum momento e ninguém percebeu: o .exe da v2.7.1
# importa msvcrt.dll, e foi assim que TODA sessão VNC passou a morrer no
# Windows — o free() do serverHost, no vs_conectar, devolvia ao heap do
# msvcrt um ponteiro que a libvncclient tinha alocado no do ucrtbase.
#
# Por isso ninguém mais ACHA nada aqui: o build mede o compilador e diz.
REPOS = {
    "ucrt64": "mingw-w64-ucrt-x86_64-",
    "mingw64": "mingw-w64-x86_64-",
}
REPO = "ucrt64"


def prefixo():
    return REPOS[REPO]


def espelho():
    return f"https://mirror.msys2.org/mingw/{REPO}"


def banco():
    return f"{REPO}.db.tar.zst"


# Pacotes de cadeia de ferramentas que NÃO entram no sysroot: quem compila
# aqui é o mingw-w64-gcc do Arch, com o CRT e os cabeçalhos dele. Misturar
# o CRT do MSYS2 (versão diferente do mingw-w64) com o crt2.o local quebra
# a ligação em símbolos internos como _gnu_exception_handler. O sysroot
# existe só para as bibliotecas de terceiros (FreeRDP, libvnc e cadeia).
SEM_CADEIA = {"crt", "headers"}


def baixar(url, destino):
    if os.path.exists(destino):
        return destino
    os.makedirs(os.path.dirname(destino), exist_ok=True)
    pedido = urllib.request.Request(url, headers={"User-Agent": "acessos-build"})
    tmp = destino + ".parcial"
    with urllib.request.urlopen(pedido, timeout=120) as r, open(tmp, "wb") as f:
        shutil.copyfileobj(r, f)
    os.rename(tmp, destino)
    return destino


def abrir_zst(caminho):
    """tarfile não lê zstd; o unzstd resolve e evita mais uma dependência
    Python só para isso."""
    return subprocess.run(["unzstd", "-c", caminho], check=True,
                          stdout=subprocess.PIPE).stdout


def ler_banco(cache):
    """Devolve {nome: (arquivo, [dependências])} lendo o banco do MSYS2.

    O banco traz um diretório por pacote, cada um com um 'desc' em blocos
    %CHAVE% seguidos de linhas. Interessam %NAME%, %FILENAME%, %DEPENDS% e
    %PROVIDES% (há pacote que depende de um nome "virtual")."""
    dados = abrir_zst(baixar(f"{espelho()}/{banco()}",
                             os.path.join(cache, banco())))
    import io
    pacotes, provedores = {}, {}
    with tarfile.open(fileobj=io.BytesIO(dados)) as tf:
        for membro in tf.getmembers():
            if not membro.name.endswith("/desc"):
                continue
            texto = tf.extractfile(membro).read().decode("utf-8", "replace")
            campos, chave = {}, None
            for linha in texto.splitlines():
                if linha.startswith("%") and linha.endswith("%"):
                    chave = linha.strip("%")
                    campos[chave] = []
                elif linha.strip() and chave:
                    campos[chave].append(linha.strip())
            nome = campos.get("NAME", [None])[0]
            if not nome:
                continue
            deps = [d.split("=")[0].split(">")[0].split("<")[0]
                    for d in campos.get("DEPENDS", [])]
            pacotes[nome] = (campos.get("FILENAME", [""])[0], deps)
            for p in campos.get("PROVIDES", []):
                provedores[p.split("=")[0]] = nome
    return pacotes, provedores


def fecho(pacotes, provedores, raizes):
    """Fecho transitivo das dependências — é aqui que mora o 'recursivo':
    pedir freerdp traz openssl, zlib, ffmpeg e o resto da cadeia sozinho."""
    vistos, fila = set(), list(raizes)
    while fila:
        nome = fila.pop()
        if nome in vistos:
            continue
        if nome not in pacotes:
            nome = provedores.get(nome, nome)
            if nome not in pacotes or nome in vistos:
                continue
        if nome[len(prefixo()):] in SEM_CADEIA:
            continue
        vistos.add(nome)
        fila.extend(pacotes[nome][1])
    return sorted(vistos)


def main():
    global REPO
    args = sys.argv[1:]
    # --repo antes de tudo: quem chama (build-windows.sh) já mediu o
    # compilador e não pode ser contrariado por um padrão daqui.
    while args and args[0].startswith("--repo"):
        if args[0] == "--repo":
            if len(args) < 2:
                print("--repo exige um valor", file=sys.stderr)
                return 2
            REPO, args = args[1], args[2:]
        else:
            REPO, args = args[0].split("=", 1)[1], args[1:]
        if REPO not in REPOS:
            print(f"repositório desconhecido: {REPO} "
                  f"(esperado: {', '.join(sorted(REPOS))})", file=sys.stderr)
            return 2
    if len(args) < 2:
        print(__doc__)
        return 2
    destino, pedidos = args[0], args[1:]
    cache = os.path.join(destino, ".cache")
    raiz = os.path.join(destino, "sysroot")

    pacotes, provedores = ler_banco(cache)
    raizes = [p if p.startswith(prefixo()) else prefixo() + p for p in pedidos]
    for r in raizes:
        if r not in pacotes:
            print(f"pacote desconhecido no MSYS2: {r}", file=sys.stderr)
            return 1

    lista = fecho(pacotes, provedores, raizes)
    print(f"{len(lista)} pacote(s) a instalar no sysroot")
    os.makedirs(raiz, exist_ok=True)
    for nome in lista:
        arquivo = pacotes[nome][0]
        if not arquivo:
            continue
        local = baixar(f"{espelho()}/{arquivo}", os.path.join(cache, arquivo))
        subprocess.run(["tar", "--use-compress-program=unzstd", "-xf", local,
                        "-C", raiz, "--exclude=.PKGINFO", "--exclude=.BUILDINFO",
                        "--exclude=.MTREE", "--exclude=.INSTALL"], check=True)
    crt = "UCRT" if REPO == "ucrt64" else "MSVCRT"
    print(f"sysroot pronto em {raiz}/{REPO} (runtime C: {crt})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
