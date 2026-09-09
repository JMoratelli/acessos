#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright (C) 2026 Jurandir Moratelli
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.
"""
Acessos — tela (VNC), shell (SSH) e RDP da mesma maquina, em abas,
mais execucao de comandos em lote no parque inteiro.

DEPENDENCIAS
    Fedora : sudo dnf install python3-gobject gtk3 gtk-vnc2 vte291 \
                 freerdp sshpass python3-paramiko
    Arch   : sudo pacman -S python-gobject gtk3 gtk-vnc vte3 \
                 freerdp sshpass python-paramiko
    Debian : sudo apt install python3-gi gir1.2-gtk-3.0 gir1.2-gtkvnc-2.0 \
                 gir1.2-vte-2.91 freerdp2-x11 sshpass python3-paramiko

    Nenhuma e obrigatoria para ABRIR o programa: o que faltar desabilita
    a funcao correspondente com aviso, em vez de impedir a execucao.
      gtk-vnc  -> abas de Tela        vte      -> abas de Shell
      freerdp  -> abas de RDP         paramiko -> execucao em LOTE

USO
    python3 acessos.py [--conf caminho/conexoes.ini] [--debug]
                       [--x11 | --wayland]

    --x11      forca XWayland: RDP embutido na aba, sem captura de teclado
    --wayland  forca Wayland nativo: captura de teclado, RDP em janela
    (o padrao vem de `x11` em [geral]; hoje 1)

ARQUIVOS   (em ~/.config/acessos/, ou onde [geral] caminho= apontar)
    conexoes.ini   uma secao por maquina, com tela + shell + rdp juntos.
                   Subgrupos com ";" no campo `grupo`.
    snippets.ini   biblioteca de comandos da execucao em lote.
    historico/     as 20 ultimas versoes do conexoes.ini, com index.txt
                   dizendo o que mudou em cada uma.

    ONDE FICAM: por padrao ~/.config/acessos/. Para mover tudo (util para
    deixar numa pasta sincronizada), ponha no INI padrao:

        [geral]
        caminho = /home/voce/Drive/acessos

    O arquivo do lugar padrao passa a funcionar como PONTEIRO: o app le
    essa chave e usa o conexoes.ini daquele diretorio, com snippets e
    historico junto. Um unico salto e seguido — dois arquivos apontando um
    para o outro nao entram em laco. `--conf` vence o caminho= sempre.

    A secao [cofre] guarda salt/kdf/verificador. SEM ELA nenhuma senha
    guardada abre, mesmo com a senha mestra certa: a chave e derivada da
    senha MAIS o salt. Uma gravacao que a removeria e abortada.

    O INI e reescrito LINHA A LINHA, preservando comentarios e ordem —
    nunca com configparser.write(), que comeria os dois. Sao gravados de
    volta: modo, ronly, auto, windows, painel, lateral, tema e x11.

MODULOS
    massa.py   motor da execucao em lote (protocolo SSH, canario,
               calibracao de timeout). Sem GTK de proposito: da para
               testar contra um sshd de descarte sem abrir janela.
               Se faltar, so o botao de lote fica desabilitado.
    verifica.py  conferidor estatico — rode antes de publicar mudanca:
               acha metodo inexistente, CSS invalido e os padroes de GTK
               que ja quebraram este projeto antes.

Desenvolvido por @JJMoratelli.
"""

import argparse
import configparser
import os
import re
import shutil
import socket
import subprocess
import sys
import threading
import time
import warnings

import gi

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
from gi.repository import Gtk, Gdk, GLib, Pango  # noqa: E402

# motor de execucao em lote (SSH). Fica em arquivo separado de proposito:
# nao depende de GTK, entao da para testar o protocolo inteiro contra um
# sshd de descarte sem abrir janela nenhuma.
try:
    import massa
    TEM_MASSA, ERRO_MASSA = True, ""
except ImportError as e:
    massa = None
    TEM_MASSA, ERRO_MASSA = False, str(e)

try:
    # GdkX11 e o unico typelib de backend que o GTK3 publica; e dele que sai
    # o get_xid() da janela, usado tanto pelo Gtk.Socket quanto pelo grab
    gi.require_version("GdkX11", "3.0")
    from gi.repository import GdkX11  # noqa: F401
except (ValueError, ImportError):
    GdkX11 = None

# VNC: apenas o widget proprio (libvncclient via vncshim.so).
#
# O gtk-vnc foi REMOVIDO, e nao apenas despriorizado. Ele congela de 2 a 3
# segundos apos repinturas grandes — abrir uma janela dentro do PDV, por
# exemplo — e durante o congelamento o processo fica com 0% de CPU, ou seja,
# esperando. O mesmo defeito aparece no GNOME Connections (que usa gtk-vnc) e
# NAO aparece no Remmina (que usa libvncclient), com o mesmo servidor, rede e
# maquina. Escala 1:1, lossy, smoothing, foco e cursor local foram testados;
# nenhum resolveu.
#
# Mante-lo como reserva so criava um caminho que ninguem quer usar e que
# reapareceria justamente quando algo desse errado. Sem o vncshim.so
# compilado, agora o VNC avisa em vez de degradar em silencio.
try:
    from vncwidget import VncWidget
    TEM_VNC, ERRO_VNC = True, ""
except Exception as e:
    VncWidget, TEM_VNC = None, False
    ERRO_VNC = ("widget VNC indisponível: %s\n\n"
                "Compile o vncshim com ./instalar.sh" % e)

# RDP embutido: widget GTK sobre o gtk-frdp. Ausente, cai no xfreerdp
# externo (classe AbaRdp), que continua sendo o caminho padrao do projeto.
try:
    from rdp import RdpWidget, TEM_FRDP, ERRO_FRDP
except Exception as e:
    RdpWidget, TEM_FRDP, ERRO_FRDP = None, False, str(e)

# Cofre de senhas: modulo separado. Ausente, o app roda normalmente com as
# senhas em claro (comportamento antigo) — nao trava por falta dele.
try:
    import cofre as _cofre
    TEM_COFRE, ERRO_COFRE = True, ""
except Exception as e:
    _cofre, TEM_COFRE, ERRO_COFRE = None, False, str(e)

# instancia unica da sessao: preenchida no arranque, usada ao ler e gravar
COFRE = None

# Transferencia de arquivos: modulo separado de proposito, para poder mexer
# nela sem tocar no resto. Ausente, o botao apenas avisa.
try:
    from sftp import AbaSftp
    TEM_SFTP, ERRO_SFTP = True, ""
except Exception as e:
    AbaSftp, TEM_SFTP, ERRO_SFTP = None, False, str(e)

# Vte e importado dentro do ssh.py, que e quem usa o terminal. TEM_VTE e
# ERRO_VTE chegam de la, junto com a classe AbaSsh.

def talvez_reexec_x11(forcar):
    """Roda o processo inteiro sob XWayland.

    O RDP embutido depende de Gtk.Socket, que exige um XID — algo que nao
    existe em Wayland nativo. Forcando GDK_BACKEND=x11 o app vira cliente
    XWayland, ganha XID, e o /parent-window: do xfreerdp volta a funcionar.

    Tem de ser feito ANTES de qualquer chamada que abra o display, por isso
    e um re-exec e nao um simples setenv.

    Preco: sob XWayland nao ha escala fracionaria por monitor. Em tela HiDPI
    com escala 125%% ou 150%% a janela sai borrada.
    """
    if not forcar:
        return
    if os.environ.get("GDK_BACKEND") == "x11":
        return                      # ja estamos sob XWayland
    if not os.environ.get("WAYLAND_DISPLAY"):
        return                      # ja e X11 de verdade, nada a fazer
    if not os.environ.get("DISPLAY"):
        sys.stderr.write("XWayland indisponivel (sem DISPLAY); "
                         "seguindo em Wayland nativo\n")
        return
    env = dict(os.environ)
    env["GDK_BACKEND"] = "x11"
    env["ACESSOS_X11"] = "1"
    try:
        os.execve(sys.executable, [sys.executable, os.path.abspath(__file__)]
                  + sys.argv[1:], env)
    except Exception as e:
        sys.stderr.write("falha ao reiniciar sob XWayland: %s\n" % e)


def _backend_x11():
    """Gtk.Socket so existe sob X11. Em Wayland puro nao ha XID para
    reparentar, e o RDP tem de sair em janela propria."""
    try:
        return type(Gdk.Display.get_default()).__name__.startswith("X11")
    except Exception:
        return False


# --------------------------------------------------------------- cemiterio
# Displays VNC aposentados esperam aqui antes de morrer.
#
# POR QUE ISTO EXISTE: a conexao VNC entrega atualizacoes de framebuffer por
# idles do main loop. Quando o widget e destruido na hora (troca de display
# na reconexao, ou fechamento da aba), os updates JA ENFILEIRADOS ainda vao
# rodar — e o handler interno do gtk-vnc (on_framebuffer_update ->
# get_render_region_info -> gdk_window_get_width) encontra a GdkWindow ja
# destruida e desreferencia NULL:
#     Gdk-CRITICAL: gdk_window_get_width: assertion 'GDK_IS_WINDOW (window)'
# Isso derruba o processo, e nao adianta desconectar nossos sinais: o
# handler e do proprio gtk-vnc, nao nosso.
#
# Segurar uma referencia nao basta — o widget precisa continuar REALIZADO
# (com GdkWindow valida). Por isso ele fica num Gtk.Offscreen, fora de
# vista, ate a fila drenar. Alguns segundos depois nao ha mais update
# possivel e a destruicao e segura.
_CEMITERIO = []
_CEMITERIO_SEGUNDOS = 3


def aposentar_display(d):
    """Tira o display de cena SEM destruir, e agenda a destruicao."""
    if d is None:
        return
    # O VncWidget proprio tem uma THREAD de rede. Destruir o widget sem
    # encerra-la deixaria a thread viva mexendo em memoria liberada. O
    # gtk-vnc nao precisa disto (usa corrotina no proprio main loop), por
    # isso a chamada e condicional.
    if hasattr(d, "desconectar"):
        try:
            d.desconectar()
        except Exception:
            pass
    try:
        pai = d.get_parent()
        if pai is not None:
            pai.remove(d)
    except Exception:
        pass
    # Janela offscreen: mantem o widget realizado (GdkWindow viva) sem
    # aparecer para o operador. Um simples hide() NAO serve — widget
    # escondido pode ser unrealized, e ai a GdkWindow morre do mesmo jeito.
    try:
        jan = Gtk.OffscreenWindow()
        jan.add(d)
        jan.show_all()
    except Exception:
        jan = None
    reg = {"display": d, "janela": jan}
    _CEMITERIO.append(reg)

    def _enterrar():
        try:
            _CEMITERIO.remove(reg)
        except ValueError:
            pass
        try:
            if jan is not None:
                jan.remove(d)
        except Exception:
            pass
        try:
            d.destroy()
        except Exception:
            pass
        try:
            if jan is not None:
                jan.destroy()
        except Exception:
            pass
        return False

    GLib.timeout_add_seconds(_CEMITERIO_SEGUNDOS, _enterrar)


def sondar_porta(host, porta, timeout, callback):
    """Testa se host:porta aceita conexao, SEM bloquear a interface.

    Por que existe: entregar um host morto ao gtk-vnc e o caminho mais
    confiavel de derrubar a aplicacao. Ele deixa uma corrotina pendurada no
    connect por ~2 min, e qualquer coisa que mexa no display nesse meio
    tempo (religa automatico, trocar de aba, fechar) vira crash. Testando
    antes com um socket comum, o gtk-vnc so recebe host que ja provou estar
    aceitando conexao.

    TCP e nao ICMP de proposito: ping responde mesmo com o servidor VNC
    fora do ar, e muita rede bloqueia ICMP. O que interessa e a porta.

    O callback e chamado SEMPRE no thread principal, via idle, com
    (ok: bool, erro: str|None).
    """
    def _trabalho():
        erro = None
        ok = False
        s = None
        try:
            s = socket.create_connection((host, porta), timeout)
            ok = True
        except socket.timeout:
            erro = "sem resposta em %gs" % timeout
        except OSError as e:
            erro = e.strerror or str(e)
        except Exception as e:                       # nunca derrubar o thread
            erro = str(e)
        finally:
            if s is not None:
                try:
                    s.close()
                except Exception:
                    pass
        GLib.idle_add(lambda: (callback(ok, erro), False)[1])

    threading.Thread(target=_trabalho, daemon=True).start()


def _bin_rdp():
    for nome in ("xfreerdp3", "xfreerdp", "wlfreerdp"):
        c = shutil.which(nome)
        if c:
            return c, nome
    return None, None


DEBUG = False
SEP_GRUPO = ";"
SECAO_GERAL = "geral"

# ssh: 255 = erro generico, 6 = identificacao do host mudou.
# O VTE devolve o status bruto do waitpid, entao 6 chega como 6 << 8 = 1536.
SSH_CHAVE_MUDOU = 6

# reconexao automatica: espera crescente, teto de 30s
ESPERAS = (3, 5, 8, 13, 21, 30)

# Largura minima util da lateral. Casa com o set_size_request(180, -1) feito
# em _lateral(): abaixo disso o Paned so mostra uma listra, sem lista nenhuma
# visivel. Serve de piso tanto ao ler quanto ao gravar a posicao do divisor.
LARG_MIN_LATERAL = 180


# ---------------------------------------------------------------- temas
#
# Paleta, fontes e a folha de estilo inteira vivem em tema.py — eram 580
# linhas de CSS no meio do codigo. Reexportamos os nomes para o resto do
# arquivo (e para sftp.py, cofre.py e ssh.py) continuar usando como antes.
from tema import (ACENTOS, TEMAS, MONO, SANS, COND,   # noqa: E402,F401
                  CSS_MOLDE, gerar_css, rgba, fonte_mono)





CONF_EXEMPLO = """\
# Um bloco por maquina: a mesma secao guarda a tela e o shell.
#
#   grupo        agrupamento na lateral. Subgrupos separados por ";"
#                    grupo = Loja 06;Caixas
#   host         ip ou nome
#
#   --- tela (VNC) ---
#   porta        5900 por padrao
#   usuario      so se o servidor pedir usuario (VeNCrypt)
#   senha        em branco = pergunta na hora
#   modo         encaixar | 1x1 | dinamico     (gravado pela interface)
#   ronly        1 = abre bloqueado            (gravado pela interface)
#   auto         1 = reconecta sozinho a tela  (gravado pela interface)
#   vnc          0 desliga a tela para esta maquina
#
#   --- shell (SSH) ---
#   ssh          1 liga. Tambem liga sozinho se houver ssh_usuario
#   ssh_porta    22 por padrao
#   ssh_usuario  usuario do ssh
#   ssh_senha    exige sshpass; sem senha usa chave
#   ssh_auto     1 = reconecta o shell sozinho (gravado pela interface)
#
#   --- RDP (xfreerdp) ---
#   rdp          1 liga. Tambem liga sozinho se houver rdp_usuario
#   rdp_porta    3389 por padrao
#   rdp_usuario / rdp_senha
#   rdp_dominio  CUIDADO: um FQDN aqui (ex. rede.local) faz o FreeRDP tentar
#                Kerberos e procurar um KDC para esse realm. Sem krb5.conf
#                configurado a busca falha e ele NAO volta para NTLM — a
#                conexao morre com LOGON_FAILURE. Prefira deixar em branco,
#                ou use o nome NetBIOS curto (REDEMACHADAO), ou embuta no
#                usuario: rdp_usuario = REDEMACHADAO\\jurandir
#   rdp_tela     dinamico | janela | cheia   (gravado pela interface)
#   rdp_auto     1 = reconecta sozinho
#   rdp_extras   opcoes cruas para o xfreerdp, separadas por espaco. Booleanas
#                do FreeRDP levam + ou -, nunca "/". +clipboard ja vai
#                sempre, mesmo sem declarar nada aqui; use -clipboard se
#                quiser desligar. Comece sem mais nada e va somando; cada
#                opcao recusada faz o cliente abortar.
#                    rdp_extras = +compression /audio-mode:1

# x11 = 1 (padrao) roda o programa sob XWayland. E o unico modo em que o RDP
# abre EMBUTIDO na aba, porque reparentar janela e um conceito do X que o
# Wayland nao tem.
#
# O que se perde: a captura total de teclado. O inibidor de atalhos do
# compositor nao alcanca janelas XWayland, entao Super e Alt+Tab continuam do
# sistema. O botao de teclado ainda pega o que o compositor nao reserva
# (Ctrl+W, Ctrl+T, F1-F12), e o menu de teclas injeta Ctrl+Alt+Del e afins.
#
# x11 = 0 inverte a troca: captura total de teclado, RDP em janela separada.
[geral]
painel  = 240
lateral = 1
tema    = claro
x11     = 1

[PDV 001]
grupo       = Loja 06;Caixas
host        = 10.6.1.31
senha       = trocar123
modo        = encaixar
ronly       = 0
auto        = 1
ssh_usuario = zanthus
ssh_senha   = trocar123
ssh_auto    = 1

[PDV 002]
grupo       = Loja 06;Caixas
host        = 10.6.1.32
senha       = trocar123
modo        = encaixar
ssh_usuario = zanthus

[SERV-RELAY-LJ06]
grupo       = Loja 06;Servidores
host        = 10.6.0.10
usuario     = suporte
senha       = trocar123
modo        = dinamico
ssh_usuario = suporte
ssh_senha   = trocar123

[Totem Primavera]
grupo = Matriz;Quiosques
host  = 10.0.0.55
modo  = 1x1
ronly = 1

[ESTACAO-CAIXA-01]
grupo       = Loja 06;Estacoes Windows
host        = 10.6.2.20
vnc         = 0
rdp_usuario = suporte
rdp_senha   = trocar123
# rdp_dominio deixado em branco de proposito: veja o aviso acima
rdp_tela    = dinamico
# rdp_extras deixado em branco de proposito: +clipboard ja e automatico

# so shell, sem tela
[Firewall Matriz]
grupo       = Matriz;Rede
host        = 10.0.0.1
vnc         = 0
ssh_porta   = 2222
ssh_usuario = admin
"""


# ---------------------------------------------------------------- helpers

def add_class(w, *nomes):
    ctx = w.get_style_context()
    for n in nomes:
        ctx.add_class(n)
    return w


def liberar_grab_gtk():
    """Desfaz qualquer gtk_grab_add() pendente antes de abrir um diálogo.

    Isto NAO e o grab de teclado (GdkSeat / XGrabKeyboard). O grab do GTK e
    interno a aplicacao: um widget passa a receber TODOS os eventos e o
    resto da interface para de responder — inclusive dialogos modais, que
    aparecem mas nao aceitam clique.

    O gtk-frdp instala um desses enquanto a sessao RDP esta ativa, e ele
    nao depende de foco: por isso, com uma aba RDP aberta (mesmo em segundo
    plano), o aviso de mudanca de host key do SSH ficava suprimido, e o ssh
    esperava para sempre por uma resposta que ninguem conseguia dar. Fechar
    a aba RDP liberava, o que apontava para o grab e nao para o SSH.

    Devolve a lista de widgets que estavam segurando o grab, para quem
    chamar poder devolver depois se quiser."""
    soltos = []
    for _ in range(8):          # pilha de grabs; 8 e folga suficiente
        w = Gtk.grab_get_current()
        if w is None:
            break
        soltos.append(w)
        try:
            Gtk.grab_remove(w)
        except Exception:
            break
    return soltos


def marcar_area_acao(dlg):
    """add_class na area de acao do dialogo, sem o aviso no terminal.

    Gtk.Dialog.get_action_area e deprecado no GTK3 e imprime
    DeprecationWarning a cada dialogo aberto. Nao ha substituto que
    devolva o mesmo container para estilizar (o caminho moderno seria
    montar a linha de botoes a mao), entao a chamada fica — apenas com o
    aviso silenciado, num lugar so em vez de repetido em cinco."""
    import warnings
    try:
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", DeprecationWarning)
            area = dlg.get_action_area()
    except Exception:
        return
    if area is not None:
        add_class(area, "area-acao")


def botao_dialogo(dlg, texto, resposta, *classes):
    """add_button + classes no botao E no label, sem foco.

    Duas armadilhas do GTK3 resolvidas aqui:

    1. O GTK pinta o Label filho, nao o botao. Sem marcar os dois, a cor da
       classe e ignorada e sai texto escuro sobre fundo escuro.

    2. A classe NAO vai no label. Classes como .acao e .perigo declaram
       "border: 1px solid <cor escura>", e aplicadas ao label essa borda
       vira um retangulo escuro em volta das letras — e, por especificidade,
       .acao (0,1,0) vence "dialog button label" (0,0,3), entao nenhuma
       regra posterior conseguia apaga-la. A cor do texto vem das regras
       especificas "dialog button.acao label { color }" no fim da folha."""
    bt = dlg.add_button(texto, resposta)
    add_class(bt, *classes)
    bt.set_can_focus(False)
    bt.set_focus_on_click(False)
    filho = bt.get_child()
    if isinstance(filho, Gtk.Label):
        filho.set_can_focus(False)
    return bt


def rotulo(texto, *classes, **kw):
    lb = Gtk.Label(label=texto, xalign=kw.pop("xalign", 0.0))
    el = kw.pop("ellipsize", Pango.EllipsizeMode.END)
    if el is not None:
        lb.set_ellipsize(el)
    return add_class(lb, *classes)


def revelar(caixa):
    """Exibe um container marcado com set_no_show_all(True).

    no_show_all e o que mantem o widget escondido no show_all() geral da
    janela — mas ele tambem impede que o show_all() DO PROPRIO widget
    alcance os filhos. Chamar caixa.show_all() nesse caso exibe a caixa
    vazia: sem campos, sem botoes.

    Este defeito ja apareceu tres vezes (painel de diagnostico, barra de
    decisao do lote e linha de comando avulso). Por isso virou funcao."""
    for filho in caixa.get_children():
        filho.show()
    caixa.set_visible(True)


def botao_fechar_aba(ao_fechar):
    """O "×" das abas, com hover, num lugar so.

    Gtk.Button (mesmo com relief NONE) carrega a classe interna ".flat" do
    tema, com padding e min-size proprios que competem com o nosso CSS —
    por isso EventBox. Em compensacao, a pseudo-classe :hover do CSS nao e
    garantida em EventBox como e em Button, entao o realce e feito na mao
    com enter/leave, so trocando a COR do glifo (o peso fica fixo: mudar
    peso muda a metrica e a aba inteira mudaria de tamanho no hover).

    Existia duplicado em dois lugares e a aba de lote acabou ficando sem o
    hover — por isso virou funcao."""
    bt = add_class(Gtk.EventBox(), "fechar-aba")
    bt.set_visible_window(True)
    bt.add_events(Gdk.EventMask.BUTTON_PRESS_MASK
                  | Gdk.EventMask.ENTER_NOTIFY_MASK
                  | Gdk.EventMask.LEAVE_NOTIFY_MASK)
    lbl = rotulo("×", "fechar-aba-x", ellipsize=None)
    bt.add(lbl)
    bt.connect("enter-notify-event",
               lambda _w, _e: (add_class(lbl, "fechar-aba-x-hover"), False)[1])
    bt.connect("leave-notify-event",
               lambda _w, _e: (lbl.get_style_context().remove_class(
                   "fechar-aba-x-hover"), False)[1])
    bt.connect("button-press-event",
               lambda _w, ev: (ao_fechar(), True)[1] if ev.button == 1
               else False)
    return bt


def chip(texto, tipo="neutro"):
    return add_class(Gtk.Label(label=texto), "chip", "chip-" + tipo)


def pintar(w, hexcor):
    prov = Gtk.CssProvider()
    prov.load_from_data(("* { background-color: %s; }" % hexcor).encode())
    w.get_style_context().add_provider(prov, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
    return w


def regua(cor, altura=2):
    r = Gtk.Box()
    r.set_size_request(-1, altura)
    return pintar(add_class(r, "regua"), cor)


ICONES_ABA = {
    "vnc":   ("video-display-symbolic", "computer-symbolic", "VNC"),
    "ssh":   ("utilities-terminal-symbolic", "terminal-symbolic", "SSH"),
    "rdp":   ("preferences-desktop-remote-desktop-symbolic",
              "network-server-symbolic", "RDP"),
    "sftp":  ("folder-symbolic", "folder", "SFTP"),
    "massa": ("view-list-symbolic", "format-justify-fill-symbolic", "LOTE"),
}


def selo_protocolo(tipo):
    """Icone do protocolo, com o texto antigo como ultimo recurso.

    O mesmo glifo aparece no card e na aba: e o que faz a aba ser
    reconhecida em vez de lida. Se o tema de icones nao tiver nenhum dos
    nomes, cai no rotulo de texto de antes — degradar para o comportamento
    velho e melhor que aba sem identificacao nenhuma."""
    nomes = ICONES_ABA.get(tipo)
    if nomes:
        tema_ic = Gtk.IconTheme.get_default()
        for nome in nomes[:-1]:
            try:
                if tema_ic.has_icon(nome):
                    img = Gtk.Image.new_from_icon_name(nome,
                                                       Gtk.IconSize.MENU)
                    # pixel_size FIXO e valign CENTER: icones simbolicos tem
                    # proporcoes diferentes entre si (o de lista e mais baixo
                    # que o de monitor) e, sem fixar, um deles desalinhava a
                    # aba inteira em relacao as vizinhas
                    img.set_pixel_size(12)
                    img.set_valign(Gtk.Align.CENTER)
                    img.set_halign(Gtk.Align.CENTER)
                    return add_class(img, "aba-tipo", "aba-" + tipo)
            except Exception:
                continue
        texto = nomes[-1]
    else:
        texto = tipo.upper()
    lb = Gtk.Label(label=texto)
    lb.set_valign(Gtk.Align.CENTER)
    return add_class(lb, "aba-tipo", "aba-" + tipo)


_PING_DIAG = {"feito": False}


def pingar(host, espera=1):
    """ICMP ping, um pacote. True = respondeu, False = nao, None = nao sei.

    Usa o binario `ping` do sistema: ICMP por socket exige privilegio e nao
    vale a complicacao — a pergunta e so "a maquina esta ligada".

    None e devolvido quando NAO DA PARA SABER (binario ausente, parametro
    recusado). Devolver False nesses casos afirmaria que a maquina esta
    offline, o que e pior do que nao mostrar nada.
    """
    if not host:
        return False
    cmd = ["ping", "-c", "1", "-W", str(espera), "-n", host]
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE,
                           stderr=subprocess.STDOUT,
                           timeout=espera + 2)
    except FileNotFoundError:
        _diag_ping("binario `ping` nao encontrado no PATH")
        return None
    except subprocess.TimeoutExpired:
        return False
    except Exception as e:
        _diag_ping("falhou: %s: %s" % (type(e).__name__, e))
        return None

    if r.returncode == 0:
        return True
    # returncode 1 = sem resposta (offline de verdade).
    # Qualquer outro codigo e o ping reclamando de uso/parametro — nao e
    # resposta sobre a maquina, entao vira "nao sei", com diagnostico.
    if r.returncode == 1:
        return False
    _diag_ping("codigo %d: %s" % (
        r.returncode,
        (r.stdout or b"").decode("utf-8", "replace").strip()[:200]))
    return None


def _diag_ping(msg):
    """Fala UMA vez no stderr. Indicador que falha calado nao se conserta."""
    if _PING_DIAG["feito"]:
        return
    _PING_DIAG["feito"] = True
    print("[acessos] indicador de vida indisponivel — %s" % msg,
          file=sys.stderr, flush=True)


def icone_acao(tipo, px=15):
    """Glifo do protocolo para o card. Mesmo mapa das abas, de proposito.

    O card e a aba precisam mostrar o MESMO simbolo — e o que faz a aba ser
    reconhecida em vez de lida. Se o tema de icones nao tiver nenhum dos
    nomes, cai no texto curto (VNC/SSH/RDP/SFTP)."""
    nomes = ICONES_ABA.get(tipo)
    if nomes:
        tema_ic = Gtk.IconTheme.get_default()
        for nome in nomes[:-1]:
            try:
                if tema_ic.has_icon(nome):
                    img = Gtk.Image.new_from_icon_name(nome,
                                                       Gtk.IconSize.MENU)
                    img.set_pixel_size(px)
                    img.set_valign(Gtk.Align.CENTER)
                    img.set_halign(Gtk.Align.CENTER)
                    return img
            except Exception:
                continue
        texto = nomes[-1]
    else:
        texto = tipo.upper()
    lb = Gtk.Label(label=texto)
    lb.set_valign(Gtk.Align.CENTER)
    return add_class(lb, "card-ico-txt")


# ---------------------------------------------------------------- efemeras
PORTA_PROTO = {"22": "ssh", "3389": "rdp", "5900": "vnc"}


def interpretar_alvo(texto):
    """Le o que foi digitado na busca e devolve (dados, protocolo) ou None.

    Aceita as formas que a mao ja digita sozinha:
        10.1.1.99                 -> VNC 5900
        10.1.1.99:22              -> SSH  (porta decide o protocolo)
        zanthus@10.1.1.99         -> SSH  (usuario implica shell)
        rdp serv-ad-2025          -> RDP  (prefixo manda, ignora inferencia)

    Devolve None quando o texto nao parece um destino — assim a busca
    continua se comportando como busca para qualquer outra coisa.
    """
    txt = (texto or "").strip()
    if not txt or len(txt) > 120:
        return None

    proto = None
    for p in ("vnc", "ssh", "rdp"):
        if txt.lower().startswith(p + " "):
            proto, txt = p, txt[len(p) + 1:].strip()
            break

    usuario = ""
    tem_usuario = False
    if "@" in txt:
        usuario, _, txt = txt.partition("@")
        usuario, txt = usuario.strip(), txt.strip()
        tem_usuario = True

    porta = ""
    if ":" in txt:
        txt, _, porta = txt.partition(":")
        txt, porta = txt.strip(), porta.strip()
        if not porta.isdigit():
            return None

    # PRECEDENCIA: prefixo explicito > porta > usuario@ > padrao.
    # A porta vence o "@" porque e sinal mais forte: em "admin@serv:3389" o
    # 3389 diz RDP alto e claro, e o usuario apenas acompanha.
    if proto is None:
        proto = PORTA_PROTO.get(porta) or ("ssh" if tem_usuario else None)

    if not txt:
        return None
    # host plausivel: sem espaco e so com caracteres de host
    if " " in txt or not all(ch.isalnum() or ch in ".-_" for ch in txt):
        return None

    proto = proto or "vnc"
    dados = {"host": txt, "grupo": "temporária",
             "vnc": "nao", "ssh": "nao", "rdp": "nao"}
    if proto == "ssh":
        dados.update(ssh="sim", ssh_porta=porta or "22", ssh_usuario=usuario)
    elif proto == "rdp":
        dados.update(rdp="sim", rdp_porta=porta or "3389",
                     rdp_usuario=usuario)
    else:
        dados.update(vnc="sim", porta=porta or "5900", usuario=usuario)
    return dados, proto


def conexao_efemera(dados):
    """Conexao que vive SO em memoria.

    Marcada com `efemera=True`: e o que mantem ela fora da execucao em lote
    e fora do cofre. Nada e escrito no conexoes.ini, e ao fechar a aba ela
    (e a senha digitada) somem junto — orquestrador nao guarda lixo.
    """
    cx = Conexao(dados["host"], dados)
    cx.efemera = True
    return cx


def agora():
    return GLib.DateTime.new_now_local().format("%H:%M:%S")




def caminho_icone():
    """Acha o SVG do programa, na ordem em que ele pode existir.

    Dentro de um AppImage o conteudo e montado num diretorio temporario e o
    caminho muda a cada execucao — por isso $APPDIR vem primeiro."""
    nomes = ("acessos.svg", "acessos.png")
    bases = []
    appdir = os.environ.get("APPDIR")
    if appdir:
        bases += [os.path.join(appdir, "usr", "share", "icons", "hicolor",
                               "scalable", "apps"),
                  appdir]
    aqui = os.path.dirname(os.path.abspath(__file__))
    bases += [
        aqui,
        os.path.join(aqui, "..", "..", "share", "icons", "hicolor",
                     "scalable", "apps"),
        os.path.expanduser("~/.local/share/icons/hicolor/scalable/apps"),
        "/usr/local/share/icons/hicolor/scalable/apps",
        "/usr/share/icons/hicolor/scalable/apps",
    ]
    for base in bases:
        for nome in nomes:
            alvo = os.path.normpath(os.path.join(base, nome))
            if os.path.isfile(alvo):
                return alvo
    return None


def identificar_aplicacao():
    """Faz o compositor reconhecer a janela como sendo do Acessos.

    Sao tres mecanismos distintos, e a janela so ganha icone se os tres
    baterem com o nome do arquivo .desktop:

      prgname        -> app_id no Wayland
      program_class  -> WM_CLASS no X11
      icone padrao   -> icone desenhado na barra de titulo e no alt-tab

    Sem isto, um AppImage aparece com o icone generico de executavel: nao ha
    .desktop instalado para o compositor casar."""
    try:
        GLib.set_prgname("acessos")
        GLib.set_application_name("Acessos")
    except Exception:
        pass
    try:
        Gdk.set_program_class("Acessos")
    except Exception:
        pass

    alvo = caminho_icone()
    if alvo:
        try:
            Gtk.Window.set_default_icon_from_file(alvo)
            return alvo
        except Exception:
            pass
    # sem arquivo proprio, tenta o tema de icones e depois um generico
    for nome in ("acessos", "preferences-desktop-remote-desktop",
                 "network-server", "computer"):
        try:
            if Gtk.IconTheme.get_default().has_icon(nome):
                Gtk.Window.set_default_icon_name(nome)
                return nome
        except Exception:
            continue
    return None




def segmentado(opcoes, ativo, ao_mudar):
    """Grupo de ToggleButton com aparencia de segmented control.
    ToggleButton e nao RadioButton: o grupo de radio nasce com um item
    marcado e engole o primeiro clique."""
    caixa = Gtk.Box(spacing=0)
    botoes = {}
    estado = {"trocando": False}

    def clicou(bt, chave):
        if estado["trocando"]:
            return
        if not bt.get_active():          # nao deixa desmarcar tudo
            estado["trocando"] = True
            bt.set_active(True)
            estado["trocando"] = False
            return
        estado["trocando"] = True
        for k, outro in botoes.items():
            if k != chave:
                outro.set_active(False)
        estado["trocando"] = False
        ao_mudar(chave)

    ultimo = len(opcoes) - 1
    for i, (chave, texto) in enumerate(opcoes):
        bt = Gtk.ToggleButton(label=texto)
        add_class(bt, "seg")
        if i == 0:
            add_class(bt, "seg-ini")
        elif i == ultimo:
            add_class(bt, "seg-fim")
        else:
            add_class(bt, "seg-meio")
        bt.set_active(chave == ativo)
        bt.connect("toggled", clicou, chave)
        botoes[chave] = bt
        caixa.pack_start(bt, False, False, 0)
    return caixa, botoes


# ------------------------------------------------- gravacao cirurgica no INI

HISTORICO_MAX = 20
SECAO_COFRE_NOME = "cofre"


def _tem_secao_cofre(linhas):
    alvo = "[%s]" % SECAO_COFRE_NOME
    return any(ln.strip().lower() == alvo for ln in linhas)


def _guardar_copia(caminho, motivo=""):
    """Copia versionada ANTES de gravar. Mantem as HISTORICO_MAX ultimas.

    Rotacao por EVENTO DE GRAVACAO, nao por tempo: o uso e em rajadas —
    doze caixas numa tarde e nada por um mes. Por tempo, a rajada inteira
    caberia numa copia so.

    Fica FORA de qualquer pasta sincronizada por decisao: 20 copias indo
    para a nuvem a cada gravacao multiplicaria trafego e exposicao. Perda
    da maquina inteira e coberta pela sincronizacao do proprio INI.
    """
    try:
        base = os.path.join(os.path.dirname(caminho) or ".", "historico")
        os.makedirs(base, exist_ok=True)
        if not os.path.exists(caminho):
            return
        carimbo = time.strftime("%Y%m%d-%H%M%S")
        destino = os.path.join(base, "conexoes.%s.ini" % carimbo)
        if not os.path.exists(destino):
            shutil.copy2(caminho, destino)
        if motivo:
            with open(os.path.join(base, "index.txt"), "a",
                      encoding="utf-8") as f:
                # o motivo vale mais que a hora: ninguem lembra o horario,
                # lembra o que fez
                f.write("%s  %s\n" % (carimbo, motivo))
        copias = sorted(n for n in os.listdir(base)
                        if n.startswith("conexoes.") and n.endswith(".ini"))
        for velha in copias[:-HISTORICO_MAX]:
            try:
                os.remove(os.path.join(base, velha))
            except OSError:
                pass
    except Exception as e:
        sys.stderr.write("[ini] historico falhou: %s\n" % e)


