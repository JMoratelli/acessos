#!/usr/bin/env bash
#
#  instalar.sh — monta o Acessos por inteiro em Arch ou Fedora.
#
#  Faz tudo numa passada:
#     1. instala as dependencias do sistema
#     2. instala os pacotes Python (paramiko, cryptography, argon2)
#     3. compila o vncshim  (VNC proprio, sem o congelamento do gtk-vnc)
#     4. compila o gtk-frdp (RDP embutido na aba, funciona em Wayland)
#     5. instala em ~/.local e cria o lancador no menu
#
#  USO:
#     ./instalar.sh              instala ou atualiza
#     ./instalar.sh --sem-rdp    pula o gtk-frdp (usa o xfreerdp externo)
#     ./instalar.sh --verificar  so testa o que ja esta instalado
#     ./instalar.sh --remover    desinstala
#
#  Sem Flatpak, sem container, sem AppImage. Instala no home do usuario,
#  entao nao precisa de root exceto para os pacotes do sistema.

set -euo pipefail

if [ -z "${BASH_VERSION:-}" ]; then
    echo "Rode com bash:  bash $0 $*" >&2
    exit 1
fi

AQUI="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIXO="$HOME/.local"
DESTINO="$PREFIXO/lib/acessos"
FONTE_FRDP="$HOME/.cache/acessos/gtk-frdp"
REPO_FRDP="https://gitlab.gnome.org/GNOME/gtk-frdp.git"

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
        PACOTES="base-devel git meson ninja pkgconf
                 gtk3 python-gobject python-cairo
                 libvncserver freerdp fuse3
                 gobject-introspection vala
                 python-paramiko python-cryptography python-argon2-cffi
                 vte3 openssh"
    elif command -v dnf >/dev/null; then
        DISTRO=fedora
        INSTALAR="sudo dnf install -y"
        PACOTES="gcc git meson ninja-build pkgconf-pkg-config
                 gtk3-devel python3-gobject python3-cairo
                 libvncserver-devel freerdp-devel fuse3-devel
                 gobject-introspection-devel vala
                 python3-paramiko python3-cryptography python3-argon2-cffi
                 vte291 openssh-clients"
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

    local tl
    tl=$(find "$LIBDIR/girepository-1.0" -name 'GtkFrdp-*.typelib' 2>/dev/null \
         | head -1) || true
    if [ -n "$tl" ]; then
        ok "  gtk-frdp ...... instalado"
    else
        nota "  gtk-frdp ...... ausente (RDP abrirá em janela separada)"
    fi

    PYTHONPATH="$DESTINO" \
    GI_TYPELIB_PATH="$LIBDIR/girepository-1.0" \
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

# ---------------------------------------------------------- 4. gtk-frdp
if [ "$SEM_RDP" = "1" ]; then
    azul "[4/5] gtk-frdp — pulado (--sem-rdp)"
    nota "o RDP usará o xfreerdp externo, em janela separada"
else
    azul "[4/5] compilando o gtk-frdp"
    mkdir -p "$(dirname "$FONTE_FRDP")"
    if [ -d "$FONTE_FRDP/.git" ]; then
        git -C "$FONTE_FRDP" pull --ff-only --quiet 2>/dev/null || true
    else
        git clone --depth 1 --quiet "$REPO_FRDP" "$FONTE_FRDP"
    fi

    # CORRECAO DO CURSOR.
    #
    # O gtk-frdp so garante priv->scale = 1.0 DEPOIS de ja ter multiplicado
    # esse valor em x, y, w e h. Quando a sessao nao usa escala — nosso caso,
    # porque ajustamos o tamanho por allow-resize — o scale continua ZERO: a
    # superficie do cursor nasce com largura zero e o GDK aborta com
    #     gdk_cursor_new_from_surface: assertion
    #     '0 <= x && x < cairo_image_surface_get_width (surface)' failed
    # derrubando a aplicacao assim que o servidor manda a forma do cursor.
    #
    # Aplicado por script, e nao por .patch, para nao quebrar quando o
    # contexto ao redor mudar no upstream.
    python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYFIX'
