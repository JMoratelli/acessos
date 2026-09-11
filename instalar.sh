#!/usr/bin/env bash
#
#  instalar.sh — monta o Acessos por inteiro em Arch ou Fedora.
#
#  Faz tudo numa passada:
#     1. instala as dependencias do sistema
#     2. instala os pacotes Python (paramiko, cryptography, argon2)
#     3. compila o vncshim  (VNC proprio, sem o congelamento do gtk-vnc)
#     4. compila o rdpshim  (RDP proprio, fala direto com a libfreerdp3)
#     5. instala em ~/.local e cria o lancador no menu
#
#  USO:
#     ./instalar.sh              instala ou atualiza
#     ./instalar.sh --sem-rdp    pula o rdpshim (RDP fica indisponivel)
#     ./instalar.sh --verificar  so testa o que ja esta instalado
#     ./instalar.sh --remover    desinstala
#
#  Sem Flatpak, sem container, sem AppImage. Instala no home do usuario,
#  entao nao precisa de root exceto para os pacotes do sistema.
#
#  SEM FALLBACK, DE PROPOSITO. O RDP ja teve dois motores de reserva ao
#  longo do projeto: primeiro o gtk-frdp (GObject Introspection), depois
#  a classe AbaRdp (xfreerdp externo + Gtk.Socket, so em X11). Os dois
#  sairam: ter caminhos que podiam se comportar diferente entre si
#  (aceitar certificado calado vs perguntar, por exemplo) era risco, nao
#  seguranca. Sem o rdpshim compilado, RDP fica indisponivel — sem plano B.

set -euo pipefail

if [ -z "${BASH_VERSION:-}" ]; then
    echo "Rode com bash:  bash $0 $*" >&2
    exit 1
fi

AQUI="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIXO="$HOME/.local"
DESTINO="$PREFIXO/lib/acessos"

SEM_RDP=0

# lib64 e real no Fedora; no Arch e um link para lib. Testar so a existencia
# poria os arquivos num caminho que o lancador nao procura.
if [ -d /usr/lib64 ] && [ ! -L /usr/lib64 ]; then
    LIBDIR="$PREFIXO/lib64"
else
    LIBDIR="$PREFIXO/lib"
fi

azul() { printf '\033[1;34m%s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m%s\033[0m\n' "$*"; }
erro() { printf '\033[1;31mERRO: %s\033[0m\n' "$*" >&2; }
nota() { printf '       %s\n' "$*"; }

# --------------------------------------------------------------- distro
detectar() {
    if command -v pacman >/dev/null; then
        DISTRO=arch
        INSTALAR="sudo pacman -S --needed --noconfirm"
        PACOTES="base-devel pkgconf
                 gtk3 python-gobject python-cairo
                 libvncserver freerdp
                 python-paramiko python-cryptography python-argon2-cffi
                 vte3 openssh"
    elif command -v dnf >/dev/null; then
        DISTRO=fedora
        INSTALAR="sudo dnf install -y"
        PACOTES="gcc pkgconf-pkg-config
                 gtk3-devel python3-gobject python3-cairo
                 libvncserver-devel freerdp-devel
                 python3-paramiko python3-cryptography python3-argon2-cffi
                 vte291 openssh-clients"
        # freerdp-devel cobre o rdpshim inteiro: freerdp3, freerdp-client3
        # e winpr3 (pkg-config). Nao ha mais dependencia de meson, vala,
        # gobject-introspection ou fuse3 — essas eram so do gtk-frdp.
    else
        erro "distro não reconhecida (esperado pacman ou dnf)."
        exit 1
    fi
}

