#!/usr/bin/env bash
# aplicar-patches-frdp.sh — aplica no fonte do gtk-frdp as mesmas correcoes
# que o instalar.sh aplica na instalacao nativa.
#
# Uso:  ./aplicar-patches-frdp.sh <diretorio-do-fonte-gtk-frdp>
#
# Existe para que o Flatpak nao compile gtk-frdp "puro" enquanto a
# instalacao nativa compila corrigido — divergencia que ja custou horas.
#
# ATENCAO A DUPLICACAO: extraido do instalar.sh. Ajustou um patch la,
# ajuste aqui — ou faca o instalar.sh chamar este script, e fica um lugar so.
set -euo pipefail

FONTE_FRDP="${1:?uso: $0 <diretorio-do-fonte-gtk-frdp>}"
[ -d "$FONTE_FRDP/src" ] || {
    echo "ERRO: $FONTE_FRDP nao parece ser o fonte do gtk-frdp." >&2; exit 1; }

# CORRECAO DO CURSOR.
#
# O gtk-frdp so garante priv->scale = 1.0 DEPOIS de ja ter multiplicado
# esse valor em x, y, w e h. Quando a sessao nao usa escala — nosso caso,
# porque ajustamos o tamanho por allow-resize — o scale continua ZERO: a
# superficie do cursor nasce com largura zero e o GDK aborta com
#     gdk_cursor_new_from_surface: assertion
#     '0 <= x && x < cairo_image_surface_get_width (surface)' failed
# derrubando a aplicacao assim que o servidor manda a forma do cursor.
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
# frdp-channel-clipboard.c cria o diretorio com
#     g_mkdtemp (g_strdup_printf ("%s/clipboard-XXXXXX/", ...))
# O g_mkdtemp EXIGE que o template termine em exatamente seis X. Aqui
# termina em "XXXXXX/" — ha uma barra depois. A funcao rejeita, devolve
# NULL, fuse_directory fica nulo, e o fuse_session_mount(session, NULL)
# falha com "fuse: reading device: Descritor de arquivo invalido". A
# thread do FUSE fica num estado ruim e trava a abertura da PROXIMA
# sessao — sintoma: so da para abrir um widget.
#
# Tirar a barra corrige tambem a comparacao com g_path_get_dirname, que
# nunca traz barra final — a igualdade jamais era verdadeira.
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
# ou seja: widget nao mapeado nao processa NADA — nem o
# WaitForMultipleObjects que drena os eventos do FreeRDP — mas continua
# agendado. Com DUAS sessoes, a que esta em aba de fundo nunca drena seus
# eventos e a thread principal trava esperando handles que ninguem
# processa. Sintoma: so da para manter um widget.
#
# Pular o DESENHO de uma aba invisivel esta certo; pular o processamento
# de eventos, nao.
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

# A fila continua sendo ESVAZIADA mesmo invisivel — so nao pedimos
# redesenho. Deixar acumular faria a aba de fundo crescer sem limite. Ao
# reentrar na aba, o acessos.py forca um redesenho completo.
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

# CORRECAO DO SELECT_TIMEOUT (v2).
#
# O update() roda NA THREAD PRINCIPAL e chama
#     WaitForMultipleObjects (..., SELECT_TIMEOUT)
# com SELECT_TIMEOUT = 20 ms — cada sessao segura o laco de eventos por ate
# 20 ms a cada volta. Com DUAS sao 40 ms por ciclo e o relogio de quadros do
# GTK (~16 ms) nao roda mais: interface RESPONSIVA porem PARADA.
#
# A v1 desta correcao baixava o timeout mas deixava o reagendamento em
# g_idle_add — combinacao que vira laço ocupado. A v2 faz as duas coisas:
# baixa o timeout E troca g_idle_add por g_timeout_add(5 ms).
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYSEL'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

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