import sys
caminho = sys.argv[1]
src = open(caminho, encoding="utf-8").read()
guard = """    if (!self->priv->scaling) {
      self->priv->scale = 1.0;
    }
"""
alvo = "    double x = priv->cursor->pointer.xPos * priv->scale;"
if "JA_CORRIGIDO_ACESSOS" in src:
    print("       correção do cursor: já aplicada")
elif alvo not in src:
    print("       AVISO: trecho do cursor não encontrado (upstream mudou);")
    print("       verifique se o bug ainda existe em frdp-session.c")
else:
    i = src.index(alvo)
    depois = src[i:].replace(guard, "", 1)
    marca = ("    /* JA_CORRIGIDO_ACESSOS: scale antes do uso, senão o\n"
             "     * cursor nasce com largura zero e o GDK aborta. */\n")
    open(caminho, "w", encoding="utf-8").write(src[:i] + marca + guard + depois)
    print("       correção do cursor aplicada")
PYFIX

    # CORRECAO DO FUSE DO CLIPBOARD.
    #
    # frdp-channel-clipboard.c monta um FUSE para o clipboard de arquivos e
    # cria o diretorio com
    #     g_mkdtemp (g_strdup_printf ("%s/clipboard-XXXXXX/", ...))
    #
    # O g_mkdtemp EXIGE que o template termine em exatamente seis X. Aqui
    # termina em "XXXXXX/" — ha uma barra depois. A funcao rejeita, devolve
    # NULL, e fuse_directory fica nulo. O fuse_session_mount(session, NULL)
    # seguinte falha com
    #     fuse: reading device: Descritor de arquivo invalido
    # e a thread do FUSE fica num estado ruim, travando a abertura da
    # PROXIMA sessao — sintoma: so da para abrir um widget.
    #
    # Tirar a barra corrige tambem a comparacao da linha ~1093, que confronta
    # fuse_directory com o resultado de g_path_get_dirname — este nunca traz
    # barra final, entao a igualdade jamais era verdadeira.
    python3 - "$FONTE_FRDP/src/frdp-channel-clipboard.c" <<'PYFUSE'
import sys
caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li o arquivo do clipboard (%s)" % e)
    sys.exit(0)

ruim = '"%s/clipboard-XXXXXX/"'
bom = '"%s/clipboard-XXXXXX"'
if ruim not in src:
    if bom in src:
        print("       correção do FUSE: já aplicada")
    else:
        print("       AVISO: template do g_mkdtemp não encontrado;")
        print("       verifique se o bug do clipboard ainda existe")
    sys.exit(0)
src = src.replace(ruim, bom, 1)
open(caminho, "w", encoding="utf-8").write(src)
print("       correção do FUSE aplicada (g_mkdtemp sem barra final)")
PYFUSE

    # CORRECAO DO update() COM ABA EM SEGUNDO PLANO.
    #
    # frdp-session.c comeca o update() com
    #     if (priv->display != NULL && !gtk_widget_get_mapped (priv->display))
    #       return TRUE;
    #
    # ou seja: se o widget nao esta mapeado, ele NAO processa nada — nem o
    # WaitForMultipleObjects que drena os eventos do FreeRDP — mas continua
    # agendado (return TRUE). A sessao fica girando em vazio.
    #
    # Com UMA sessao isso passa despercebido. Com DUAS, a que esta em aba de
    # fundo nunca drena seus eventos, e a thread principal trava esperando
    # handles que ninguem processa:
    #     gtk_main -> update (frdp-session.c) -> WaitForMultipleObjectsEx
    #     -> poll
    # A interface continua clicavel e para de atualizar. Sintoma: so da para
    # manter um widget.
    #
    # Pular o DESENHO de uma aba invisivel esta certo; pular o processamento
    # de eventos, nao. A correcao troca o return antecipado por um desvio que
    # so evita a fila de redesenho.
    python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYUPD'
import sys
caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