def escrever_ini(caminho, linhas, motivo=""):
    """Ponto UNICO de escrita do INI. Atomico, com fsync e guardas.

    - GUARDA DO COFRE: se o arquivo atual tem [cofre] e o conteudo novo
      nao, a gravacao e ABORTADA. A secao guarda o salt; sem ele, a mesma
      senha mestra deriva outra chave e nenhuma senha guardada abre. Bug
      aqui custaria o cofre inteiro, entao ele nao passa.
    - fsync antes do replace: sem ele o os.replace pode trocar o nome
      enquanto o conteudo ainda esta em cache, e uma queda deixa o arquivo
      novo VAZIO — pior que o antigo intacto.
    """
    try:
        with open(caminho, encoding="utf-8") as f:
            antigas = f.readlines()
    except OSError:
        antigas = []

    if antigas and _tem_secao_cofre(antigas) and not _tem_secao_cofre(linhas):
        sys.stderr.write(
            "[ini] GRAVACAO ABORTADA: o conteudo novo nao tem a secao "
            "[%s] e o arquivo atual tem. Sem o salt as senhas guardadas "
            "ficam irrecuperaveis.\n" % SECAO_COFRE_NOME)
        return False

    _guardar_copia(caminho, motivo)

    tmp = caminho + ".tmp"
    try:
        with open(tmp, "w", encoding="utf-8") as f:
            f.writelines(linhas)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, caminho)
        return True
    except OSError as e:
        sys.stderr.write("[ini] falha ao gravar: %s\n" % e)
        try:
            os.remove(tmp)
        except OSError:
            pass
        return False


def gravar_chave(caminho, secao, chave, valor):
    """Troca UMA linha do INI preservando comentarios, ordem e alinhamento.
    O configparser.write() reescreveria o arquivo inteiro e comeria todos os
    comentarios — inaceitavel num arquivo que a pessoa edita a mao."""
    valor = str(valor)
    # CIFRAGEM NA SAIDA, ponto unico. Todo caminho de gravacao passa por
    # aqui, entao basta interceptar neste lugar: o resto do programa lida
    # com senha em claro e nao precisa saber que existe cofre.
    if (COFRE is not None and not COFRE.trancado()
            and TEM_COFRE and chave in _cofre.CAMPOS_SIGILOSOS and valor):
        try:
            valor = COFRE.cifrar(valor)
        except Exception as e:
            sys.stderr.write("cofre: falha ao cifrar %s: %s\n" % (chave, e))
    try:
        with open(caminho, encoding="utf-8") as f:
            linhas = f.readlines()
    except OSError:
        return False

    re_sec = re.compile(r"^\s*\[(?P<n>[^\]]+)\]\s*$")
    re_kv = re.compile(r"^(?P<esp>\s*)(?P<k>[^#;=\s][^=]*?)(?P<pad>\s*)=(?P<r>.*)$")

    ini = fim = None
    for i, ln in enumerate(linhas):
        m = re_sec.match(ln)
        if not m:
            continue
        if m.group("n").strip() == secao:
            ini = i
        elif ini is not None and fim is None:
            fim = i
    if ini is None:
        if linhas and not linhas[-1].endswith("\n"):
            linhas.append("\n")
        linhas.append("\n[%s]\n%s = %s\n" % (secao, chave, valor))
    else:
        if fim is None:
            fim = len(linhas)
        alvo = None
        for i in range(ini + 1, fim):
            m = re_kv.match(linhas[i])
            if m and m.group("k").strip().lower() == chave.lower():
                alvo = i
                break
        if alvo is not None:
            m = re_kv.match(linhas[alvo])
            linhas[alvo] = "%s%s%s= %s\n" % (
                m.group("esp"), m.group("k"), m.group("pad"), valor)
        else:
            corte = fim
            while corte > ini + 1 and not linhas[corte - 1].strip():
                corte -= 1
            linhas.insert(corte, "%s = %s\n" % (chave, valor))

    if not escrever_ini(caminho, linhas, "%s/%s" % (secao, chave)):
        return False
    try:
        if DEBUG:
            sys.stderr.write("[ini] [%s] %s = %s\n" % (secao, chave, valor))
        return True
    except OSError:
        return False


def remover_secao(caminho, secao):
    """Apaga a secao e as linhas que a acompanham, preservando o resto.

    Os comentarios que vem IMEDIATAMENTE antes do cabecalho tambem saem:
    quase sempre descrevem aquela maquina e ficariam orfaos."""
    try:
        with open(caminho, encoding="utf-8") as f:
            linhas = f.readlines()
    except OSError:
        return False

    re_sec = re.compile(r"^\s*\[(?P<n>[^\]]+)\]\s*$")
    ini = fim = None
    for i, ln in enumerate(linhas):
        m = re_sec.match(ln)
        if not m:
            continue
        if m.group("n").strip() == secao:
            ini = i
        elif ini is not None and fim is None:
            fim = i
    if ini is None:
        return False
    if fim is None:
        fim = len(linhas)

    topo = ini
    while topo > 0:
        anterior = linhas[topo - 1].strip()
        if anterior.startswith("#") or not anterior:
            topo -= 1
        else:
            break
    # nao engole a linha em branco que separa da secao anterior
    if topo > 0 and not linhas[topo].strip():
        topo += 1

    del linhas[topo:fim]
    try:
        return escrever_ini(caminho, linhas, "secao %s" % secao)
    except OSError:
        return False


def gravar_secao(caminho, secao, pares):
    """Grava um conjunto de chaves. Valor vazio significa remover a chave."""
    for chave, valor in pares:
        if valor in ("", None):
            apagar_chave(caminho, secao, chave)
        else:
            gravar_chave(caminho, secao, chave, valor)
    return True


def apagar_chave(caminho, secao, chave):
    try:
        with open(caminho, encoding="utf-8") as f:
            linhas = f.readlines()
    except OSError:
        return False
    re_sec = re.compile(r"^\s*\[(?P<n>[^\]]+)\]\s*$")
    re_kv = re.compile(r"^\s*(?P<k>[^#;=\s][^=]*?)\s*=")
    ini = fim = None
    for i, ln in enumerate(linhas):
        m = re_sec.match(ln)
        if not m:
            continue
        if m.group("n").strip() == secao:
            ini = i
        elif ini is not None and fim is None:
            fim = i
    if ini is None:
        return False
    if fim is None:
        fim = len(linhas)
    for i in range(fim - 1, ini, -1):
        m = re_kv.match(linhas[i])
        if m and m.group("k").strip().lower() == chave.lower():
            del linhas[i]
    try:
        return escrever_ini(caminho, linhas, "secao %s" % secao)
    except OSError:
        return False


# ---------------------------------------------------------------- modelo

def verdade(txt, padrao=False):
    t = (txt or "").strip().lower()
    if not t:
        return padrao
    return t in ("1", "sim", "s", "true", "yes", "y", "on")


class Conexao:
    def __init__(self, nome, sec):
        self.nome  = nome
        # efemera: conexao digitada na busca, viva so nesta sessao. Default
        # False para que qualquer codigo possa consultar sem verificar.
        self.efemera = False
        self.host  = sec.get("host", "").strip()
        self.grupo = sec.get("grupo", "Sem grupo").strip()

        self.porta   = sec.get("porta", "5900").strip() or "5900"
        self.usuario = sec.get("usuario", "").strip()
        self.senha   = sec.get("senha", "").strip()
        self.modo    = sec.get("modo", "encaixar").strip().lower()
        self.ronly   = verdade(sec.get("ronly", ""), False)
        self.auto    = verdade(sec.get("auto", ""), False)
        self.tem_vnc = verdade(sec.get("vnc", ""), True)
        if self.modo not in ("encaixar", "1x1", "dinamico"):
            self.modo = "encaixar"

        self.ssh_porta   = sec.get("ssh_porta", "22").strip() or "22"
        self.ssh_usuario = sec.get("ssh_usuario", "").strip()
        self.ssh_senha   = sec.get("ssh_senha", "").strip()
        self.ssh_auto    = verdade(sec.get("ssh_auto", ""), False)
        self.tem_ssh = verdade(sec.get("ssh", ""), bool(self.ssh_usuario))

        self.rdp_porta   = sec.get("rdp_porta", "3389").strip() or "3389"
        self.rdp_usuario = sec.get("rdp_usuario", "").strip()
        self.rdp_senha   = sec.get("rdp_senha", "").strip()
        self.rdp_dominio = sec.get("rdp_dominio", "").strip()
        self.rdp_tela    = sec.get("rdp_tela", "dinamico").strip().lower()
        self.rdp_auto    = verdade(sec.get("rdp_auto", ""), False)
        # opcoes cruas repassadas ao xfreerdp, separadas por espaco.
        # Ex.: rdp_extras = +clipboard +compression /audio-mode:1
        self.rdp_extras  = [x for x in sec.get("rdp_extras", "").split() if x]
        self.tem_rdp = verdade(sec.get("rdp", ""), bool(self.rdp_usuario))
        if self.rdp_tela not in ("dinamico", "cheia", "janela"):
            self.rdp_tela = "dinamico"

        # plataforma: 0/ausente = Linux. A sonda grava isto sozinha, mas
        # pode ser editado a mao. Decide qual protocolo o executor em lote
        # usa e quais snippets aparecem para esta maquina.
        self.windows = verdade(sec.get("windows", ""), False)

    def tem(self, tipo):
        return {"vnc": self.tem_vnc, "ssh": self.tem_ssh,
                "rdp": self.tem_rdp}.get(tipo, False)

    @property
    def ordem(self):
        """Chave de ordenacao alfabetica, insensivel a caixa e acento."""
        import unicodedata
        def limpa(t):
            t = unicodedata.normalize("NFKD", t)
            return "".join(c for c in t if not unicodedata.combining(c)).lower()
        return ([limpa(p) for p in self.caminho_grupo], limpa(self.nome))

    @property
    def caminho_grupo(self):
        p = [x.strip() for x in self.grupo.split(SEP_GRUPO) if x.strip()]
        return p or ["Sem grupo"]

    @property
    def destino(self):
        return "%s:%s" % (self.host, self.porta)

    @property
    def destino_ssh(self):
        return "%s:%s" % (self.host, self.ssh_porta)

    @property
    def destino_rdp(self):
        return "%s:%s" % (self.host, self.rdp_porta)


# ---------------------------------------------------------------- snippets

SNIPPETS_EXEMPLO = """\
# Biblioteca de comandos para a execucao em lote.
#
#   descricao    o que aparece na lista
#   plataforma   linux | windows | ambos
#   root         sim = precisa elevar (a senha de root e SEMPRE digitada
#                na hora, nunca gravada em arquivo)
#   ignorar_exit sim = codigo != 0 nao interrompe a fila deste PDV
#   comando      linha indentada continua a anterior; e assim que
#                comando multilinha e guardado

[versao_zeus]
descricao    = Ver versao do Zeus
plataforma   = linux
root         = nao
ignorar_exit = nao
comando      = cat /Zanthus/Zeus/versao.txt 2>/dev/null || echo "sem arquivo"

[espaco_disco]
descricao    = Espaco em disco
plataforma   = ambos
root         = nao
ignorar_exit = nao
comando      = df -h /

[servicos_windows]
descricao    = Servicos parados que deveriam rodar
plataforma   = windows
root         = nao
ignorar_exit = sim
comando      = Get-Service | Where-Object { $_.StartType -eq 'Automatic' -and $_.Status -ne 'Running' }
"""


class Snippet:
    def __init__(self, chave, sec):
        self.chave = chave
        self.descricao = sec.get("descricao", chave).strip()
        self.plataforma = sec.get("plataforma", "ambos").strip().lower()
        self.root = verdade(sec.get("root", ""), False)
        self.ignorar_exit = verdade(sec.get("ignorar_exit", ""), False)
        self.comando = sec.get("comando", "").strip()
        if self.plataforma not in ("linux", "windows", "ambos"):
            self.plataforma = "ambos"

    def serve_para(self, conexao):
        """Filtro por plataforma: um PDV Linux nao ve snippet de Windows."""
        if self.plataforma == "ambos":
            return True
        alvo = "windows" if getattr(conexao, "windows", False) else "linux"
        return self.plataforma == alvo


def caminho_snippets():
    # acompanha o caminho= do [geral]: separar snippets do INI faria a
    # pasta sincronizada levar metade da configuracao
    return os.path.join(dir_dados(), "snippets.ini")


def carregar_snippets(caminho=None):
    caminho = caminho or caminho_snippets()
    if not os.path.exists(caminho):
        os.makedirs(os.path.dirname(caminho), exist_ok=True)
        with open(caminho, "w", encoding="utf-8") as f:
            f.write(SNIPPETS_EXEMPLO)
    # interpolation=None: comandos de shell usam % a vontade (stat --print=%w,
    # date +%H:%M, awk '{print $1}%'...). Com a interpolacao padrao ligada, o
    # ConfigParser le "%w" como sintaxe de substituicao e estoura
    # ValueError: invalid interpolation syntax. Tem de estar desligada na
    # LEITURA e na ESCRITA — senao o que foi gravado nao volta a abrir.
    cp = configparser.ConfigParser(interpolation=None)
    cp.read(caminho, encoding="utf-8")
    itens = [Snippet(sec, cp[sec]) for sec in cp.sections()]
    itens.sort(key=lambda x: x.descricao.lower())
    return itens


BASE_DADOS = None      # diretorio efetivo dos arquivos do app


def _dir_padrao():
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return os.path.join(base, "acessos")


def _ler_caminho_geral(arquivo):
    """Le [geral] caminho= sem carregar o INI inteiro.

    Precisa ser leitura crua: neste ponto o cofre ainda nao existe e o
    parser completo do app ainda nao rodou.
    """
    try:
        cp = configparser.ConfigParser(interpolation=None)
        cp.read(arquivo, encoding="utf-8")
        valor = (cp["geral"].get("caminho", "") if cp.has_section("geral")
                 else "").strip()
    except Exception:
        return ""
    return os.path.abspath(os.path.expanduser(valor)) if valor else ""


def caminho_conf(arg=None):
    """Descobre o conexoes.ini efetivo e fixa BASE_DADOS.

    A chave [geral] caminho= aponta um DIRETORIO. O INI do lugar padrao
    funciona como ponteiro: se ele traz essa chave, o app passa a usar o
    conexoes.ini daquele diretorio, e snippets e historico acompanham.

    Isso resolve o problema do ovo e da galinha — para ler a chave e
    preciso abrir algum INI antes. Um unico salto e permitido, de
    proposito: dois arquivos apontando um para o outro entrariam em laco.
    """
    global BASE_DADOS

    if arg:
        alvo = os.path.abspath(os.path.expanduser(arg))
        BASE_DADOS = os.path.dirname(alvo)
        return alvo

    padrao = os.path.join(_dir_padrao(), "conexoes.ini")
    destino = _ler_caminho_geral(padrao)

    if destino and os.path.isdir(destino):
        alvo = os.path.join(destino, "conexoes.ini")
        if os.path.exists(alvo) and os.path.abspath(alvo) != os.path.abspath(padrao):
            BASE_DADOS = destino
            if DEBUG:
                sys.stderr.write("[ini] caminho= redireciona para %s\n" % alvo)
            return alvo
        if not os.path.exists(alvo):
            sys.stderr.write(
                "[ini] [geral] caminho aponta para %s, mas nao ha "
                "conexoes.ini la — usando o arquivo padrao.\n" % destino)

    BASE_DADOS = _dir_padrao()
    return padrao


def mover_dados_para(destino):
    """Passa a usar `destino` como pasta dos arquivos do app.

    Devolve (diretorio_efetivo, lista_do_que_foi_copiado).

    Regras, todas pensadas para nao surpreender:
      - a pasta e criada se nao existir;
      - se ja tiver conexoes.ini, ele e USADO como esta — nada e
        sobrescrito, porque a pasta pode ser a de outra maquina que ja
        sincronizou;
      - se estiver vazia, os arquivos atuais sao COPIADOS para la;
      - o original NAO e apagado. Se algo der errado, e so limpar a chave
        `caminho` e tudo volta ao que era;
      - a chave vai no INI do lugar PADRAO, que passa a ser o ponteiro. Se
        ela fosse gravada no arquivo de destino, ninguem a leria.
    """
    destino = os.path.abspath(os.path.expanduser(destino))
    os.makedirs(destino, exist_ok=True)
    if not os.access(destino, os.W_OK):
        raise OSError("sem permissão de escrita em %s" % destino)

    padrao_dir = _dir_padrao()
    origem = dir_dados()
    copiados = []

    if os.path.abspath(destino) != os.path.abspath(origem):
        for nome in ("conexoes.ini", "snippets.ini"):
            alvo = os.path.join(destino, nome)
            fonte = os.path.join(origem, nome)
            if not os.path.exists(alvo) and os.path.exists(fonte):
                shutil.copy2(fonte, alvo)
                copiados.append(nome)

    # a chave mora no arquivo do lugar padrao: e ele que o app abre primeiro
    os.makedirs(padrao_dir, exist_ok=True)
    ini_padrao = os.path.join(padrao_dir, "conexoes.ini")
    if not os.path.exists(ini_padrao):
        with open(ini_padrao, "w", encoding="utf-8") as f:
            f.write("[geral]\n")

    if os.path.abspath(destino) == os.path.abspath(padrao_dir):
        apagar_chave(ini_padrao, "geral", "caminho")
    else:
        gravar_chave(ini_padrao, "geral", "caminho", destino)

    return destino, copiados


def dir_dados():
    """Diretorio dos arquivos do app. Resolve tarde para respeitar caminho=."""
    return BASE_DADOS or _dir_padrao()