# O padrao precisa aceitar o prefixo: no fonte a linha e
#     self->priv->update_id = g_idle_add ((GSourceFunc) update, self);
# Um regex ancorado em "update_id\s*=" sem \b escolhia 0 — justamente o
# valor que vira laço ocupado com g_idle_add.
usa_idle = re.search(r"update_id\s*=\s*g_idle_add\b", src) is not None
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
# seja, continuamente. Ele monopoliza o despacho e as demais sources do
# GLib ficam esperando: o aviso de término do ssh de outra aba só era
# entregue quando a sessão RDP fechava.
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
# Responder ao sinal nao adianta, e o certificado nunca e gravado: o erro
# se repete para sempre e maquina nova nunca conecta.
#
# Agrava o caso de conectar por IP: o certificado e emitido para um NOME
# (redemachado.local), entao ha name mismatch.
#
# A biblioteca nao expoe opcao para afrouxar isso. Ligamos a flag do
# proprio FreeRDP — o equivalente ao /cert:ignore ja adotado neste projeto.
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
    # Só IgnoreCertificate: AutoAcceptCertificate não existe em toda versão
    # do FreeRDP 3, e como FreeRDP_* são valores de ENUM (não macros) um
    # #ifdef não protegeria — a compilação quebraria do mesmo jeito.
)
open(caminho, "w", encoding="utf-8").write(src.replace(linha, extra, 1))
print("       correção do certificado aplicada (verificação relaxada)")
PYCERT

# CORRECAO DO LAYOUT DE TECLADO.
#
# O gtk-frdp nunca define FreeRDP_KeyboardLayout: ele confia na
# autodeteccao do FreeRDP, que so existe pelo caminho X11 (XOpenDisplay em
# libfreerdp/locale/keyboard_x11.c). Dentro do Flatpak o DISPLAY chega
# VAZIO — verificado: `echo $DISPLAY` no sandbox nao devolve nada, porque
# a interface e Wayland e o fallback-x11 nao exporta a variavel. Sem
# DISPLAY a deteccao nem tenta, o FreeRDP assume o layout US e o teclado
# ABNT2 escreve qualquer coisa menos o certo.
#
# O xfreerdp externo acerta porque ELE e um cliente X11 e a deteccao roda
# no processo dele. No caminho embutido nao ha esse luxo, entao paramos de
# adivinhar e dizemos o layout.
#
# O valor vem de ACESSOS_KBD_LAYOUT (hex), com 0x416 = pt-BR como padrao —
# assim quem tiver maquina com outro layout muda pela variavel, sem
# recompilar. KeyboardType 7 / SubType 2 e o ABNT2.
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYKBD'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

if "JA_CORRIGIDO_ACESSOS_KBD" in src:
    print("       correção do teclado: já aplicada")
    sys.exit(0)

ancora = re.search(
    r"[ \t]*freerdp_settings_set_bool \(settings, FreeRDP_SupportDisplayControl, TRUE\);",
    src)
if not ancora:
    print("       AVISO: ponto de configuração não encontrado (upstream mudou);")
    print("       o teclado continuará dependendo da autodetecção (US)")
    sys.exit(0)

linha = ancora.group(0)
ident = linha[:len(linha) - len(linha.lstrip())]
i = ident
extra = (
    linha + "\n"
    + i + "/* JA_CORRIGIDO_ACESSOS_KBD\n"
    + i + " * Sem DISPLAY dentro do Flatpak a autodetecção de layout do\n"
    + i + " * FreeRDP não roda e ele assume US — o ABNT2 escrevia\n"
    + i + " * caracteres aleatórios. Fixamos o layout, com escape por\n"
    + i + " * ACESSOS_KBD_LAYOUT para quem usa outro. Equivale ao\n"
    + i + " * /kbd:layout: do xfreerdp. */\n"
    + i + "{\n"
    + i + "  const char *acessos_kbd = g_getenv (\"ACESSOS_KBD_LAYOUT\");\n"
    + i + "  guint32 acessos_layout = 0x00000416;  /* pt-BR ABNT2 */\n"
    + i + "  if (acessos_kbd != NULL && *acessos_kbd != '\\0')\n"
    + i + "    acessos_layout = (guint32) g_ascii_strtoull (acessos_kbd, NULL, 0);\n"
    + i + "  freerdp_settings_set_uint32 (settings, FreeRDP_KeyboardLayout, acessos_layout);\n"
    + i + "  freerdp_settings_set_uint32 (settings, FreeRDP_KeyboardType, 7);\n"
    + i + "  freerdp_settings_set_uint32 (settings, FreeRDP_KeyboardSubType, 2);\n"
    + i + "}\n"
)
open(caminho, "w", encoding="utf-8").write(src.replace(linha, extra, 1))
print("       correção do teclado aplicada (layout fixo, padrão pt-BR)")
PYKBD

