#!/usr/bin/env python3
"""Resolve, recursivamente, as DLLs do sysroot MinGW de que o .exe precisa.

O linker grava no PE só as dependências DIRETAS; cada DLL copiada traz as
suas. Sem percorrer a cadeia inteira, o app abre e só quebra quando o
operador tenta conectar (libfreerdp3 sem libwinpr3, por exemplo). As DLLs
do próprio Windows (KERNEL32, WS2_32…) ficam de fora: existem em qualquer
máquina e copiá-las é receita para conflito.

Uso: dlls-windows.py <exe> <sysroot/mingw64> <destino>
"""
import os
import shutil
import subprocess
import sys

OBJDUMP = os.environ.get("OBJDUMP", "x86_64-w64-mingw32-objdump")


def dependencias(caminho):
    saida = subprocess.run([OBJDUMP, "-p", caminho], capture_output=True,
                           text=True).stdout
    return [l.split()[-1] for l in saida.splitlines() if "DLL Name:" in l]


def resolver(exe, bindir):
    vistas, fila = set(), dependencias(exe)
    while fila:
        nome = fila.pop(0)
        if nome in vistas:
            continue
        # o sistema de arquivos do Windows ignora caixa; o do Linux não
        origem = os.path.join(bindir, nome)
        if not os.path.isfile(origem):
            continue                      # DLL do próprio Windows
        vistas.add(nome)
        fila += dependencias(origem)
    return sorted(vistas)


def main():
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    exe, sysroot, destino = sys.argv[1:]
    bindir = os.path.join(sysroot, "bin")
    os.makedirs(destino, exist_ok=True)
    nomes = resolver(exe, bindir)
    for nome in nomes:
        shutil.copy2(os.path.join(bindir, nome), os.path.join(destino, nome))
    print(f"{len(nomes)} DLLs copiadas para {destino}")


if __name__ == "__main__":
    main()