# ------------------------------------------------------------ verificar
verificar() {
    local falhas=0
    echo
    azul "Verificação"

    if [ -f "$DESTINO/libvncshim.so" ]; then
        ok "  vncshim ....... instalado"
    else
        erro "  vncshim não encontrado em $DESTINO"
        falhas=1
    fi

    if [ -f "$DESTINO/librdpshim.so" ]; then
        ok "  rdpshim ....... instalado (RDP embutido)"
    else
        nota "  rdpshim ....... ausente (RDP fica indisponível, sem fallback)"
    fi

    PYTHONPATH="$DESTINO" \
    LD_LIBRARY_PATH="$LIBDIR:$DESTINO" \
    python3 - <<'PY' || falhas=1
import sys
falhou = []
try:
    import gi
    gi.require_version("Gtk", "3.0")
    from gi.repository import Gtk
except Exception as e:
    falhou.append("GTK3: %s" % e)
for mod in ("paramiko", "cryptography"):
    try:
        __import__(mod)
    except Exception as e:
        falhou.append("%s: %s" % (mod, e))
try:
    import vncwidget
    print("  VNC próprio ... ok")
except Exception as e:
    falhou.append("vncwidget: %s" % e)
try:
    import rdp
    print("  RDP embutido .. %s" % ("ok" if rdp.TEM_FRDP else "indisponível"))
except Exception as e:
    falhou.append("rdp: %s" % e)
try:
    import cofre
    print("  Cofre ......... %s" % ("ok" if cofre.TEM_CRIPTO else "sem cripto"))
except Exception as e:
    falhou.append("cofre: %s" % e)
try:
    import massa
    import massa_ui
    print("  Execução em lote %s"
          % ("ok" if massa_ui.TEM_MASSA else "sem motor"))
except Exception as e:
    falhou.append("massa: %s" % e)
try:
    import ssh
    print("  Terminal SSH .. %s" % ("ok" if ssh.TEM_VTE else "sem VTE"))
except Exception as e:
    falhou.append("ssh: %s" % e)
if falhou:
    print("\n  problemas:")
    for f in falhou:
        print("   - %s" % f)
    sys.exit(1)
PY
    return $falhas
}

# -------------------------------------------------------------- remover
remover() {
    azul "Removendo"
    rm -rf "$DESTINO"
    rm -f "$PREFIXO/bin/acessos"
    rm -f "$PREFIXO/share/applications/acessos.desktop"
    rm -f "$PREFIXO/share/icons/hicolor/scalable/apps/acessos.svg"
    # limpeza de instalacoes antigas, de quando o RDP embutido ainda
    # dependia do gtk-frdp (compilado a parte, via meson/git)
    find "$LIBDIR" -name 'libgtk-frdp*' -delete 2>/dev/null || true
    find "$LIBDIR/girepository-1.0" -name 'GtkFrdp-*' -delete 2>/dev/null || true
    ok "removido. A configuração em ~/.config/acessos foi preservada."
    exit 0
}

# ------------------------------------------------------------ argumentos
case "${1:-}" in
    --remover)   detectar; remover ;;
    --verificar) detectar; verificar && ok "tudo certo" || exit 1; exit 0 ;;
    --sem-rdp)   SEM_RDP=1 ;;
    "")          ;;
    *)           sed -n '3,18p' "$0" | sed 's/^#\s\?//'; exit 0 ;;
esac

detectar
azul "Acessos — instalação ($DISTRO)"
echo

# ------------------------------------------------------ 1. dependencias
azul "[1/5] dependências do sistema"
# shellcheck disable=SC2086
$INSTALAR $PACOTES

# Python: no Arch e Fedora os pacotes acima cobrem tudo. Se algum faltar
# (distro mais antiga), completamos com pip --user em vez de falhar.
azul "[2/5] conferindo pacotes Python"
faltantes=""
for mod in paramiko cryptography argon2; do
    python3 -c "import $mod" 2>/dev/null || faltantes="$faltantes $mod"
done
if [ -n "$faltantes" ]; then
    nota "instalando via pip:$faltantes"
    # argon2-cffi e o nome no PyPI; o modulo importado chama-se argon2
    pip3 install --user --quiet ${faltantes/argon2/argon2-cffi} || \
        nota "pip falhou; o cofre cai em PBKDF2 e segue funcionando"
else
    nota "todos presentes"
fi

# ---------------------------------------------------------- 3. vncshim
azul "[3/5] compilando o vncshim"
mkdir -p "$DESTINO"
gcc -shared -fPIC -O2 -Wall -o "$DESTINO/libvncshim.so" "$AQUI/src/vncshim.c" \
    $(pkg-config --cflags --libs libvncclient)
nota "$(basename "$DESTINO/libvncshim.so") — $(stat -c%s "$DESTINO/libvncshim.so") bytes"

# ---------------------------------------------------------- 4. rdpshim
#
# Motor RDP proprio: fala com a libfreerdp3 direto (via este shim em C), a
# mesma solucao ja adotada para o VNC. Certificado (pergunta sempre, nunca
# aceita calado), pipeline grafico (RDPGFX), clipboard de texto (CLIPRDR) e
# redimensionamento dinamico (Display Control) — tudo neste shim, sem
# GObject Introspection e sem segundo motor de reserva. Ver o topo de
# src/rdpshim.c e python/rdpwidget.py para o detalhe de cada canal.
#
# rdp.py usa este motor sempre que ele compilar. --sem-rdp pula a
# compilacao — SEM RDP nenhum, nao ha mais xfreerdp externo como plano B.
if [ "$SEM_RDP" = "1" ]; then
    azul "[4/5] rdpshim — pulado (--sem-rdp)"
    nota "RDP ficará indisponível (nenhum fallback externo)"