alvo = """  if (priv->display != NULL && !gtk_widget_get_mapped (priv->display))
    return TRUE;
"""
novo = """  /* JA_CORRIGIDO_ACESSOS_UPDATE
   * Antes havia aqui um "return TRUE" quando o widget não estava mapeado.
   * Isso pulava TAMBÉM o WaitForMultipleObjects abaixo, então uma sessão em
   * aba de fundo nunca drenava seus eventos e travava as demais. Agora só
   * o redesenho é pulado; o processamento de eventos segue sempre. */
  gboolean frdp_visivel = (priv->display == NULL ||
                           gtk_widget_get_mapped (priv->display));
"""
if "JA_CORRIGIDO_ACESSOS_UPDATE" in src:
    print("       correção do update: já aplicada")
    sys.exit(0)
if alvo not in src:
    print("       AVISO: trecho do update() não encontrado (upstream mudou);")
    print("       verifique se a sessão em segundo plano ainda trava")
    sys.exit(0)

src = src.replace(alvo, novo, 1)

# a fila de redesenho passa a depender da visibilidade
# A fila continua sendo ESVAZIADA mesmo invisivel — so nao pedimos
# redesenho. Deixar acumular faria a aba de fundo crescer sem limite, e ao
# voltar para ela o GTK teria milhares de retangulos para processar de uma
# vez. Ao reentrar na aba, o acessos.py forca um redesenho completo.
fila = """      rectangle = g_queue_pop_head (priv->area_draw_queue);
      gtk_widget_queue_draw_area (priv->display, rectangle->x, rectangle->y, rectangle->width, rectangle->height);
      g_free (rectangle);"""
fila_nova = """      rectangle = g_queue_pop_head (priv->area_draw_queue);
      if (frdp_visivel)
        gtk_widget_queue_draw_area (priv->display, rectangle->x, rectangle->y, rectangle->width, rectangle->height);
      g_free (rectangle);"""
if fila in src:
    src = src.replace(fila, fila_nova, 1)
else:
    print("       AVISO: fila de redesenho não encontrada; revise o patch")

open(caminho, "w", encoding="utf-8").write(src)
print("       correção do update aplicada (eventos drenam em segundo plano)")
PYUPD

    # CORRECAO DO SELECT_TIMEOUT.
    #
    # O update() roda NA THREAD PRINCIPAL e chama
    #     WaitForMultipleObjects (..., SELECT_TIMEOUT)
    # com SELECT_TIMEOUT = 20 ms. Ou seja: cada sessao segura o laco de
    # eventos por ate 20 ms a cada volta.
    #
    # Com UMA sessao ainda sobra folga. Com DUAS sao 40 ms por ciclo dentro
    # do poll, e o relogio de quadros do GTK — que precisa rodar a cada
    # ~16 ms — nao consegue mais. O resultado e a interface RESPONSIVA porem
    # PARADA: os cliques sao entregues e enfileirados, o log responde, e nada
    # e repintado, porque nao sobra tempo de laco para desenhar.
    #
    # Com 0 o WaitForMultipleObjects apenas CONSULTA os handles e volta na
    # hora; o GLib reagenda a funcao normalmente. Nao se perde evento algum:
    # o freerdp_check_event_handles continua sendo chamado na mesma
    # frequencia, so que sem bloquear ninguem.
    python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYSEL'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

# Versao da correcao. Instalacoes feitas com a v1 ficaram com
# SELECT_TIMEOUT 0 e g_idle_add — combinacao que vira laço ocupado. Elas
# precisam ser refeitas, e por isso a marca carrega a versao.
if "JA_CORRIGIDO_ACESSOS_SELECT_V2" in src:
    print("       correção do SELECT_TIMEOUT: já aplicada")
    sys.exit(0)
if "JA_CORRIGIDO_ACESSOS_SELECT" in src:
    # v1 deixou SELECT_TIMEOUT 0 com g_idle_add — laço ocupado. Desfazemos
    # a v1 no proprio texto e seguimos para aplicar a v2 logo abaixo.
    src = re.sub(r"/\* JA_CORRIGIDO_ACESSOS_SELECT:.*?\*/\n", "", src,
                 count=1, flags=re.S)
    src = re.sub(r"#define\s+SELECT_TIMEOUT\s+\d+",
                 "#define SELECT_TIMEOUT 20", src, count=1)
    print("       correção do SELECT_TIMEOUT: versão antiga desfeita")