def instalar_css_cedo(tema):
    """Instala a folha de estilo ANTES de qualquer janela existir.

    O provider so era adicionado dentro de Janela.__init__, mas o dialogo do
    cofre aparece antes disso — no main, para as senhas ja virem decifradas
    na carga. Resultado: o dialogo tinha as classes certas e nenhuma folha
    para aplica-las, saindo com a cara do tema do sistema.

    A Janela chama _aplicar_css() de novo depois; adicionar o provider duas
    vezes e inofensivo, e a segunda chamada e que passa a valer quando o
    tema e trocado em tempo de execucao.
    """
    tema = (tema or "claro").strip().lower()
    if tema not in TEMAS:
        tema = "claro"
    prov = Gtk.CssProvider()
    try:
        prov.load_from_data(gerar_css(tema))
    except GLib.Error:
        return None
    Gtk.StyleContext.add_provider_for_screen(
        Gdk.Screen.get_default(), prov,
        Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
    return prov


def ler_geral(caminho, chave, padrao=""):
    """Le UMA chave da secao [geral] sem carregar o resto.

    Necessario porque duas decisoes acontecem antes de o cofre abrir — o
    tema (para o dialogo sair estilizado) e o backend X11 (que reexecuta o
    processo) — e carregar() so funciona com o cofre ja aberto."""
    try:
        cp = configparser.ConfigParser(interpolation=None)
        cp.read(caminho, encoding="utf-8")
        if cp.has_section(SECAO_GERAL):
            return cp[SECAO_GERAL].get(chave, padrao)
    except Exception:
        pass
    return padrao


def ler_tema(caminho):
    """So o tema, sem carregar o resto — usado antes do cofre abrir."""
    return ler_geral(caminho, "tema", "claro") or "claro"


def carregar(caminho):
    if not os.path.exists(caminho):
        os.makedirs(os.path.dirname(caminho), exist_ok=True)
        with open(caminho, "w", encoding="utf-8") as f:
            f.write(CONF_EXEMPLO)
    # inline_comment_prefixes fica DESLIGADO de proposito: com ele ligado o
    # ";" dos subgrupos viraria comentario e "Loja 06;Caixas" perderia a cauda.
    cp = configparser.ConfigParser()
    cp.read(caminho, encoding="utf-8")
    geral = dict(cp[SECAO_GERAL]) if cp.has_section(SECAO_GERAL) else {}
    pular = {SECAO_GERAL}
    if TEM_COFRE:
        pular.add(_cofre.SECAO_COFRE)
    conexoes = [Conexao(s, cp[s]) for s in cp.sections() if s not in pular]
    # DECIFRAR NA CARGA: o resto do programa continua trabalhando com senha
    # em texto claro na memoria, sem saber que existe cofre. Assim a
    # integracao nao se espalha por dezenas de pontos.
    if COFRE is not None and not COFRE.trancado():
        for cx in conexoes:
            for campo in _cofre.CAMPOS_SIGILOSOS:
                valor = getattr(cx, campo, None)
                if valor:
                    try:
                        setattr(cx, campo, COFRE.decifrar(valor))
                    except Exception as e:
                        sys.stderr.write("cofre: %s em [%s].%s\n"
                                         % (e, cx.nome, campo))
    conexoes.sort(key=lambda c: c.ordem)      # alfabetica por padrao
    return conexoes, geral


# ---------------------------------------------------------------- abas

class GrabNativo:
    """Captura de teclado pelas funcoes nativas, nao pelo gdk_seat_grab.

    Por que nao gdk_seat_grab: ele NAO fala o protocolo de inibicao do
    Wayland. Sao mecanismos distintos, e eu presumi que um implicava o
    outro — dai o botao "pegar" sem que Super e Alt+Tab mudassem de dono.

    Dois caminhos, escolhidos pelo backend real:

    X11 e XWAYLAND -> XGrabKeyboard, via ctypes.
        Sob XWayland isso nao e um grab "so do X": o proprio XWayland traduz
        o pedido para zwp_xwayland_keyboard_grab_v1 e o compositor concede.
        Por isso funciona mesmo com x11 = 1, que e o modo do RDP embutido.

    WAYLAND NATIVO -> gdk_wayland_window_inhibit_shortcuts, via ctypes.
        Existe no GDK3 e fala zwp_keyboard_shortcuts_inhibit_v1, mas nao e
        exposta pelo PyGObject (nao ha typelib GdkWayland no GTK3).

    ctypes nao perdoa: ponteiro errado nao levanta excecao, derruba o
    processo. Por isso todo argtypes/restype e declarado, e nada e chamado
    sem checar antes que o backend bate com a funcao.
    """

    _libX11 = None
    _libgdk = None
    _xdisplay = None
    _carregado = False

    # constantes do X
    GRAB_MODE_ASYNC = 1
    CURRENT_TIME = 0
    GRAB_SUCCESS = 0

    @classmethod
    def _carregar(cls):
        if cls._carregado:
            return
        cls._carregado = True
        import ctypes
        import ctypes.util

        if _backend_x11():
            nome = ctypes.util.find_library("X11") or "libX11.so.6"
            try:
                lib = ctypes.CDLL(nome)
                lib.XOpenDisplay.argtypes = [ctypes.c_char_p]
                lib.XOpenDisplay.restype = ctypes.c_void_p
                lib.XGrabKeyboard.argtypes = [
                    ctypes.c_void_p,   # Display*
                    ctypes.c_ulong,    # Window
                    ctypes.c_int,      # owner_events
                    ctypes.c_int,      # pointer_mode
                    ctypes.c_int,      # keyboard_mode
                    ctypes.c_ulong,    # Time
                ]
                lib.XGrabKeyboard.restype = ctypes.c_int
                lib.XUngrabKeyboard.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
                lib.XUngrabKeyboard.restype = ctypes.c_int
                lib.XFlush.argtypes = [ctypes.c_void_p]
                lib.XFlush.restype = ctypes.c_int
                # conexao propria: o grab pertence a este cliente e pode ser
                # solto sem depender do estado interno do GTK
                disp = lib.XOpenDisplay(None)
                if disp:
                    cls._libX11 = lib
                    cls._xdisplay = ctypes.c_void_p(disp)
            except Exception as e:
                sys.stderr.write("libX11 indisponivel: %s\n" % e)
            return

        # Wayland nativo
        try:
            lib = ctypes.CDLL("libgdk-3.so.0")
            for fn in ("gdk_wayland_window_inhibit_shortcuts",
                       "gdk_wayland_window_restore_shortcuts"):
                if not hasattr(lib, fn):
                    raise AttributeError(fn)
            lib.gdk_wayland_window_inhibit_shortcuts.argtypes = [
                ctypes.c_void_p, ctypes.c_void_p]
            lib.gdk_wayland_window_inhibit_shortcuts.restype = None
            lib.gdk_wayland_window_restore_shortcuts.argtypes = [ctypes.c_void_p]
            lib.gdk_wayland_window_restore_shortcuts.restype = None
            cls._libgdk = lib
        except Exception as e:
            sys.stderr.write("inhibit_shortcuts indisponivel: %s\n" % e)

    @classmethod
    def disponivel(cls):
        cls._carregar()
        return cls._libX11 is not None or cls._libgdk is not None

    @classmethod
    def _ponteiro(cls, obj):
        """Endereco do GObject por tras do wrapper Python.

        hash() sobre um objeto do PyGObject devolve o endereco do GObject.
        Nao e API garantida, so detalhe de implementacao — por isso o
        resultado e validado antes de virar argumento de funcao C."""
        import ctypes
        end = hash(obj)
        if not isinstance(end, int) or end <= 0:
            return None
        return ctypes.c_void_p(end)

    @classmethod
    def pegar(cls, gdkwin):
        """Devolve (ok, mensagem)."""
        cls._carregar()
        if gdkwin is None:
            return False, "janela ainda não realizada"

        if cls._libX11 is not None:
            try:
                xid = gdkwin.get_xid()
            except Exception as e:
                return False, "sem XID: %s" % e
            st = cls._libX11.XGrabKeyboard(
                cls._xdisplay, xid, 1,
                cls.GRAB_MODE_ASYNC, cls.GRAB_MODE_ASYNC, cls.CURRENT_TIME)
            cls._libX11.XFlush(cls._xdisplay)
            if st == cls.GRAB_SUCCESS:
                return True, "XGrabKeyboard concedido"
            motivos = {1: "já capturado por outra janela",
                       2: "janela não visível",
                       3: "recusado pelo servidor",
                       4: "congelado"}
            return False, "XGrabKeyboard recusado (%s)" % motivos.get(
                st, "código %s" % st)

        if cls._libgdk is not None:
            seat = Gdk.Display.get_default().get_default_seat()
            pw, ps = cls._ponteiro(gdkwin), cls._ponteiro(seat)
            if pw is None or ps is None:
                return False, "não consegui os ponteiros nativos"
            cls._libgdk.gdk_wayland_window_inhibit_shortcuts(pw, ps)
            return True, "inibidor de atalhos pedido ao compositor"

        return False, "nenhum mecanismo de captura disponível"

    @classmethod
    def soltar(cls, gdkwin):
        cls._carregar()
        if cls._libX11 is not None and cls._xdisplay is not None:
            cls._libX11.XUngrabKeyboard(cls._xdisplay, cls.CURRENT_TIME)
            cls._libX11.XFlush(cls._xdisplay)
            return True
        if cls._libgdk is not None and gdkwin is not None:
            pw = cls._ponteiro(gdkwin)
            if pw is not None:
                cls._libgdk.gdk_wayland_window_restore_shortcuts(pw)
            return True
        return False

    @classmethod
    def soltar_tudo(cls):
        """Rede de seguranca: chamada no encerramento, aconteca o que
        acontecer. Teclado preso apos o programa morrer e o pior desfecho
        possivel — o usuario fica sem desktop."""
        if cls._libX11 is not None and cls._xdisplay is not None:
            try:
                cls._libX11.XUngrabKeyboard(cls._xdisplay, cls.CURRENT_TIME)
                cls._libX11.XFlush(cls._xdisplay)
            except Exception:
                pass


class CapturaTeclado:
    """Liga o GrabNativo ao ciclo de vida da aba.

    O perigo de um grab e esquece-lo: ele captura o teclado do desktop
    inteiro e tudo parece congelado. A liberacao esta pendurada em todos os
    caminhos de saida — perder foco, sair da aba, desmapear, destruir, abrir
    dialogo — mais a tecla Pause como valvula de escape e um atexit."""

    def _iniciar_captura(self):
        self._grab_ativo = False
        self.connect("unmap", lambda *_a: self._grab(False))
        self.connect("destroy", lambda *_a: self._grab(False))

    def _janela_grab(self):
        topo = self.get_toplevel()
        if topo is None or not topo.get_realized():
            return None
        return topo.get_window()

    def _grab(self, ligar):
        gdkwin = self._janela_grab()
        if not ligar:
            if self._grab_ativo:
                GrabNativo.soltar(gdkwin)
                self._grab_ativo = False
                if hasattr(self, "janela"):
                    self.janela.marcar_captura(False)
            return True
        if self._grab_ativo:
            return True
        ok, msg = GrabNativo.pegar(gdkwin)
        self._grab_ativo = ok
        self.reg(("captura: %s" % msg) if ok else ("captura falhou: %s" % msg))
        if ok:
            self.janela.marcar_captura(True)
        return ok

    def soltar_teclado(self):
        self._grab(False)
        # SOLTAR TAMBEM O GRAB DO WIDGET.
        #
        # O _grab acima desfaz so o GrabNativo. O grab de seat feito pelo
        # proprio widget (VncWidget.set_keyboard_grab, e o equivalente
        # interno do gtk-frdp) e independente e ficava preso.
        #
        # Consequencia observada: com uma aba RDP aberta em segundo plano, o
        # terminal da aba SSH nao recebia tecla nenhuma. O ssh ficava parado
        # esperando a resposta ao aviso de mudanca de host key, sem que o
        # operador conseguisse digitar — e so destravava ao FECHAR o RDP,
        # quando o grab enfim caia.
        alvo = getattr(self, "display", None) or getattr(self, "tela", None)
        if alvo is not None and hasattr(alvo, "set_keyboard_grab"):
            try:
                alvo.set_keyboard_grab(False)
            except Exception:
                pass

    def reatar_teclado(self):
        """Refaz a captura depois que a janela recupera o foco."""
        if not self.bt_teclado.get_active():
            return False
        if not self._grab_ativo:
            self._grab(True)
        # O grab do WIDGET tambem se perde no focus-out, e ele e independente
        # do _grab_ativo (que rastreia so o GrabNativo). Sem refaze-lo, o
        # VNC voltava sem captura mesmo com o botao marcado — e no RDP o
        # gtk-frdp precisa do foco de volta pelo mesmo motivo.
        alvo = getattr(self, "display", None) or getattr(self, "tela", None)
        if alvo is not None:
            if hasattr(alvo, "set_keyboard_grab"):
                try:
                    alvo.set_keyboard_grab(True)
                except Exception:
                    pass
            elif hasattr(alvo, "focar"):
                alvo.focar()
        return False


class AbaBase(Gtk.Box):
    def __init__(self, conexao, janela):
        super().__init__(orientation=Gtk.Orientation.VERTICAL)
        self.cx = conexao
        self.janela = janela
        self.fechando = False
        self.religando = False
        self.tentativa = 0
        self.timer_auto = None
        self._timers = set()

        self.barra = Gtk.Box(spacing=4)
        # 2 -> 0: a altura da barra vinha do border_width, nao da fonte.
        # Zerando, ela encolhe sem que nenhum texto mude de tamanho.
        self.barra.set_border_width(0)
        self.chip_estado = chip("AGUARDE", "neutro")
        self.lb_estado = rotulo("…", "secundario")
        self.lb_geo = rotulo("", "secundario", xalign=1.0)
        self.barra.pack_start(self.chip_estado, False, False, 0)
        self.barra.pack_start(self.lb_estado, False, False, 0)
        self.barra.pack_end(self.lb_geo, False, False, 0)
        self.pack_start(self.barra, False, False, 0)
        self.pack_start(regua(janela.cor("borda"), 1), False, False, 0)

        self.bt_auto = Gtk.ToggleButton(label="↻")
        add_class(self.bt_auto, "tog", "tog-ok", "tog-glifo")
        self.bt_auto.set_tooltip_text(
            "↻ Reconectar sozinho quando a sessão cair. "
            "Espera crescente até 30s; o estado é gravado no INI.")
        self.barra.pack_end(self.bt_auto, False, False, 0)

        self.buf_log = Gtk.TextBuffer()
        # o painel comeca escondido; quem manda e o botao de diagnostico
        # no cabecalho, valendo para todas as abas de uma vez
        tv = Gtk.TextView(buffer=self.buf_log)
        tv.set_editable(False)
        tv.set_monospace(True)
        add_class(tv, "log")
        sw = Gtk.ScrolledWindow()
        sw.set_size_request(-1, 110)
        sw.add(tv)
        self.exp_log = sw
        self.exp_log.set_no_show_all(True)      # show_all nao pode reabri-lo
        self.exp_log.set_visible(False)

    # ---- log e estado
    # assuntos que sempre valem uma linha no terminal, mesmo sem --debug:
    # sao os que o usuario precisa ver quando algo nao funciona
    SEMPRE = ("captura", "grab", "XGrabKeyboard", "inibidor", "xdotool",
              "enviado", "falha", "erro", "recusad")

    def reg(self, txt):
        try:
            self.buf_log.insert(self.buf_log.get_end_iter(),
                                "%s  %s\n" % (agora(), txt))
        except Exception:
            pass
        if DEBUG or any(p in txt for p in self.SEMPRE):
            sys.stderr.write("[%s] %s\n" % (self.cx.nome, txt))
            sys.stderr.flush()

    def _estado(self, txt_chip, tipo, txt):
        self.chip_estado.set_text(txt_chip)
        ctx = self.chip_estado.get_style_context()
        for c in ("chip-ok", "chip-erro", "chip-neutro", "chip-atencao"):
            ctx.remove_class(c)
        ctx.add_class("chip-" + tipo)
        self.lb_estado.set_text(txt)

    # ---- reconexao automatica
    def _ligar_auto(self, chave_ini):
        self.bt_auto.connect("toggled", self._trocar_auto, chave_ini)

    def _trocar_auto(self, bt, chave_ini):
        self.janela.gravar(self.cx.nome, chave_ini,
                           "1" if bt.get_active() else "0")
        if bt.get_active():
            self.reg("reconexão automática ligada")
        else:
            self.reg("reconexão automática desligada")
            self._cancelar_auto()

    def _cancelar_auto(self):
        if self.timer_auto:
            self.timer_auto = self._cancelar_timer(self.timer_auto)

    def _inicio_manual(self):
        """Marca que a proxima queda foi PROVOCADA por nos.

        Reconectar a mao mata o processo/sessao atual, o que dispara o mesmo
        sinal de queda que uma desconexao real. Sem esta marca o automatico
        agenda mais uma tentativa em cima da que ja esta em curso, e as duas
        passam a se realimentar."""
        self.religando = True
        self._cancelar_auto()
        self._agendar(1500, self._fim_manual)

    def _fim_manual(self):
        self.religando = False
        return False

    def _agendar_auto(self, motivo=""):
        """Chamado quando a sessao cai. Nao dispara se o operador fechou a
        aba nem se o auto esta desligado."""
        if self.fechando or self.religando or not self.bt_auto.get_active():
            return
        self._cancelar_auto()
        espera = ESPERAS[min(self.tentativa, len(ESPERAS) - 1)]
        self.tentativa += 1
        self._estado("RELIGANDO", "atencao",
                     "nova tentativa em %ds (#%d)" % (espera, self.tentativa))
        self.reg("reconectando em %ds%s" % (espera, (" — " + motivo) if motivo else ""))
        self.timer_auto = self._agendar(espera, self._disparar_auto, segundos=True)

    def _disparar_auto(self):
        self.timer_auto = None
        if self.fechando or not self.bt_auto.get_active():
            return False
        self.reconectar()
        return False

    def sucesso(self):
        """Zera o contador de backoff depois de uma conexao boa."""
        self.tentativa = 0

    def _guardar_timer(self, origem):
        """Todo timeout criado pela aba passa por aqui.

        Um GLib.timeout que sobrevive ao fechamento da aba continua chamando
        metodos de widgets ja destruidos — e a causa classica de travamento
        depois de abrir e fechar muitas sessoes."""
        self._timers.add(origem)
        return origem

    def _agendar(self, intervalo, callback, segundos=False):
        """Registra um timeout e SOZINHO tira o proprio id de _timers quando
        o callback termina (retorna False/None).

        Sem isto, um timer que dispara e se auto-remove (comportamento
        normal do GLib quando o callback retorna False) continuava na lista
        ate o fechamento da aba, e o _matar_timers() tentava remover um id
        que o GLib ja tinha descartado — dai o aviso inofensivo
        'Source ID N was not found when attempting to remove it', que so
        aparecia no terminal e nao travava nada, mas sujava o log a toa."""
        caixa = {}

        def encapsulado(*a):
            # try/finally e essencial: se o callback LEVANTA EXCECAO, o GLib
            # descarta a source do mesmo jeito, mas sem isto o discard nunca
            # rodava e o id ficava orfao em _timers. Ao fechar a aba, o
            # _matar_timers ia remover um id que ja nao existia — origem do
            # 'Source ID N was not found'. Sob G_DEBUG=fatal-criticals esse
            # aviso vira abort, ou seja: a aplicacao caindo ao fechar aba.
            #
            # Note que a solucao NAO e o _matar_timers deixar de remover
            # (ja tentamos: pular a remocao deixa timers VIVOS depois da
            # aba morrer, e isso vira SIGSEGV). A solucao e nao produzir id
            # orfao em primeiro lugar, que e o que este finally faz.
            continuar = False
            try:
                continuar = callback(*a)
                return continuar
            finally:
                if not continuar:
                    caixa["terminou"] = True
                    self._timers.discard(caixa.get("id"))

        fn = GLib.timeout_add_seconds if segundos else GLib.timeout_add
        tid = fn(intervalo, encapsulado)
        # ORDEM IMPORTA: se o callback ja tiver rodado antes desta linha, o
        # discard dele nao achou id nenhum em caixa e o add abaixo
        # RESSUSCITARIA um id morto na lista. Por isso o encapsulado usa
        # caixa.get("id") e nos so adicionamos se ele ainda nao terminou.
        caixa["id"] = tid
        if caixa.get("terminou") is not True:
            self._timers.add(tid)
        return tid

    def _cancelar_timer(self, tid):
        """Cancela um timer criado pelo _agendar e TIRA o id de _timers.

        Era esta a segunda fonte de 'Source ID N was not found': os timers
        nascem pelo _agendar (que os registra em _timers), mas alguns eram
        cancelados chamando GLib.source_remove direto no atributo. A source
        morria e o id continuava na lista — zumbi. Ao fechar a aba, o
        _matar_timers ia remover algo que ja nao existia, e sob
        G_DEBUG=fatal-criticals isso vira abort na cara do operador.

        Cancelar SEMPRE por aqui mantem lista e realidade em sincronia."""
        if not tid:
            return None
        self._timers.discard(tid)
        try:
            GLib.source_remove(tid)
        except Exception:
            pass
        return None

    def _matar_timers(self):
        for t in list(self._timers):
            try:
                GLib.source_remove(t)
            except Exception:
                pass
        self._timers.clear()

    def conectar(self):
        raise NotImplementedError

    def reconectar(self):
        raise NotImplementedError

    def desconectar(self):
        self.fechando = True
        self._cancelar_auto()
        self._matar_timers()


class AbaVnc(AbaBase, CapturaTeclado):
    tipo = "vnc"
    # segundos para decidir se a porta VNC esta aceitando conexao. Curto de
    # proposito: em LAN o PDV responde em milissegundos (ping <1ms), e o que
    # nao responder nesse prazo esta desligado ou bloqueado.
    TIMEOUT_SONDA = 2.0

    def __init__(self, conexao, janela):
        super().__init__(conexao, janela)
        self.escalando = None
        self.remoto = (0, 0)
        self.pendente = False
        self.adiada = False
        # DISPLAY DO __init__ E SEMPRE FRIO.
        #
        # Ele e criado aqui so para a aba ter conteudo montado, e fica
        # parado ate a conexao comecar — mesmo no caminho de clique
        # esquerdo, onde o conectar() vem logo em seguida por um idle.
        # Conectar nesse widget parado e o que ainda derrubava a aplicacao
        # ao abrir; pelo clique do MEIO nao acontecia justamente porque
        # aquele caminho ja marcava _display_frio e o conectar() trocava o
        # widget antes de abrir a conexao. Marcando aqui, os dois caminhos
        # passam a fazer a mesma coisa: primeira conexao sempre em display
        # recem-criado.
        self._display_frio = True
        self._ultimo_recebido = None
        self._ultimo_enviado = None
        self._timer_vigia = None

        self.bt_auto.set_active(self.cx.auto)
        self._ligar_auto("auto")

        # sem label no construtor: quem monta o filho e o _sync_olho. Com o
        # emoji aqui, o primeiro desenho ja saia com a altura dele e a barra
        # nascia grande antes da primeira troca de estado.
        self.bt_olho = Gtk.ToggleButton()
        add_class(self.bt_olho, "tog", "tog-bloq", "tog-glifo")
        self.bt_olho.set_active(self.cx.ronly)
        self.bt_olho.connect("toggled", self._trocar_ronly)
        self.barra.pack_end(self.bt_olho, False, False, 0)

        self.bt_teclado = Gtk.ToggleButton(label="⌨")
        add_class(self.bt_teclado, "tog", "tog-ok", "tog-glifo")
        self.bt_teclado.set_tooltip_text(
            "Capturar o teclado. Sob XWayland pega o que o compositor não "
            "reserva (Ctrl+W, Ctrl+T, F-keys); Super e Alt+Tab continuam do "
            "sistema — para esses, use o menu ⌁. Pause libera."
            if _backend_x11() else
            "Capturar o teclado: Super, Alt+Tab e afins vão para a máquina "
            "remota. Pause libera.")
        self.bt_teclado.connect("toggled", self._trocar_teclado)
        self.barra.pack_end(self.bt_teclado, False, False, 0)

        self.bt_transf = Gtk.ToggleButton(label="⇄")
        add_class(self.bt_transf, "tog", "tog-ok", "tog-glifo")
        self.bt_transf.set_tooltip_text(
            "⇄ Área de transferência sincronizada automaticamente nos dois "
            "sentidos. Use Ctrl+C e Ctrl+V normais.")
        self.bt_transf.set_active(True)
        self.barra.pack_end(self.bt_transf, False, False, 0)

        bt_rec = add_class(Gtk.Button(label="Reconectar"), "secundaria")
        bt_rec.set_tooltip_text("Derruba e refaz a sessão sem fechar a aba")
        bt_rec.connect("clicked", lambda _b: self.reconectar(manual=True))
        self.barra.pack_end(bt_rec, False, False, 0)

        bt_env = add_class(Gtk.Button(label="⌁"), "secundaria", "tog-glifo")
        bt_env.set_tooltip_text("Enviar combinação de teclas à máquina remota "
                                "(Ctrl+Alt+Del, Alt+F4, Super…)")
        bt_env.connect("clicked", self._menu_teclas)
        self.barra.pack_end(bt_env, False, False, 0)

        if self.cx.tem_ssh:
            bt = add_class(Gtk.Button(label="Shell"), "secundaria")
            bt.set_tooltip_text("Abrir SSH em %s" % self.cx.destino_ssh)
            bt.connect("clicked", lambda _b: self.janela.abrir(self.cx, "ssh"))
            self.barra.pack_end(bt, False, False, 0)

        # segmentado no lugar do ComboBox, que ignorava o tema
        self.seg, self.seg_botoes = segmentado(
            [("encaixar", "Encaixar"), ("1x1", "1:1"), ("dinamico", "Dinâmico")],
            self.cx.modo, self._trocar_modo)
        self.barra.pack_end(self.seg, False, False, 0)

        self.palco = Gtk.ScrolledWindow()
        self.palco.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
        self.palco.set_propagate_natural_width(False)
        self.palco.set_propagate_natural_height(False)
        self.palco.set_size_request(1, 1)
        add_class(self.palco, "palco")
        self.suporte = add_class(Gtk.Box(), "palco")
        self.suporte.set_halign(Gtk.Align.CENTER)
        self.suporte.set_valign(Gtk.Align.CENTER)
        self.palco.add(self.suporte)
        self.pack_start(self.palco, True, True, 0)
        self.pack_start(self.exp_log, False, False, 0)

        self.display = None
        self._novo_display()
        self.clip = Gtk.Clipboard.get(Gdk.SELECTION_CLIPBOARD)
        # O Gtk.Clipboard e um objeto GLOBAL: a conexao nao morre com a aba.
        # Sem guardar o id e desconectar no fim, cada sessao aberta deixa um
        # handler vivo apontando para widgets destruidos.
        self._sinal_clip = self.clip.connect("owner-change",
                                             self._on_dono_transf)
        self.connect("destroy", self._desligar_clip)
        self._sync_olho()
        self._iniciar_captura()

    def _novo_display(self):
        """Um VncDisplay fechado nao reabre de forma confiavel entre builds.
        Para reconectar, troca-se o widget."""
        if self.display is not None:
            # DESCONECTAR ANTES DE DESTRUIR: o widget continua emitindo
            # sinais durante a propria destruicao, e eles caiam em metodos
            # que tocam widgets ja mortos. Era a instabilidade que so
            # aparecia depois de varias reconexoes e fechamentos de aba —
            # o travamento que derrubava o programa inteiro.
            for hid in getattr(self, "_sinais_display", []):
                try:
                    self.display.disconnect(hid)
                except Exception:
                    pass
            self._sinais_display = []
            try:
                self.display.close()
            except Exception:
                pass
            # NAO destruir aqui: updates de framebuffer ja enfileirados
            # ainda vao rodar e o gtk-vnc precisa da GdkWindow viva.
            # Ver aposentar_display().
            aposentar_display(self.display)
            self.display = None
        d = VncWidget()
        d.set_force_size(False)
        d.set_keep_aspect_ratio(True)
        # TESTE DE LATENCIA DE INPUT (pointer_local).
        #
        # Estava False. Nesse modo o gtk-vnc NAO desenha o cursor
        # localmente: cada movimento depende do servidor devolver a posicao
        # e a forma do cursor, o que soma um ida-e-volta por evento de
        # ponteiro. Sintoma relatado: atraso no primeiro clique/tecla logo
        # apos uma repintura grande (abrir uma funcao de digitacao dentro do
        # PDV), com mouse e teclado afetados juntos. Nao era rede (ping
        # <1ms), nem escala (identico em 1x1), nem o PDV (Remmina, que usa
        # libvncclient, responde instantaneo no mesmo cenario).
        #
        # EFEITO COLATERAL POSSIVEL: com alguns servidores aparecem DOIS
        # cursores (o local e o desenhado pelo servidor no framebuffer). Se
        # isso acontecer, voltar para False e investigar por outro lado.
        d.set_pointer_local(True)
        d.set_read_only(self.bt_olho.get_active() if hasattr(self, "bt_olho")
                        else self.cx.ronly)
        d.set_can_focus(True)
        # FOCO SEGUE O PONTEIRO dentro do palco.
        #
        # O gtk-vnc so encaminha input quando o widget tem foco. Antes, o
        # unico caminho para obter foco era o CLIQUE (_on_clique), e o
        # resultado era: passar o mouse sobre a tela remota nao produzia
        # hover nenhum, e o primeiro clique "demorava" — na verdade ele era
        # gasto adquirindo foco, e so o seguinte agia de fato. Bastava o
        # foco ter ido para a lateral, para o filtro ou para outra aba.
        #
        # Com enter-notify pegando o foco, mover o ponteiro para dentro do
        # palco ja habilita movimento e teclado, que e como o Remmina se
        # comporta e o que o operador espera.
        d.add_events(Gdk.EventMask.ENTER_NOTIFY_MASK)
        d.connect("enter-notify-event", self._on_entrar_palco)
        for metodo, valor in (("set_shared_flag", True),
                              # LOSSY FICA DESLIGADO. Ja foi testado ligado,
                              # na tentativa de encurtar o congelamento de
                              # 2-3s em repinturas grandes: NAO resolveu, e
                              # ainda piorou qualidade e estabilidade.
                              # Coerente com o diagnostico — o Remmina
                              # (libvncclient) e fluido em qualidade cheia,
                              # entao o gargalo nao e volume de dados.
                              ("set_lossy_encoding", False)):
            if hasattr(d, metodo):
                getattr(d, metodo)(valor)
        # os ids sao guardados para poder DESCONECTAR antes de destruir
        self._sinais_display = []
        # SO OS SINAIS QUE O VncWidget EMITE.
        #
        # vnc-auth-credential e vnc-auth-unsupported eram do gtk-vnc e sairam
        # com ele: aqui as credenciais sao definidas ANTES do open_host, pois
        # a libvncclient as pede por callback dentro da thread de rede.
        #
        # O except TypeError abaixo mascarava isso — mas o erro vinha antes,
        # ao AVALIAR self._on_credencial, que nao existe mais. Por isso a aba
        # nem chegava a abrir. Agora o except avisa em vez de silenciar.
        for sinal, fn in (
                ("vnc-connected", self._on_conectado),
                ("vnc-initialized", self._on_iniciado),
                ("vnc-disconnected", self._on_desconectado),
                ("vnc-desktop-resize", self._on_resize_remoto),
                ("vnc-error", self._on_erro),
                ("vnc-auth-failure", self._on_auth_falhou),
                ("vnc-server-cut-text", self._on_texto_remoto)):
            try:
                self._sinais_display.append(d.connect(sinal, fn))
            except TypeError as e:
                sys.stderr.write("vnc: sinal %r não ligado: %s\n"
                                 % (sinal, e))
        self._sinais_display.append(
            d.connect("button-press-event", self._on_clique))
        self.suporte.pack_start(d, True, True, 0)
        d.show()
        self.display = d
        self.escalando = None
        self.remoto = (0, 0)
        if not hasattr(self, "_alocar_ligado"):
            self.palco.connect("size-allocate", self._on_alocar)
            # Abrindo em segundo plano a aba nunca foi mapeada: _reavaliar
            # desiste no get_mapped() e o escalonamento nunca e aplicado,
            # entao o widget fica preto ate voce mexer no modo. Reavaliar no
            # map-event e o que faltava.
            self.connect("map-event", self._on_mapeou)
            self._alocar_ligado = True

    def conectar(self):
        # SONDAGEM ANTES DE CONECTAR.
        #
        # Nunca entregar host morto ao gtk-vnc. Ele deixa uma corrotina
        # pendurada no connect por ~2 min, e a partir dai qualquer coisa que
        # mexa no display (religa automatico, trocar de aba, fechar a aba)
        # vira crash. Um socket comum decide isso em 2s, e so passamos
        # adiante o que ja provou estar aceitando conexao.
        self._estado("AGUARDE", "neutro", "testando %s…" % self.cx.destino)
        self.reg("sondando %s (porta %s)"
                 % (self.cx.host, self.cx.porta))
        host, porta = self.cx.host, self.cx.porta
        self._sondando = (host, porta)
        sondar_porta(host, porta, self.TIMEOUT_SONDA,
                     lambda ok, erro: self._sonda_respondeu(ok, erro,
                                                            host, porta))

    def _sonda_respondeu(self, ok, erro, host, porta):
        """Volta da sonda, ja no thread principal."""
        # o mundo pode ter mudado enquanto a sonda corria
        if self.fechando:
            return
        if getattr(self, "_sondando", None) != (host, porta):
            return                      # outra tentativa comecou; esta e velha
        self._sondando = None

        if not ok:
            self.reg("sem resposta em %s:%s — %s" % (host, porta,
                                                     erro or "inalcançável"))
            self._estado("SEM RESPOSTA", "erro", erro or "host inalcançável")
            # Nao chamamos open_host: e justamente o que travava. O religa
            # automatico, se ligado, tenta de novo mais tarde; senao fica
            # aguardando o botao Reconectar.
            if self.bt_auto.get_active():
                self._agendar_auto("host não respondeu")
            return

        self._abrir_conexao()

    def _abrir_conexao(self):
        # DISPLAY FRIO: o widget criado no __init__ fica parado ate a
        # conexao comecar — segundos, no clique esquerdo; minutos, quando a
        # aba foi aberta em segundo plano e so recebe foco depois. Abrir a
        # conexao nesse widget parado dava "Server closed the connection" na
        # primeira tentativa e, em alguns casos, derrubava a aplicacao.
        #
        # A prova estava no proprio comportamento: a retentativa automatica
        # funcionava porque passa por reconectar(), que chama
        # _novo_display() antes. Fazemos o mesmo aqui, entao a PRIMEIRA
        # tentativa tambem sai num display recem-criado, venha ela do
        # clique esquerdo ou do clique do meio.
        if getattr(self, "_display_frio", False):
            self._display_frio = False
            self._novo_display()
        if self.display is None:
            return

        # CREDENCIAIS ANTES DE CONECTAR (backend libvncclient).
        #
        # No gtk-vnc as credenciais chegavam pelo sinal vnc-auth-credential,
        # em pleno handshake, e eram respondidas com set_credential(). A
        # libvncclient pede por callback JA DENTRO da thread de rede, entao
        # nao ha oportunidade de perguntar ao operador naquele instante:
        # elas precisam estar definidas antes.
        #
        # Se a conexao tem senha gravada, usamos. Se nao tem, perguntamos
        # aqui — antes de abrir — em vez de no meio do handshake.
        senha = self.cx.senha
        if not senha:
            senha = self.janela.pedir_senha(self.cx.nome, self.cx.destino)
            if senha is None:
                self._estado("CANCELADO", "neutro", "senha não informada")
                self.reg("conexão cancelada: senha não informada")
                return
        self.display.definir_credenciais(
            usuario=self.cx.usuario or None, senha=senha)

        self._estado("AGUARDE", "neutro", "conectando em %s…" % self.cx.destino)
        self.reg("abrindo %s (usuario=%s, senha=%s)" % (
            self.cx.destino, self.cx.usuario or "<nenhum>",
            "<definida>" if self.cx.senha else "<vazia>"))
        self.display.open_host(self.cx.host, self.cx.porta)
        if self._timer_vigia:
            self._timer_vigia = self._cancelar_timer(self._timer_vigia)
        self._timer_vigia = self._agendar(15, self._vigia, segundos=True)

    def reconectar(self, manual=False):
        if manual:
            self._inicio_manual()
            self.tentativa = 0
            self.reg("reconexão manual")
        self._grab(False)
        self._novo_display()
        self.conectar()

    def adiar_ate_focar(self):
        """Mesmo tratamento que a AbaRdp ja recebia, agora tambem no VNC.

        O motivo aqui e outro, e mais grave que tela preta: abrindo em
        segundo plano, o codigo antigo trocava para a aba nova, chamava
        conectar() e VOLTAVA para a aba anterior no mesmo ciclo. O
        open_host() disparava e o display era desmapeado logo em seguida,
        com a corrotina do gtk-vnc recem-iniciada — e ela ia atras de um
        widget que acabara de sair de baixo dela:
            coroutine_yieldto: assertion '!to->caller' failed
        na hora da abertura, uma vez por aba aberta com o clique do meio.
        Acumulando varias, o estado de corrotina corrompia e o processo
        morria com SIGSEGV.

        Adiando, o open_host() so acontece com a aba mapeada e estavel:
        nao ha desmapeamento no meio do handshake."""
        self.adiada = True
        # o display criado no __init__ vai ficar parado ate o foco chegar;
        # marcado como frio, sera trocado por um novo no conectar()
        self._display_frio = True
        self._estado("EM ESPERA", "neutro", "conecta ao entrar na aba")
        self.reg("aberta em segundo plano: conexão adiada até a aba receber "
                 "foco, para não desmapear o display no meio do handshake")

    def _vigia(self):
        self._timer_vigia = None
        if self.fechando:
            return False
        if self.chip_estado.get_text() in ("AGUARDE", "CONECTADO"):
            self.reg("15s sem inicializar — handshake travado")
            self._estado("TRAVADO", "atencao", "handshake sem resposta")
            self._agendar_auto("handshake travado")
        return False

    def desconectar(self):
        super().desconectar()
        self._grab(False)
        self._desligar_clip()
        # mesma razao do _novo_display: sinal chegando em widget que ja
        # esta sendo desmontado derruba o processo
        for hid in getattr(self, "_sinais_display", []):
            try:
                self.display.disconnect(hid)
            except Exception:
                pass
        self._sinais_display = []
        # invalida qualquer sonda em curso: seu retorno nao deve abrir
        # conexao numa aba que ja esta sendo desmontada
        self._sondando = None
        try:
            self.display.close()
        except Exception:
            pass
        # Aposentar TAMBEM aqui, e nao so no _novo_display. Este e o caminho
        # de FECHAR A ABA: logo apos desconectar(), o fechar() chama
        # remove_page(), que destroi a pagina inteira — e o display junto,
        # com updates de framebuffer possivelmente ainda enfileirados. Era
        # exatamente onde o processo caia com
        #   gdk_window_get_width: assertion 'GDK_IS_WINDOW (window)' failed
        # Tirando o widget da pagina antes, ele morre no seu proprio tempo,
        # no cemiterio, ja fora da hierarquia que esta sendo desmontada.
        aposentar_display(self.display)
        self.display = None

    def _on_conectado(self, _d):
        self._estado("CONECTADO", "atencao", "negociando…")
        self.reg("TCP estabelecido, aguardando handshake RFB")

    def _on_iniciado(self, d):
        if self.fechando or d is not self.display:
            return
        self._estado("ATIVO", "ok", self.cx.destino)
        self.reg("sessao inicializada")
        self.sucesso()
        if self.cx.modo == "dinamico":
            self._ligar_dinamico(True)
        self._aplicar_ronly()
        if self.bt_teclado.get_active():
            self._trocar_teclado(self.bt_teclado)
        self.display.grab_focus()
        self._reavaliar(forcar=True)

    def _on_desconectado(self, d):
        # _novo_display() troca o widget; o antigo ainda emite sinais durante
        # a destruicao e sobrescreveria o estado da conexao nova
        if self.fechando or d is not self.display:
            return
        self._estado("ENCERRADO", "erro", "sessão encerrada")
        self._agendar_auto("sessão caiu")

    def _on_erro(self, d, msg):
        if self.fechando or d is not self.display:
            return
        self._estado("ERRO", "erro", msg)
        self.reg("erro: %s" % msg)

    def _on_auth_falhou(self, _d, msg):
        self._estado("AUTH", "erro", "autenticação recusada")
        self.reg("autenticacao recusada: %s" % msg)
        self._cancelar_auto()          # senha errada nao melhora com insistencia
        self.bt_auto.set_active(False)


    def _on_entrar_palco(self, _w, _ev):
        """Ponteiro entrou no palco: garante foco para o input fluir."""
        if self.fechando or self.display is None:
            return False
        if self.chip_estado.get_text() != "ATIVO":
            return False
        if not self.display.has_focus():
            self.display.grab_focus()
        return False        # nao consome: o gtk-vnc tambem quer este evento

    def _on_clique(self, _w, ev):
        if self.display is None:
            return False
        self.display.grab_focus()
        if ev.button == 2:
            # clique do meio no palco: atalho para reconectar
            self.reconectar(manual=True)
            return True
        return False

    # ---- transferencia
    def _on_texto_remoto(self, _d, texto):
        if not self.bt_transf.get_active() or texto is None:
            return
        if texto == self._ultimo_enviado:
            return
        self._ultimo_recebido = texto
        self.clip.set_text(texto, -1)
        self.reg("área de transferência recebida (%d car.)" % len(texto))

    def _enviar_transf(self):
        if not self.bt_transf.get_active() or self.fechando:
            return False
        # SESSAO PRECISA ESTAR INICIALIZADA. O Gtk.Clipboard e GLOBAL: uma
        # copia qualquer no sistema dispara owner-change em TODAS as abas
        # abertas, inclusive nas que ainda estao no handshake RFB. Mandar
        # client_cut_text antes de o handshake terminar faz o gtk-vnc
        # derrubar a conexao — no log aparecia como um
        # "Unable to connect ... Connection timed out" INSTANTANEO, no
        # mesmo segundo do "abrindo", logo depois do "área de transferência
        # enviada". Nao era timeout nenhum: era a sessao sendo cortada.
        # E a queda dispara o religa automatico, que e onde a aplicacao
        # costumava travar.
        if self.chip_estado.get_text() != "ATIVO":
            return False
        if self.display is None:
            return False
        if self.bt_olho.get_active():
            return False           # somente visualizacao nao escreve no remoto
        # ASSINCRONO, obrigatoriamente. wait_for_text() e SINCRONO: ele pede
        # o conteudo ao dono atual do clipboard e roda um loop de eventos
        # ANINHADO ate a resposta chegar. Se o dono demora a responder
        # (navegador, app Electron, algo via XWayland), a interface inteira
        # congela nesse meio tempo.
        #
        # Como isto e chamado pelo redesenhar() — ou seja, toda vez que voce
        # ENTRA na aba —, o efeito pratico era um atraso no PRIMEIRO clique
        # ou primeira tecla depois de trocar de sessao, atingindo mouse e
        # teclado igualmente (nao e input lento: e a UI parada). Depois do
        # primeiro, tudo normal. O Remmina nao faz essa consulta neste
        # momento, e por isso nao apresentava o sintoma.
        self.clip.request_text(self._recebeu_clip_local, None)
        return False

    def _recebeu_clip_local(self, _clip, texto, _dados):
        """Continuacao do _enviar_transf, agora que o texto chegou.

        Reavalia as condicoes: entre o pedido e a resposta o operador pode
        ter trocado de aba, fechado a sessao ou a conexao pode ter caido."""
        if self.fechando or self.display is None:
            return
        if not self.bt_transf.get_active() or self.bt_olho.get_active():
            return
        if self.chip_estado.get_text() != "ATIVO":
            return
        if not texto or texto == self._ultimo_recebido:
            return
        if texto == self._ultimo_enviado:
            return                 # ja subiu; nao repete
        if not hasattr(self.display, "client_cut_text"):
            self.reg("build sem client_cut_text")
            return
        self._ultimo_enviado = texto
        try:
            self.display.client_cut_text(texto)
            self.reg("área de transferência enviada (%d car.)" % len(texto))
        except Exception as e:
            self.reg("falha ao enviar: %s" % e)

    def _desligar_clip(self, *_a):
        if getattr(self, "_sinal_clip", None):
            try:
                self.clip.disconnect(self._sinal_clip)
            except Exception:
                pass
            self._sinal_clip = None

    def _on_dono_transf(self, _clip, _ev):
        """A area de transferencia local mudou de dono.

        Sem atalho proprio: voce faz Ctrl+C aqui, o conteudo sobe sozinho, e
        dentro da sessao voce usa Ctrl+V normal. Ctrl+C e Ctrl+V nao sao mais
        interceptados — vao inteiros para a maquina remota, como qualquer
        outra tecla."""
        if self.fechando or not self.bt_transf.get_active():
            return
        # so a aba visivel empurra: varias sessoes abertas disputando o
        # clipboard produziriam colagens do host errado
        if not self.get_mapped():
            return
        GLib.idle_add(self._enviar_transf)

    # ---- credenciais
    @staticmethod
    def _desempacotar(creds):
        try:
            bruto = list(creds)
        except TypeError:
            # GValueArray so expoe get_nth, que o PyGObject marca como
            # depreciado sem oferecer substituto para este sinal. O aviso e
            # ruido: silenciado no ponto exato, nao globalmente.
            n = getattr(creds, "n_values", 0)
            with warnings.catch_warnings():
                warnings.simplefilter("ignore", DeprecationWarning)
                bruto = [creds.get_nth(i) for i in range(n)]
        saida = []
        for c in bruto:
            if hasattr(c, "get_value"):
                c = c.get_value()
            try:
                saida.append(int(c))
            except (TypeError, ValueError):
                pass
        return saida


    def _sync_olho(self):
        # so o glifo: o estado ja e dito pela cor e pelo tooltip
        travado = self.bt_olho.get_active()
        # ICONE SIMBOLICO, nao emoji.
        #
        # O glifo 👁 e mais ALTO que o 🚫 na fonte de emoji, e a altura do
        # botao acompanha a do texto: a barra inteira crescia ao liberar a
        # entrada e encolhia ao bloquear. Depende da fonte de emoji da
        # distro, por isso acontecia no Arch e nao no Fedora.
        #
        # set_size_request nao resolve sozinho: ele define o MINIMO, e um
        # glifo mais alto passa por cima. Icone simbolico tem tamanho em
        # pixel definido por nos e nao depende de fonte nenhuma. O emoji
        # fica so como ultimo recurso.
        nomes = (("changes-prevent-symbolic", "action-unavailable-symbolic")
                 if travado else
                 ("changes-allow-symbolic", "view-reveal-symbolic"))
        img = None
        tema_ic = Gtk.IconTheme.get_default()
        for nome in nomes:
            try:
                if tema_ic.has_icon(nome):
                    img = Gtk.Image.new_from_icon_name(nome,
                                                       Gtk.IconSize.MENU)
                    img.set_pixel_size(14)
                    break
            except Exception:
                continue
        filho = self.bt_olho.get_child()
        if filho is not None:
            filho.destroy()
        if img is not None:
            img.show()
            self.bt_olho.add(img)
        else:
            lb = Gtk.Label(label="🚫" if travado else "👁")
            lb.show()
            self.bt_olho.add(lb)
        # trava largura E altura, e centra: assim nem o fallback de emoji
        # consegue esticar a barra
        self.bt_olho.set_size_request(28, 20)
        self.bt_olho.set_valign(Gtk.Align.CENTER)
        self.bt_olho.set_tooltip_text(
            "Entrada BLOQUEADA — clique para liberar teclado e mouse"
            if travado else
            "Entrada liberada — clique para bloquear (somente visualização)")

    def _aplicar_ronly(self):
        travado = self.bt_olho.get_active()
        self.display.set_read_only(travado)
        if not travado:
            self.display.grab_focus()

    def _trocar_ronly(self, _b):
        self._sync_olho()
        self._aplicar_ronly()
        if self.bt_olho.get_active() and self.bt_teclado.get_active():
            self.bt_teclado.set_active(False)
        self.cx.ronly = self.bt_olho.get_active()
        self.janela.gravar(self.cx.nome, "ronly", "1" if self.cx.ronly else "0")

    def _trocar_teclado(self, bt):
        """Captura de teclado. Duas camadas: GdkSeat (funciona em Wayland) e
        o grab do proprio gtk-vnc (X11)."""
        ligado = bt.get_active()
        if self.display is None:
            return
        if ligado and self.bt_olho.get_active():
            self.reg("somente visualização: captura não faz sentido")
            bt.set_active(False)
            return
        for metodo in ("set_keyboard_grab", "set_pointer_grab"):
            if hasattr(self.display, metodo):
                try:
                    getattr(self.display, metodo)(ligado)
                except Exception as e:
                    self.reg("%s falhou: %s" % (metodo, e))
        if ligado:
            # o foco tem de estar no display ANTES do grab, senao ele fica no
            # botao que voce acabou de clicar e as teclas somem
            self.display.grab_focus()
        ok = self._grab(ligado)
        # O grab do widget (GdkSeat, o mesmo mecanismo que o RDP usa) vale
        # por si: quando o GrabNativo nao encontra libX11 nem o inibidor do
        # Wayland, ele falha, mas o widget ainda captura. Antes o log dizia
        # "captura falhou" mesmo com o teclado funcionando na sessao.
        pelo_widget = bool(getattr(self.display, "_seat_grab", None))
        if ligado and not ok and pelo_widget:
            self.reg("teclado capturado pelo widget — Super e Alt+Tab podem "
                     "continuar com o compositor")
        else:
            self.reg("teclado %s%s" % (
                "capturado — atalhos globais vão para a máquina remota"
                if ligado else "liberado",
                "" if (ok or not ligado)
                else " (o compositor recusou o inibidor)"))

    # Injecao explicita pelo protocolo RFB: independe de grab e de
    # compositor, o cliente monta o evento e manda direto pro servidor VNC,
    # que repassa como entrada de teclado real na maquina — Windows incluso.
    # Confirmado pelo Jura: o combo literal Ctrl+Alt+Del chega certinho.
    COMBINACOES = [
        ("Ctrl+Alt+Del",  [Gdk.KEY_Control_L, Gdk.KEY_Alt_L, Gdk.KEY_Delete]),
        ("Alt+F4",        [Gdk.KEY_Alt_L, Gdk.KEY_F4]),
        ("Alt+Tab",       [Gdk.KEY_Alt_L, Gdk.KEY_Tab]),
        ("Ctrl+Esc  (menu Iniciar)", [Gdk.KEY_Control_L, Gdk.KEY_Escape]),
        ("Super",         [Gdk.KEY_Super_L]),
        ("Ctrl+Shift+Esc", [Gdk.KEY_Control_L, Gdk.KEY_Shift_L, Gdk.KEY_Escape]),
        ("Ctrl+Alt+F1",   [Gdk.KEY_Control_L, Gdk.KEY_Alt_L, Gdk.KEY_F1]),
        ("Ctrl+Alt+F2",   [Gdk.KEY_Control_L, Gdk.KEY_Alt_L, Gdk.KEY_F2]),
        ("PrintScreen",   [Gdk.KEY_Print]),
    ]

    def _menu_teclas(self, botao):
        menu = Gtk.Menu()
        travado = self.bt_olho.get_active()
        for rotulo_txt, teclas in self.COMBINACOES:
            mi = Gtk.MenuItem(label=rotulo_txt)
            mi.set_sensitive(not travado)
            mi.connect("activate", lambda _m, t=teclas, r=rotulo_txt:
                       self._enviar_teclas(t, r))
            menu.append(mi)
        if travado:
            menu.append(Gtk.SeparatorMenuItem())
            mi = Gtk.MenuItem(label="(desbloqueie o olho para enviar)")
            mi.set_sensitive(False)
            menu.append(mi)
        menu.show_all()
        menu.popup_at_widget(botao, Gdk.Gravity.SOUTH_WEST,
                             Gdk.Gravity.NORTH_WEST, None)

    def _enviar_teclas(self, teclas, rotulo_txt):
        """Injeta a combinacao pelo protocolo RFB.

        Tres condicoes tinham de ser satisfeitas e nenhuma estava garantida:
        a sessao precisa estar inicializada, a entrada nao pode estar em
        somente-leitura, e o widget precisa ter foco — sem foco o gtk-vnc
        descarta o envio calado."""
        if self.chip_estado.get_text() != "ATIVO":
            self.reg("sessão não está ativa; nada enviado")
            return
        if self.bt_olho.get_active():
            self.reg("entrada bloqueada pelo olho; nada enviado")
            return
        self.display.grab_focus()

        # enviar_combinacao pressiona na ordem dada e solta na inversa. Era
        # precedido de um caminho alternativo com send_keys_ex e os enums do
        # GtkVnc; saiu junto com o backend antigo.
        try:
            self.display.enviar_combinacao(list(teclas))
            self.reg("enviado: %s (%s)" % (
                rotulo_txt, " ".join(Gdk.keyval_name(k) or str(k)
                                     for k in teclas)))
        except Exception as e:
            self.reg("falha ao enviar %s: %s" % (rotulo_txt, e))

    # ---- dimensionamento
    def _ligar_dinamico(self, valor):
        if hasattr(self.display, "set_allow_resize"):
            self.display.set_allow_resize(valor)
            return True
        return False

    def _trocar_modo(self, novo):
        if novo == self.cx.modo:
            return
        if novo == "dinamico" and not self._ligar_dinamico(True):
            self.janela.avisar("Modo dinâmico indisponível",
                               "Esta build do gtk-vnc não expõe "
                               "set_allow_resize(). Voltando para Encaixar.")
            self.seg_botoes["encaixar"].set_active(True)
            return
        self.cx.modo = novo
        if novo != "dinamico":
            self._ligar_dinamico(False)
        self.janela.gravar(self.cx.nome, "modo", novo)
        self._reavaliar(forcar=True)

    def _on_resize_remoto(self, _d, w, h):
        self.remoto = (w, h)
        self.reg("tela remota agora %dx%d" % (w, h))
        self._agendar_reavaliacao()

    def _on_mapeou(self, *_a):
        self.escalando = None          # obriga a recalcular do zero
        GLib.idle_add(self._reavaliar, True, priority=GLib.PRIORITY_LOW)
        return False

    def redesenhar(self):
        """Chamado ao entrar na aba."""
        self.escalando = None
        self._reavaliar(forcar=True)
        # o que voce copiou enquanto estava noutra aba sobe agora
        if self.bt_transf.get_active():
            GLib.idle_add(self._enviar_transf)
        if self.display is not None:
            # em segundo plano o widget descarta as areas sujas (nao pinta o
            # que ninguem ve), entao ao voltar e preciso repintar tudo
            if hasattr(self.display, "redesenhar_tudo"):
                self.display.redesenhar_tudo()
            else:
                self.display.queue_draw()
            # foco de volta ao palco ao entrar na aba: sem isto o teclado so
            # passava a funcionar depois de um clique (ver _on_entrar_palco)
            if (not self.fechando
                    and self.chip_estado.get_text() == "ATIVO"
                    and not self.display.has_focus()):
                self.display.grab_focus()
        return False

    def _on_alocar(self, _w, _a):
        self._agendar_reavaliacao()

    def _agendar_reavaliacao(self):
        """Nome proprio, sem conflito: existia um metodo chamado _agendar()
        aqui (sem argumentos, so para a reavaliacao de escala) que colidia
        com o AbaBase._agendar(intervalo, callback, segundos=False) —
        a versao daqui vencia por definicao mais recente na classe, e
        qualquer chamada com argumentos (ex.: self._agendar(15, self._vigia,
        segundos=True)) quebrava com TypeError."""
        if self.pendente:
            return
        self.pendente = True
        GLib.idle_add(self._reavaliar, priority=GLib.PRIORITY_DEFAULT_IDLE)

    def _reavaliar(self, forcar=False):
        self.pendente = False
        if getattr(self, "_reavaliando", False):
            return False
        if not self.get_mapped() or self.display is None or self.fechando:
            return False
        self._reavaliando = True
        try:
            return self._reavaliar_agora(forcar)
        finally:
            self._reavaliando = False

    def _reavaliar_agora(self, forcar):
        rw, rh = self.remoto
        if rw <= 0 or rh <= 0:
            return False
        al = self.palco.get_allocation()
        escalar = False if self.cx.modo == "1x1" else (rw > al.width or rh > al.height)
        if escalar == self.escalando and not forcar:
            return False
        self.escalando = escalar
        self.display.set_scaling(escalar)
        if escalar:
            self.suporte.set_halign(Gtk.Align.FILL)
            self.suporte.set_valign(Gtk.Align.FILL)
            self.display.set_size_request(-1, -1)
            self.palco.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.NEVER)
        else:
            self.suporte.set_halign(Gtk.Align.CENTER)
            self.suporte.set_valign(Gtk.Align.CENTER)
            self.display.set_size_request(rw, rh)
            self.palco.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
        self.lb_geo.set_text("%dx%d %s" % (rw, rh,
                                           "reduzido" if escalar else "1:1"))
        return False