else
    azul "[4/5] compilando o rdpshim"
    if pkg-config --exists freerdp3 freerdp-client3 winpr3 2>/dev/null; then
        gcc -shared -fPIC -O2 -Wall -pthread -o "$DESTINO/librdpshim.so" \
            "$AQUI/src/rdpshim.c" \
            $(pkg-config --cflags --libs freerdp3 freerdp-client3 winpr3)
        nota "$(basename "$DESTINO/librdpshim.so") — $(stat -c%s "$DESTINO/librdpshim.so") bytes"
    else
        erro "freerdp3/winpr3 (pkg-config) não encontrados — RDP ficará"
        erro "indisponível (sem fallback externo)."
        nota "confira se freerdp-devel (Fedora) ou freerdp (Arch) instalou"
        nota "uma versao 3.x com pkg-config: pkg-config --modversion freerdp3"
    fi
fi

# ------------------------------------------------------- 5. aplicacao
azul "[5/5] instalando o Acessos"
# GLOB, nao lista de nomes. A lista explicita que morava aqui era um
# esquecimento esperando acontecer: bastava um modulo novo entrar no
# projeto e ninguem lembrar de acrescentar o nome, e a instalacao NATIVA
# saia sem ele — silenciosamente, porque o acessos.py importa os modulos
# dentro de try/except e degrada em vez de falhar. Foi o que aconteceu com
# o chaveiro.py: sem ele o app roda, mas SEM COFRE, com as senhas em claro
# e as ja cifradas inacessiveis. O build do Flatpak ja usava glob pelo
# mesmo motivo (ver build.sh); agora os dois caminhos concordam.
#
# "-D -t": o -D sozinho nao aceita varias origens com destino de
# diretorio (falha com "alvo inexistente"); com -t ele cria o diretorio e
# instala todos de uma vez. O acessos.py e reinstalado logo depois so
# para ganhar o bit de execucao.
install -Dm644 -t "$DESTINO/" "$AQUI"/python/*.py
install -m755 "$AQUI/python/acessos.py" "$DESTINO/acessos.py"

# metainfo.xml ao lado dos modulos: e dali que atualizador.versao_instalada()
# le a versao para mostrar no rodape e no dialogo Sobre. Fora do Flatpak nao
# ha /app/share/metainfo, entao o proprio modulo cai neste caminho como
# alternativa (ver _CAMINHOS_METAINFO em atualizador.py).
install -Dm644 "$AQUI/flatpak/org.jj.Acessos.metainfo.xml" \
    "$DESTINO/org.jj.Acessos.metainfo.xml"

install -Dm644 "$AQUI/icones/acessos.svg" \
    "$PREFIXO/share/icons/hicolor/scalable/apps/acessos.svg"

# LANCADOR. Junta tudo o que o app precisa enxergar:
#   PYTHONPATH      — os modulos ao lado do acessos.py
#   LD_LIBRARY_PATH — a libvncshim e a librdpshim
cat > "$PREFIXO/bin/acessos" <<EOF
#!/usr/bin/env bash
export PYTHONPATH="$DESTINO\${PYTHONPATH:+:\$PYTHONPATH}"
export LD_LIBRARY_PATH="$LIBDIR:$DESTINO\${LD_LIBRARY_PATH:+:\$LD_LIBRARY_PATH}"
exec python3 "$DESTINO/acessos.py" "\$@"
EOF
chmod +x "$PREFIXO/bin/acessos"

cat > "$PREFIXO/share/applications/acessos.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Acessos
Comment=Acesso remoto às máquinas (VNC, RDP, SSH, arquivos)
Exec=$PREFIXO/bin/acessos
Icon=acessos
Terminal=false
Categories=Network;RemoteAccess;System;
StartupNotify=true
EOF

update-desktop-database "$PREFIXO/share/applications" 2>/dev/null || true
gtk-update-icon-cache -f -t "$PREFIXO/share/icons/hicolor" 2>/dev/null || true

verificar || true

echo
ok "pronto."
echo
nota "Rodar:  acessos          (ou pelo menu de aplicativos)"
if ! echo "$PATH" | grep -q "$PREFIXO/bin"; then
    echo
    nota "ATENÇÃO: $PREFIXO/bin não está no seu PATH."
    nota "  bash/zsh:  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc"
    nota "  fish:      fish_add_path ~/.local/bin"
fi
echo
nota "O RDP embutido (rdpshim) funciona em qualquer backend, X11 ou"
nota "Wayland — não depende mais de XEmbed nem de processo externo."
