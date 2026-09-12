#!/usr/bin/env python3
"""Monta um sysroot MinGW-w64 a partir dos pacotes binários do MSYS2.

Por que isto existe: o cgo deste projeto liga em libvncclient e
libfreerdp3, e o Arch não empacota nenhuma das duas para MinGW (nem o
repositório oficial, nem o AUR). Compilá-las do zero significaria também
compilar OpenSSL, zlib, libjpeg e mais meia dúzia de dependências — meia
hora de build para chegar exatamente no que o MSYS2 já publica pronto.

Então: baixamos os pacotes do MSYS2 (repositório "mingw64", que é o
MinGW-w64 com MSVCRT, a mesma ABI do mingw-w64-gcc do Arch), resolvemos as
dependências RECURSIVAMENTE pelo banco de dados oficial e extraímos tudo
num prefixo local. Nenhuma instalação no sistema, nenhum root, e o
resultado é o mesmo em qualquer máquina.

Uso:
    scripts/sysroot-msys2.py <diretório> pacote [pacote...]
"""

import os
import shutil
import subprocess
import sys
import tarfile
import urllib.request

# O repositório tem de casar com a ABI do compilador local: o
# mingw-w64-gcc do Arch gera binários UCRT (as importações api-ms-win-crt-*
# no .exe provam), então usamos o repositório "ucrt64" do MSYS2. Misturar
# com o repositório "mingw64" (MSVCRT) daria dois runtimes C no mesmo
# processo — memória alocada por um e liberada pelo outro, que é o tipo de
# falha que só aparece em produção.
REPO = "ucrt64"
ESPELHO = f"https://mirror.msys2.org/mingw/{REPO}"
BANCO = f"{REPO}.db.tar.zst"
PREFIXO = "mingw-w64-ucrt-x86_64-"

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
    dados = abrir_zst(baixar(f"{ESPELHO}/{BANCO}", os.path.join(cache, BANCO)))
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
        if nome[len(PREFIXO):] in SEM_CADEIA:
            continue
        vistos.add(nome)
        fila.extend(pacotes[nome][1])
    return sorted(vistos)


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    destino, pedidos = sys.argv[1], sys.argv[2:]
    cache = os.path.join(destino, ".cache")
    raiz = os.path.join(destino, "sysroot")

    pacotes, provedores = ler_banco(cache)
    raizes = [p if p.startswith(PREFIXO) else PREFIXO + p for p in pedidos]
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
        local = baixar(f"{ESPELHO}/{arquivo}", os.path.join(cache, arquivo))
        subprocess.run(["tar", "--use-compress-program=unzstd", "-xf", local,
                        "-C", raiz, "--exclude=.PKGINFO", "--exclude=.BUILDINFO",
                        "--exclude=.MTREE", "--exclude=.INSTALL"], check=True)
    print(f"sysroot pronto em {raiz}/{REPO}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