class AbaRdpEmbutido(AbaBase, CapturaTeclado):
    """RDP como widget GTK de verdade, via gtk-frdp.

    POR QUE EXISTE, ao lado da AbaRdp
    ---------------------------------
    A AbaRdp lanca o xfreerdp e o reparenta num Gtk.Socket. Isso depende de
    XEmbed, que so existe em X11 — em Wayland nativo o RDP vira janela
    separada.

    E isso passou a pesar: o congelamento da interface que perseguimos por
    dias so acontece sob XWayland. Wayland nativo e a configuracao boa para
    o VNC, e era justamente a que impedia o RDP embutido.

    O gtk-frdp resolve: e um GtkDrawingArea, embute em qualquer backend.
    Testado contra Windows real com FreeRDP 3.30 — conecta, escala
    acompanhando a janela, captura atalhos sem vazar para o host, e aguenta
    video em tela cheia sem travar o laco de eventos.

    A AbaRdp NAO foi tocada: sob X11 ela continua sendo usada, por ser mais
    madura. A escolha e feita em abrir(), e `rdp_embutido = 0` no [geral]
    forca o caminho antigo.
    """

    tipo = "rdp"

    def __init__(self, conexao, janela):
        super().__init__(conexao, janela)
        self.adiada = False
        self._sondando = None
        # OBRIGATORIO ao herdar de CapturaTeclado: cria _grab_ativo e liga
        # os caminhos de saida (unmap, destroy). Sem isto, qualquer perda de
        # foco da janela estoura com
        #     AttributeError: ... has no attribute '_grab_ativo'
        # porque soltar_capturas() varre TODAS as abas.
        self._iniciar_captura()

        if self.cx.tem_ssh:
            bt = add_class(Gtk.Button(label="Shell"), "secundaria")
            bt.set_tooltip_text("Abrir SSH em %s" % self.cx.destino_ssh)
            bt.connect("clicked", lambda _b: self.janela.abrir(self.cx, "ssh"))
            self.barra.pack_end(bt, False, False, 0)

        bt_arq = add_class(Gtk.Button(label="📁"), "secundaria", "tog-glifo")
        bt_arq.set_tooltip_text("Arquivos desta máquina (SFTP)")
        bt_arq.connect("clicked", lambda _b: self.janela.abrir_sftp(self.cx))
        self.barra.pack_end(bt_arq, False, False, 0)

        bt_rec = add_class(Gtk.Button(label="Reconectar"), "secundaria")
        bt_rec.set_tooltip_text("Derruba e refaz a sessão sem fechar a aba")
        bt_rec.connect("clicked", lambda _b: self.reconectar(manual=True))
        self.barra.pack_end(bt_rec, False, False, 0)

        # bt_teclado: a CapturaTeclado consulta este botao em
        # retomar_teclado(), entao ele PRECISA existir. Sem ele:
        #     AttributeError: ... has no attribute 'bt_teclado'
        self.bt_teclado = Gtk.ToggleButton(label="⌨")
        add_class(self.bt_teclado, "tog", "tog-ok", "tog-glifo")
        self.bt_teclado.set_tooltip_text(
            "Capturar o teclado: os atalhos vão para a máquina remota. "
            "Ligado por padrão no RDP. Pause libera.")
        self.bt_teclado.connect("toggled", self._trocar_teclado)
        # LIGADO POR PADRAO no RDP: e o uso normal — quem abre uma sessao
        # RDP quer digitar nela. Continua desligavel a qualquer momento, e o
        # _trocou_aba solta o grab das abas que saem de vista, entao uma
        # sessao em segundo plano nao rouba o teclado das outras.
        self.bt_teclado.set_active(True)
        self.barra.pack_end(self.bt_teclado, False, False, 0)

        self.bt_ajuste = add_class(Gtk.ToggleButton(label="Ajustar"),
                                   "secundaria")
        self.bt_ajuste.set_tooltip_text(
            "Pedir ao servidor que adapte a resolução ao tamanho da aba")
        self.bt_ajuste.set_active(True)
        self.bt_ajuste.connect(
            "toggled",
            lambda b: self.tela.definir_escala(b.get_active()))
        self.barra.pack_end(self.bt_ajuste, False, False, 0)

        self.palco = Gtk.ScrolledWindow()
        self.palco.set_policy(Gtk.PolicyType.AUTOMATIC,
                              Gtk.PolicyType.AUTOMATIC)
        self.palco.set_size_request(1, 1)
        add_class(self.palco, "palco")
        self.tela = RdpWidget()
        self.palco.add(self.tela)
        self.pack_start(self.palco, True, True, 0)
        self.pack_start(self.exp_log, False, False, 0)

        # o toggled do bt_teclado disparou no __init__, antes de self.tela
        # existir, e foi ignorado pela guarda. Aplicamos agora.
        if self.bt_teclado.get_active():
            self._trocar_teclado(self.bt_teclado)

        self.tela.connect("rdp-conectado", self._on_conectado)
        self.tela.connect("rdp-desconectado", self._on_caiu)
        self.tela.connect("rdp-erro", self._on_erro)

    # ------------------------------------------------------------ conexao
    def conectar(self):
        # SONDA ANTES, mesma licao do VNC: entregar host morto ao cliente
        # deixa uma conexao pendurada e complica todo o resto.
        self._estado("AGUARDE", "neutro", "testando %s…" % self.cx.destino)
        host, porta = self.cx.host, int(self.cx.rdp_porta or 3389)
        self._sondando = (host, porta)
        self.reg("sondando %s:%s (RDP embutido)" % (host, porta))
        sondar_porta(host, porta, 2.0,
                     lambda ok, erro: self._sonda(ok, erro, host, porta))

    def _sonda(self, ok, erro, host, porta):
        if self.fechando or self._sondando != (host, porta):
            return
        self._sondando = None
        if not ok:
            self.reg("sem resposta em %s:%s — %s" % (host, porta,
                                                     erro or "inalcançável"))
            self._estado("SEM RESPOSTA", "erro", erro or "host inalcançável")
            if self.bt_auto.get_active():
                self._agendar_auto("host não respondeu")
            return
        self._estado("AGUARDE", "neutro", "conectando em %s…" % self.cx.destino)
        self.reg("abrindo %s:%s (usuario=%s)"
                 % (host, porta, self.cx.rdp_usuario or "<nenhum>"))
        self.tela.conectar(host, porta,
                           usuario=self.cx.rdp_usuario or None,
                           senha=self.cx.rdp_senha or None,
                           dominio=self.cx.rdp_dominio or None,
                           escalar=self.bt_ajuste.get_active())

    def reconectar(self, manual=False):
        self.reg("reconectando" + (" (manual)" if manual else ""))
        self.tela.desconectar()
        self.conectar()

    def desconectar(self):
        super().desconectar()
        self._sondando = None
        self.tela.desconectar()

    def adiar_ate_focar(self):
        """Mesmo tratamento da AbaVnc: aba em segundo plano nao conecta."""
        self.adiada = True
        self._estado("EM ESPERA", "neutro", "conecta ao entrar na aba")
        self.reg("aberta em segundo plano: conexão adiada até receber foco")

    def redesenhar(self):
        """Chamado ao entrar na aba.

        Repinta TUDO: em segundo plano os retangulos sujos sao descartados
        (a sessao segue processando eventos, mas nao pede desenho do que
        ninguem ve), entao ao voltar a tela pode estar defasada."""
        if self.tela.conectado():
            self.tela.redesenhar_tudo()
            self.tela.focar()
        return False

    # ------------------------------------------------------------- sinais
    def _on_conectado(self, _w):
        self._estado("ATIVO", "ok", self.cx.destino)
        self.reg("sessão ativa")
        self._cancelar_auto()
        self.tela.focar()

    def _on_caiu(self, _w):
        if self.fechando:
            return
        self._estado("CAIU", "atencao", "sessão encerrada")
        self.reg("sessão encerrada pelo servidor")
        if self.bt_auto.get_active():
            self._agendar_auto("sessão caiu")

    def _on_erro(self, _w, msg):
        self._estado("ERRO", "erro", msg)
        self.reg("erro: %s" % msg)

    def _trocar_teclado(self, bt):
        """Captura de teclado.

        O gtk-frdp ja retem os atalhos enquanto o widget tem foco, mas isso
        nao basta: sem o grab do GdkSeat, o compositor continua ficando com
        Super, Alt+Tab e afins. E sem marcar_captura() a janela continuaria
        interceptando F9/F12 antes de a sessao ve-los."""
        # O botao nasce marcado (ver __init__), e o toggled dispara ANTES de
        # self.tela existir. Sem esta guarda, criar a aba estouraria com
        # AttributeError.
        if not hasattr(self, "tela"):
            return
        ligado = bt.get_active()
        if ligado:
            self.tela.focar()
        ok = self._grab(ligado)
        self.reg("teclado %s%s" % (
            "capturado — atalhos globais vão para a máquina remota"
            if ligado else "liberado",
            "" if (ok or not ligado) else " (o compositor recusou o inibidor)"))


class AbaRdp(AbaBase, CapturaTeclado):
    """RDP pelo xfreerdp do sistema.

    Sob X11 o cliente e reparentado dentro de um Gtk.Socket e vira aba de
    verdade. Sob Wayland nao existe XID para reparentar: o FreeRDP abre em
    janela propria e a aba fica sendo o painel de controle da sessao. Nao ha
    widget RDP embutivel em GTK3 — o Remmina resolve isso com um plugin em C
    falando direto com a libfreerdp, que e trabalho de outra ordem."""

    tipo = "rdp"

    def __init__(self, conexao, janela):
        super().__init__(conexao, janela)
        self.pid = None
        self.socket = None
        self.sem_socket = False
        self.modo_seguro = False
        self.plug_ok = False
        self._timer_plug = None
        self.sem_dominio = False
        self.kerberos_falhou = False
        self.logon_falhou = False
        self.adiada = False
        self._tent_pronto = 0
        self.embutido = _backend_x11()

        self.bt_auto.set_active(self.cx.rdp_auto)
        self._ligar_auto("rdp_auto")

        bt_rec = add_class(Gtk.Button(label="Reconectar"), "secundaria")
        bt_rec.connect("clicked", lambda _b: self.reconectar(manual=True))
        self.barra.pack_end(bt_rec, False, False, 0)

        # SEM menu de injecao de teclas no RDP (⌁). Removido de proposito:
        # o xdotool, sob XWayland/Mutter, aciona o portal de "Area de
        # Trabalho Remota" do GNOME ao tentar sintetizar Super — pedindo
        # permissao para controlar a PROPRIA maquina local, nao a sessao
        # RDP. Aceitar esse dialogo colide com o nosso proprio XGrabKeyboard
        # (duas fontes de captura de teclado disputando), e ja causou
        # travamento do sistema operacional inteiro em teste real. Perigoso
        # demais para manter, mesmo remendado — nao ha garantia de que
        # outras combinacoes nao acionem o mesmo caminho.
        #
        # Ctrl+Alt+Del e afins continuam alcancaveis: se o servidor for
        # UltraVNC/uvnc_service, a aba de Tela (VNC) da mesma maquina envia
        # pelo protocolo RFB, que nao tem esse problema.

        bt_cmd = add_class(Gtk.Button(label="⧉"), "secundaria", "tog-glifo")
        bt_cmd.set_tooltip_text("Copiar o comando xfreerdp para testar no "
                                "terminal (com a senha real)")
        bt_cmd.connect("clicked", self._copiar_comando)
        self.barra.pack_end(bt_cmd, False, False, 0)

        for rot, tp in (("Tela", "vnc"), ("Shell", "ssh")):
            if self.cx.tem(tp):
                b = add_class(Gtk.Button(label=rot), "secundaria")
                b.connect("clicked", lambda _b, t=tp: self.janela.abrir(self.cx, t))
                self.barra.pack_end(b, False, False, 0)

        self.bt_teclado = Gtk.ToggleButton(label="⌨")
        add_class(self.bt_teclado, "tog", "tog-ok", "tog-glifo")
        self.bt_teclado.set_tooltip_text(
            "Capturar o teclado. Atalhos reservados pelo compositor não "
            "passam — para esses, use o menu ⌁. Pause libera.")
        self.bt_teclado.connect("toggled", self._trocar_teclado)
        self.barra.pack_end(self.bt_teclado, False, False, 0)

        self.seg, self.seg_botoes = segmentado(
            [("dinamico", "Dinâmico"), ("janela", "Janela"), ("cheia", "Cheia")],
            self.cx.rdp_tela, self._trocar_tela)
        self.barra.pack_end(self.seg, False, False, 0)

        self.palco = add_class(Gtk.Box(orientation=Gtk.Orientation.VERTICAL), "palco")
        self.palco.set_size_request(1, 1)
        self.pack_start(self.palco, True, True, 0)
        self._iniciar_captura()
        self.pack_start(self.exp_log, False, False, 0)

        if self.embutido:
            self.socket = Gtk.Socket()
            self.socket.connect("plug-removed", self._on_plug_saiu)
            self.socket.connect("plug-added", self._on_plug_entrou)
            # sem minimo proprio o Gtk.Socket adota o tamanho pedido pelo
            # plug como MINIMO da janela inteira, e a janela cresce sem parar
            # ate os botoes sairem da tela
            self.socket.set_size_request(1, 1)
            self.socket.set_hexpand(True)
            self.socket.set_vexpand(True)
            rolagem = Gtk.ScrolledWindow()
            rolagem.set_policy(Gtk.PolicyType.AUTOMATIC,
                               Gtk.PolicyType.AUTOMATIC)
            rolagem.set_propagate_natural_width(False)
            rolagem.set_propagate_natural_height(False)
            rolagem.add(self.socket)
            self.palco.pack_start(rolagem, True, True, 0)
        else:
            self.palco.pack_start(self._aviso_externo(), True, True, 0)

    def _trocar_para_externo(self):
        """Desmonta o socket e poe o painel de controle no lugar."""
        self.embutido = False
        self.socket = None
        # destroy(): o socket e o painel antigos precisam MORRER, nao apenas
        # sair do container — senao suas GdkWindow continuam capturando
        # clique por cima do que vier depois
        for f in self.palco.get_children():
            f.destroy()
        self.palco.pack_start(self._aviso_externo(), True, True, 0)
        self.palco.show_all()

    def _aviso_externo(self):
        cx = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        cx.set_valign(Gtk.Align.CENTER)
        cx.set_halign(Gtk.Align.CENTER)
        t = rotulo("Sessão RDP em janela separada", "hero-titulo", xalign=0.5)
        t.set_ellipsize(Pango.EllipsizeMode.NONE)
        cx.pack_start(t, False, False, 0)
        motivo = ("esta build do FreeRDP não suporta /parent-window"
                  if self.sem_socket else
                  "em Wayland não existe XID para reparentar a janela")
        lb = rotulo("A imagem aparece na janela do xfreerdp porque %s.\n"
                    "Esta aba controla a sessão: reconectar, encerrar e log."
                    % motivo, "hero-sub", xalign=0.5)
        lb.set_ellipsize(Pango.EllipsizeMode.NONE)
        lb.set_justify(Gtk.Justification.CENTER)
        cx.pack_start(lb, False, False, 0)
        return cx

    # ---- ciclo
    def _argv(self):
        """Linha de comando MINIMA.

        Cada opcao extra e uma chance de o FreeRDP recusar e abortar, e a
        recusa nao vem com mensagem util. Aqui fica so o indispensavel; os
        adornos (clipboard, audio, compressao) entram por rdp_extras no INI,
        conscientemente, e nunca por conta propria."""
        binario, nome = _bin_rdp()
        if not binario:
            return None, None

        argv = [binario, "/v:%s:%s" % (self.cx.host, self.cx.rdp_porta),
                "/cert:ignore"]
        if DEBUG:
            argv.append("/log-level:INFO")
        if self.cx.rdp_usuario:
            argv.append("/u:%s" % self.cx.rdp_usuario)
        if self.cx.rdp_dominio and not self.sem_dominio:
            argv.append("/d:%s" % self.cx.rdp_dominio)
        if self.cx.rdp_senha:
            # aparece no ps local; mesmo compromisso do sshpass
            argv.append("/p:%s" % self.cx.rdp_senha)

        embutir = (self.embutido and self.socket is not None
                   and not self.sem_socket)
        if embutir:
            if not self.socket.get_realized():
                try:
                    self.socket.realize()
                except Exception as e:
                    self.reg("realize do socket falhou: %s" % e)
            xid = self.socket.get_id()
            if xid:
                al = self.palco.get_allocation()
                larg = al.width if al.width > 1 else 1280
                alt = al.height if al.height > 1 else 800
                argv.append("/parent-window:%d" % xid)
                argv.append("/size:%dx%d" % (max(larg, 640), max(alt, 480)))
            else:
                self.reg("Gtk.Socket sem XID; abrindo em janela externa")
                embutir = False
        if not embutir:
            if self.cx.rdp_tela == "cheia":
                argv.append("/f")
            elif self.cx.rdp_tela == "dinamico":
                argv.append("/dynamic-resolution")

        if not self.modo_seguro:
            # +clipboard sempre ligado por padrao. Durante a caçada ao
            # SIGABRT (codigo 134) eu tirei isso do padrao para isolar qual
            # flag causava o abort — a causa real era outra (/grab-keyboard
            # sem o prefixo certo), e eu nunca devolvi o clipboard depois.
            # A pessoa que usa este programa todo dia nao deveria precisar
            # descobrir e declarar rdp_extras so para copiar e colar.
            if "+clipboard" not in self.cx.rdp_extras and \
               "-clipboard" not in self.cx.rdp_extras:
                argv.append("+clipboard")
            for extra in self.cx.rdp_extras:
                argv.append(extra)
            if getattr(self, "bt_teclado", None):
                # booleana do FreeRDP leva + ou -, nunca "/"
                argv.append("+grab-keyboard" if self.bt_teclado.get_active()
                            else "-grab-keyboard")
        return argv, nome

    def linha_comando(self, ocultar_senha=True):
        argv, _n = self._argv()
        if not argv:
            return ""
        return " ".join(
            "/p:***" if (ocultar_senha and a.startswith("/p:")) else a
            for a in argv)

    def conectar(self):
        # A aba acabou de ser criada: o Gtk.Socket ainda nao tem alocacao nem
        # XID valido. Spawnar aqui entrega um /parent-window: invalido e o
        # FreeRDP aborta com SIGABRT (codigo 134).
        if self.embutido and not self.get_mapped():
            self._estado("AGUARDE", "neutro", "preparando…")
            self._agendar(50, self._conectar_quando_pronto)
            return
        self._conectar_agora()

    def _conectar_quando_pronto(self):
        """Espera a aba ter geometria real antes de lancar o xfreerdp.

        O contador vive na instancia. Antes era um argumento com default
        mutavel (tentativas=[0]), que em Python e criado UMA vez e fica
        compartilhado por todas as abas RDP: duas sessoes abrindo juntas
        somavam no mesmo contador e uma delas desistia cedo demais."""
        if self.fechando:
            return False
        al = self.palco.get_allocation()
        if self.get_mapped() and al.width > 1:
            self._tent_pronto = 0
            self._conectar_agora()
            return False
        self._tent_pronto += 1
        if self._tent_pronto > 40:      # ~2s
            self._tent_pronto = 0
            self.reg("aba em segundo plano: usando 1280x800 como tamanho")
            self._conectar_agora()
            return False
        return True

    def _conectar_agora(self):
        argv, nome = self._argv()
        if not argv:
            self._estado("ERRO", "erro", "xfreerdp não encontrado")
            self.reg("instale o freerdp: dnf install freerdp / pacman -S freerdp")
            self.janela.avisar(
                "FreeRDP ausente",
                "Fedora: sudo dnf install freerdp\n"
                "Arch:   sudo pacman -S freerdp")
            return
        self._estado("AGUARDE", "neutro", "abrindo %s…" % self.cx.destino_rdp)
        self.reg("exec (%s): %s" % (nome, " ".join(
            "/p:***" if a.startswith("/p:") else a for a in argv)))
        if self.modo_seguro:
            self.reg("modo mínimo: rdp_extras e grab-keyboard desativados")
        try:
            pid, _i, _o, err_fd = GLib.spawn_async(
                argv, flags=GLib.SpawnFlags.DO_NOT_REAP_CHILD |
                GLib.SpawnFlags.SEARCH_PATH,
                standard_error=True)
        except Exception as e:
            self._estado("ERRO", "erro", "falha ao iniciar")
            self.reg("spawn falhou: %s" % e)
            return
        self._ler_erro(err_fd)
        self.pid = pid
        GLib.child_watch_add(GLib.PRIORITY_DEFAULT, pid, self._on_saiu)
        if self.embutido and not self.sem_socket:
            self.plug_ok = False
            if self._timer_plug:
                self._timer_plug = self._cancelar_timer(self._timer_plug)
            self._timer_plug = self._agendar(8, self._vigia_plug, segundos=True)
            self._estado("AGUARDE", "atencao", "aguardando a janela remota…")
        else:
            self._estado("ATIVO", "ok", self.cx.destino_rdp)
        self.reg("xfreerdp iniciado (pid %s, %s%s)" % (
            pid, "embutido" if self.embutido else "janela externa",
            ", modo mínimo" if self.modo_seguro else ""))
        self.sucesso()

    def _ler_erro(self, fd):
        """Sem isto o motivo do SIGABRT morre no vazio: o FreeRDP explica o
        que houve no stderr, que o spawn descartava."""
        try:
            canal = GLib.IOChannel.unix_new(fd)
            canal.set_flags(GLib.IOFlags.NONBLOCK)
            canal.set_close_on_unref(True)
        except Exception:
            return

        def leu(ch, cond):
            if self.fechando:
                try:
                    ch.shutdown(False)
                except Exception:
                    pass
                return False
            if cond & (GLib.IOCondition.HUP | GLib.IOCondition.ERR):
                try:
                    ch.shutdown(False)
                except Exception:
                    pass
                return False
            try:
                estado, linha, _t, _e = ch.read_line()
            except Exception:
                return False
            if estado != GLib.IOStatus.NORMAL or not linha:
                return estado == GLib.IOStatus.AGAIN
            linha = linha.strip()
            if linha:
                self._analisar_erro(linha)
                if DEBUG or "[ERROR]" in linha or "ERRCONNECT" in linha:
                    self.reg("freerdp: %s" % linha)
            return True

        GLib.io_add_watch(canal,
                          GLib.PRIORITY_DEFAULT,
                          GLib.IOCondition.IN | GLib.IOCondition.HUP,
                          leu)

    def _analisar_erro(self, linha):
        """Um /d: com FQDN faz o FreeRDP tentar Kerberos e procurar um KDC
        para aquele realm. Sem krb5.conf apontando para o domínio a busca
        falha, e ele NAO volta para NTLM: segue e leva LOGON_FAILURE. Sem
        /d: o servidor usa o dominio padrao dele e a autenticacao passa."""
        if "Cannot find KDC for realm" in linha:
            self.kerberos_falhou = True
        if "ERRCONNECT_LOGON_FAILURE" in linha:
            self.logon_falhou = True

    def _on_saiu(self, pid, status, *_a):
        GLib.spawn_close_pid(pid)
        self.pid = None
        if self.fechando:
            return          # aba ja foi embora: nada de religar nem repintar
        codigo = status >> 8 if status > 255 else status
        if codigo == 0:
            self._estado("ENCERRADO", "neutro", "sessão finalizada")
            return

        if (self.logon_falhou and self.cx.rdp_dominio
                and not self.sem_dominio):
            self.sem_dominio = True
            motivo = ("o domínio '%s' foi tratado como realm Kerberos e não "
                      "há KDC alcançável" % self.cx.rdp_dominio
                      if self.kerberos_falhou else
                      "a autenticação com domínio foi recusada")
            self.reg("%s; repetindo sem /d: — o servidor usará o domínio "
                     "padrão dele" % motivo)
            self._estado("SEM DOMÍNIO", "atencao", "repetindo sem domínio…")
            self.logon_falhou = self.kerberos_falhou = False
            self._agendar(400, lambda: (self._conectar_agora(), False)[1])
            return

        # Abortou embutido? Nem toda build tem /parent-window: o cliente SDL
        # do FreeRDP 3, por exemplo, nao implementa reparenting e aborta.
        # Em vez de acusar o usuario, tenta uma vez em janela externa.
        if codigo in (134, 139, 255) and not self.modo_seguro:
            # Primeiro suspeito de um abort e sempre uma opcao que a build
            # nao aceita. Antes de desistir do embutido, tenta so o essencial.
            self.modo_seguro = True
            self.reg("abortou (código %s); repetindo apenas com as opções "
                     "essenciais para isolar a flag culpada" % codigo)
            self._estado("SEGURO", "atencao", "repetindo em modo mínimo…")
            self._agendar(500, lambda: (self._conectar_agora(), False)[1])
            return

        if codigo in (134, 139, 255) and self.embutido and not self.sem_socket:
            self.sem_socket = True
            self.reg("abortou mesmo em modo mínimo; esta build não deve "
                     "suportar /parent-window. Reabrindo em janela externa.")
            self._estado("EXTERNO", "atencao", "reabrindo fora da aba…")
            self._trocar_para_externo()
            self._agendar(500, lambda: (self._conectar_agora(), False)[1])
            return

        self._estado("ENCERRADO", "erro", "saiu com código %s" % codigo)
        self.reg("xfreerdp terminou, código %s" % codigo)
        if codigo == 134:
            self.reg("SIGABRT também fora do socket — veja as linhas "
                     "'freerdp:' acima para a causa real.")
            self.bt_auto.set_active(False)
            return
        # 131 = credenciais recusadas; insistir nao ajuda
        if codigo in (131, 132):
            self.reg("credenciais recusadas — auto desligado")
            self.bt_auto.set_active(False)
            return
        self._agendar_auto("código %s" % codigo)

    def _on_plug_entrou(self, _s):
        self.plug_ok = True
        self.reg("janela do FreeRDP acoplada ao socket")
        self._estado("ATIVO", "ok", self.cx.destino_rdp)

    def _vigia_plug(self):
        """Tela preta = o processo subiu mas nada foi desenhado. Quase sempre
        significa que o cliente ignorou /parent-window e abriu (ou tentou
        abrir) em outro lugar. Sem esta checagem a aba fica preta para sempre
        sem dizer nada."""
        self._timer_plug = None
        if self.fechando or self.plug_ok or not self.pid:
            return False
        self.reg("8s sem acoplar: o cliente aceitou /parent-window mas não "
                 "reparentou. Reabrindo em janela externa.")
        self.sem_socket = True
        try:
            os.kill(self.pid, 15)
        except Exception:
            pass
        self.pid = None
        self._trocar_para_externo()
        self._agendar(500, lambda: (self._conectar_agora(), False)[1])
        return False

    def _on_plug_saiu(self, _s):
        self.reg("a janela do FreeRDP foi removida do socket")
        return True          # nao destroi o Gtk.Socket: ele sera reusado

    def adiar_ate_focar(self):
        self.adiada = True
        self._estado("EM ESPERA", "neutro",
                     "conecta ao entrar na aba (evita tela preta)")
        self.reg("aberta em segundo plano: conexão adiada até a aba receber "
                 "foco, porque o FreeRDP não desenha em janela não mapeada")

    def reatar_foco(self):
        """Voltar para a aba deixa o socket sem foco de entrada e o mouse
        para de responder dentro da sessao."""
        if self.socket is not None and self.socket.get_realized():
            self.socket.grab_focus()
        return False

    def _copiar_comando(self, _b):
        linha = self.linha_comando(ocultar_senha=False)
        Gtk.Clipboard.get(Gdk.SELECTION_CLIPBOARD).set_text(linha, -1)
        self.reg("comando copiado (com senha) para a área de transferência")
        self._estado(self.chip_estado.get_text(), "neutro",
                     "comando copiado — cole num terminal para testar")

    def _trocar_teclado(self, bt):
        if bt.get_active():
            self.reatar_foco()
        ok = self._grab(bt.get_active())
        self.reg("teclado %s%s" % (
            "capturado — atalhos globais vão para a sessão"
            if bt.get_active() else "liberado",
            "" if (ok or not bt.get_active()) else " (grab recusado)"))

    def _trocar_tela(self, novo):
        if novo == self.cx.rdp_tela:
            return
        self.cx.rdp_tela = novo
        self.janela.gravar(self.cx.nome, "rdp_tela", novo)
        self.reg("modo de tela %s — vale na próxima conexão" % novo)

    def desconectar(self):
        super().desconectar()
        self._grab(False)
        if self.pid:
            try:
                os.kill(self.pid, 15)
            except Exception:
                pass

    def reconectar(self, manual=False):
        if manual:
            self._inicio_manual()
        if self.pid:
            try:
                os.kill(self.pid, 15)
            except Exception:
                pass
            self.pid = None
        self._agendar(400, lambda: (self.conectar(), False)[1])


def proximo_nome(nome):
    """Incrementa o ultimo grupo de digitos, preservando os zeros a esquerda.

        PDV 1      -> PDV 2
        PDV5201    -> PDV5202
        PDV 009    -> PDV 010
        SERV-MGV   -> SERV-MGV 2      (sem digito: acrescenta)
    """
    m = None
    for m in re.finditer(r"\d+", nome):
        pass
    if m is None:
        return "%s 2" % nome
    bloco = m.group(0)
    novo = str(int(bloco) + 1)
    if len(novo) < len(bloco):
        novo = novo.zfill(len(bloco))
    return nome[:m.start()] + novo + nome[m.end():]


def proximo_host(host):
    """Soma 1 no ultimo octeto de um IPv4.

        10.6.1.31   -> 10.6.1.32
        10.6.1.254  -> 10.6.1.254   (fica; .255 e broadcast)

    Nome de maquina volta intacto: incrementar 'srv-fiscal' nao significa
    nada, e chutar aqui daria um host que nao existe."""
    partes = host.strip().split(".")
    if len(partes) != 4:
        return host
    try:
        nums = [int(p) for p in partes]
    except ValueError:
        return host
    if any(n < 0 or n > 255 for n in nums):
        return host
    if nums[3] >= 254:
        return host
    nums[3] += 1
    return ".".join(str(n) for n in nums)