achou = re.search(r"#define\s+SELECT_TIMEOUT\s+(\d+)", src)
if not achou:
    print("       AVISO: SELECT_TIMEOUT não encontrado (upstream mudou)")
    sys.exit(0)

valor = achou.group(1)

# Se o update() for reagendado por g_idle_add, timeout 0 viraria laço
# ocupado — o idle rodaria sem parar, queimando CPU. Nesse caso 1 ms
# preserva o ganho (o laço volta a respirar) sem girar em vazio.
# O padrao precisa aceitar o prefixo: no fonte a linha e
#     self->priv->update_id = g_idle_add ((GSourceFunc) update, self);
# Um regex ancorado em "update_id\s*=" NAO casa por causa do "self->priv->",
# e a deteccao escolhia 0 — justamente o valor que vira laço ocupado com
# g_idle_add. O sintoma foi feio: o update() monopolizava o despacho e
# outras sources do GLib (o aviso de término do ssh, por exemplo) só rodavam
# quando a aba RDP era fechada.
usa_idle = re.search(r"update_id\s*=\s*g_idle_add\b", src) is not None

# Com g_idle_add nao basta 1 ms: o idle roda sempre que o laço fica ocioso,
# entao mesmo um bloqueio curto se repete milhares de vezes por segundo. O
# certo e trocar o agendamento por g_timeout_add, dando um intervalo real
# entre as verificacoes. Fazemos as duas coisas abaixo.
alvo_ms = "1" if usa_idle else "0"

comentario = (
    "/* JA_CORRIGIDO_ACESSOS_SELECT_V2: era %s ms.\n"
    " * update() roda na thread principal e bloqueia aqui a cada volta.\n"
    " * Com duas sessões são %s ms por ciclo dentro do poll, e o relógio de\n"
    " * quadros do GTK (~16 ms) não roda mais: a interface fica responsiva\n"
    " * porém sem repintar nada. Consultar os handles sem bloquear devolve\n"
    " * o controle ao laço, sem perder eventos. */\n"
    "#define SELECT_TIMEOUT %s" % (valor, int(valor) * 2, alvo_ms))

src = src.replace(achou.group(0), comentario, 1)

# TROCAR g_idle_add POR g_timeout_add no reagendamento do update().
#
# Com g_idle_add o update() volta a rodar assim que o laço fica ocioso — ou
# seja, continuamente. Ele monopoliza o despacho e as demais sources do GLib
# ficam esperando: o aviso de término de um processo filho (o ssh de outra
# aba, por exemplo) só era entregue quando a sessão RDP fechava.
#
# Com um timeout curto o comportamento é o mesmo do ponto de vista da
# sessão (5 ms entre verificações é imperceptível), e o laço volta a
# atender todo mundo.
alvo_idle = re.search(
    r"(update_id\s*=\s*)g_idle_add \(\(GSourceFunc\) update, self\)", src)
if alvo_idle:
    src = src.replace(
        alvo_idle.group(0),
        alvo_idle.group(1) +
        "g_timeout_add (5, (GSourceFunc) update, self)", 1)
    print("       reagendamento do update: g_idle_add -> g_timeout_add(5 ms)")
elif "g_timeout_add (5, (GSourceFunc) update" in src:
    print("       reagendamento do update: já ajustado")
else:
    print("       AVISO: reagendamento do update() não encontrado;")
    print("       se a interface engasgar com RDP aberto, revise isto")

open(caminho, "w", encoding="utf-8").write(src)
print("       correção do SELECT_TIMEOUT aplicada (%s ms -> %s)"
      % (valor, alvo_ms))
PYSEL

    # CORRECAO DO CERTIFICADO.
    #
    # O gtk-frdp emite rdp-needs-certificate-verification, mas o FreeRDP JA
    # RECUSOU antes de o sinal chegar — o log mostra a ordem:
    #     x509_utils_from_pem: BIO_new failed for certificate
    #     Host key verification failed.
    #     rdp: certificado novo, aceitando        <- tarde demais
    # Responder ao sinal nao adianta, e o certificado nunca e gravado. Por
    # isso o erro se repete para sempre, e maquina nova nunca conecta.
    #
    # Agrava o caso de conectar por IP: o certificado do servidor e emitido
    # para um NOME (redemachado.local), entao ha name mismatch e o FreeRDP
    # trata como identidade invalida.
    #
    # A biblioteca nao expoe nenhuma opcao para afrouxar isso. Ligamos as
    # flags do proprio FreeRDP — o equivalente ao /cert:ignore que a linha de
    # comando do xfreerdp usa neste mesmo projeto. Nao e uma politica nova:
    # e a mesma ja adotada, agora tambem no caminho embutido.
    python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYCERT'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

