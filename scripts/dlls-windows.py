#!/usr/bin/env python3
"""Resolve, recursivamente, as DLLs do sysroot MinGW de que o .exe precisa.

O linker grava no PE só as dependências DIRETAS; cada DLL copiada traz as
suas. Sem percorrer a cadeia inteira, o app abre e só quebra quando o
operador tenta conectar (libfreerdp3 sem libwinpr3, por exemplo). As DLLs
do próprio Windows (KERNEL32, WS2_32…) ficam de fora: existem em qualquer
máquina e copiá-las é receita para conflito.

Uso: dlls-windows.py <exe> <sysroot/mingw64> <destino>

Qualquer dependência que não esteja no sysroot E não seja reconhecida
como DLL do próprio Windows interrompe o build (ver DLLS_SISTEMA) — o
objetivo é nunca gerar em silêncio um instalador faltando biblioteca,
como aconteceu quando o sysroot ficou incompleto (libcrypto/libssl,
entre outras, ausentes) e ninguém percebeu até testar no Windows.
"""
import os
import shutil
import subprocess
import sys

OBJDUMP = os.environ.get("OBJDUMP", "x86_64-w64-mingw32-objdump")

# DLLs que fazem parte do próprio Windows (ou do runtime UCRT, que já vem
# com o sistema a partir do Windows 10) — nunca existem no sysroot MinGW
# porque não fazem sentido empacotadas com o app. Fora desta lista, ou do
# prefixo api-ms-win- (API sets do Windows, mesmo raciocínio), qualquer
# dependência que não apareça no sysroot é tratada como sysroot
# incompleto, não como "deve ser do Windows".
DLLS_SISTEMA = {
    "kernel32.dll", "user32.dll", "gdi32.dll", "advapi32.dll", "ws2_32.dll",
    "rpcrt4.dll", "ole32.dll", "oleaut32.dll", "shell32.dll", "shlwapi.dll",
    "msvcrt.dll", "ntdll.dll", "ncrypt.dll", "credui.dll", "dbghelp.dll",
    "crypt32.dll", "dnsapi.dll", "iphlpapi.dll", "msimg32.dll",
    "userenv.dll", "usp10.dll", "winmm.dll", "winspool.drv", "bcrypt.dll",
    "bcryptprimitives.dll", "gdiplus.dll", "dwrite.dll", "version.dll",
    "comctl32.dll", "comdlg32.dll", "setupapi.dll", "dwmapi.dll",
    "uxtheme.dll", "secur32.dll", "psapi.dll", "netapi32.dll", "imm32.dll",
    "d3d9.dll", "d3d11.dll", "dxgi.dll", "avicap32.dll", "mf.dll",
    "mfplat.dll", "mfreadwrite.dll", "propsys.dll", "wtsapi32.dll",
}


def dependencias(caminho):
    saida = subprocess.run([OBJDUMP, "-p", caminho], capture_output=True,
                           text=True).stdout
    return [l.split()[-1] for l in saida.splitlines() if "DLL Name:" in l]


def dll_do_windows(nome):
    baixo = nome.lower()
    return baixo.startswith("api-ms-win-") or baixo in DLLS_SISTEMA


def resolver(exe, bindir):
    # índice por nome em caixa baixa: o sistema de arquivos do Windows
    # ignora caixa, o do Linux não, e o nome que o objdump lê do PE pode
    # não bater byte a byte com o do arquivo no sysroot.
    disponiveis = {f.lower(): f for f in os.listdir(bindir)
                   if os.path.isfile(os.path.join(bindir, f))}

    vistas, faltando, fila = set(), set(), dependencias(exe)
    while fila:
        nome = fila.pop(0)
        if nome in vistas:
            continue
        real = disponiveis.get(nome.lower())
        if real is None:
            if not dll_do_windows(nome):
                faltando.add(nome)
            continue
        vistas.add(nome)
        fila += dependencias(os.path.join(bindir, real))

    if faltando:
        print("ERRO: dependências ausentes do sysroot, nem reconhecidas "
              "como DLL do Windows (pacote MSYS2 faltando ou sysroot "
              "desatualizado — apague build/win/sysroot e deixe o build "
              "montá-lo de novo):", file=sys.stderr)
        for nome in sorted(faltando):
            print(f"  {nome}", file=sys.stderr)
        sys.exit(1)

    return sorted((nome, disponiveis[nome.lower()]) for nome in vistas)


def main():
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    exe, sysroot, destino = sys.argv[1:]
    bindir = os.path.join(sysroot, "bin")
    os.makedirs(destino, exist_ok=True)
    nomes = resolver(exe, bindir)
    for _, real in nomes:
        shutil.copy2(os.path.join(bindir, real), os.path.join(destino, real))
    print(f"{len(nomes)} DLLs copiadas para {destino}")


if __name__ == "__main__":
    main()