class EditorSnippets(Gtk.Dialog):
    """Biblioteca de comandos para a execucao em lote.

    Selecionar um snippet CARREGA no editor, nunca executa direto — quem
    vai disparar em 200 maquinas revisa antes."""

    def __init__(self, janela):
        super().__init__(title="Snippets", transient_for=janela, modal=True,
                         use_header_bar=True)
        add_class(self, "acessos-dialogo")
        self.janela = janela
        self.caminho = caminho_snippets()
        self.set_default_size(860, 560)
        janela.soltar_capturas()
        try:
            import dialogo_ui
            dialogo_ui.estilizar_headerbar(self, "Snippets")
        except Exception:
            pass

        botao_dialogo(self, "Fechar", Gtk.ResponseType.CLOSE, "perigo")
        botao_dialogo(self, "Salvar", Gtk.ResponseType.APPLY, "acao")
        self.connect("response", self._resposta)

        raiz = add_class(Gtk.Box(spacing=10), "fundo")
        raiz.set_border_width(14)
        self.get_content_area().pack_start(raiz, True, True, 0)

        # --- lista
        esq = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        esq.set_size_request(240, -1)
        self.store = Gtk.ListStore(str, str)      # chave, descricao
        self.lista = Gtk.TreeView(model=self.store)
        self.lista.set_headers_visible(False)
        add_class(self.lista, "lista")
        col = Gtk.TreeViewColumn("", Gtk.CellRendererText(), text=1)
        self.lista.append_column(col)
        self.lista.get_selection().connect("changed", self._trocou)
        rol = Gtk.ScrolledWindow()
        rol.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
        rol.add(self.lista)
        add_class(rol, "cartao")
        esq.pack_start(rol, True, True, 0)

        linha = Gtk.Box(spacing=4, homogeneous=True)
        for rot, fn, classe in (("Novo", self._novo, "acao"),
                                ("Remover", self._remover, "perigo")):
            b = add_class(Gtk.Button(label=rot), classe)
            b.connect("clicked", lambda _b, f=fn: f())
            linha.pack_start(b, True, True, 0)
        esq.pack_start(linha, False, False, 0)
        raiz.pack_start(esq, False, False, 0)

        # --- formulario
        dir_ = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        self.campos = {}
        grade = Gtk.Grid(column_spacing=8, row_spacing=6)
        for i, (chave, rot) in enumerate((("descricao", "Descrição"),)):
            lb = rotulo(rot, "rotulo")
            lb.set_width_chars(10)
            ent = Gtk.Entry()
            ent.set_hexpand(True)
            self.campos[chave] = ent
            grade.attach(lb, 0, i, 1, 1)
            grade.attach(ent, 1, i, 1, 1)
        dir_.pack_start(grade, False, False, 0)

        opc = Gtk.Box(spacing=8)
        opc.pack_start(rotulo("Plataforma", "rotulo"), False, False, 0)
        self.seg_plat, self.seg_plat_bt = segmentado(
            [("linux", "Linux"), ("windows", "Windows"), ("ambos", "Ambos")],
            "ambos", lambda _k: None)
        opc.pack_start(self.seg_plat, False, False, 0)
        self.cb_root = Gtk.CheckButton(label="precisa de root")
        self.cb_root.set_tooltip_text(
            "A senha de root é sempre digitada na hora, nunca gravada")
        self.cb_ignorar = Gtk.CheckButton(label="ignorar exit code")
        opc.pack_end(self.cb_ignorar, False, False, 0)
        opc.pack_end(self.cb_root, False, False, 0)
        dir_.pack_start(opc, False, False, 0)

        dir_.pack_start(rotulo("COMANDO", "titulo-secao"), False, False, 0)
        self.buf_cmd = Gtk.TextBuffer()
        tv = Gtk.TextView(buffer=self.buf_cmd)
        tv.set_monospace(True)
        # sem cursor visivel o campo parece nao ter foco quando esta vazio
        tv.set_editable(True)
        tv.set_cursor_visible(True)
        tv.set_left_margin(8)
        tv.set_right_margin(8)
        tv.set_top_margin(6)
        add_class(tv, "log")
        rol2 = Gtk.ScrolledWindow()
        rol2.add(tv)
        add_class(rol2, "cartao")
        dir_.pack_start(rol2, True, True, 0)
        raiz.pack_start(dir_, True, True, 0)

        self.itens = {}
        self._recarregar()
        self.show_all()

    # ---- dados
    def _recarregar(self):
        self.store.clear()
        self.itens = {}
        for sn in carregar_snippets(self.caminho):
            self.itens[sn.chave] = sn
            self.store.append([sn.chave, sn.descricao])
        self.atual = None

    def _trocou(self, sel):
        modelo, it = sel.get_selected()
        if not it:
            return
        self._guardar_atual()
        chave = modelo[it][0]
        sn = self.itens.get(chave)
        if not sn:
            return
        self.atual = chave
        self.campos["descricao"].set_text(sn.descricao)
        self.seg_plat_bt[sn.plataforma].set_active(True)
        self.cb_root.set_active(sn.root)
        self.cb_ignorar.set_active(sn.ignorar_exit)
        self.buf_cmd.set_text(sn.comando)

    def _guardar_atual(self):
        if not self.atual or self.atual not in self.itens:
            return
        sn = self.itens[self.atual]
        sn.descricao = self.campos["descricao"].get_text().strip() or sn.chave
        for k, b in self.seg_plat_bt.items():
            if b.get_active():
                sn.plataforma = k
        sn.root = self.cb_root.get_active()
        sn.ignorar_exit = self.cb_ignorar.get_active()
        ini, fim = self.buf_cmd.get_bounds()
        sn.comando = self.buf_cmd.get_text(ini, fim, False).strip()

    def _novo(self):
        self._guardar_atual()
        base, n = "novo", 1
        while "%s_%d" % (base, n) in self.itens:
            n += 1
        chave = "%s_%d" % (base, n)
        # interpolation=None pelo mesmo motivo de carregar_snippets(): este
        # SectionProxy vira o armazenamento do Snippet, e o comando pode ter %
        cp = configparser.ConfigParser(interpolation=None)
        cp.add_section(chave)
        sn = Snippet(chave, cp[chave])
        sn.descricao = "Novo snippet %d" % n
        self.itens[chave] = sn
        self.store.append([chave, sn.descricao])
        self.lista.get_selection().select_path(len(self.store) - 1)

    def _remover(self):
        modelo, it = self.lista.get_selection().get_selected()
        if not it:
            return
        chave = modelo[it][0]
        if not self.janela.confirmar(
                "Remover snippet",
                "Remover '%s' da biblioteca?" % self.itens[chave].descricao,
                ok="Remover", destrutivo=True):
            return
        self.itens.pop(chave, None)
        self.atual = None
        modelo.remove(it)

    def _salvar(self):
        self._guardar_atual()
        # interpolation=None: ver carregar_snippets(). Sem isto, salvar um
        # comando com % (ex.: stat --print=%w) levanta ValueError.
        cp = configparser.ConfigParser(interpolation=None)
        for chave, sn in self.itens.items():
            cp[chave] = {
                "descricao": sn.descricao,
                "plataforma": sn.plataforma,
                "root": "sim" if sn.root else "nao",
                "ignorar_exit": "sim" if sn.ignorar_exit else "nao",
                # linha continuada indentada: e assim que comando
                # multilinha e guardado num INI
                "comando": sn.comando.replace("\n", "\n               "),
            }
        try:
            os.makedirs(os.path.dirname(self.caminho), exist_ok=True)
            with open(self.caminho, "w", encoding="utf-8") as f:
                cp.write(f)
            return True
        except OSError as e:
            self.janela.avisar("Não consegui salvar", str(e))
            return False

    def _resposta(self, _dlg, resp):
        if resp == Gtk.ResponseType.APPLY:
            if self._salvar():
                self.janela.avisar("Snippets salvos",
                                   "Biblioteca gravada em:\n%s" % self.caminho)
            self.stop_emission_by_name("response")