if "JA_CORRIGIDO_ACESSOS_CERT" in src:
    print("       correção do certificado: já aplicada")
    sys.exit(0)

# ancora: uma linha de configuracao que sabemos existir nesta versao
ancora = re.search(
    r"[ \t]*freerdp_settings_set_bool \(settings, FreeRDP_SupportDisplayControl, TRUE\);",
    src)
if not ancora:
    print("       AVISO: ponto de configuração não encontrado (upstream mudou);")
    print("       o certificado continuará sendo verificado com rigor")
    sys.exit(0)

linha = ancora.group(0)
ident = linha[:len(linha) - len(linha.lstrip())]
extra = (
    linha + "\n"
    + ident + "/* JA_CORRIGIDO_ACESSOS_CERT\n"
    + ident + " * Parque interno com certificados autoassinados, e conexões\n"
    + ident + " * feitas por IP contra certificados emitidos para nome — o\n"
    + ident + " * FreeRDP recusaria antes mesmo de emitir o sinal de\n"
    + ident + " * verificação, e o certificado nunca seria gravado.\n"
    + ident + " * Equivale ao /cert:ignore já usado no xfreerdp aqui. */\n"
    + ident + "freerdp_settings_set_bool (settings, FreeRDP_IgnoreCertificate, TRUE);\n"
    # Só IgnoreCertificate: AutoAcceptCertificate não existe em toda
    # versão do FreeRDP 3, e como FreeRDP_* são valores de ENUM (não
    # macros) um #ifdef não protegeria — a compilação quebraria do
    # mesmo jeito. IgnoreCertificate sozinho já desliga a verificação.
)
open(caminho, "w", encoding="utf-8").write(src.replace(linha, extra, 1))
print("       correção do certificado aplicada (verificação relaxada)")
PYCERT

    (
      cd "$FONTE_FRDP"
      meson setup build --prefix="$PREFIXO" --libdir="$(basename "$LIBDIR")" \
            -Dexamples=false --wipe >/dev/null 2>&1 ||
      meson setup build --prefix="$PREFIXO" --libdir="$(basename "$LIBDIR")" \
            -Dexamples=false >/dev/null
      ninja -C build >/dev/null
      ninja -C build install >/dev/null
    )
    nota "instalado em $LIBDIR"
fi

# ------------------------------------------------------- 5. aplicacao
azul "[5/5] instalando o Acessos"
install -Dm755 "$AQUI/python/acessos.py"   "$DESTINO/acessos.py"
for m in vncwidget sftp cofre rdp massa massa_ui ssh tema dialogo_ui atualizador; do
    install -Dm644 "$AQUI/python/$m.py" "$DESTINO/$m.py"
done

install -Dm644 "$AQUI/icones/acessos.svg" \
    "$PREFIXO/share/icons/hicolor/scalable/apps/acessos.svg"

# LANCADOR. Junta tudo o que o app precisa enxergar:
#   PYTHONPATH      — os modulos ao lado do acessos.py
#   GI_TYPELIB_PATH — o typelib do gtk-frdp
#   LD_LIBRARY_PATH — a libgtk-frdp e a libvncshim
cat > "$PREFIXO/bin/acessos" <<EOF
#!/usr/bin/env bash
export PYTHONPATH="$DESTINO\${PYTHONPATH:+:\$PYTHONPATH}"
export GI_TYPELIB_PATH="$LIBDIR/girepository-1.0\${GI_TYPELIB_PATH:+:\$GI_TYPELIB_PATH}"
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
nota "O RDP embutido só entra em Wayland nativo. Sob X11 o Acessos usa o"
nota "xfreerdp, que já embute na aba por outro caminho."