# CORRECAO DO SCANCODE (deslocamento de 8).
#
# SINTOMA MEDIDO: digitando "q w e r t" o servidor Windows recebia
# "o p ´ [ Enter"; digitando "1 2 3 4 5" recebia "9 0 - = Backspace". Em
# ambos, a tecla da posicao N chegava como a da posicao N+8 — deslocamento
# constante, nao aleatorio.
#
# CAUSA: keycode_x11 = keycode_evdev + 8. O GDK entrega
# event->hardware_keycode no padrao X11 (mesmo em Wayland). O gtk-frdp
# repassa esse numero ao FreeRDP, que so o converte corretamente se
# freerdp_keyboard_init() tiver montado a tabela X11 — o que exige
# XOpenDisplay. Sem DISPLAY no sandbox a tabela nao existe, o FreeRDP trata
# o numero como se ja fosse evdev, e sobram os 8.
#
# Contra servidor Linux (xrdp) isso passa despercebido: ele reinterpreta
# pelo caractere. O Windows obedece o scancode literalmente.
#
# CORRECAO: subtrair o deslocamento antes da conversao. O valor sai de
# ACESSOS_KBD_OFFSET (padrao 8) para o caso de alguma combinacao nao
# precisar dele — ACESSOS_KBD_OFFSET=0 desliga sem recompilar.
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYSCAN'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

if "JA_CORRIGIDO_ACESSOS_SCAN" in src:
    print("       correção do scancode: já aplicada")
    sys.exit(0)

# A funcao auxiliar entra logo depois do ultimo #include, para estar
# declarada antes de qualquer uso.
inc = list(re.finditer(r"^#include[^\n]*\n", src, flags=re.M))
if not inc:
    print("       AVISO: nenhum #include em frdp-session.c; patch abortado")
    sys.exit(0)

helper = """
/* JA_CORRIGIDO_ACESSOS_SCAN
 * keycode_x11 = keycode_evdev + 8. O GDK entrega hardware_keycode no
 * padrão X11 mesmo sob Wayland; sem DISPLAY o FreeRDP não monta a tabela
 * de tradução e trata o número como evdev, deslocando TODAS as teclas em
 * 8 posições (medido: "qwert" chegava como "op´[Enter"). Descontamos aqui.
 * ACESSOS_KBD_OFFSET=0 desliga sem recompilar. */
static guint
acessos_keycode (guint hardware_keycode)
{
  static gint desloc = -1;

  if (desloc < 0)
    {
      const char *env = g_getenv ("ACESSOS_KBD_OFFSET");
      desloc = (env != NULL && *env != '\\0') ? (gint) g_ascii_strtoll (env, NULL, 10) : 8;
    }

  if ((gint) hardware_keycode > desloc)
    return hardware_keycode - desloc;
  return hardware_keycode;
}
"""

pos = inc[-1].end()
src = src[:pos] + helper + src[pos:]

# Ancoramos na CHAMADA de conversao, nao no nome da variavel — no fonte
# atual o keycode chega como key->hardware_keycode, mas o nome ja mudou
# antes e o patch anterior errou justamente por causa disso.
#
# So esta chamada e tocada. A outra, GetVirtualKeyCodeFromKeycode com
# WINPR_KEYCODE_TYPE_XKB, espera keycode X11 DE VERDADE: descontar 8 la
# quebraria o codigo virtual. O deslocamento so existe no caminho do
# scancode, que e o que o Windows obedece literalmente.
alvo = re.search(
    r"freerdp_keyboard_get_rdp_scancode_from_x11_keycode\s*\(\s*([^)]+?)\s*\)",
    src)
if not alvo:
    print("       AVISO: chamada de conversão de scancode não encontrada;")
    print("       o teclado continuará deslocado contra servidores Windows")
    sys.exit(0)

src = src.replace(
    alvo.group(0),
    "freerdp_keyboard_get_rdp_scancode_from_x11_keycode (acessos_keycode (%s))"
    % alvo.group(1), 1)

open(caminho, "w", encoding="utf-8").write(src)
print("       correção do scancode aplicada (deslocamento 8)")
PYSCAN