class EditorConexao(Gtk.Dialog):
    """Formulario de adicionar/editar.

    Espelha todas as chaves do INI: quem prefere o arquivo continua editando
    a mao, e o que sai daqui preserva os comentarios existentes."""

    def __init__(self, janela, conexao=None, grupos=()):
        titulo = "Editar conexão" if conexao else "Nova conexão"
        super().__init__(title=titulo, transient_for=janela, modal=True,
                         use_header_bar=True)
        add_class(self, "acessos-dialogo")
        self.janela = janela
        self.cx = conexao
        self.nome_antigo = conexao.nome if conexao else None
        # so a largura e fixada; a altura fica livre para o dialogo se
        # ajustar ao conteudo (sem ScrolledWindow, isso volta a fazer sentido
        # e evita area vazia ou cortada quando poucos/muitos blocos abrem)
        self.set_default_size(640, -1)

        # Um seat grab de uma aba VNC continuaria segurando o teclado e o
        # dialogo abriria com os campos inertes.
        janela.soltar_capturas()

        # Cancelar e VERMELHO. Regra da interface: acao destrutiva usa a cor
        # de erro sempre — nao so no hover, nao em contorno tenue. Cancelar
        # descarta o que foi digitado, entao entra na regra.
        botao_dialogo(self, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
        botao_dialogo(self, "Salvar", Gtk.ResponseType.OK, "acao")
        try:
            # import local: o dialogo_ui resolve o estilo pelo __main__ em
            # tempo de chamada, entao importar aqui (e nao no topo) evita
            # qualquer ordem de carga esquisita
            import dialogo_ui
            dialogo_ui.estilizar_headerbar(self, titulo)
        except Exception:
            pass
        # sem set_default_response: o estado .default e uma das vias pelas
        # quais o tema desenha a moldura. Enter e tratado abaixo.
        self.connect("key-press-event", self._teclas)

        # SEM ScrolledWindow/Viewport de proposito.
        #
        # O Viewport que o GTK3 cria (a mao ou implicito) para embrulhar uma
        # coluna comum tem back-store proprio, e o GTK3 nem sempre invalida
        # essa area quando um filho so MUDA DE VISIBILIDADE — sobra pixel
        # preto (fundo cru da janela) ate um resize de verdade forcar o
        # repaint inteiro. Tentei forcar queue_draw/invalidate_rect depois de
        # cada toggle e na abertura; o resíduo persistiu.
        #
        # O formulario e pequeno e cabe numa tela normal (tres blocos
        # recolhiveis). Sem rolagem nao ha Viewport, e sem Viewport a classe
        # inteira desse bug deixa de existir — a coluna e so mais uma Box
        # dentro da area de conteudo do dialogo, que ja sabe se redesenhar
        # direito quando os filhos mudam de visibilidade.
        coluna = add_class(Gtk.Box(orientation=Gtk.Orientation.VERTICAL,
                                   spacing=10), "fundo")
        coluna.set_border_width(14)
        coluna.set_size_request(560, -1)
        raiz = self.get_content_area()
        raiz.pack_start(coluna, True, True, 0)

        self.campos = {}
        coluna.pack_start(self._identificacao(grupos), False, False, 0)

        self.blocos = {}
        self.chaves = {}
        coluna.pack_start(self._servico(
            "vnc", "TELA · VNC",
            [("porta", "Porta", "5900"),
             ("usuario", "Usuário", "só se o servidor pedir (VeNCrypt)"),
             ("senha", "Senha", "vazio pergunta na hora")]), False, False, 0)
        coluna.pack_start(self._servico(
            "ssh", "SHELL · SSH",
            [("ssh_porta", "Porta", "22"),
             ("ssh_usuario", "Usuário", "obrigatório"),
             ("ssh_senha", "Senha", "exige sshpass; vazio usa chave")]),
            False, False, 0)
        coluna.pack_start(self._servico(
            "rdp", "RDP",
            [("rdp_porta", "Porta", "3389"),
             ("rdp_usuario", "Usuário", ""),
             ("rdp_senha", "Senha", ""),
             ("rdp_dominio", "Domínio",
              "FQDN dispara Kerberos e costuma falhar — prefira vazio"),
             ("rdp_extras", "Extras",
              "+clipboard já é automático — só o que mais quiser")]),
            False, False, 0)

        self._preencher()
        self.show_all()
        self._sincronizar_blocos()
        # show_all() reabre TODOS os corpos; o acordeao e exclusivo, entao o
        # estado inicial precisa ser reimposto depois dele. Abre o primeiro
        # servico ligado — e o que o usuario vai querer conferir.
        self._acordeao_inicial()
        self.campos["nome"].grab_focus()

    def _acordeao_inicial(self):
        escolhido = None
        for tipo in ("vnc", "ssh", "rdp"):
            if tipo in self._disclosures and self.chaves[tipo].get_active():
                escolhido = tipo
                break
        if escolhido is None:
            escolhido = "vnc"
        self._abrir_bloco(escolhido)
        self._encolher_para_conteudo()

    def _teclas(self, _w, ev):
        nome = Gdk.keyval_name(ev.keyval) or ""
        if nome in ("Return", "KP_Enter"):
            # Enter dentro de um campo de texto salva, como se espera de um
            # formulario; nos demais widgets deixa passar
            if isinstance(self.get_focus(), Gtk.Entry):
                self.response(Gtk.ResponseType.OK)
                return True
        return False

    # ---- identificacao
    def _identificacao(self, grupos):
        quadro = add_class(Gtk.Box(orientation=Gtk.Orientation.VERTICAL,
                                   spacing=6), "bloco")
        quadro.set_border_width(10)
        quadro.pack_start(rotulo("IDENTIFICAÇÃO", "bloco-cab"), False, False, 0)

        grade = Gtk.Grid(column_spacing=8, row_spacing=6)
        for i, (chave, rot, dica) in enumerate(
                [("nome", "Nome", "vira o [bloco] no INI e o rótulo da aba"),
                 ("host", "Host", "ip ou nome")]):
            lb = rotulo(rot, "rotulo")
            # width_chars em vez de size_request: um minimo rigido em pixels
            # gera largura negativa quando o dialogo ainda nao tem tamanho,
            # e o GTK reclama de "Negative content width"
            lb.set_width_chars(9)
            lb.set_xalign(0.0)
            ent = Gtk.Entry()
            ent.set_hexpand(True)
            ent.set_placeholder_text(dica)
            self.campos[chave] = ent
            grade.attach(lb, 0, i, 1, 1)
            grade.attach(ent, 1, i, 1, 1)

        # grupo com as secoes ja existentes no INI, mas aceitando texto novo
        combo = Gtk.ComboBoxText.new_with_entry()
        for g in grupos:
            combo.append_text(g)
        ent_g = combo.get_child()
        ent_g.set_placeholder_text("subgrupos com ; — Loja 06;Caixas")
        self.campos["grupo"] = ent_g
        self.combo_grupo = combo
        lb = rotulo("Grupo", "rotulo")
        lb.set_width_chars(9)
        lb.set_xalign(0.0)
        grade.attach(lb, 0, 2, 1, 1)
        grade.attach(combo, 1, 2, 1, 1)
        quadro.pack_start(grade, False, False, 0)
        return quadro

    # ---- um servico por bloco com cabecalho proprio (SEM Gtk.Expander)
    def _servico(self, tipo, titulo, itens):
        """Um Gtk.Switch dentro de exp.set_label_widget() e anti-padrao do
        GTK3: a area de titulo do Expander intercepta o clique para
        abrir/fechar e nao repassa de forma confiavel para widgets
        interativos ali dentro. Era por isso que os switches de VNC/SSH/RDP
        so alternavam o recolher, nunca o proprio estado.

        Aqui o cabecalho e uma Box comum — o Switch e um irmao normal,
        recebe clique como qualquer widget. O disclosure (▸/▾) e um botao
        proprio e pequeno, sem ambiguidade com o switch."""
        raiz = add_class(Gtk.Box(orientation=Gtk.Orientation.VERTICAL,
                                 spacing=0), "bloco")

        cab = Gtk.Box(spacing=8)
        cab.set_border_width(10)

        # ACORDEAO EXCLUSIVO: o cabecalho INTEIRO e o alvo de clique, nao um
        # glifo de 12px no canto. O disclosure textual (▸/▾) sai — o proprio
        # estado aberto/fechado ja e visivel pelo corpo do bloco.
        bt_disc = Gtk.Button()
        add_class(bt_disc, "bloco-cabbt")
        bt_disc.set_relief(Gtk.ReliefStyle.NONE)
        bt_disc.set_hexpand(True)

        # ICONE DO PROTOCOLO no cabecalho: o mesmo glifo do card e da aba.
        # Fica ao lado do disclosure, colorido quando o servico esta ligado
        # e cinza quando desligado — a mesma leitura dos icones-acao do
        # card, entao o editor fala a mesma lingua do resto.
        #
        # Os blocos continuam INDEPENDENTES de proposito: dois abertos ao
        # mesmo tempo permite comparar porta e usuario entre VNC e SSH, que
        # e o caso real ao cadastrar maquina nova. Um acordeao que fecha o
        # anterior mataria essa comparacao.
        self.icones_bloco = getattr(self, "icones_bloco", {})
        ic = icone_acao(tipo, 15)
        add_class(ic, "bloco-ico", "bloco-ico-" + tipo)
        self.icones_bloco[tipo] = ic

        dentro = Gtk.Box(spacing=8)
        dentro.pack_start(ic, False, False, 0)
        dentro.pack_start(rotulo(titulo, "bloco-cab"), False, False, 0)
        bt_disc.add(dentro)
        cab.pack_start(bt_disc, True, True, 0)
        self.resumos = getattr(self, "resumos", {})
        self.resumos[tipo] = rotulo("", "dica", xalign=1.0)
        cab.pack_start(self.resumos[tipo], False, False, 0)

        chave = Gtk.Switch()
        chave.set_valign(Gtk.Align.CENTER)
        self.chaves[tipo] = chave
        cab.pack_end(chave, False, False, 0)

        raiz.pack_start(cab, False, False, 0)

        corpo = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        corpo.set_border_width(10)
        corpo.set_margin_top(0)
        self.blocos[tipo] = corpo
        raiz.pack_start(corpo, False, False, 0)

        def alternar_disclosure(_b=None):
            """Exclusivo: abre este e fecha os outros; se ja estava aberto,
            fecha e o dialogo encolhe. A janela so estica pelo bloco que
            estiver aberto — era esse o pedido."""
            if corpo.get_visible():
                corpo.set_visible(False)
                bt_disc.get_style_context().remove_class("bloco-aberto")
            else:
                self._abrir_bloco(tipo)
            self._encolher_para_conteudo()

        bt_disc.connect("clicked", alternar_disclosure)
        self._disclosures = getattr(self, "_disclosures", {})
        self._disclosures[tipo] = (corpo, bt_disc)

        grade = Gtk.Grid(column_spacing=8, row_spacing=6)
        for i, (ch, rot, dica) in enumerate(itens):
            lb = rotulo(rot, "rotulo")
            lb.set_width_chars(9)
            lb.set_xalign(0.0)
            ent = Gtk.Entry()
            ent.set_hexpand(True)
            if dica:
                ent.set_placeholder_text(dica)
            if "senha" in ch:
                ent.set_visibility(False)
                olho = Gtk.ToggleButton(label="👁")
                add_class(olho, "tog", "tog-glifo")
                olho.connect("toggled",
                             lambda b, e=ent: e.set_visibility(b.get_active()))
                grade.attach(olho, 2, i, 1, 1)
            self.campos[ch] = ent
            grade.attach(lb, 0, i, 1, 1)
            grade.attach(ent, 1, i, 1, 1)
        corpo.pack_start(grade, False, False, 0)

        linha = Gtk.Box(spacing=8)
        if tipo == "vnc":
            self.seg_modo, self.seg_modo_bt = segmentado(
                [("encaixar", "Encaixar"), ("1x1", "1:1"),
                 ("dinamico", "Dinâmico")], "encaixar", lambda _k: None)
            linha.pack_start(rotulo("Modo", "rotulo"), False, False, 0)
            linha.pack_start(self.seg_modo, False, False, 0)
            self.cb_ronly = Gtk.CheckButton(label="abrir bloqueado")
            self.cb_auto = Gtk.CheckButton(label="reconectar sozinho")
            linha.pack_end(self.cb_auto, False, False, 0)
            linha.pack_end(self.cb_ronly, False, False, 0)
        elif tipo == "ssh":
            self.cb_ssh_auto = Gtk.CheckButton(label="reconectar sozinho")
            linha.pack_end(self.cb_ssh_auto, False, False, 0)
        else:
            self.seg_rdp, self.seg_rdp_bt = segmentado(
                [("dinamico", "Dinâmico"), ("janela", "Janela"),
                 ("cheia", "Cheia")], "dinamico", lambda _k: None)
            linha.pack_start(rotulo("Tela", "rotulo"), False, False, 0)
            linha.pack_start(self.seg_rdp, False, False, 0)
            self.cb_rdp_auto = Gtk.CheckButton(label="reconectar sozinho")
            linha.pack_end(self.cb_rdp_auto, False, False, 0)
        corpo.pack_start(linha, False, False, 0)

        chave.connect("notify::active", self._chave_mudou, tipo)
        return raiz

    def _chave_mudou(self, chave, _p, tipo):
        """NUNCA chamar set_label() neste botao.

        Gtk.Button.set_label() DESTROI o filho atual e cria um Label no
        lugar. Como o cabecalho carrega uma Box com o icone e o titulo
        dentro, cada set_label apagava os dois e deixava so a setinha — era
        isso que fazia o icone sumir ao ligar o switch.

        Ligar um servico agora ABRE o bloco dele (e fecha os outros, o
        acordeao e exclusivo); desligar apenas fecha."""
        ligado = chave.get_active()
        if ligado:
            self._abrir_bloco(tipo)
        else:
            corpo, bt_disc = self._disclosures[tipo]
            corpo.set_visible(False)
            bt_disc.get_style_context().remove_class("bloco-aberto")
        self._sincronizar_blocos()
        self._encolher_para_conteudo()

    def _abrir_bloco(self, alvo):
        """Deixa somente `alvo` aberto. Ponto unico do acordeao."""
        for tipo, (corpo, bt) in self._disclosures.items():
            ativo = (tipo == alvo)
            corpo.set_visible(ativo)
            ctx = bt.get_style_context()
            ctx.remove_class("bloco-aberto")
            if ativo:
                ctx.add_class("bloco-aberto")

    def _encolher_para_conteudo(self):
        """O GTK3 nao encolhe a janela sozinho quando um filho fica menor —
        so cresce, nunca reduz por conta propria (e proposital, evita saltos
        de layout). resize(1, 1) pede o menor tamanho possivel; o GTK
        recalcula pra cima usando o tamanho NATURAL que os widgets visiveis
        agora pedem, entao a janela volta a caber no conteudo atual em vez
        de sobrar espaco em branco onde um bloco recolhido estava."""
        self.resize(1, 1)

    def _sincronizar_blocos(self):
        for tipo, corpo in self.blocos.items():
            ligado = self.chaves[tipo].get_active()
            corpo.set_sensitive(ligado)
            ctx = corpo.get_style_context()
            ctx.remove_class("bloco-off")
            if not ligado:
                ctx.add_class("bloco-off")
            self.resumos[tipo].set_text("" if ligado else "desligado")
            # o icone do cabecalho segue o switch: colorido = configurado,
            # cinza = desligado. Mesma leitura dos icones-acao do card.
            ic = getattr(self, "icones_bloco", {}).get(tipo)
            if ic is not None:
                ictx = ic.get_style_context()
                ictx.remove_class("bloco-ico-off")
                if not ligado:
                    ictx.add_class("bloco-ico-off")

    # ---- carga
    def _preencher(self):
        c = self.cx
        if c is None:
            self.campos["porta"].set_text("5900")
            self.campos["ssh_porta"].set_text("22")
            self.campos["rdp_porta"].set_text("3389")
            self.chaves["vnc"].set_active(True)
            # o proprio notify::active de set_active ja aciona _chave_mudou,
            # que mostra o corpo do bloco — nao precisa repetir aqui
            return
        for k, v in (("nome", c.nome), ("grupo", c.grupo), ("host", c.host),
                     ("porta", c.porta), ("usuario", c.usuario),
                     ("senha", c.senha), ("ssh_porta", c.ssh_porta),
                     ("ssh_usuario", c.ssh_usuario), ("ssh_senha", c.ssh_senha),
                     ("rdp_porta", c.rdp_porta), ("rdp_usuario", c.rdp_usuario),
                     ("rdp_senha", c.rdp_senha),
                     ("rdp_dominio", c.rdp_dominio),
                     ("rdp_extras", " ".join(c.rdp_extras))):
            self.campos[k].set_text(v or "")
        self.chaves["vnc"].set_active(c.tem_vnc)
        self.chaves["ssh"].set_active(c.tem_ssh)
        self.chaves["rdp"].set_active(c.tem_rdp)
        # set_active so dispara notify::active quando o valor MUDA; se a
        # conexao ja nasce com o servico desligado (False == default do
        # widget), o corpo tem de ser escondido explicitamente aqui
        for tipo, ligado in (("vnc", c.tem_vnc), ("ssh", c.tem_ssh),
                             ("rdp", c.tem_rdp)):
            corpo, _bt = self._disclosures[tipo]
            # so visibilidade: set_label aqui destruiria icone e titulo
            corpo.set_visible(ligado)
        self.seg_modo_bt[c.modo].set_active(True)
        self.seg_rdp_bt[c.rdp_tela].set_active(True)
        self.cb_ronly.set_active(c.ronly)
        self.cb_auto.set_active(c.auto)
        self.cb_ssh_auto.set_active(c.ssh_auto)
        self.cb_rdp_auto.set_active(c.rdp_auto)

    # ---- leitura
    @staticmethod
    def _ativo(grupo_bt):
        for k, b in grupo_bt.items():
            if b.get_active():
                return k
        return None

    def valores(self):
        v = {k: e.get_text().strip() for k, e in self.campos.items()}
        v["_vnc"] = self.chaves["vnc"].get_active()
        v["_ssh"] = self.chaves["ssh"].get_active()
        v["_rdp"] = self.chaves["rdp"].get_active()
        v["modo"] = self._ativo(self.seg_modo_bt) or "encaixar"
        v["rdp_tela"] = self._ativo(self.seg_rdp_bt) or "dinamico"
        v["ronly"] = self.cb_ronly.get_active()
        v["auto"] = self.cb_auto.get_active()
        v["ssh_auto"] = self.cb_ssh_auto.get_active()
        v["rdp_auto"] = self.cb_rdp_auto.get_active()
        return v

    def validar(self, v, nomes_existentes):
        if not v["nome"]:
            return "O nome é obrigatório: ele vira o [bloco] no INI."
        if "[" in v["nome"] or "]" in v["nome"]:
            return "O nome não pode conter colchetes."
        if v["nome"].lower() == SECAO_GERAL:
            return "'%s' é reservado para as preferências." % SECAO_GERAL
        if (v["nome"] != self.nome_antigo
                and v["nome"].lower() in nomes_existentes):
            return "Já existe uma conexão chamada '%s'." % v["nome"]
        if not v["host"]:
            return "O host é obrigatório."
        if not (v["_vnc"] or v["_ssh"] or v["_rdp"]):
            return "Ligue ao menos um serviço: tela, shell ou RDP."
        if v["_ssh"] and not v["ssh_usuario"]:
            return "SSH ligado exige um usuário."
        for chave, rot in (("porta", "Porta da tela"),
                           ("ssh_porta", "Porta do SSH"),
                           ("rdp_porta", "Porta do RDP")):
            if v[chave] and not v[chave].isdigit():
                return "%s deve ser numérica." % rot
        return None


# ---------------------------------------------------------------- janela

# ---------------------------------------------------------------------------
# ABA SSH: definida em ssh.py.
#
# Construida AQUI, e nao no topo do arquivo, porque a classe herda de
# AbaBase — que so existe a partir deste ponto. O modulo ssh.py expoe uma
# fabrica justamente para permitir isso sem import circular.
#
# Sem o modulo (ou sem o VTE), AbaSsh fica None e abrir() avisa em vez de
# quebrar.
# ---------------------------------------------------------------------------
try:
    import ssh as _ssh
    AbaSsh = _ssh.construir(
        AbaBase,
        add_class=add_class,
        carregar_snippets=carregar_snippets,
        fonte_mono=fonte_mono,
        rgba=rgba)
    TEM_VTE, ERRO_VTE = _ssh.TEM_VTE, _ssh.ERRO_VTE
except Exception as e:
    AbaSsh, TEM_VTE = None, False
    ERRO_VTE = "módulo ssh.py indisponível: %s" % e


# ---------------------------------------------------------------------------
# ABA DE EXECUCAO EM MASSA: definida em massa_ui.py.
#
# Construida AQUI porque usa os utilitarios de interface deste arquivo
# (add_class, chip, rotulo...), que so existem a partir deste ponto. O par
# e massa.py, o motor — que nao importa GTK e por isso continua testavel
# sem display.
# ---------------------------------------------------------------------------
try:
    import massa_ui as _massa_ui
    AbaLote = _massa_ui.construir(
        Snippet=Snippet, add_class=add_class, agora=agora,
        carregar_snippets=carregar_snippets, chip=chip, regua=regua,
        revelar=revelar, rotulo=rotulo)
    # TEM_MASSA ja foi definido no topo pelo import do motor; aqui so
    # rebaixamos se a INTERFACE faltar. Sem uma das duas partes nao ha
    # execucao em lote.
    if not _massa_ui.TEM_MASSA:
        TEM_MASSA, ERRO_MASSA = False, _massa_ui.ERRO_MASSA
except Exception as e:
    AbaLote, TEM_MASSA = None, False
    ERRO_MASSA = "módulo massa_ui.py indisponível: %s" % e



class Janela(Gtk.Window):
    # Altura ABSOLUTA e igual para toda aba (inicio e sessao). CSS min-height
    # so garante um piso, nunca um teto — a unica forma de a barra de abas
    # nunca crescer, aconteça o que acontecer dentro de cada aba, e travar
    # esse valor explicitamente em toda construcao de rotulo de aba.
    ALTURA_ABA = 22
    # Alturas ABSOLUTAS das duas partes do card que variavam de tamanho
    # conforme o conteudo (bloco de meta e linha de botoes). Travadas em
    # pixels, todo card fica com a MESMA altura, tenha ele 1, 2 ou 3
    # servicos configurados.
    ALT_META = 34
    ALT_BOTOES = 30

    def __init__(self, conexoes, geral, caminho):
        super().__init__(title="Acessos")
        self.set_role("acessos-principal")
        try:
            # set_wmclass e deprecado (e ruidoso no terminal), mas ainda e
            # o que faz gestores de janela X11 antigos agruparem a janela.
            # Chamada com o warning suprimido: efeito mantido, log limpo.
            import warnings
            with warnings.catch_warnings():
                warnings.simplefilter("ignore", DeprecationWarning)
                self.set_wmclass("acessos", "Acessos")   # X11 legado
        except Exception:
            pass
        self.conexoes = conexoes
        self.geral = geral
        self.caminho = caminho
        self.abas = {}
        self._ja_ofereceu_x11 = False
        self._timer_busca = None
        self._timer_lateral = None
        self._encerrando = False
        # selecao para execucao em lote, por NOME (nao por objeto): a lista
        # de conexoes e recriada a cada recarregamento do INI, e guardar
        # referencia de objeto perderia a selecao a cada edicao
        self.selecionados = set()
        self._marcas = {}
        self._marcas_grupo = []
        self._sync_marcas = False
        self.mostrar_log = verdade(geral.get("log", ""), False)
        self.tema = geral.get("tema", "claro").strip().lower()
        if self.tema not in TEMAS:
            self.tema = "claro"
        self.prov = Gtk.CssProvider()

        self.set_default_size(1340, 840)
        self.connect("destroy", self._sair)
        self._aplicar_css()
        self._montar()

    def cor(self, chave):
        return TEMAS[self.tema][chave]

    def acento(self, i):
        return ACENTOS[i % len(ACENTOS)]

    def _aplicar_css(self):
        # Uma regra invalida faz o CssProvider recusar a folha INTEIRA e
        # levanta GError — sem este guarda, um erro de estilo impede o
        # programa de abrir. Melhor rodar feio do que nao rodar.
        try:
            self.prov.load_from_data(gerar_css(self.tema))
        except GLib.Error as e:
            sys.stderr.write("CSS recusado (%s); seguindo com o tema do "
                             "sistema\n" % e.message)
            try:
                self.prov.load_from_data(b"window { background-color: #eef0f2; }")
            except GLib.Error:
                return
        Gtk.StyleContext.add_provider_for_screen(
            Gdk.Screen.get_default(), self.prov,
            Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

        settings = Gtk.Settings.get_default()
        # Fixar o MOTOR de tema, nao so a preferencia de escuro.
        #
        # Rodando "python3 acessos.py" o GTK descobre o tema via gsettings/
        # D-Bus da sessao (o que a pessoa escolheu no GNOME, KWin, etc.) e
        # aplica em cima disso. Compilado (Nuitka standalone ou AppImage), o
        # processo pode nao enxergar a mesma sessao/D-Bus, e o GTK cai num
        # Adwaita "de fabrica" cujas variantes clara/escura nao necessariamente
        # coincidem com o que aparecia no .py direto — dai o "invertido".
        #
        # Todas as cores que importam ja vem da NOSSA folha CSS (TEMAS +
        # gerar_css); o motor do sistema so pinta os poucos widgets nativos
        # que nao cobrimos (Switch, popup do ComboBox, barra de rolagem).
        # Fixando o motor em Adwaita, essa pintura nativa fica identica nas
        # duas formas de rodar, independente do que a maquina tem instalado
        # ou de qual sessao o processo enxerga.
        try:
            settings.set_property("gtk-theme-name", "Adwaita")
        except Exception:
            pass
        settings.set_property(
            "gtk-application-prefer-dark-theme", self.tema == "escuro")

    def gravar(self, secao, chave, valor):
        gravar_chave(self.caminho, secao, chave, valor)

    def _sair(self, _w):
        # marca antes de qualquer coisa: temporizadores pendentes checam isto
        self._encerrando = True
        for t in (self._timer_busca, self._timer_lateral,
                  getattr(self, "_timer_colunas", None)):
            if t:
                try:
                    GLib.source_remove(t)
                except Exception:
                    pass
        for aba in list(self.abas.values()):
            try:
                aba.desconectar()
            except Exception:
                pass
        self.soltar_capturas()
        GrabNativo.soltar_tudo()
        try:
            # NAO gravar posicao colapsada. Com a lateral escondida (F9) o
            # Paned reporta posicao perto de zero; gravando isso, o proximo
            # arranque abria a lateral com 1px de largura — visivel so como
            # uma listra fina, dando a impressao de que o F9 nao funcionava.
            # Abaixo do minimo, preserva-se o valor anterior.
            pos = self.painel.get_position()
            if pos >= LARG_MIN_LATERAL:
                self.gravar(SECAO_GERAL, "painel", pos)
            self.gravar(SECAO_GERAL, "lateral",
                        "1" if self.bt_lateral.get_active() else "0")
            self.gravar(SECAO_GERAL, "tema", self.tema)
        except Exception:
            pass
        Gtk.main_quit()

    # -------------------------------------------------- estrutura
    def _montar(self):
        self.set_titlebar(self._titlebar())
        raiz = add_class(Gtk.Box(orientation=Gtk.Orientation.VERTICAL), "fundo")
        self.add(raiz)

        self.painel = Gtk.Paned(orientation=Gtk.Orientation.HORIZONTAL)
        try:
            # max() com o minimo: conserta tambem os .ini que ja ficaram
            # gravados com um valor colapsado (painel = 1), caso em que a
            # lateral existe e esta visivel, mas com largura de uma listra.
            self.painel.set_position(
                max(LARG_MIN_LATERAL, int(self.geral.get("painel", 240))))
        except (TypeError, ValueError):
            self.painel.set_position(240)
        raiz.pack_start(self.painel, True, True, 0)
        self.lateral = self._lateral()
        self.painel.pack1(self.lateral, False, True)
        self.painel.pack2(self._notebook(), True, False)
        self.rodape = self._rodape()
        raiz.pack_start(self.rodape, False, False, 0)
        self.rodape.set_no_show_all(True)
        self.nb.connect("switch-page", self._trocou_aba)
        self.bt_lateral.set_active(verdade(self.geral.get("lateral", "1"), True))

    def _titlebar(self):
        hb = Gtk.HeaderBar()
        hb.set_show_close_button(False)
        hb.set_has_subtitle(False)
        add_class(hb, "integrada")

        esq = Gtk.Box(spacing=8)
        self.bt_lateral = Gtk.ToggleButton(label="☰")
        add_class(self.bt_lateral, "btn-topo")
        # ALINHAMENTO, nao CSS: botao em headerbar nasce com valign FILL e
        # estica ate a altura da barra — o min-height do tema nunca ia
        # segurar. CENTER e o que faz a pilula ter altura propria.
        self.bt_lateral.set_valign(Gtk.Align.CENTER)
        self.bt_lateral.set_tooltip_text("Mostrar ou esconder a lista (F9)")
        self.bt_lateral.connect("toggled", self._alternar_lateral)
        esq.pack_start(self.bt_lateral, False, False, 0)
        esq.pack_start(rotulo("Acessos", "marca-topo"), False, False, 4)
        esq.pack_start(rotulo("VNC · SSH · RDP", "marca-sub"), False, False, 0)
        self.lb_captura = chip("⌨ CAPTURADO — Pause libera", "atencao")
        self.lb_captura.set_no_show_all(True)
        esq.pack_start(self.lb_captura, False, False, 4)
        hb.pack_start(esq)

        dir_ = Gtk.Box(spacing=0)
        for glifo, dica, fn, extra in (
                ("—", "Minimizar", lambda _b: self.iconify(), None),
                ("▢", "Maximizar/restaurar", self._alternar_max, None),
                ("✕", "Fechar", lambda _b: self.close(), "btn-fechar")):
            b = add_class(Gtk.Button(label=glifo), "btn-janela")
            if extra:
                add_class(b, extra)
            b.set_tooltip_text(dica)
            b.connect("clicked", fn)
            dir_.pack_start(b, False, False, 0)
        hb.pack_end(dir_)

        acoes = Gtk.Box(spacing=5)
        # o glifo de engrenagem foi para os AJUSTES, que e a convencao
        # universal; o diagnostico vira pilula de texto como as demais
        # O diagnostico saiu da barra e foi para os Ajustes: e opcao, nao
        # acao frequente. O ToggleButton continua existindo SEM PAI — ele
        # segue sendo a fonte unica do estado e o alvo do F12, e o
        # interruptor dos Ajustes apenas o espelha. Assim nao ha dois
        # lugares guardando a mesma verdade.
        self.bt_log = Gtk.ToggleButton()
        self.bt_log.set_active(self.mostrar_log)
        self.bt_log.connect("toggled", self._alternar_log)

        self.bt_tema = Gtk.ToggleButton(
            label="☾" if self.tema == "claro" else "☀")
        add_class(self.bt_tema, "btn-topo")
        self.bt_tema.set_valign(Gtk.Align.CENTER)
        self.bt_tema.set_active(self.tema == "escuro")
        self.bt_tema.set_tooltip_text("Alternar tema claro/escuro")
        self.bt_tema.connect("toggled", self._alternar_tema)
        acoes.pack_start(self.bt_tema, False, False, 0)
        for texto, dica, fn in (
                ("✎ snippets", "Biblioteca de comandos para execução em lote",
                 self.abrir_snippets),
                ("＋ nova", "Cadastrar uma conexão", self.nova_conexao),
                ("⌂  início", "Voltar ao painel", self._ir_home),
                ("⟳", "Recarregar o INI", self._recarregar),
                ("✎  INI", "Abrir o arquivo de conexões", self._editar_ini),
                ("⚙", "Ajustes", self._abrir_ajustes)):
            b = add_class(Gtk.Button(label=texto), "btn-topo")
            b.set_valign(Gtk.Align.CENTER)
            b.set_tooltip_text(dica)
            b.connect("clicked", fn)
            acoes.pack_start(b, False, False, 0)
        hb.pack_end(acoes)

        self.connect("key-press-event", self._teclas_globais)
        self.connect("focus-out-event", lambda *_a: self.soltar_capturas())
        # RETOMAR AO VOLTAR. O focus-out solta o grab de proposito (teclado
        # preso com a janela em segundo plano deixaria o operador sem
        # teclado no resto do sistema), mas nada o reatava: era preciso
        # clicar no botao de novo toda vez que se alternava de janela.
        #
        # O botao continua marcado, entao o estado que o operador pediu
        # segue valendo — so o grab precisa ser refeito.
        self.connect("focus-in-event", lambda *_a: self.reatar_capturas())
        return hb

    def reatar_capturas(self):
        """Refaz o grab da aba visível, se o botão de teclado está ligado.

        Só a aba VISÍVEL: reatar em todas colocaria o grab numa sessão que
        o operador nem está vendo."""
        pagina = self.nb.get_current_page()
        aba = self.nb.get_nth_page(pagina) if pagina >= 0 else None
        if aba is not None and hasattr(aba, "reatar_teclado"):
            # num idle: durante o proprio focus-in o GTK ainda esta
            # acertando o foco interno, e um grab pedido aqui pode ser
            # recusado ou apontar para o widget errado
            GLib.idle_add(aba.reatar_teclado)
        return False

    def soltar_capturas(self, desligar_botao=False):
        """Chamado de todo lugar que possa deixar um grab preso."""
        for aba in self.abas.values():
            if hasattr(aba, "soltar_teclado"):
                aba.soltar_teclado()
                if desligar_botao and hasattr(aba, "bt_teclado"):
                    aba.bt_teclado.set_active(False)
        return False

    def marcar_captura(self, ativo):
        """Aviso visível: teclado capturado é estado perigoso de esquecer."""
        # guardado tambem como estado: o _teclas_globais precisa saber que
        # ha captura para PARAR de interceptar F9/F12 (ver la)
        self.captura_ativa = bool(ativo)
        self.set_title("Acessos — TECLADO CAPTURADO (Pause libera)"
                       if ativo else "Acessos")
        if hasattr(self, "lb_captura"):
            self.lb_captura.set_visible(ativo)

    def _alternar_log(self, bt):
        self.mostrar_log = bt.get_active()
        for aba in self.abas.values():
            if self.mostrar_log:
                # no_show_all impediu o show_all inicial de percorrer os
                # filhos: o painel aparecia como um retangulo vazio porque o
                # TextView dentro dele nunca tinha sido exibido
                revelar(aba.exp_log)
            aba.exp_log.set_visible(self.mostrar_log)
        self.gravar(SECAO_GERAL, "log", "1" if self.mostrar_log else "0")

    def _alternar_max(self, _b):
        self.unmaximize() if self.is_maximized() else self.maximize()

    def _alternar_lateral(self, bt):
        self.lateral.set_visible(bt.get_active())

    def _teclas_globais(self, _w, ev):
        # F11 NAO e mais atalho global do app. A aplicacao ja abre
        # maximizada (main()), entao nao ha alternancia a fazer aqui — e o
        # motivo real e deixar F11 passar intacto para dentro da sessao
        # ativa: navegadores e apps dentro do VNC/RDP usam F11 para o
        # PROPRIO fullscreen, e um acelerador nosso na mesma tecla roubava
        # o evento antes de a sessao remota ve-lo.
        nome = Gdk.keyval_name(ev.keyval) or ""

        # PAUSE PRIMEIRO, e sempre. E a valvula de escape: precisa funcionar
        # inclusive — principalmente — com o teclado capturado.
        if nome == "Pause":
            self.soltar_capturas(desligar_botao=True)
            return True

        # COM CAPTURA ATIVA, O APP NAO FICA COM TECLA NENHUMA.
        #
        # Este handler esta ligado na JANELA, e no GTK3 o key-press-event da
        # janela roda ANTES de chegar ao widget com foco. Ou seja: F9 e F12
        # eram engolidos aqui e nunca alcançavam a sessao remota, mesmo com
        # o grab ligado. Capturar o teclado tem de significar exatamente
        # isso — tudo vai para a maquina remota.
        if getattr(self, "captura_ativa", False):
            return False

        if nome == "F9":
            self.bt_lateral.set_active(not self.bt_lateral.get_active())
            return True
        if nome == "F12":
            self.bt_log.set_active(not self.bt_log.get_active())
            return True
        return False

    def _alternar_tema(self, bt):
        self.tema = "escuro" if bt.get_active() else "claro"
        bt.set_label("☀" if self.tema == "escuro" else "☾")
        self._aplicar_css()
        self.gravar(SECAO_GERAL, "tema", self.tema)
        self._encher_home()
        for aba in self.abas.values():          # o VTE nao le CSS
            if isinstance(aba, AbaSsh):
                try:
                    aba.term.set_colors(rgba(self.cor("term_fg")),
                                        rgba(self.cor("term_bg")), None)
                except Exception:
                    pass

    # -------------------------------------------------- lateral
    def _lateral(self):
        cx = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        cx.set_border_width(8)
        cx.set_size_request(180, -1)

        self.busca = Gtk.SearchEntry()
        self.busca.set_placeholder_text("filtrar…")
        self.busca.connect("search-changed", self._filtrar_lateral_adiado)
        cx.pack_start(self.busca, False, False, 0)

        self.store = Gtk.TreeStore(str, int, str, str)
        self.filtro = self.store.filter_new()
        self.filtro.set_visible_func(self._visivel)

        self.tree = Gtk.TreeView(model=self.filtro)
        self.tree.set_headers_visible(False)
        self.tree.set_enable_search(False)
        self.tree.set_level_indentation(4)
        add_class(self.tree, "lista")

        col = Gtk.TreeViewColumn("Conexão")
        rs = Gtk.CellRendererText()
        rs.set_property("scale", 0.72)
        rs.set_property("foreground", self.cor("fraco"))
        col.pack_start(rs, False)
        col.add_attribute(rs, "text", 3)
        r1 = Gtk.CellRendererText()
        r1.set_property("ellipsize", Pango.EllipsizeMode.MIDDLE)
        col.pack_start(r1, True)
        col.add_attribute(r1, "text", 0)
        self.tree.append_column(col)
        self.tree.add_events(Gdk.EventMask.BUTTON_PRESS_MASK)
        self.tree.connect("row-activated", self._ativar_linha)
        self.tree.connect("button-press-event", self._menu_lateral)

        sw = Gtk.ScrolledWindow()
        sw.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
        sw.add(self.tree)
        add_class(sw, "cartao")
        cx.pack_start(sw, True, True, 0)

        linha = Gtk.Box(spacing=4)
        bt_novo = add_class(Gtk.Button(label="＋"), "secundaria", "tog-glifo")
        bt_novo.set_tooltip_text("Cadastrar uma conexão")
        bt_novo.connect("clicked", self.nova_conexao)
        linha.pack_start(bt_novo, False, False, 0)
        for rot, tp, classe in (("Tela", "vnc", "acao"),
                                ("Shell", "ssh", "secundaria"),
                                ("RDP", "rdp", "secundaria")):
            b = add_class(Gtk.Button(label=rot), classe)
            b.connect("clicked", lambda _x, t=tp: self._abrir_sel(t))
            b.add_events(Gdk.EventMask.BUTTON_PRESS_MASK)
            b.connect("button-press-event", self._meio_sel, tp)
            b.set_tooltip_text("%s · clique do meio abre em segundo plano" % rot)
            linha.pack_start(b, True, True, 0)
        cx.pack_start(linha, False, False, 0)

        self._popular()
        GLib.idle_add(self._sync_botao_lote)
        return cx

    def _navegar_abas(self, ev):
        """Passo de navegacao propriamente dito, sem nenhuma checagem de
        procedencia — quem chama e que garante que o evento veio da barra."""
        total = self.nb.get_n_pages()
        if total < 2:
            return False
        atual = self.nb.get_current_page()
        if ev.direction == Gdk.ScrollDirection.DOWN:
            self._acum_scroll = 0.0
            passo = 1
        elif ev.direction == Gdk.ScrollDirection.UP:
            self._acum_scroll = 0.0
            passo = -1
        elif ev.direction == Gdk.ScrollDirection.SMOOTH:
            # Roda de ALTA RESOLUCAO manda dezenas de eventos por entalhe,
            # com deltas fracionarios (0.05, 0.1…). Tratar cada um como um
            # passo fazia um unico giro atravessar a lista inteira.
            # Por isso acumulamos: so anda uma aba quando o somatorio
            # completa 1.0, e o resto fica guardado para o proximo evento.
            ok, _dx, dy = ev.get_scroll_deltas()
            if not ok or dy == 0:
                return False
            acum = getattr(self, "_acum_scroll", 0.0)
            # inverteu o sentido? descarta o residuo, senao o primeiro
            # movimento para o outro lado sai atrasado
            if (dy > 0) != (acum > 0):
                acum = 0.0
            acum += dy
            if abs(acum) < 1.0:
                self._acum_scroll = acum
                return True          # consumido, mas ainda sem passo
            passo = 1 if acum > 0 else -1
            self._acum_scroll = acum - passo
        else:
            return False
        # sem dar a volta: chegar na ponta e continuar rolando nao deve
        # saltar para o outro extremo, que desorienta
        novo = max(0, min(total - 1, atual + passo))
        if novo != atual:
            self.nb.set_current_page(novo)
        return True

    def _rolar_rotulo(self, _w, ev):
        """Scroll vindo do EventBox de um rotulo de aba.

        Nao ha checagem de procedencia: por construcao o evento nasceu na
        barra, porque este handler so existe no EventBox do rotulo. E o
        unico caminho que navega entre abas."""
        return self._navegar_abas(ev)

    def _notebook(self):
        self.nb = Gtk.Notebook()
        self.nb.set_scrollable(True)
        self.nb.set_show_border(False)
        # a roda do mouse so chega ao Notebook se a mascara for pedida
        self.nb.add_events(Gdk.EventMask.SCROLL_MASK
                           | Gdk.EventMask.SMOOTH_SCROLL_MASK)
        # SEM handler de scroll no Notebook.
        #
        # Ele existia para captar a roda "sobre a barra", mas o Notebook
        # recebe por propagacao o scroll de QUALQUER lugar da pagina. A
        # guarda que tentava distinguir a origem comparava GdkWindow, e nao
        # funciona: widgets sem janela propria (a AbaVnc e um Gtk.Box)
        # devolvem a janela do ancestral, entao ou a guarda bloqueava tudo,
        # ou — ao ser afrouxada — deixava passar scroll de dentro da sessao
        # e a roda trocava de aba enquanto o operador rolava um log.
        #
        # Agora cada ROTULO de aba tem seu proprio handler (_rolar_rotulo),
        # e so ele navega. A origem do evento fica garantida por
        # construcao, sem adivinhacao.
        self.home = self._pagina_home()
        # ALTURA fixa da aba, sempre: um Box com altura travada em pixels,
        # igual para "inicio" e para toda aba de sessao — CSS min-height so
        # define um PISO, nao um teto, entao a unica forma de garantir que a
        # barra NUNCA cresça e forcar a mesma altura absoluta em todo lugar
        # que vira rotulo de aba.
        rotulo_home = Gtk.Box()
        rotulo_home.set_size_request(-1, self.ALTURA_ABA)
        # Icone com CONFIRMACAO de existencia, e nao no escuro.
        #
        # O historico deste ponto: glifo "⌂" (fraco no corpo pequeno),
        # "go-home-symbolic" (o Gtk.Image renderiza NADA, sem erro nenhum,
        # se o icone nao existir no tema instalado) e desenho em Cairo
        # (funcionava, mas era codigo demais para um rotulo de aba).
        #
        # Agora eu PERGUNTO ao tema quais icones ele tem, com
        # IconTheme.has_icon(), e so uso o primeiro que existir de verdade.
        # Se nenhum existir, cai em texto puro — que nao depende de nada.
        # Assim nao ha como sumir de novo, em maquina nenhuma.
        rotulo_home.set_valign(Gtk.Align.CENTER)
        tema_icones = Gtk.IconTheme.get_default()
        alvo = None
        for nome_icone in ("go-home-symbolic", "go-home",
                           "user-home-symbolic", "user-home",
                           "gtk-home"):
            try:
                if tema_icones.has_icon(nome_icone):
                    alvo = nome_icone
                    break
            except Exception:
                continue
        if alvo:
            img_home = Gtk.Image.new_from_icon_name(alvo, Gtk.IconSize.MENU)
            img_home.set_valign(Gtk.Align.CENTER)
            img_home.set_halign(Gtk.Align.CENTER)
            rotulo_home.pack_start(img_home, True, True, 8)
        else:
            rotulo_home.pack_start(
                rotulo("Painel", "aba-nome", xalign=0.5, ellipsize=None),
                True, True, 6)
        rotulo_home.set_tooltip_text("Voltar ao painel")
        # ERA ISTO. O rotulo da aba e um Gtk.Box com filhos, e um Box so
        # exibe os filhos depois de show_all() — as abas de sessao ja faziam
        # isso (ev.show_all()), esta aqui nunca fez. Enquanto era um
        # Gtk.Label simples funcionava, porque o Notebook mostra rotulo
        # simples sozinho; ao virar Box com um Image dentro, o icone ficava
        # invisivel. Nao era o nome do icone, nem o tema, nem a cor.
        rotulo_home.show_all()
        self.nb.append_page(self.home, rotulo_home)
        return self.nb

    # -------------------------------------------------- dashboard
    def _pagina_home(self):
        sw = Gtk.ScrolledWindow()
        # CLASSE PROPRIA em vez de confiar no seletor "scrolledwindow".
        # O Adwaita estiliza esses widgets por CLASSE, e classe vence
        # elemento na especificidade — a regra generica do tema perdia e o
        # viewport continuava opaco, tampando o gradiente da janela.
        add_class(sw, "transparente")
        sw.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
        self.home_caixa = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        self.home_caixa.set_valign(Gtk.Align.START)
        sw.add(self.home_caixa)

        # Hero e barra de ferramentas sao construidos UMA vez e nunca mais
        # destruidos. Antes eu recriava tudo a cada tecla, o que destruia o
        # proprio Gtk.SearchEntry no meio da digitacao: o foco sumia, o
        # backspace ia para o widget morto e a tela piscava sem parar.
        self.hero_caixa = Gtk.Box()
        self.home_caixa.pack_start(self.hero_caixa, False, False, 0)

        ferr = self._ferramentas_home()
        ferr.set_margin_start(18)
        ferr.set_margin_end(18)
        ferr.set_margin_top(14)
        ferr.set_margin_bottom(10)
        self.home_caixa.pack_start(ferr, False, False, 0)

        self.grupos_caixa = Gtk.Box(orientation=Gtk.Orientation.VERTICAL,
                                    spacing=14)
        self.grupos_caixa.set_valign(Gtk.Align.START)
        self.grupos_caixa.set_margin_start(18)
        self.grupos_caixa.set_margin_end(18)
        self.grupos_caixa.set_margin_bottom(18)
        self.home_caixa.pack_start(self.grupos_caixa, False, False, 0)
        self._timer_colunas = None
        # redimensionar a janela ou mover a lateral muda quantos cards cabem
        self.grupos_caixa.connect("size-allocate", self._agendar_colunas)

        self._atualizar_hero()
        self._encher_home()
        return sw

    def _hero(self):
        """Faixa de topo do painel.

        Antes era um cartao arredondado com margem por todos os lados, o que
        o fazia parecer solto no meio do vazio. Agora ele encosta nas bordas
        e ancora a pagina: e a primeira coisa que a vista encontra."""
        hero = add_class(Gtk.Box(spacing=0), "hero")
        hero.set_hexpand(True)

        esq = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=3)
        esq.set_valign(Gtk.Align.CENTER)
        esq.set_margin_start(24)
        esq.set_margin_top(20)
        esq.set_margin_bottom(20)
        esq.pack_start(rotulo("PAINEL DE ACESSOS", "hero-eyebrow"),
                       False, False, 0)
        t = rotulo(os.path.basename(os.path.dirname(self.caminho)) or "acessos",
                   "hero-titulo")
        t.set_ellipsize(Pango.EllipsizeMode.NONE)
        esq.pack_start(t, False, False, 0)
        # caminho do INI e credito na MESMA linha: o credito entra sem
        # custar altura nenhuma ao hero, so ocupando a folga que ja existia
        # a direita daquela linha
        linha_rodape = Gtk.Box(spacing=10)
        linha_rodape.pack_start(rotulo(self.caminho, "hero-sub"),
                                False, False, 0)
        linha_rodape.pack_start(rotulo("Desenvolvido por @JJMoratelli",
                                       "hero-credito"), False, False, 0)
        esq.pack_start(linha_rodape, False, False, 0)
        hero.pack_start(esq, True, True, 0)

        n_vnc = sum(1 for c in self.conexoes if c.tem_vnc)
        n_ssh = sum(1 for c in self.conexoes if c.tem_ssh)
        n_rdp = sum(1 for c in self.conexoes if c.tem_rdp)
        n_gru = len({tuple(c.caminho_grupo) for c in self.conexoes})

        painel = Gtk.Box(spacing=0)
        painel.set_valign(Gtk.Align.CENTER)
        painel.set_margin_end(24)
        dados = ((len(self.conexoes), "MÁQUINAS"), (n_vnc, "TELA"),
                 (n_ssh, "SHELL"), (n_rdp, "RDP"),
                 (n_gru, "GRUPOS"), (len(self.abas), "ABERTAS"))
        for i, (valor, cap) in enumerate(dados):
            if i:
                painel.pack_start(
                    pintar(add_class(Gtk.Box(), "hero-risco"), "#ffffff"),
                    False, False, 0)
                painel.get_children()[-1].set_size_request(1, 30)
            col = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
            col.set_valign(Gtk.Align.CENTER)
            n = rotulo(str(valor), "hero-num", xalign=0.5)
            n.set_ellipsize(Pango.EllipsizeMode.NONE)
            col.pack_start(n, False, False, 0)
            col.pack_start(rotulo(cap, "hero-cap", xalign=0.5), False, False, 0)
            col.set_size_request(78, -1)
            painel.pack_start(col, False, False, 0)
        hero.pack_end(painel, False, False, 0)
        return hero

    def _ferramentas_home(self):
        """Construida uma unica vez; ver comentario em _pagina_home.

        Recria-la a cada filtragem destruia o proprio SearchEntry no meio da
        digitacao: o foco sumia e o backspace ia para o widget morto."""
        barra = Gtk.Box(spacing=6)
        self.busca_home = Gtk.SearchEntry()
        self.busca_home.set_placeholder_text(
            "filtrar máquinas, hosts ou grupos…")
        self.busca_home.connect("search-changed", self._busca_home_mudou)
        # Enter: se o texto parece um destino e nada bate com o filtro,
        # conecta direto no protocolo inferido. Achou maquina? Enter nao faz
        # nada e voce clica no card, como sempre.
        self.busca_home.connect("activate", self._busca_home_enter)
        barra.pack_start(self.busca_home, True, True, 0)

        # --- selecao e execucao em lote, na mesma linha da busca
        self.lb_selecao = rotulo("", "card-meta", xalign=1.0)
        barra.pack_end(self.lb_selecao, False, False, 4)

        self.bt_lote = add_class(Gtk.Button(label="⚡  executar"), "acao")
        self.bt_lote.set_tooltip_text(
            "Executar comandos nas máquinas selecionadas, em lote")
        self.bt_lote.set_sensitive(False)
        self.bt_lote.connect("clicked", lambda _b: self.abrir_lote())
        barra.pack_end(self.bt_lote, False, False, 0)

        # MenuButton em vez de ComboBoxText: o combo do GTK abre o popup
        # no botao PRESSIONADO e fecha ao soltar, o que obriga a clicar,
        # segurar e arrastar ate a opcao. Nao era proposital — e o
        # comportamento padrao dele. O MenuButton abre no clique e fica
        # aberto, que e o que se espera de um menu de acoes.
        bt_sel = Gtk.MenuButton(label="seleção  ▾")
        add_class(bt_sel, "secundaria")
        bt_sel.set_tooltip_text("Seleção para execução em lote")
        menu_sel = Gtk.Menu()
        for acao, rot in (("todos", "selecionar todas"),
                          ("nenhum", "limpar seleção"),
                          ("inverter", "inverter seleção")):
            mi = Gtk.MenuItem(label=rot)
            mi.connect("activate", lambda _m, a=acao: self._selecao_massa(a))
            menu_sel.append(mi)
        menu_sel.show_all()
        bt_sel.set_popup(menu_sel)
        barra.pack_end(bt_sel, False, False, 0)

        for texto, val in (("expandir tudo", True), ("recolher tudo", False)):
            b = add_class(Gtk.Button(label=texto), "secundaria")
            b.connect("clicked", lambda _b, v=val: self._todos_grupos(v))
            barra.pack_end(b, False, False, 0)
        return barra

    def _busca_home_mudou(self, _e=None):
        if self._timer_busca:
            GLib.source_remove(self._timer_busca)
        self._timer_busca = GLib.timeout_add(320, self._busca_home_aplicar)

    def _busca_home_enter(self, _e=None):
        txt = self.busca_home.get_text().strip()
        if not txt:
            return
        alvo = [c for c in self.conexoes
                if txt.lower() in c.nome.lower()
                or txt.lower() in c.host.lower()]
        if alvo:
            return          # tem resultado: Enter nao atropela a escolha
        proposta = interpretar_alvo(txt)
        if proposta:
            self._conectar_efemero(dict(proposta[0]), proposta[1])

    def _busca_home_aplicar(self):
        self._timer_busca = None
        self._encher_home()
        return False

    def _todos_grupos(self, aberto):
        def andar(w):
            if isinstance(w, Gtk.Expander):
                w.set_expanded(aberto)
                filho = w.get_child()
                if filho is not None:
                    andar(filho)
                return
            if isinstance(w, Gtk.Container):
                for f in w.get_children():
                    andar(f)
        andar(self.grupos_caixa)

    def _encher_home(self):
        # A reconstrucao zera a rolagem: o ScrolledWindow perde a posicao
        # junto com os filhos. Ao duplicar uma conexao no meio de 276
        # maquinas, a lista pulava para o topo e voce perdia o lugar.
        # Guarda o valor e devolve DEPOIS que o novo conteudo foi alocado —
        # antes disso o ajuste ainda nao tem altura para aceitar o valor.
        try:
            ajuste = self.home.get_vadjustment()
            pos = ajuste.get_value()
        except Exception:
            ajuste, pos = None, 0.0

        # destroy(), NAO remove().
        #
        # remove() apenas desvincula do container: o widget continua VIVO, e
        # cada EventBox mantem sua propria GdkWindow recebendo clique nas
        # coordenadas ANTIGAS. Dai o sintoma de "clico numa coisa e abre
        # outra", e o de botao que nao responde — o clique estava sendo
        # capturado por um card fantasma de uma reconstrucao anterior.
        #
        # Como _encher_home roda a cada tecla digitada na busca e a cada
        # expandir/recolher, isso vazava centenas de widgets por minuto.
        for f in self.grupos_caixa.get_children():
            f.destroy()
        # os widgets de marca vao ser recriados junto com os cards; o
        # conjunto self.selecionados (por nome) e que preserva a escolha
        self._marcas.clear()
        self._marcas_grupo.clear()

        # GERACAO: os cards acabaram de ser destruidos. Toda sonda em voo
        # carrega o numero da geracao em que nasceu e e DESCARTADA se a
        # lista foi reconstruida no meio do caminho. Sem isso um worker
        # voltaria escrevendo num Gtk.Box ja destruido — que e exatamente o
        # tipo de acesso que derruba ou congela o processo.
        self._geracao = getattr(self, "_geracao", 0) + 1
        self._vidas = {}

        txt = (self.busca_home.get_text() or "").strip().lower()
        alvo = self.conexoes
        txt_bruto = self.busca_home.get_text().strip()
        if txt:
            alvo = [c for c in alvo
                    if txt in c.nome.lower() or txt in c.host.lower()
                    or txt in c.grupo.lower()]
        if not alvo:
            # ESTADO VAZIO UTIL: buscar e nao achar e exatamente o momento em
            # que voce quer conectar em algo que nao esta cadastrado. Em vez
            # de so avisar que nao achou, a busca oferece a conexao.
            vazia = interpretar_alvo(txt_bruto) if txt_bruto else None
            if vazia:
                self.grupos_caixa.pack_start(
                    self._card_efemero(*vazia), False, False, 12)
            else:
                self.grupos_caixa.pack_start(
                    rotulo("nenhuma máquina bate com o filtro", "card-meta",
                           xalign=0.5), False, False, 20)
            self.grupos_caixa.show_all()
            self._restaurar_rolagem(ajuste, pos)
            return

        # Resultado parcial ainda merece a oferta: digitar 192.168.12.23
        # casa com .230 e .231, mas quem digitou pode querer justamente o
        # .23 que nao existe. So esconde quando o texto bate EXATAMENTE com
        # um host ja cadastrado — ai nao ha o que oferecer.
        proposta = interpretar_alvo(txt_bruto) if txt_bruto else None
        if proposta:
            alvo_host = proposta[0]["host"].lower()
            if any(c.host.lower() == alvo_host or c.nome.lower() == alvo_host
                   for c in self.conexoes):
                proposta = None
        if proposta:
            self.grupos_caixa.pack_start(
                self._card_efemero(*proposta), False, False, 8)

        raiz = {"subs": {}, "itens": []}
        for c in alvo:
            no = raiz
            for parte in c.caminho_grupo:
                no = no["subs"].setdefault(parte, {"subs": {}, "itens": []})
            no["itens"].append(c)

        abrir = bool(txt)
        self._cont_acento = 0
        for nome in sorted(raiz["subs"], key=lambda x: x.lower()):
            self.grupos_caixa.pack_start(
                self._no_grupo(nome, raiz["subs"][nome], abrir, 0),
                False, False, 0)
        self.grupos_caixa.show_all()
        self._restaurar_rolagem(ajuste, pos)
        self._sondar_visiveis()

    def _despertar_cards(self):
        """Refaz o ciclo unmap/map dos cards ao voltar da sessao.

        Cada card e um EventBox, e EventBox tem GdkWindow propria. Ao sair
        para uma aba de sessao e voltar, essas janelas ficam com o
        empilhamento desatualizado e param de receber clique — a interface
        parece congelada, mas so o roteamento de evento e que se perdeu.

        Recolher e expandir o grupo resolvia porque desmapeia e remapeia os
        widgets. Isto faz o MESMO, sem reconstruir nada: e um ciclo de
        hide/show no container, nao uma nova montagem, entao nao perde
        rolagem, selecao nem estado dos grupos.
        """
        try:
            if self.grupos_caixa.get_mapped():
                self.grupos_caixa.hide()
                self.grupos_caixa.show()
        except Exception:
            pass
        # retoma o que ficou sem veredito enquanto voce estava na sessao
        GLib.idle_add(self._sondar_visiveis, priority=GLib.PRIORITY_LOW)
        return False

    def _sondar_visiveis(self):
        """Checa so os cards que existem AGORA (grupo expandido).

        Sob demanda de proposito: varrer as 276 continuamente transformaria
        o painel num monitor, com trafego e threads que voce nao pediu.
        Expandiu o grupo, checa aquele grupo.
        """
        # so os que ainda nao tem veredito: expandir um segundo grupo nao
        # deve refazer o ping do primeiro
        # devolve False sempre: e usado como callback de GLib.idle_add ao
        # expandir um grupo, e um retorno verdadeiro faria o idle repetir
        # para sempre
        alvos = []
        for nome in list(self._vidas):
            w = self._vidas[nome]
            ctx = w.get_style_context()
            if ctx.has_class("vida-on") or ctx.has_class("vida-off"):
                continue
            alvos.append((nome, w))
        if not alvos:
            return False

        # NAO SONDA FORA DO PAINEL.
        # Cada ping bifurca um processo; dezenas disso enquanto o gtk-frdp
        # negocia certificado e autenticacao deixavam a interface
        # irresponsiva ate a sessao resolver. Se voce nao esta olhando a
        # lista, a cor pode esperar — quando voltar, o proprio despertar dos
        # cards dispara de novo.
        try:
            if self.nb.get_nth_page(self.nb.get_current_page()) is not self.home:
                return False
        except Exception:
            pass

        geracao = self._geracao
        por_nome = {c.nome: c for c in self.conexoes}

        def _trabalho():
            from concurrent.futures import ThreadPoolExecutor
            import traceback
            # poucos workers: sao 30 a 60 cards por grupo, e o gargalo e o
            # timeout, nao a CPU
            # 6, nao 16: o gargalo e o timeout do ping, e cada worker
            # bifurca um processo. Mais paralelismo nao acelera e disputa
            # CPU com a sessao remota que estiver desenhando.
            with ThreadPoolExecutor(max_workers=6) as pool:
                def _um(par):
                    nome, _w = par
                    cx = por_nome.get(nome)
                    if cx is None:
                        return nome, None
                    return nome, pingar(cx.host)
                # LOTE, nao um idle por maquina: 60 idle_add separados
                # entopem o laço principal justamente quando ele precisa
                # atender o dialogo de autenticacao do RDP.
                lote = []
                for nome, vivo in pool.map(_um, alvos):
                    if vivo is None:
                        continue
                    lote.append((nome, vivo))
                    if len(lote) >= 12:
                        GLib.idle_add(self._pintar_lote, geracao, lote)
                        lote = []
                if lote:
                    GLib.idle_add(self._pintar_lote, geracao, lote)

        def _guardado():
            # excecao em thread morre calada e o sintoma vira "tudo cinza",
            # que nao distingue erro de maquina fora do ar
            try:
                _trabalho()
            except Exception:
                traceback.print_exc()

        import traceback
        threading.Thread(target=_guardado, daemon=True).start()
        return False

    def _pintar_lote(self, geracao, lote):
        for nome, vivo in lote:
            self._pintar_vida(geracao, nome, vivo)
        return False

    def _pintar_vida(self, geracao, nome, vivo):
        # a lista pode ter sido reconstruida enquanto a sonda corria
        if geracao != getattr(self, "_geracao", 0):
            return False
        w = self._vidas.get(nome)
        if w is None:
            return False
        ctx = w.get_style_context()
        ctx.remove_class("vida-on")
        ctx.remove_class("vida-off")
        ctx.add_class("vida-on" if vivo else "vida-off")
        return False

    @staticmethod
    def _restaurar_rolagem(ajuste, pos):
        """Devolve a posicao de rolagem quando o conteudo ficar alto o
        bastante para aceita-la.

        Nao basta um idle_add: no primeiro ciclo o _agendar_colunas ainda
        nao recalculou as colunas, o `upper` do ajuste ainda e o do conteudo
        pequeno e o valor pedido e CORTADO para o teto de entao — que muitas
        vezes e zero. Era por isso que a lista voltava ao topo ao duplicar
        mesmo com a posicao guardada.

        Aqui a restauracao espera o ajuste crescer, com um numero limitado
        de tentativas para nao virar timer eterno se o conteudo encolheu de
        verdade (uma busca que filtrou quase tudo, por exemplo).
        """
        if ajuste is None or pos <= 0:
            return

        tentativas = [12]

        def _voltar():
            tentativas[0] -= 1
            teto = max(0.0, ajuste.get_upper() - ajuste.get_page_size())
            if teto >= pos - 1 or tentativas[0] <= 0:
                ajuste.set_value(min(pos, teto))
                return False
            return True          # ainda nao cresceu: tenta de novo

        GLib.timeout_add(30, _voltar)

    def _conta_no(self, no):
        return len(no["itens"]) + sum(self._conta_no(v)
                                      for v in no["subs"].values())

    def _no_grupo(self, nome, no, aberto, nivel):
        """Um Expander por nivel. Subgrupos entram recuados dentro do pai."""
        acento = self.acento(self._cont_acento)
        self._cont_acento += 1
        total = self._conta_no(no)

        exp = Gtk.Expander()
        exp.set_expanded(aberto)
        exp.set_resize_toplevel(False)
        exp.set_valign(Gtk.Align.START)
        exp.set_vexpand(False)

        cab = Gtk.Box(spacing=8)
        trilho = pintar(add_class(Gtk.Box(), "trilho"), acento)
        trilho.set_size_request(4, 18 if nivel else 20)
        cab.pack_start(trilho, False, False, 0)

        # Caixa do GRUPO: marca/desmarca tudo que esta abaixo, subgrupos
        # inclusos. Fica FORA do EventBox do cabecalho — dentro dele o
        # clique seria capturado para abrir/fechar o Expander e a caixa
        # nunca receberia o evento.
        elegiveis = [c for c in self._itens_de(no) if c.tem_ssh]
        marca_g = None
        if elegiveis:
            marca_g = Gtk.CheckButton()
            add_class(marca_g, "marca-lote")
            marca_g.set_valign(Gtk.Align.CENTER)
            marca_g.set_tooltip_text(
                "Selecionar as %d máquina(s) deste grupo para o lote"
                % len(elegiveis))
            marcados = sum(1 for c in elegiveis if c.nome in self.selecionados)
            marca_g.set_active(marcados == len(elegiveis))
            # inconsistent = parcialmente marcado, sinal visual de "alguns"
            marca_g.set_inconsistent(0 < marcados < len(elegiveis))
            marca_g.connect("toggled", self._marcar_grupo, elegiveis)
            self._marcas_grupo.append((marca_g, elegiveis))

        tt = rotulo(nome, "grupo-titulo" if nivel == 0 else "subgrupo-titulo")
        tt.set_ellipsize(Pango.EllipsizeMode.NONE)
        cab.pack_start(tt, False, False, 0)
        cab.pack_start(rotulo("%d" % total, "grupo-cont"), False, False, 0)
        cab.show_all()

        ev = add_class(Gtk.EventBox(), "evt")
        ev.set_above_child(True)
        ev.add_events(Gdk.EventMask.BUTTON_PRESS_MASK)
        ev.add(cab)
        ev.connect("button-press-event", self._meio_grupo, nome, no)

        exp.set_label_widget(ev)

        corpo = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        corpo.set_margin_top(6)
        corpo.set_margin_start(14)
        corpo.set_margin_bottom(6)
        corpo.set_valign(Gtk.Align.START)
        corpo.set_vexpand(False)
        exp.add(corpo)

        estado = {"montado": False}

        def montar(*_a):
            if estado["montado"] or not exp.get_expanded():
                return
            estado["montado"] = True
            # a largura da pagina so existe depois da alocacao; montar no
            # clique faria o FlowBox concluir "cabe uma coluna"
            GLib.idle_add(construir, priority=GLib.PRIORITY_HIGH_IDLE)

        def construir():
            for sub in sorted(no["subs"], key=lambda x: x.lower()):
                corpo.pack_start(
                    self._no_grupo(sub, no["subs"][sub], aberto, nivel + 1),
                    False, False, 0)
            if no["itens"]:
                corpo.pack_start(self._grade(no["itens"], acento, corpo),
                                 False, False, 0)
            corpo.show_all()
            # segunda passada: na primeira o FlowBox ainda decide a altura
            # com a largura antiga e sobra um vazio embaixo do grupo
            GLib.idle_add(recalcular, priority=GLib.PRIORITY_LOW)
            # OS CARDS SO NASCEM AQUI.
            # Com os grupos recolhidos, _encher_home termina sem nenhum card
            # criado — e a sonda disparada la nao tinha o que checar, por
            # isso as linhas ficavam todas cinzas. A checagem tem de sair
            # DAQUI, que e o momento em que os cards deste grupo passam a
            # existir. Tambem e o comportamento certo: checa o grupo que
            # voce abriu, nao as 276.
            GLib.idle_add(self._sondar_visiveis, priority=GLib.PRIORITY_LOW)
            return False

        def recalcular():
            # a largura ja e definitiva aqui: reaplica as colunas e refaz o
            # calculo de altura
            self._ajustar_colunas()
            corpo.queue_resize()
            exp.queue_resize()
            self.home_caixa.queue_resize()
            return False

        exp.connect("notify::expanded", montar)
        if aberto:
            montar()

        if marca_g is None:
            return exp

        # A caixa fica IRMA do Expander, nunca dentro do label_widget dele:
        # o Gtk.Expander intercepta o clique em TODO o seu rotulo para
        # abrir/fechar, e widgets interativos ali dentro nunca recebem o
        # evento. E o mesmo padrao que ja tinha mordido com o Gtk.Switch
        # no editor de conexao.
        linha = Gtk.Box(spacing=6)
        linha.set_valign(Gtk.Align.START)
        caixa_ali = Gtk.Box()
        caixa_ali.set_valign(Gtk.Align.START)
        caixa_ali.set_margin_top(4 if nivel == 0 else 3)
        caixa_ali.pack_start(marca_g, False, False, 0)
        linha.pack_start(caixa_ali, False, False, 0)
        linha.pack_start(exp, True, True, 0)
        return linha

    # 258 -> 152: card compacto e quadrado. Com 276 maquinas, a largura
    # antiga dava 6 colunas em 1920px; esta da 11. O quadrado sai de
    # ALT_CARD, porque aspect-ratio nao existe no GTK3.
    LARG_CARD = 152      # largura pedida pela moldura do card
    ALT_CARD = 152       # altura fixa = largura: card quadrado
    ESP_CARD = 10        # column_spacing do FlowBox
    FOLGA_CARD = 4       # bordas do card + margem do FlowBoxChild
    FOLGA_ROLAGEM = 18   # barra de rolagem vertical, quando aparece

    @classmethod
    def _cols_para(cls, largura):
        """n cards cabem em L se  n*W + (n-1)*S <= L,  ou seja
           n <= (L + S) / (W + S).

        Eu vinha usando L // (W + S), que cobra espacamento tambem depois do
        ultimo card: com 7 colunas isso sobrestimava e estourava a janela;
        com poucas, sobrava um vao."""
        largura = largura - cls.FOLGA_ROLAGEM
        passo = cls.LARG_CARD + cls.FOLGA_CARD + cls.ESP_CARD
        if largura < cls.LARG_CARD:
            return 1
        return max(1, int((largura + cls.ESP_CARD) // passo))

    def _largura_util(self, widget=None):
        """Largura realmente disponivel para a grade.

        Cada nivel de subgrupo recua o corpo em 14px, entao a largura correta
        e a do container onde o FlowBox vive, nao a da coluna inteira."""
        if widget is not None:
            larg = widget.get_allocated_width()
            if larg > 1:
                return larg
        larg = self.grupos_caixa.get_allocated_width()
        if larg > 1:
            return larg
        return max(self.get_allocated_width() - self.painel.get_position() - 60,
                   self.LARG_CARD)

    def _ajustar_colunas(self):
        """Reflui as grades quando a janela ou a lateral mudam de largura.

        Cada FlowBox usa a largura do PROPRIO pai: subgrupos aninhados tem
        menos espaco que os de primeiro nivel.

        SO mexe na CONTAGEM de colunas — nunca aplica set_size_request em
        cada card a partir daqui. Uma versao anterior recalculava e
        reaplicava a largura de cada card TODA VEZ que a alocacao mudava, e
        isso realimentava a si mesmo: aplicar tamanho dispara alocacao,
        que dispara recalculo, que aplica tamanho de novo — com o
        ScrolledWindow horizontal em NEVER (sem freio de scroll), a janela
        inteira entrava num loop de crescimento sem fim. A largura elastica
        de verdade fica por conta do proprio GTK (hexpand + homogeneous no
        FlowBox), que nao tem esse problema porque nao SE REALIMENTA."""
        self._timer_colunas = None
        mudou = False

        def andar(w):
            nonlocal mudou
            if isinstance(w, Gtk.FlowBox):
                pai = w.get_parent()
                cols = self._cols_para(self._largura_util(pai))
                if cols != w.get_max_children_per_line():
                    w.set_min_children_per_line(cols)
                    w.set_max_children_per_line(cols)
                    mudou = True
                return
            if isinstance(w, Gtk.Container):
                for f in w.get_children():
                    andar(f)
        andar(self.grupos_caixa)
        if mudou:
            self.grupos_caixa.queue_resize()
        return False

    def _agendar_colunas(self, *_a):
        if getattr(self, "_timer_colunas", None):
            GLib.source_remove(self._timer_colunas)
        self._timer_colunas = GLib.timeout_add(180, self._ajustar_colunas)

    def _grade(self, itens, acento, dono=None):
        fb = Gtk.FlowBox()
        fb.set_selection_mode(Gtk.SelectionMode.NONE)
        # FILL + expand para o FlowBox receber a largura real da pagina.
        # Com halign START ele so recebia a largura MINIMA, que cabe um
        # unico card — por isso a grade virava lista de uma coluna.
        fb.set_halign(Gtk.Align.FILL)
        fb.set_valign(Gtk.Align.START)
        fb.set_vexpand(False)
        # min = max = colunas calculadas: assim o FlowBox nao tem margem para
        # decidir sozinho com uma largura que ainda nao existe
        cols = self._cols_para(self._largura_util(dono))
        fb.set_min_children_per_line(cols)
        fb.set_max_children_per_line(cols)
        fb.set_column_spacing(self.ESP_CARD)
        fb.set_row_spacing(self.ESP_CARD)
        # homogeneous iguala largura E altura dentro deste FlowBox — e o
        # proprio GTK quem distribui elasticamente, sem calculo manual
        # nenhum, entao nao ha realimentacao possivel.
        fb.set_homogeneous(True)
        for c in sorted(itens, key=lambda x: x.ordem):
            filho = Gtk.FlowBoxChild()
            filho.set_valign(Gtk.Align.START)
            filho.set_halign(Gtk.Align.FILL)
            filho.set_hexpand(True)
            filho.add(self._card(c, acento))
            fb.add(filho)
        return fb

    def _meio_grupo(self, _w, ev, nome, no):
        if ev.button != 2:
            return False        # esquerdo segue para o Expander normalmente
        itens = list(no["itens"])
        pilha = list(no["subs"].values())
        while pilha:
            atual = pilha.pop()
            itens.extend(atual["itens"])
            pilha.extend(atual["subs"].values())
        self._perguntar_lote(nome, itens)
        return True

    # ------------------------------------------------ conexao instantanea
    def _card_efemero(self, dados, proto):
        """Card fantasma para um destino digitado que nao esta no INI.

        Reaproveita a linguagem do card normal — trilho, nome, host, icones
        de protocolo — mas marcado como temporario. Clicar num icone pede a
        credencial e conecta; nada e gravado."""
        moldura = add_class(Gtk.Box(spacing=0), "card", "card-efemero")
        # canto esquerdo, alinhado com a grade: o mouse acabou de sair da
        # busca, que fica na esquerda — centralizar obrigaria a atravessar a
        # tela para clicar
        moldura.set_halign(Gtk.Align.START)
        moldura.set_margin_start(2)
        moldura.set_size_request(self.LARG_CARD + 60, -1)

        trilho = pintar(add_class(Gtk.Box(), "trilho"), self.cor("atencao_fg"))
        trilho.set_size_request(3, -1)
        moldura.pack_start(trilho, False, False, 0)

        card = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=2)
        card.set_border_width(10)
        moldura.pack_start(card, True, True, 0)

        topo = Gtk.Box(spacing=6)
        nome = rotulo(dados["host"], "card-nome")
        nome.set_max_width_chars(22)
        topo.pack_start(nome, True, True, 0)
        topo.pack_end(add_class(Gtk.Label(label="TEMPORÁRIA"),
                                "chip", "chip-atencao"), False, False, 0)
        card.pack_start(topo, False, False, 0)

        card.pack_start(rotulo("não está no conexoes.ini · vive só nesta "
                               "sessão", "card-meta"), False, False, 0)

        botoes = Gtk.Box(spacing=4, homogeneous=True)
        botoes.set_size_request(-1, self.ALT_BOTOES)
        for tp, dica in (("vnc", "Tela · VNC"), ("ssh", "Shell · SSH"),
                         ("rdp", "RDP")):
            b = Gtk.Button()
            b.add(icone_acao(tp))
            add_class(b, "card-ico", "card-ico-" + tp)
            b.set_tooltip_text(dica + " — pede a senha e conecta")
            b.connect("clicked",
                      lambda _b, d=dict(dados), t=tp: self._conectar_efemero(d, t))
            botoes.pack_start(b, True, True, 0)
        card.pack_start(botoes, False, False, 6)
        return moldura

    def _conectar_efemero(self, dados, proto):
        """Pede credencial e abre a sessao. Nada e gravado em lugar nenhum."""
        import dialogo_ui

        # o protocolo escolhido no clique manda: refaz as chaves do dict
        for k in ("vnc", "ssh", "rdp"):
            dados[k] = "sim" if k == proto else "nao"

        campo_user = {"vnc": "usuario", "ssh": "ssh_usuario",
                      "rdp": "rdp_usuario"}[proto]
        campo_senha = {"vnc": "senha", "ssh": "ssh_senha",
                       "rdp": "rdp_senha"}[proto]

        # VNC costuma nao ter usuario; so pergunta quando o protocolo pede.
        if proto in ("ssh", "rdp") and not dados.get(campo_user):
            u = dialogo_ui.perguntar(
                self, "Conectar a %s" % dados["host"], "USUÁRIO",
                ok="Continuar",
                validar=lambda t: None if t else "Informe o usuário")
            if u is None:
                return
            dados[campo_user] = u

        senha = dialogo_ui.perguntar(
            self, "Conectar a %s" % dados["host"], "SENHA", senha=True,
            ok="Conectar",
            dica="não será salva — vive só enquanto a aba existir")
        if senha is None:
            return
        dados[campo_senha] = senha

        cx = conexao_efemera(dados)
        self.abrir(cx, proto)

    def _card(self, cx, acento):
        moldura = add_class(Gtk.Box(spacing=0), "card")
        # Elastico simples: hexpand+FILL, e o FlowBox pai (homogeneous=True)
        # distribui a largura da linha entre as colunas. Sem calculo manual,
        # sem set_size_request reaplicado a cada resize — e exatamente por
        # isso que nao ha risco de realimentacao/crescimento sem fim.
        moldura.set_hexpand(True)
        moldura.set_halign(Gtk.Align.FILL)
        # quadrado: aspect-ratio nao existe no GTK3, entao a altura e pedida
        moldura.set_size_request(-1, self.ALT_CARD)
        trilho = pintar(add_class(Gtk.Box(), "trilho"), acento)
        trilho.set_size_request(3, -1)
        moldura.pack_start(trilho, False, False, 0)

        # SEGUNDA LINHA: vida da maquina, colada no trilho do grupo.
        # Verde = a porta respondeu, vermelho = nao respondeu, cinza = ainda
        # nao foi checada. Ocupa 3px de largura e nenhuma altura.
        vida = add_class(Gtk.Box(), "trilho", "trilho-vida")
        vida.set_size_request(3, -1)
        moldura.pack_start(vida, False, False, 0)
        self._vidas[cx.nome] = vida

        card = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=2)
        card.set_border_width(10)
        moldura.pack_start(card, True, True, 0)

        # max_width_chars em TODOS os textos do card, sempre. Sem isso, o
        # rotulo pede a largura NATURAL do texto completo antes de truncar
        # (ellipsize so afeta o desenho, nao o pedido de tamanho) — e um
        # FlowBox homogeneo calcula a largura por LINHA de cards, entao
        # linhas com textos mais longos ficavam mais largas que outras.
        # Travando o max_width_chars, toda linha pede a MESMA largura.
        nome_lbl = rotulo(cx.nome, "card-nome")
        nome_lbl.set_max_width_chars(16)
        topo = Gtk.Box(spacing=6)

        # Caixa de selecao para a execucao em lote. So aparece em maquina
        # com SSH configurado: o executor fala SSH, entao uma maquina
        # so-VNC ou so-RDP nao teria como participar.
        # getattr com default: conexoes antigas em memoria podem nao ter o
        # atributo se o objeto veio de outro caminho
        if cx.tem_ssh and not getattr(cx, "efemera", False):
            marca = Gtk.CheckButton()
            add_class(marca, "marca-lote")
            marca.set_valign(Gtk.Align.CENTER)
            marca.set_active(cx.nome in self.selecionados)
            marca.set_tooltip_text("Incluir na execução em lote")
            marca.connect("toggled", self._marcar_lote, cx)
            self._marcas[cx.nome] = marca
            topo.pack_start(marca, False, False, 0)

        topo.pack_start(nome_lbl, True, True, 0)
        card.pack_start(topo, False, False, 0)
        host_lbl = rotulo(cx.host or "sem host", "card-host")
        host_lbl.set_max_width_chars(20)
        card.pack_start(host_lbl, False, False, 0)



        # ICONES-ACAO: indicador E botao no mesmo controle.
        #
        # Antes o card mostrava tres pontinhos de estado E dois botoes
        # escritos dizendo a mesma coisa. Fundindo os dois, "apagado" passa
        # a significar exatamente uma coisa — nao configurado, logo nao
        # clicavel — e sobra altura para o card virar quadrado.
        #
        # Os quatro slots aparecem SEMPRE, mesmo desligados: icone que some
        # obriga a reler o card toda vez; posicao fixa se aprende e para de
        # ser lida. O SFTP e sempre o quarto.
        botoes = Gtk.Box(spacing=4, homogeneous=True)
        botoes.set_size_request(-1, self.ALT_BOTOES)
        botoes.set_valign(Gtk.Align.END)
        for tp, dica in (("vnc", "Tela · VNC"), ("ssh", "Shell · SSH"),
                         ("rdp", "RDP"), ("sftp", "Arquivos · SFTP")):
            ligado = cx.tem_ssh if tp == "sftp" else cx.tem(tp)
            b = Gtk.Button()
            b.add(icone_acao(tp))
            add_class(b, "card-ico", "card-ico-" + tp if ligado
                      else "card-ico-off")
            b.set_sensitive(bool(ligado))
            b.set_tooltip_text(dica if ligado else dica + " — não configurado")
            if ligado:
                if tp == "sftp":
                    b.connect("clicked", lambda _b, c=cx: self.abrir_sftp(c))
                else:
                    b.connect("clicked",
                              lambda _b, c=cx, t=tp: self.abrir(c, t))
                    # um Gtk.Button consome o clique antes de chegar ao
                    # EventBox do card, entao o botao do meio e tratado nele
                    b.add_events(Gdk.EventMask.BUTTON_PRESS_MASK)
                    b.connect("button-press-event", self._meio_botao, cx, tp)
            botoes.pack_start(b, True, True, 0)
        card.pack_end(botoes, False, False, 0)

        # Meta LOGO ACIMA dos icones.
        # Com pack_end o PRIMEIRO widget empacotado fica mais embaixo, entao
        # os botoes vao antes e a meta depois — o inverso empilhava os
        # icones por cima do texto.
        #
        # Eu tinha tirado para o card virar quadrado; a informacao e util e
        # cabe embaixo. Continua com altura ABSOLUTA (ALT_META) e as tres
        # linhas sempre presentes: uma linha vazia de verdade ("") faz o
        # Pango medir MENOS — sem glifo ele nao tem metrica de fonte — e os
        # cards ficariam de alturas diferentes. Por isso VAZIA e um espaco.
        VAZIA = " "
        linha_vnc = ("tela %s · %s%s" % (cx.porta, cx.modo,
                                         " · auto" if cx.auto else "")
                     if cx.tem_vnc else VAZIA)
        linha_ssh = ("ssh %s@%s%s" % (cx.ssh_usuario or "?", cx.ssh_porta,
                                      " · auto" if cx.ssh_auto else "")
                     if cx.tem_ssh else VAZIA)
        linha_rdp = ("rdp %s@%s · %s" % (cx.rdp_usuario or "?",
                                         cx.rdp_porta, cx.rdp_tela)
                     if cx.tem_rdp else VAZIA)
        meta_lbl = rotulo("\n".join((linha_vnc, linha_ssh, linha_rdp)),
                          "card-meta")
        meta_lbl.set_max_width_chars(22)
        meta_lbl.set_size_request(-1, self.ALT_META)
        meta_lbl.set_valign(Gtk.Align.END)
        card.pack_end(meta_lbl, False, False, 0)

        # clique do meio em qualquer ponto vazio do card
        ev = add_class(Gtk.EventBox(), "evt")
        # visible_window fica TRUE (padrao): sem GdkWindow proprio o
        # add_events dispara gdk_window_get_width: assertion 'GDK_IS_WINDOW'
        ev.add_events(Gdk.EventMask.BUTTON_PRESS_MASK)
        ev.set_halign(Gtk.Align.FILL)
        ev.set_hexpand(True)
        ev.add(moldura)
        ev.connect("button-press-event", self._meio_card, cx)
        return ev

    def _meio_botao(self, _w, ev, cx, tipo):
        if ev.button != 2:
            return False
        self.abrir(cx, tipo, focar=False)
        return True

    # -------------------------------------------------- selecao em lote
    def _itens_de(self, no):
        """Todas as maquinas abaixo de um no, subgrupos inclusos."""
        saida = list(no["itens"])
        pilha = list(no["subs"].values())
        while pilha:
            atual = pilha.pop()
            saida.extend(atual["itens"])
            pilha.extend(atual["subs"].values())
        return saida

    def _marcar_grupo(self, botao, elegiveis):
        if getattr(self, "_sync_marcas", False):
            return
        botao.set_inconsistent(False)
        ligado = botao.get_active()
        for c in elegiveis:
            if ligado:
                self.selecionados.add(c.nome)
            else:
                self.selecionados.discard(c.nome)
        self._refletir_selecao()

    def _refletir_selecao(self):
        """Reaplica o estado das caixas sem disparar os handlers.

        A guarda _sync_marcas evita recursao: mexer numa caixa de grupo
        dispara toggled em cada caixa de maquina, que por sua vez tentaria
        recalcular o grupo."""
        self._sync_marcas = True
        try:
            for nome, marca in self._marcas.items():
                marca.set_active(nome in self.selecionados)
            for marca_g, elegiveis in self._marcas_grupo:
                n = sum(1 for c in elegiveis if c.nome in self.selecionados)
                marca_g.set_inconsistent(0 < n < len(elegiveis))
                marca_g.set_active(n == len(elegiveis))
        finally:
            self._sync_marcas = False
        self._sync_botao_lote()

    def _marcar_lote(self, botao, cx):
        if getattr(self, "_sync_marcas", False):
            return
        if botao.get_active():
            self.selecionados.add(cx.nome)
        else:
            self.selecionados.discard(cx.nome)
        self._refletir_selecao()

    def _sync_botao_lote(self):
        """O botao de lote so existe quando ha o que executar."""
        n = len(self.selecionados)
        if hasattr(self, "bt_lote"):
            self.bt_lote.set_sensitive(n > 0 and TEM_MASSA)
            self.bt_lote.set_label("⚡  executar (%d)" % n if n
                                   else "⚡  executar")
        if hasattr(self, "lb_selecao"):
            self.lb_selecao.set_text("%d selecionada(s)" % n if n else "")

    def _selecao_massa(self, acao):
        if not acao:
            return
        elegiveis = [c for c in self.conexoes if c.tem_ssh]
        if acao == "todos":
            self.selecionados = {c.nome for c in elegiveis}
        elif acao == "nenhum":
            self.selecionados.clear()
        elif acao == "inverter":
            nomes = {c.nome for c in elegiveis}
            self.selecionados = nomes - self.selecionados
        self._refletir_selecao()

    def _meio_card(self, _w, ev, cx):
        if ev.button == 3:
            menu = Gtk.Menu()
            for texto, fn in (("Editar…", lambda: self.editar_conexao(cx)),
                              ("Duplicar…", lambda: self.duplicar_conexao(cx)),
                              ("Remover…", lambda: self.remover_conexao(cx))):
                mi = Gtk.MenuItem(label=texto)
                mi.connect("activate", lambda _m, f=fn: f())
                menu.append(mi)
            menu.show_all()
            menu.popup_at_pointer(ev)
            return True
        if ev.button != 2:
            return False
        for tp in ("vnc", "ssh", "rdp"):
            if cx.tem(tp):
                self.abrir(cx, tp, focar=False)
                break
        return True

    def _atualizar_hero(self):
        if not hasattr(self, "hero_caixa"):
            return False
        for f in self.hero_caixa.get_children():
            f.destroy()
        h = self._hero()
        self.hero_caixa.pack_start(h, True, True, 0)
        self.hero_caixa.show_all()
        return False

    def _trocou_aba(self, _nb, pagina, _num):
        # Voltar para o painel com um gtk_grab pendente deixava a lista
        # inteira sem responder a clique: o grab e interno a aplicacao e
        # entrega TODOS os eventos a um widget so. Sessoes que capturam
        # teclado (VNC/RDP) podem deixar esse grab para tras ao perder o
        # foco, e o sintoma era "abri RDP, voltei pra lista e nao clica".
        if pagina is self.home:
            liberar_grab_gtk()
            GLib.idle_add(self._despertar_cards)

        for aba in self.abas.values():
            if aba is pagina:
                if hasattr(aba, "redesenhar"):
                    GLib.idle_add(aba.redesenhar)
                if getattr(aba, "adiada", False):
                    aba.adiada = False
                    GLib.timeout_add(120, lambda a=aba:
                                     (a.conectar(), False)[1])
                if hasattr(aba, "reatar_foco"):
                    GLib.idle_add(aba.reatar_foco)
            elif hasattr(aba, "soltar_teclado"):
                aba.soltar_teclado()
        """O caminho do INI so interessa no painel. Numa sessao aberta ele
        rouba altura util e ainda aparece por cima do terminal remoto."""
        self.rodape.set_visible(pagina is self.home)

    def _ir_home(self, _b=None):
        self.nb.set_current_page(self.nb.page_num(self.home))
        self.rodape.set_visible(True)

    def _rodape(self):
        cx = Gtk.Box(spacing=8)
        cx.set_border_width(5)
        cx.pack_start(rotulo(self.caminho, "rodape-info"), True, True, 0)
        self.lb_conta = rotulo("%d máquinas" % len(self.conexoes),
                               "rodape-info", xalign=1.0)
        cx.pack_end(self.lb_conta, False, False, 0)
        return cx

    # -------------------------------------------------- lista
    def _popular(self):
        self.store.clear()
        nos = {}

        def no_grupo(caminho):
            chave = tuple(caminho)
            if chave in nos:
                return nos[chave]
            pai = no_grupo(caminho[:-1]) if len(caminho) > 1 else None
            nos[chave] = self.store.append(pai, [caminho[-1], -1, "", ""])
            return nos[chave]

        for i, c in enumerate(self.conexoes):
            selos = ("V" if c.tem_vnc else "") + ("S" if c.tem_ssh else "") \
                + ("R" if c.tem_rdp else "")
            self.store.append(no_grupo(c.caminho_grupo),
                              [c.nome, i, c.host, selos])
        # tudo recolhido: com milhares de hosts, expand_all inviabiliza a lista
        self.tree.collapse_all()

    def _filtrar_lateral_adiado(self, _e=None):
        if self._timer_lateral:
            GLib.source_remove(self._timer_lateral)
        self._timer_lateral = GLib.timeout_add(
            250, lambda: (self._filtrar_lateral(), False)[1])

    def _filtrar_lateral(self, _e=None):
        self._timer_lateral = None
        self.filtro.refilter()
        # com filtro ativo o resultado tem de aparecer sem exigir cliques
        if (self.busca.get_text() or "").strip():
            self.tree.expand_all()
        else:
            self.tree.collapse_all()

    def _visivel(self, modelo, it, _d):
        txt = (self.busca.get_text() or "").strip().lower()
        if not txt:
            return True
        if modelo[it][1] < 0:
            return self._algum_filho(modelo, it, txt)
        return txt in modelo[it][0].lower() or txt in modelo[it][2].lower()

    def _algum_filho(self, modelo, it, txt):
        for f in modelo[it].iterchildren():
            if f[1] < 0:
                if self._algum_filho(modelo, f.iter, txt):
                    return True
            elif txt in f[0].lower() or txt in f[2].lower():
                return True
        return False

    def _selecionada(self):
        modelo, it = self.tree.get_selection().get_selected()
        if not it:
            return None
        idx = modelo[it][1]
        return self.conexoes[idx] if idx >= 0 else None

    def _conexao_em(self, caminho):
        """Resolve pelo Gtk.TreePath recebido no sinal.

        Nao usar a selecao aqui: em row-activated e em button-press a selecao
        ainda aponta para a linha ANTERIOR, e o programa abria a maquina
        errada — sempre a ultima que estivera selecionada."""
        try:
            it = self.filtro.get_iter(caminho)
        except (ValueError, TypeError):
            return None
        idx = self.filtro[it][1]
        return self.conexoes[idx] if 0 <= idx < len(self.conexoes) else None

    def _ativar_linha(self, tree, caminho, _col):
        cx = self._conexao_em(caminho)
        if cx:
            for tp in ("vnc", "ssh", "rdp"):
                if cx.tem(tp):
                    self.abrir(cx, tp)
                    break
        elif tree.row_expanded(caminho):
            tree.collapse_row(caminho)
        else:
            tree.expand_row(caminho, False)

    def _abrir_sel(self, tipo, focar=True):
        cx = self._selecionada()
        if cx:
            self.abrir(cx, tipo, focar=focar)

    def _meio_sel(self, _w, ev, tipo):
        if ev.button != 2:
            return False
        self._abrir_sel(tipo, focar=False)
        return True

    def _itens_sob(self, modelo, it):
        """Todas as conexoes abaixo de um no de grupo, recursivamente."""
        saida = []
        for f in modelo[it].iterchildren():
            if f[1] < 0:
                saida.extend(self._itens_sob(modelo, f.iter))
            else:
                saida.append(self.conexoes[f[1]])
        return saida

    def _menu_lateral(self, tree, ev):
        if ev.button not in (2, 3):
            return False
        info = tree.get_path_at_pos(int(ev.x), int(ev.y))
        if not info:
            return False
        caminho = info[0]
        tree.get_selection().select_path(caminho)
        modelo = self.filtro
        try:
            it = modelo.get_iter(caminho)
        except (ValueError, TypeError):
            return False
        idx = modelo[it][1]

        # ---- no de grupo
        if idx < 0:
            nome_g = modelo[it][0]
            itens = self._itens_sob(modelo, it)
            self._perguntar_lote(nome_g, itens)
            return True

        # ---- maquina
        cx = self.conexoes[idx]
        if ev.button == 2:
            for tp in ("vnc", "ssh", "rdp"):
                if cx.tem(tp):
                    self.abrir(cx, tp, focar=False)
                    break
            return True

        menu = Gtk.Menu()

        def item(texto, fn, sensivel=True):
            mi = Gtk.MenuItem(label=texto)
            mi.set_sensitive(sensivel)
            mi.connect("activate", lambda _m: fn())
            menu.append(mi)

        item("Abrir tela (VNC)", lambda: self.abrir(cx, "vnc"), cx.tem_vnc)
        item("Abrir shell (SSH)", lambda: self.abrir(cx, "ssh"), cx.tem_ssh)
        item("Abrir RDP", lambda: self.abrir(cx, "rdp"), cx.tem_rdp)
        menu.append(Gtk.SeparatorMenuItem())
        item("Abrir em segundo plano",
             lambda: [self.abrir(cx, t, focar=False)
                      for t in ("vnc", "ssh", "rdp") if cx.tem(t)][:1])
        menu.append(Gtk.SeparatorMenuItem())
        item("Copiar host", lambda: Gtk.Clipboard.get(
            Gdk.SELECTION_CLIPBOARD).set_text(cx.host, -1))
        item("Detectar plataforma", lambda: self.detectar_plataforma([cx]),
             TEM_MASSA)
        menu.append(Gtk.SeparatorMenuItem())
        item("Editar…", lambda: self.editar_conexao(cx))
        item("Duplicar…", lambda: self.duplicar_conexao(cx))
        item("Remover…", lambda: self.remover_conexao(cx))
        menu.show_all()
        menu.popup_at_pointer(ev)
        return True

    # -------------------------------------------------- abertura em lote
    def _perguntar_lote(self, nome_grupo, itens):
        """Clique do meio num grupo. Pergunta o que abrir e avisa o tamanho
        do estrago antes de encher a barra de abas."""
        if not itens:
            self.avisar("Grupo vazio", "Não há máquinas em %s." % nome_grupo)
            return
        n_vnc = sum(1 for c in itens if c.tem_vnc)
        n_ssh = sum(1 for c in itens if c.tem_ssh)
        n_rdp = sum(1 for c in itens if c.tem_rdp)

        dlg = Gtk.Dialog(title="Abrir grupo", transient_for=self, modal=True)
        add_class(dlg, "acessos-dialogo")
        marcar_area_acao(dlg)
        botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
        botao_dialogo(dlg, "Abrir", Gtk.ResponseType.OK, "acao")
        cxb = dlg.get_content_area()
        cxb.set_border_width(16)
        cxb.set_spacing(8)

        t = rotulo(nome_grupo, "grupo-titulo")
        t.set_ellipsize(Pango.EllipsizeMode.NONE)
        cxb.pack_start(t, False, False, 0)
        av = rotulo("Isto abre TODAS as máquinas abaixo deste grupo, "
                    "inclusive as dos subgrupos: %d no total.\n"
                    "Cada aba é uma sessão de verdade — abrir muitas de uma "
                    "vez consome rede e memória." % len(itens), "secundario")
        av.set_ellipsize(Pango.EllipsizeMode.NONE)
        av.set_line_wrap(True)
        av.set_max_width_chars(56)
        cxb.pack_start(av, False, False, 0)
        cxb.pack_start(regua(self.cor("borda"), 1), False, False, 4)

        marcas = {}
        for tp, rot, n in (("vnc", "Tela (VNC)", n_vnc),
                           ("ssh", "Shell (SSH)", n_ssh),
                           ("rdp", "RDP", n_rdp)):
            cb = Gtk.CheckButton(label="%s — %d máquina%s"
                                 % (rot, n, "" if n == 1 else "s"))
            cb.set_sensitive(n > 0)
            cb.set_active(tp == "vnc" and n > 0)
            marcas[tp] = cb
            cxb.pack_start(cb, False, False, 0)

        fundo = Gtk.CheckButton(label="Abrir em segundo plano (não trocar de aba)")
        fundo.set_active(True)
        cxb.pack_start(fundo, False, False, 4)
        cxb.show_all()

        resp = dlg.run()
        escolhidos = [tp for tp, cb in marcas.items()
                      if cb.get_active() and cb.get_sensitive()]
        em_fundo = fundo.get_active()
        dlg.destroy()
        if resp != Gtk.ResponseType.OK or not escolhidos:
            return

        # abre em fatias para nao congelar a interface com centenas de sessoes
        fila = [(c, tp) for c in itens for tp in escolhidos if c.tem(tp)]
        self._fila_lote = fila
        self._fila_fundo = em_fundo
        GLib.idle_add(self._drenar_lote)

    def _drenar_lote(self):
        if self._encerrando or not getattr(self, "_fila_lote", None):
            return False
        cx, tp = self._fila_lote.pop(0)
        try:
            self.abrir(cx, tp, focar=False)
        except Exception as e:
            sys.stderr.write("falha ao abrir %s (%s): %s\n" % (cx.nome, tp, e))
        return bool(self._fila_lote)

    # -------------------------------------------------- abas
    @staticmethod
    def _chave(cx, tipo):
        return "%s::%s" % (cx.nome, tipo)

    def _cabecalho_aba(self, cx, tipo):
        cab = Gtk.Box(spacing=5)
        cab.set_size_request(-1, self.ALTURA_ABA)
        cab.set_tooltip_text("Clique do meio fecha esta aba")
        # "aba-cab" + "aba-<tipo>-t" desenham o trilho de 2px da aba ativa;
        # quem liga o trilho e o seletor "tab:checked .aba-cab" no tema
        add_class(cab, "aba-cab", "aba-" + tipo + "-t")
        cab.pack_start(selo_protocolo(tipo), False, False, 0)
        lb = Gtk.Label(label=cx.nome)
        lb.set_ellipsize(Pango.EllipsizeMode.NONE)
        lb.set_max_width_chars(28)
        alvo = {"ssh": cx.destino_ssh, "rdp": cx.destino_rdp}.get(tipo,
                                                                   cx.destino)
        lb.set_tooltip_text("%s — %s" % (cx.nome, alvo))
        add_class(lb, "aba-nome")
        cab.pack_start(lb, False, False, 0)
        bt = botao_fechar_aba(lambda: self.fechar(self._chave(cx, tipo)))
        cab.pack_start(bt, False, False, 0)

        # o rotulo de aba nao recebe eventos sozinho: precisa de EventBox
        # com janela propria e a mascara pedida explicitamente
        ev = add_class(Gtk.EventBox(), "evt")
        # BUTTON_PRESS_MASK sozinho nao basta: o EventBox tem janela propria
        # e, sem pedir tambem SCROLL_MASK/SMOOTH_SCROLL_MASK, a roda do
        # mouse sobre o rotulo (que ocupa quase toda a aba) nunca chegava ao
        # scroll-event do Notebook — so funcionava nas frestas vazias da
        # barra. Este e o UNICO ponto que navega entre abas com a roda: o
        # Notebook nao tem mais handler de scroll (ver _notebook).
        ev.add_events(Gdk.EventMask.BUTTON_PRESS_MASK
                      | Gdk.EventMask.SCROLL_MASK
                      | Gdk.EventMask.SMOOTH_SCROLL_MASK)
        ev.add(cab)
        ev.connect("button-press-event", self._meio_aba, self._chave(cx, tipo))
        ev.connect("scroll-event", self._rolar_rotulo)
        ev.show_all()
        return ev

    def _meio_aba(self, _w, ev, chave):
        if ev.button != 2:
            return False
        self.fechar(chave)
        return True

    def abrir(self, cx, tipo="vnc", focar=True):
        classe_rdp = AbaRdp        # ajustado no ramo do RDP, se for o caso
        if tipo == "rdp":
            binario, _n = _bin_rdp()
            if not binario:
                self.avisar("FreeRDP ausente",
                            "Fedora: sudo dnf install freerdp\n"
                            "Arch:   sudo pacman -S freerdp")
                return
            # Decidido AQUI, e nao mais abaixo: o aviso de "janela separada"
            # precisa saber qual caminho sera usado.
            #
            # Sob X11 fica a AbaRdp: ela ja embute por Gtk.Socket e e mais
            # madura. Em Wayland, onde XEmbed nao existe, entra o widget
            # gtk-frdp — quando disponivel.
            if (TEM_FRDP and RdpWidget is not None
                    and verdade(self.geral.get("rdp_embutido", ""), True)
                    and not _backend_x11()):
                classe_rdp = AbaRdpEmbutido

            if not cx.tem_rdp:
                self.avisar("Sem RDP configurado",
                            "Defina rdp_usuario (ou rdp = 1) no bloco [%s]."
                            % cx.nome)
                return
            # Aviso SO quando o RDP realmente vai sair em janela separada.
            #
            # A condicao original supunha que Wayland nativo implicava janela
            # externa — verdade enquanto o unico caminho era o xfreerdp com
            # Gtk.Socket. Com o widget gtk-frdp o RDP embute em Wayland, e o
            # aviso passou a aparecer contradizendo o que o operador via na
            # tela: dizia "janela separada" e a sessao abria dentro da aba.
            if (not _backend_x11() and os.environ.get("WAYLAND_DISPLAY")
                    and classe_rdp is AbaRdp
                    and not self._ja_ofereceu_x11):
                self._ja_ofereceu_x11 = True
                self.avisar(
                    "RDP em janela separada",
                    "Você está em Wayland nativo (x11 = 0), onde não existe "
                    "XID para reparentar a janela do FreeRDP. A aba controla "
                    "a sessão; a imagem aparece fora dela.\n\n"
                    "Para embutir o RDP na aba, use x11 = 1 no INI — em "
                    "troca, a captura total de teclado deixa de funcionar.")
        if tipo == "vnc" and not TEM_VNC:
            self.avisar("VNC indisponível",
                        "O widget VNC próprio não carregou.\n\n"
                        "Compile o vncshim rodando ./instalar.sh na pasta "
                        "do projeto.\n\n" + ERRO_VNC)
            return
        if tipo == "ssh" and not TEM_VTE:
            self.avisar("VTE ausente",
                        "Fedora: sudo dnf install vte291\n"
                        "Arch:   sudo pacman -S vte3\n\n" + ERRO_VTE)
            return
        if tipo == "ssh" and not cx.tem_ssh:
            self.avisar("Sem SSH configurado",
                        "Defina ssh_usuario (ou ssh = 1) no bloco [%s]." % cx.nome)
            return
        if tipo == "vnc" and not cx.tem_vnc:
            self.avisar("Sem tela configurada",
                        "O bloco [%s] está com vnc = 0." % cx.nome)
            return

        chave = self._chave(cx, tipo)
        if chave in self.abas:
            self.nb.set_current_page(self.nb.page_num(self.abas[chave]))
            return

        if tipo == "ssh":
            classe = AbaSsh
        elif tipo == "rdp":
            classe = classe_rdp        # definido no ramo do RDP, acima
        else:
            classe = AbaVnc
        aba = classe(cx, self)
        if self.mostrar_log:
            revelar(aba.exp_log)
        aba.exp_log.set_visible(self.mostrar_log)
        self.abas[chave] = aba
        self.nb.append_page(aba, self._cabecalho_aba(cx, tipo))
        aba.show_all()

        # O Gtk.Notebook so realiza a pagina ATIVA. Em segundo plano o VTE e o
        # Gtk.Socket ficam sem GdkWindow e qualquer consulta de geometria
        # dispara gdk_window_get_height: assertion 'GDK_IS_WINDOW' failed —
        # que no Gtk.Socket derruba o processo.
        #
        # realize() na mao NAO resolve: realizar um filho cujo pai nao esta
        # realizado e justamente o que quebra. O jeito seguro e deixar a
        # pagina ativa por um ciclo do laco principal, o que produz um
        # map-event de verdade, e so entao voltar para a aba anterior.
        if tipo in ("rdp", "vnc") and not focar:
            # RDP: o xfreerdp so desenha numa janela mapeada. Em segundo
            # plano ele conecta, consome banda e entrega um retangulo preto.
            #
            # VNC: motivo diferente e pior. A alternativa era trocar para a
            # aba, conectar e voltar no mesmo ciclo — o que desmapeava o
            # display com a corrotina do gtk-vnc no meio do handshake e
            # disparava 'coroutine_yieldto: assertion !to->caller failed'
            # logo na abertura. Varias abas assim corrompiam o estado e
            # derrubavam o processo.
            #
            # Nos dois casos a sessao sobe quando voce entrar na aba
            # (_trocou_aba consome o flag `adiada`).
            aba.adiar_ate_focar()
            return

        anterior = self.nb.get_current_page()

        # freeze/thaw evita o "piscar": sem isto, trocar de pagina e voltar
        # pinta DOIS quadros de verdade na tela (a aba nova, depois a
        # anterior de novo) — visivel como um glitch rapido, sobretudo ao
        # abrir varias abas em segundo plano com o clique do meio.
        # freeze_updates() manda o GDK acumular tudo sem desenhar; so ao
        # dar thaw_updates() o quadro final e pintado de uma vez.
        # SO CONGELA QUANDO VAI HAVER O FLIP.
        #
        # freeze_updates() para de pintar a JANELA INTEIRA ate o thaw. Com
        # focar=True nao ha ida-e-volta entre paginas — nao ha piscar a
        # evitar — entao congelar so cria risco sem beneficio.
        #
        # E o risco se concretizou: com uma sessao RDP aberta, o update() do
        # gtk-frdp ocupa o laco de eventos o tempo todo, e o thaw_updates()
        # estava num idle de PRIORITY_LOW — que so roda quando nao ha nada
        # mais urgente. Como sempre havia, o thaw nunca acontecia e a janela
        # ficava congelada para sempre: responsiva ao clique, sem repintar
        # nada. Batia com o relato — so pelo clique esquerdo, so depois de um
        # RDP aberto, e afetando VNC e SSH igualmente, porque o congelado era
        # a janela, nao a sessao.
        descongelado = [False]
        gdkwin = self.get_window() if not focar else None
        if gdkwin is not None:
            gdkwin.freeze_updates()
            # Rede de seguranca: se por qualquer motivo o thaw abaixo nao
            # rodar, este descongela assim mesmo. Prioridade ALTA de
            # proposito — e justamente o idle de prioridade baixa que nao
            # roda quando o laco esta ocupado.
            def _destravar():
                # so descongela se ninguem descongelou ainda: thaw a mais
                # que freeze deixa o contador negativo no GDK
                if not descongelado[0]:
                    descongelado[0] = True
                    try:
                        gdkwin.thaw_updates()
                    except Exception:
                        pass
                return False
            GLib.timeout_add(700, _destravar)

        self.nb.set_current_page(self.nb.page_num(aba))

        def depois_do_mapa():
            # try/finally: se aba.conectar() levantar qualquer excecao, o
            # "voltar para a aba anterior" (e o thaw) tem que rodar do mesmo
            # jeito — foi a falta do primeiro que deixou uma aba presa em
            # primeiro plano numa rodada anterior.
            try:
                aba.conectar()
            finally:
                # a ordem importa: primeiro volta para a aba anterior,
                # SO DEPOIS descongela — assim o unico quadro pintado ja
                # reflete o estado final, sem frame intermediario visivel
                if not focar and anterior >= 0:
                    self.nb.set_current_page(anterior)
                if gdkwin is not None and not descongelado[0]:
                    descongelado[0] = True
                    try:
                        gdkwin.thaw_updates()
                    except Exception:
                        pass
            return False

        # PRIORITY_DEFAULT_IDLE, nao PRIORITY_LOW: com uma sessao RDP aberta
        # o laco vive ocupado, e um idle de prioridade baixa pode nunca ser
        # chamado — foi o que travou a janela.
        GLib.idle_add(depois_do_mapa, priority=GLib.PRIORITY_DEFAULT_IDLE)
        if hasattr(self, "home_caixa"):
            GLib.idle_add(self._atualizar_hero)

    def fechar(self, chave):
        aba = self.abas.pop(chave, None)
        if not aba:
            return
        # ORDEM IMPORTA: desconectar (que solta grabs, mata timers e
        # desliga sinais) ANTES de tirar a pagina. Removendo primeiro, o
        # GTK destroi a arvore de widgets enquanto ainda ha callbacks
        # apontando para ela.
        try:
            aba.desconectar()
        except Exception as e:
            sys.stderr.write("erro ao desconectar %s: %s\n" % (chave, e))
        pagina = self.nb.page_num(aba)
        if pagina >= 0:
            self.nb.remove_page(pagina)
        GLib.idle_add(self._atualizar_hero)
        if not self.abas:
            self._ir_home()

    # -------------------------------------------------- dialogos
    def pedir_senha(self, nome, destino):
        # Um gtk_grab_add() ativo (o gtk-frdp instala um enquanto a
        # sessao RDP roda) faz o dialogo aparecer sem receber clique.
        # Nao depende de foco: bastava ter uma aba RDP aberta.
        liberar_grab_gtk()
        # um modal sob grab trava o teclado do desktop
        self.soltar_capturas()
        dlg = Gtk.Dialog(title="Senha", transient_for=self, modal=True)
        add_class(dlg, "acessos-dialogo")
        marcar_area_acao(dlg)
        botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
        botao_dialogo(dlg, "Conectar", Gtk.ResponseType.OK, "acao")

        cxb = dlg.get_content_area()
        cxb.set_border_width(14)
        cxb.set_spacing(6)
        cxb.pack_start(rotulo("SENHA", "rotulo"), False, False, 0)
        cxb.pack_start(rotulo("%s — %s" % (nome, destino), "secundario"),
                       False, False, 0)
        ent = Gtk.Entry()
        ent.set_visibility(False)
        # activates_default nao serve mais: o Enter e ligado direto a resposta
        ent.connect("activate", lambda _e: dlg.response(Gtk.ResponseType.OK))
        cxb.pack_start(ent, False, False, 0)
        cxb.show_all()
        resp = dlg.run()
        valor = ent.get_text() if resp == Gtk.ResponseType.OK else None
        dlg.destroy()
        return valor

    def confirmar(self, titulo, texto, ok="Confirmar", destrutivo=False):
        # Um gtk_grab_add() ativo (o gtk-frdp instala um enquanto a
        # sessao RDP roda) faz o dialogo aparecer sem receber clique.
        # Nao depende de foco: bastava ter uma aba RDP aberta.
        liberar_grab_gtk()
        # um modal sob grab trava o teclado do desktop
        self.soltar_capturas()
        dlg = Gtk.Dialog(title=titulo, transient_for=self, modal=True)
        add_class(dlg, "acessos-dialogo")
        marcar_area_acao(dlg)
        botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
        # confirmacao destrutiva tambem e vermelha; as demais ficam azuis
        classe = "perigo" if destrutivo else "acao"
        botao_dialogo(dlg, ok, Gtk.ResponseType.OK, classe)
        cxb = dlg.get_content_area()
        add_class(cxb, "fundo")
        cxb.set_border_width(16)
        cxb.set_spacing(8)
        lt = rotulo(titulo, "grupo-titulo")
        lt.set_ellipsize(Pango.EllipsizeMode.NONE)
        lt.set_line_wrap(True)
        cxb.pack_start(lt, False, False, 0)
        lb = rotulo(texto, "secundario")
        lb.set_ellipsize(Pango.EllipsizeMode.NONE)
        lb.set_line_wrap(True)
        lb.set_max_width_chars(58)
        cxb.pack_start(lb, False, False, 0)
        cxb.show_all()
        r = dlg.run()
        dlg.destroy()
        return r == Gtk.ResponseType.OK

    def avisar(self, titulo, texto):
        # Um gtk_grab_add() ativo (o gtk-frdp instala um enquanto a
        # sessao RDP roda) faz o dialogo aparecer sem receber clique.
        # Nao depende de foco: bastava ter uma aba RDP aberta.
        liberar_grab_gtk()
        # um modal sob grab trava o teclado do desktop
        self.soltar_capturas()
        # MessageDialog usa o desenho do sistema e ignora o tema daqui
        dlg = Gtk.Dialog(title=titulo, transient_for=self, modal=True)
        add_class(dlg, "acessos-dialogo")
        marcar_area_acao(dlg)
        botao_dialogo(dlg, "Entendi", Gtk.ResponseType.OK, "acao")
        cxb = dlg.get_content_area()
        add_class(cxb, "fundo")
        cxb.set_border_width(16)
        cxb.set_spacing(8)
        lt = rotulo(titulo, "grupo-titulo")
        lt.set_ellipsize(Pango.EllipsizeMode.NONE)
        lt.set_line_wrap(True)
        cxb.pack_start(lt, False, False, 0)
        lb = rotulo(texto, "secundario")
        lb.set_ellipsize(Pango.EllipsizeMode.NONE)
        lb.set_line_wrap(True)
        lb.set_max_width_chars(58)
        cxb.pack_start(lb, False, False, 0)
        cxb.show_all()
        dlg.run()
        dlg.destroy()

    # -------------------------------------------------- CRUD
    def nova_conexao(self, _b=None):
        self._editor(None)

    def editar_conexao(self, cx):
        self._editor(cx)

    def _grupos_existentes(self):
        vistos = []
        for c in self.conexoes:
            if c.grupo and c.grupo not in vistos:
                vistos.append(c.grupo)
        return sorted(vistos, key=lambda x: x.lower())

    def _estado_grupos(self):
        """Quais grupos estao abertos agora, para restaurar depois."""
        abertos = set()

        def andar(w):
            if isinstance(w, Gtk.Expander):
                rot = w.get_label_widget()
                nome = self._nome_do_expander(rot)
                if nome and w.get_expanded():
                    abertos.add(nome)
                filho = w.get_child()
                if filho is not None:
                    andar(filho)
                return
            if isinstance(w, Gtk.Container):
                for f in w.get_children():
                    andar(f)
        andar(self.grupos_caixa)
        return abertos

    @staticmethod
    def _nome_do_expander(rot):
        if rot is None:
            return None
        alvo = rot.get_child() if isinstance(rot, Gtk.EventBox) else rot
        if not isinstance(alvo, Gtk.Container):
            return None
        for f in alvo.get_children():
            if isinstance(f, Gtk.Label) and f.get_style_context().has_class(
                    "grupo-titulo") or (
                    isinstance(f, Gtk.Label)
                    and f.get_style_context().has_class("subgrupo-titulo")):
                return f.get_text()
        return None

    def _restaurar_grupos(self, abertos):
        def andar(w):
            if isinstance(w, Gtk.Expander):
                nome = self._nome_do_expander(w.get_label_widget())
                if nome in abertos:
                    w.set_expanded(True)
                filho = w.get_child()
                if filho is not None:
                    GLib.idle_add(andar, filho)
                return
            if isinstance(w, Gtk.Container):
                for f in w.get_children():
                    andar(f)
        andar(self.grupos_caixa)

    def _editor(self, cx):
        self.soltar_capturas()
        # em cadastro repetido, perder a arvore aberta a cada salvamento
        # quebra a sequencia de trabalho
        abertos = self._estado_grupos()
        pagina = self.nb.get_current_page()
        dlg = EditorConexao(self, cx, self._grupos_existentes())
        existentes = {c.nome.lower() for c in self.conexoes}
        while True:
            if dlg.run() != Gtk.ResponseType.OK:
                dlg.destroy()
                return
            v = dlg.valores()
            erro = dlg.validar(v, existentes)
            if erro is None:
                break
            self.avisar("Dados incompletos", erro)
        nome_antigo = dlg.nome_antigo
        dlg.destroy()

        nome = v["nome"]
        # numa duplicata o objeto tem nome novo e nao existe no arquivo:
        # nome_antigo do dialogo e quem sabe se houve renomeacao real
        antigo = nome_antigo if cx else None
        if antigo and antigo not in {c.nome for c in self.conexoes}:
            antigo = None
        # renomear = recriar: o nome da secao E a identidade no INI
        if antigo and antigo != nome:
            remover_secao(self.caminho, antigo)
            for tipo in ("vnc", "ssh", "rdp"):
                self.fechar(self._chave(cx, tipo))

        pares = [
            ("grupo", v["grupo"] or "Sem grupo"),
            ("host", v["host"]),
            ("vnc", "1" if v["_vnc"] else "0"),
            ("porta", v["porta"] if v["_vnc"] else ""),
            ("usuario", v["usuario"] if v["_vnc"] else ""),
            ("senha", v["senha"] if v["_vnc"] else ""),
            ("modo", v["modo"] if v["_vnc"] else ""),
            ("ronly", ("1" if v["ronly"] else "0") if v["_vnc"] else ""),
            ("auto", ("1" if v["auto"] else "0") if v["_vnc"] else ""),
            ("ssh", "1" if v["_ssh"] else "0"),
            ("ssh_porta", v["ssh_porta"] if v["_ssh"] else ""),
            ("ssh_usuario", v["ssh_usuario"] if v["_ssh"] else ""),
            ("ssh_senha", v["ssh_senha"] if v["_ssh"] else ""),
            ("ssh_auto", ("1" if v["ssh_auto"] else "0") if v["_ssh"] else ""),
            ("rdp", "1" if v["_rdp"] else "0"),
            ("rdp_porta", v["rdp_porta"] if v["_rdp"] else ""),
            ("rdp_usuario", v["rdp_usuario"] if v["_rdp"] else ""),
            ("rdp_senha", v["rdp_senha"] if v["_rdp"] else ""),
            ("rdp_dominio", v["rdp_dominio"] if v["_rdp"] else ""),
            ("rdp_tela", v["rdp_tela"] if v["_rdp"] else ""),
            ("rdp_extras", v["rdp_extras"] if v["_rdp"] else ""),
            ("rdp_auto", ("1" if v["rdp_auto"] else "0") if v["_rdp"] else ""),
        ]
        gravar_secao(self.caminho, nome, pares)
        self._recarregar()
        GLib.idle_add(self._restaurar_grupos, abertos)
        if pagina >= 0:
            self.nb.set_current_page(pagina)

    # -------------------------------------------------- lote e snippets
    def abrir_snippets(self, _b=None):
        self.soltar_capturas()
        dlg = EditorSnippets(self)
        dlg.run()
        dlg.destroy()

    def abrir_lote(self, _b=None):
        if not TEM_MASSA:
            self.avisar("Motor ausente",
                        "O arquivo massa.py precisa estar ao lado do "
                        "acessos.py.\n\n" + ERRO_MASSA)
            return
        # O massa.py importa mesmo sem paramiko — quem falha e a CONEXAO,
        # host por host. Sem esta checagem antecipada o operador so
        # descobria depois de disparar, com uma linha de erro por maquina.
        if not massa.TEM_PARAMIKO:
            self.avisar(
                "Falta o paramiko",
                "A execução em lote usa a biblioteca SSH do Python, que "
                "não está instalada.\n\n"
                "Fedora:  sudo dnf install python3-paramiko\n"
                "Arch:    sudo pacman -S python-paramiko\n"
                "Debian:  sudo apt install python3-paramiko\n\n"
                "Ou, sem tocar no sistema:\n"
                "  pip install --user paramiko\n\n"
                "Detalhe: " + massa.ERRO_PARAMIKO)
            return
        alvos = [c for c in self.conexoes
                 if c.nome in self.selecionados and c.tem_ssh]
        if not alvos:
            self.avisar("Nada selecionado",
                        "Marque as máquinas na página inicial. Só entram "
                        "máquinas com SSH configurado.")
            return
        if "::lote" in self.abas:
            self.nb.set_current_page(self.nb.page_num(self.abas["::lote"]))
            return

        aba = AbaLote(self, alvos)
        self.abas["::lote"] = aba
        cab = Gtk.Box(spacing=5)
        cab.set_size_request(-1, self.ALTURA_ABA)
        add_class(cab, "aba-cab", "aba-massa-t")
        cab.pack_start(selo_protocolo("massa"), False, False, 0)
        lb = Gtk.Label(label="%d máquinas" % len(alvos))
        add_class(lb, "aba-nome")
        cab.pack_start(lb, False, False, 0)
        # era aqui que faltava o hover: o codigo estava duplicado e so um
        # dos dois tinha os handlers de enter/leave
        bt = botao_fechar_aba(lambda: self.fechar("::lote"))
        cab.pack_start(bt, False, False, 0)
        cab.show_all()
        self.nb.append_page(aba, cab)
        aba.show_all()
        aba.faixa.set_visible(False)
        self.nb.set_current_page(self.nb.page_num(aba))

    def abrir_sftp(self, cx=None):
        """Abre (ou foca) a ABA UNICA de transferencia.

        Chamada pelo botao de pasta da aba SSH. Como a aba e unica, se ela
        ja existir apenas trocamos o seletor para a maquina pedida — assim
        o operador tem o atalho contextual sem multiplicar abas."""
        if not TEM_SFTP:
            self.avisar("SFTP indisponível",
                        "Módulo sftp.py não carregou:\n%s" % ERRO_SFTP)
            return
        aba = self.abas.get("::sftp")
        if aba is None:
            aba = AbaSftp(self)
            self.abas["::sftp"] = aba
            cab = Gtk.Box(spacing=5)
            cab.set_size_request(-1, self.ALTURA_ABA)
            add_class(cab, "aba-cab", "aba-sftp-t")
            cab.pack_start(selo_protocolo("sftp"), False, False, 0)
            lb = Gtk.Label(label="Arquivos")
            add_class(lb, "aba-nome")
            cab.pack_start(lb, False, False, 0)
            cab.pack_start(botao_fechar_aba(lambda: self.fechar("::sftp")),
                           False, False, 0)
            cab.show_all()
            self.nb.append_page(aba, cab)
            aba.show_all()
        aba.recarregar_conexoes([c for c in self.conexoes if c.tem_ssh])
        if cx is not None:
            aba.selecionar(cx)
        self.nb.set_current_page(self.nb.page_num(aba))

    # -------------------------------------------------- sonda de plataforma
    def detectar_plataforma(self, conexoes):
        """Le o banner do servidor SSH e grava `windows` no INI.

        O servidor manda a linha de identificacao ANTES de qualquer
        negociacao ou autenticacao (RFC 4253 4.2) — abrir TCP, ler, fechar.
        Nenhuma credencial trafega, entao e seguro rodar no parque inteiro.

        Roda em thread: com dezenas de maquinas, algumas desligadas, fazer
        isto na thread da interface congelaria a janela por minutos."""
        if not TEM_MASSA:
            self.avisar("Motor ausente",
                        "O arquivo massa.py precisa estar ao lado do "
                        "acessos.py.\n\n" + ERRO_MASSA)
            return
        alvos = [c for c in conexoes if c.host]
        if not alvos:
            return

        dlg = Gtk.Dialog(title="Detectando plataforma", transient_for=self,
                         modal=True)
        add_class(dlg, "acessos-dialogo")
        marcar_area_acao(dlg)
        botao_dialogo(dlg, "Fechar", Gtk.ResponseType.CLOSE, "acao")
        cxb = dlg.get_content_area()
        add_class(cxb, "fundo")
        cxb.set_border_width(16)
        cxb.set_spacing(8)
        cxb.pack_start(rotulo("Lendo o banner SSH de %d máquina(s)"
                              % len(alvos), "grupo-titulo"), False, False, 0)
        barra = Gtk.ProgressBar()
        cxb.pack_start(barra, False, False, 0)
        buf = Gtk.TextBuffer()
        tv = Gtk.TextView(buffer=buf)
        tv.set_editable(False)
        tv.set_monospace(True)
        add_class(tv, "log")
        rol = Gtk.ScrolledWindow()
        rol.set_size_request(560, 260)
        rol.add(tv)
        cxb.pack_start(rol, True, True, 0)
        cxb.show_all()

        estado = {"parar": False, "achados": []}

        def trabalhar():
            for i, cx in enumerate(alvos):
                if estado["parar"]:
                    return
                porta = cx.ssh_porta if cx.tem_ssh else "22"
                ok, banner, plat, ms = massa.sondar(cx.host, porta)
                estado["achados"].append((cx, plat))
                GLib.idle_add(mostrar, i + 1, cx, ok, banner, plat, ms)
            GLib.idle_add(concluir)

        def mostrar(feitos, cx, ok, banner, plat, ms):
            barra.set_fraction(feitos / len(alvos))
            barra.set_text("%d de %d" % (feitos, len(alvos)))
            if not ok:
                txt = "%-22s inacessível (%s)" % (cx.nome, banner[:40])
            else:
                # versao do OpenSSH e a ferramenta para diagnosticar
                # maquina que "deveria" conectar e nao conecta
                txt = "%-22s %-9s %s" % (cx.nome, plat or "?", banner[:44])
            buf.insert(buf.get_end_iter(), txt + "\n")
            return False

        def concluir():
            gravados = 0
            for cx, plat in estado["achados"]:
                if plat not in ("linux", "windows"):
                    continue          # nao grava palpite: melhor admitir
                novo = (plat == "windows")
                if novo != cx.windows:
                    self.gravar(cx.nome, "windows", "1" if novo else "0")
                    cx.windows = novo
                    gravados += 1
            buf.insert(buf.get_end_iter(),
                       "\n%d máquina(s) tiveram a plataforma atualizada no INI"
                       % gravados)
            barra.set_text("concluído")
            if gravados:
                self._popular()
                self._encher_home()
            return False

        import threading
        threading.Thread(target=trabalhar, daemon=True).start()
        dlg.run()
        estado["parar"] = True
        dlg.destroy()

    def remover_conexao(self, cx):
        if not self.confirmar(
                "Remover %s?" % cx.nome,
                "O bloco [%s] sai do arquivo, junto com os comentários que "
                "estiverem logo acima dele. As abas abertas desta máquina "
                "serão fechadas.\n\nIsto não pode ser desfeito pela "
                "interface — só editando o INI." % cx.nome,
                ok="Remover", destrutivo=True):
            return
        abertos = self._estado_grupos()
        for tipo in ("vnc", "ssh", "rdp"):
            self.fechar(self._chave(cx, tipo))
        if remover_secao(self.caminho, cx.nome):
            self._recarregar()
            GLib.idle_add(self._restaurar_grupos, abertos)
        else:
            self.avisar("Não removido",
                        "Não encontrei o bloco [%s] no arquivo." % cx.nome)

    def duplicar_conexao(self, cx):
        import copy
        novo = copy.copy(cx)
        # incrementa o ultimo numero do nome: PDV 001 -> PDV 002.
        # Pula os que ja existem, para cadastro em sequencia nao esbarrar.
        usados = {c.nome.lower() for c in self.conexoes}
        candidato = proximo_nome(cx.nome)
        for _ in range(200):
            if candidato.lower() not in usados:
                break
            candidato = proximo_nome(candidato)
        novo.nome = candidato

        # o IP acompanha o nome: PDV 001/10.6.1.31 -> PDV 002/10.6.1.32.
        # Se o host seguinte ja estiver cadastrado, avanca ate um livre.
        hosts = {c.host for c in self.conexoes}
        alvo = proximo_host(cx.host)
        for _ in range(200):
            if alvo not in hosts or alvo == cx.host:
                break
            seguinte = proximo_host(alvo)
            if seguinte == alvo:        # bateu no teto do octeto
                break
            alvo = seguinte
        novo.host = alvo
        self._editor(novo)

    def _recarregar(self, _b=None):
        self.conexoes, self.geral = carregar(self.caminho)
        self._popular()
        self._encher_home()
        self.lb_conta.set_text("%d máquinas" % len(self.conexoes))

    # ---------------------------------------------------------- ajustes
    def _abrir_ajustes(self, _b=None):
        """Painel de ajustes. Usa os blocos do dialogo_ui — nada montado a
        mao aqui, senao a proxima tela nasce fora do padrao outra vez."""
        import dialogo_ui
        dlg, cx, _ok = dialogo_ui.editor(
            # sem botao de acao: o X da barra fecha, e cada opcao ja se
            # aplica no momento em que voce mexe nela
            "Ajustes", self, ok=None, cancelar=None,
            largura=560, altura=430)

        # ---- diagnostico
        dialogo_ui.secao(cx, "DIAGNÓSTICO", primeira=True)
        dialogo_ui.interruptor(
            cx, "Painel de diagnóstico nas abas (F12)", self.mostrar_log,
            # espelha o ToggleButton, que segue sendo a fonte da verdade e o
            # alvo do F12 — assim o estado nao existe em dois lugares
            lambda ativo: self.bt_log.set_active(ativo),
            "Mostra o log da sessão dentro de cada aba. Útil para entender "
            "uma conexão que cai.")

        # ---- local dos arquivos
        dialogo_ui.secao(cx, "LOCAL DOS ARQUIVOS")
        lb_atual = rotulo(dir_dados(), "opcao-txt")
        lb_atual.set_ellipsize(Pango.EllipsizeMode.MIDDLE)
        cx.pack_start(lb_atual, False, False, 0)
        dialogo_ui.nota(
            cx, "conexoes.ini, snippets.ini e histórico ficam aqui. "
                "Apontando para uma pasta sincronizada, o backup passa a "
                "ser automático.", "dlg-dica")

        aviso = rotulo("", "dlg-dica", ellipsize=None)
        aviso.set_line_wrap(True)

        def _aplicar(destino):
            try:
                novo_dir, copiados = mover_dados_para(destino)
            except Exception as e:
                add_class(aviso, "dlg-dica-erro")
                aviso.set_text("Não foi possível usar essa pasta: %s" % e)
                return
            lb_atual.set_text(novo_dir)
            aviso.get_style_context().remove_class("dlg-dica-erro")
            aviso.set_text(
                ("Pasta vazia: %s copiado(s) para lá. " % ", ".join(copiados)
                 if copiados else "A pasta já tinha os arquivos. ")
                + "Reinicie o Acessos para passar a usar o novo local.")

        def _escolher():
            esc = Gtk.FileChooserDialog(
                title="Pasta dos arquivos do Acessos", transient_for=dlg,
                action=Gtk.FileChooserAction.SELECT_FOLDER)
            esc.add_button("Cancelar", Gtk.ResponseType.CANCEL)
            esc.add_button("Usar esta pasta", Gtk.ResponseType.OK)
            add_class(esc, "acessos-dialogo")
            try:
                esc.set_current_folder(dir_dados())
            except Exception:
                pass
            resp = esc.run()
            destino = esc.get_filename() if resp == Gtk.ResponseType.OK else None
            esc.destroy()
            if destino:
                _aplicar(destino)

        dialogo_ui.linha_botoes(cx, [
            ("Escolher pasta…", "acao", _escolher),
            ("Voltar ao padrão", "secundaria",
             lambda: _aplicar(_dir_padrao())),
        ])
        cx.pack_start(aviso, False, False, 0)

        dlg.show_all()
        dlg.run()
        dlg.destroy()

    def _editar_ini(self, _b=None):
        Gtk.show_uri_on_window(self, "file://" + self.caminho, Gdk.CURRENT_TIME)


# ---------------------------------------------------------------- main

def main():
    import atexit
    # MEDIDOR DE RESPONSIVIDADE (ACESSOS_PULSO=1).
    #
    # Distingue dois congelamentos que parecem iguais na tela:
    #   - loop de eventos BLOQUEADO: o intervalo medido dispara
    #   - loop OK, janela sem repintar: o intervalo fica normal
    # Sem isto, "a interface congelou" e ambiguo e leva a caçar o alvo
    # errado. Ligue com:  ACESSOS_PULSO=1 ./acessos.py
    if os.environ.get("ACESSOS_PULSO"):
        _ult = [time.monotonic()]

        def _pulso():
            agora = time.monotonic()
            atraso = (agora - _ult[0] - 0.5) * 1000
            _ult[0] = agora
            if atraso > 200:
                print("[pulso] LOOP BLOQUEADO por %.0f ms" % atraso,
                      flush=True)
            return True

        GLib.timeout_add(500, _pulso)
    # DIAGNOSTICO DE TRAVAMENTO: com SIGUSR1 o processo despeja a pilha
    # Python de todos os threads no terminal, sem interromper a execucao.
    #
    # Uso: provoque o congelamento e, ENQUANTO ele dura, rode noutro
    # terminal:
    #     kill -USR1 $(pgrep -f acessos.py)
    # A pilha impressa mostra a linha exata onde o loop de eventos esta
    # parado. Serve para distinguir "a aplicacao esta bloqueada em alguma
    # chamada nossa" de "a aplicacao esta ociosa e o atraso vem de fora"
    # (servidor, compositor, driver) — nesse segundo caso a pilha aparece
    # parada no proprio gtk_main, sem nada nosso em cima.
    try:
        import faulthandler
        import signal
        faulthandler.register(signal.SIGUSR1, all_threads=True, chain=False)
    except Exception:
        pass

    # se o processo morrer com o teclado capturado, o usuario fica sem
    # desktop. Esta e a ultima linha de defesa.
    atexit.register(GrabNativo.soltar_tudo)

    ap = argparse.ArgumentParser()
    ap.add_argument("--conf", help="caminho do conexoes.ini")
    ap.add_argument("--debug", action="store_true")
    ap.add_argument("--x11", action="store_true",
                    help="forcar XWayland: embute o RDP na aba, mas desliga "
                         "a captura de teclado")
    ap.add_argument("--wayland", action="store_true",
                    help="forcar Wayland nativo, ignorando x11 do INI")
    args = ap.parse_args()

    # GTK_THEME, se definida no ambiente, tem prioridade sobre o
    # gtk-theme-name definido em codigo (Janela._aplicar_css). Um shell
    # interativo as vezes carrega essa variavel via script de sessao; um
    # binario compilado, chamado a partir de um lancador de aplicativos ou
    # AppImage, pode ou nao herdar o mesmo ambiente — era mais uma fonte de
    # divergencia entre "python3 acessos.py" e o binario. Removida aqui para
    # que o nosso set_property seja sempre quem decide.
    os.environ.pop("GTK_THEME", None)

    global DEBUG
    DEBUG = args.debug
    if DEBUG:
        os.environ.setdefault("GTK_VNC_DEBUG", "1")

    # antes de criar qualquer janela: o app_id do Wayland e lido na criacao
    icone = identificar_aplicacao()
    if DEBUG:
        sys.stderr.write("icone: %s\n" % (icone or "<nenhum encontrado>"))

    caminho = caminho_conf(args.conf)

    # REEXEC ANTES DO COFRE. Esta chamada troca o processo por um novo sob
    # XWayland. Se o cofre viesse primeiro, o operador digitaria a senha,
    # o processo seria substituido e o processo NOVO pediria a senha de
    # novo — a senha aparecia duas vezes. Por isso a decisao do backend
    # grafico vem antes de qualquer dialogo.
    #
    # A chave x11 e lida direto do arquivo: carregar() depende do cofre
    # aberto, e aqui ele ainda nao esta.
    if not args.wayland:
        # padrao TRUE: sem XWayland nao ha RDP embutido
        talvez_reexec_x11(args.x11
                          or verdade(ler_geral(caminho, "x11", ""), True))

    # o dialogo do cofre e a PRIMEIRA janela a aparecer; sem a folha de
    # estilo instalada aqui, ele sairia com o tema do sistema
    instalar_css_cedo(ler_tema(caminho))

    # COFRE ANTES DE CARREGAR: carregar() decifra os campos sigilosos
    # usando o cofre global, entao ele precisa estar aberto aqui. Sem
    # cofre no arquivo, ou sem a biblioteca, destrancar() devolve None e
    # tudo segue como antes, com as senhas em claro.
    if TEM_COFRE:
        try:
            global COFRE
            COFRE, seguir = _cofre.destrancar(caminho)
            if not seguir:
                return 0              # operador desistiu na senha mestra
        except Exception as e:
            sys.stderr.write("cofre: %s\n" % e)
            COFRE = None

    conexoes, geral = carregar(caminho)



    j = Janela(conexoes, geral, caminho)
    # Sem toggle de F11 para maximizar: o app ja abre maximizado, e o botao
    # "▢" no cabecalho continua disponivel para quem quiser restaurar.
    j.maximize()
    j.show_all()
    # no_show_all impede o show_all de exibi-lo; a home comeca ativa, entao
    # o rodape tem de ser ligado a mao aqui
    j.rodape.set_visible(True)
    # show_all liga tudo; a lateral respeita o estado salvo depois disso
    j.lateral.set_visible(j.bt_lateral.get_active())
    Gtk.main()


if __name__ == "__main__":
    sys.exit(main())
