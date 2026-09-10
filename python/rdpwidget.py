#!/usr/bin/env python3
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
"""RdpWidget — Gtk.DrawingArea que fala RDP via libfreerdp3, sem o gtk-frdp.

MESMA RAZAO DE SER DO VncWidget (vncwidget.py): falar direto com a
libfreerdp, atraves do shim em C (librdpshim.so — ver rdpshim.c), em vez de
depender da casca GObject do gtk-frdp. O gtk-frdp continua sendo a
REFERENCIA de implementacao (foi de la que a logica de certificado, o
pipeline grafico e a conversao de teclado foram portadas) — nao reinventamos
o protocolo RDP, so trocamos quem desenha e quem processa a thread de rede,
do mesmo jeito que ja fizemos com o VNC.

ARQUITETURA (identica a do VNC)
--------------------------------
    thread de rede             thread principal (GTK)
    --------------             ----------------------
    rs_esperar/rs_processar    GLib.timeout_add (~60fps)
    grava no framebuffer  -->  draw(): set_source_surface + paint
                               (o Cairo aponta para A MESMA memoria do
                                gdi->primary_buffer; sem copia por quadro)

CERTIFICADO
-----------
IgnoreCertificate=TRUE (lado libfreerdp) so evita que a lib RECUSE sozinha
um certificado desconhecido antes mesmo de nos perguntar — necessario para
o parque interno, onde os certificados sao autoassinados e a conexao por
IP nunca bate com o nome do certificado. A DECISAO em si nunca e automatica:
tanto o certificado NOVO quanto o certificado MUDADO sao perguntados ao
operador (estilo SSH — "authenticity of host ... can't be established"),
ver _c_certificado_novo/_c_certificado_mudou. So aceita quem confirmar.
"""

import ctypes
import os
import threading

import cairo
import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk, GLib, GObject  # noqa: E402

import dialogo_ui


def _carregar_shim():
    aqui = os.path.dirname(os.path.abspath(__file__))
    for caminho in (os.path.join(aqui, "librdpshim.so"), "librdpshim.so"):
        try:
            return ctypes.CDLL(caminho)
        except OSError:
            continue
    return None


_lib = _carregar_shim()
TEM_FRDP_SHIM = _lib is not None

if TEM_FRDP_SHIM:
    CB_ATUALIZOU = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_int,
                                    ctypes.c_int, ctypes.c_int, ctypes.c_int)
    CB_REDIMENSIONOU = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_int,
                                        ctypes.c_int)
    CB_DESCONECTOU = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_char_p)
    CB_CERT_NOVO = ctypes.CFUNCTYPE(
        ctypes.c_int, ctypes.c_void_p, ctypes.c_char_p, ctypes.c_uint16,
        ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p,
        ctypes.c_uint32)
    CB_CERT_MUDOU = ctypes.CFUNCTYPE(
        ctypes.c_int, ctypes.c_void_p, ctypes.c_char_p, ctypes.c_uint16,
        ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p,
        ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p, ctypes.c_uint32)
    CB_CLIP_TEXTO = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_char_p,
                                     ctypes.c_int)
    CB_DISP_PRONTO = ctypes.CFUNCTYPE(None, ctypes.c_void_p)

    _lib.rs_criar.restype = ctypes.c_void_p
    _lib.rs_criar.argtypes = [ctypes.c_void_p, CB_ATUALIZOU, CB_REDIMENSIONOU,
                              CB_DESCONECTOU, CB_CERT_NOVO, CB_CERT_MUDOU,
                              CB_CLIP_TEXTO, CB_DISP_PRONTO]
    _lib.rs_clipboard_definir_texto.argtypes = [
        ctypes.c_void_p, ctypes.c_char_p, ctypes.c_int]
    _lib.rs_definir_credenciais.argtypes = [
        ctypes.c_void_p, ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p]
    _lib.rs_conectar.restype = ctypes.c_int
    _lib.rs_conectar.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_int]
    _lib.rs_esperar.restype = ctypes.c_int
    _lib.rs_esperar.argtypes = [ctypes.c_void_p, ctypes.c_int]
    _lib.rs_processar.restype = ctypes.c_int
    _lib.rs_processar.argtypes = [ctypes.c_void_p]
    _lib.rs_framebuffer.restype = ctypes.c_void_p
    _lib.rs_framebuffer.argtypes = [ctypes.c_void_p]
    _lib.rs_largura.restype = ctypes.c_int
    _lib.rs_largura.argtypes = [ctypes.c_void_p]
    _lib.rs_altura.restype = ctypes.c_int
    _lib.rs_altura.argtypes = [ctypes.c_void_p]
    _lib.rs_stride.restype = ctypes.c_int
    _lib.rs_stride.argtypes = [ctypes.c_void_p]
    _lib.rs_morto.restype = ctypes.c_int
    _lib.rs_morto.argtypes = [ctypes.c_void_p]
    _lib.rs_erro_auth.restype = ctypes.c_int
    _lib.rs_erro_auth.argtypes = [ctypes.c_void_p]
    _lib.rs_erro_msg.restype = ctypes.c_char_p
    _lib.rs_erro_msg.argtypes = [ctypes.c_void_p]
    _lib.rs_ponteiro_mover.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int]
    _lib.rs_ponteiro_botao.argtypes = [ctypes.c_void_p, ctypes.c_int,
                                       ctypes.c_int, ctypes.c_int, ctypes.c_int]
    _lib.rs_ponteiro_roda.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int]
    _lib.rs_pedir_resize.restype = ctypes.c_int
    _lib.rs_pedir_resize.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int]
    _lib.rs_tecla.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_int]
    _lib.rs_destruir.argtypes = [ctypes.c_void_p]


class RdpWidget(Gtk.DrawingArea):
    __gsignals__ = {
        "rdp-conectado": (GObject.SignalFlags.RUN_FIRST, None, ()),
        "rdp-desconectado": (GObject.SignalFlags.RUN_FIRST, None, ()),
        "rdp-erro": (GObject.SignalFlags.RUN_FIRST, None, (str,)),
    }

    def __init__(self):
        super().__init__()
        if not TEM_FRDP_SHIM:
            raise RuntimeError("librdpshim.so indisponível")
        self._sessao = None
        self._surface = None
        self._buf = None
        self._remoto = (0, 0)
        self._cx = None
        self._usuario = None
        self._senha = None
        self._dominio = None
        self._conectado = False
        self._teclas_presas = set()
        self._capturando = False
        self._seat_grab = None
        self._sujo_rect = None
        self._lock_sujo = threading.Lock()
        self._lock = threading.Lock()
        self._parar = threading.Event()
        self._thread = None
        self._timer_bate = None
        # gancho: decide sobre certificado MUDADO. Sem ele, RECUSA por
        # seguranca (ver rs_criar/hook_certificado_mudou no shim).
        self._ao_verificar_certificado = None

        # CLIPBOARD (canal CLIPRDR, so texto).
        #
        # HOST -> REMOTO: escutamos o "owner-change" do clipboard padrao do
        # GTK; quando o operador copia algo no host, mandamos para o shim
        # (que anuncia ao servidor). REMOTO -> HOST: o shim ja busca o texto
        # sozinho quando o servidor avisa que o clipboard de la mudou (ver
        # hook_clip_server_format_list no rdpshim.c) — aqui so recebemos e
        # colocamos no clipboard do GTK.
        #
        # _texto_local_ultimo evita eco: sem isto, ao recebermos texto do
        # remoto e colocarmos no clipboard do GTK, o proprio owner-change
        # que isso dispara mandaria o mesmo texto de volta para o remoto.
        self._clip = Gtk.Clipboard.get_default(Gdk.Display.get_default())
        self._clip_handler = None
        self._texto_local_ultimo = None

        # SPLASH DE CARREGAMENTO.
        #
        # Confirmado contra servidor real: apos "rdp-conectado" a tela passa
        # por uma sequencia BRANCO -> PRETO -> desktop de verdade, que e o
        # proprio Windows preparando a sessao (nao e bug nosso — o xfreerdp
        # e o Remmina mostram a mesma coisa). Sem isto o operador via uma
        # tela branca/preta por 1-2s achando que travou.
        #
        # Criterio de saida, o que vier primeiro:
        #   - N atualizacoes de tela reais (EndPaint) desde o connect: o
        #     desktop comecou a desenhar de verdade;
        #   - um teto de tempo, para nao segurar o splash para sempre se o
        #     servidor for lento a desenhar (o splash so e cosmetico).
        self._splash = False
        self._splash_pinturas = 0
        self._timer_splash = None
        self._SPLASH_MIN_PINTURAS = 4
        self._SPLASH_TETO_MS = 4000

        # AJUSTE DINAMICO (canal Display Control / "disp").
        #
        # Ao contrario do gtk-frdp — onde o ajuste de tamanho por escala
        # (set_scaling) dava tela branca com matriz nao inversivel — aqui
        # pedimos ao PROPRIO SERVIDOR que redesenhe na resolucao nova
        # (SendMonitorLayout), o mesmo que o /dynamic-resolution do
        # xfreerdp ja fazia neste projeto. Sem essa dependencia de escala
        # local, o problema antigo nao se aplica.
        self._escalar = True
        self._timer_resize = None
        self._pedido_pendente = None
        self._ultimo_pedido = None      # evita reenviar o mesmo tamanho

        self._cb_at = CB_ATUALIZOU(self._c_atualizou)
        self._cb_rz = CB_REDIMENSIONOU(self._c_redimensionou)
        self._cb_desc = CB_DESCONECTOU(self._c_desconectou)
        self._cb_cn = CB_CERT_NOVO(self._c_certificado_novo)
        self._cb_cm = CB_CERT_MUDOU(self._c_certificado_mudou)
        self._cb_clip = CB_CLIP_TEXTO(self._c_clip_texto)
        self._cb_disp = CB_DISP_PRONTO(self._c_disp_pronto)

        self.set_can_focus(True)
        self.add_events(
            Gdk.EventMask.BUTTON_PRESS_MASK
            | Gdk.EventMask.BUTTON_RELEASE_MASK
            | Gdk.EventMask.POINTER_MOTION_MASK
            | Gdk.EventMask.SCROLL_MASK
            | Gdk.EventMask.KEY_PRESS_MASK
            | Gdk.EventMask.KEY_RELEASE_MASK
            | Gdk.EventMask.ENTER_NOTIFY_MASK
            | Gdk.EventMask.FOCUS_CHANGE_MASK)

        self.connect("draw", self._desenhar)
        self.connect("button-press-event", self._botao)
        self.connect("button-release-event", self._botao)
        self.connect("motion-notify-event", self._movimento)
        self.connect("scroll-event", self._roda)
        self.connect("key-press-event", self._tecla)
        self.connect("key-release-event", self._tecla)
        self.connect("enter-notify-event", self._entrou)
        self.connect("focus-out-event", self._perdeu_foco)
        self.connect("size-allocate", self._realocou)
        self.connect("destroy", lambda *_a: self.desconectar())

    # ------------------------------------------------------------ ciclo
    def definir_credenciais(self, usuario=None, senha=None, dominio=None):
        self._usuario = usuario
        self._senha = senha
        self._dominio = dominio

    def conectar(self, host, porta=3389, usuario=None, senha=None,
                 dominio=None, escalar=False):
        if not TEM_FRDP_SHIM:
            self.emit("rdp-erro", "librdpshim.so indisponível")
            return False
        self.desconectar()
        self._parar.clear()
        self._conectado = False
        self._cx = (host, int(porta))
        self._splash = True
        self._splash_pinturas = 0
        self._ultimo_pedido = None
        if usuario is not None:
            self._usuario = usuario
        if senha is not None:
            self._senha = senha
        if dominio is not None:
            self._dominio = dominio

        self._sessao = _lib.rs_criar(None, self._cb_at, self._cb_rz,
                                     self._cb_desc, self._cb_cn, self._cb_cm,
                                     self._cb_clip, self._cb_disp)
        if not self._sessao:
            self.emit("rdp-erro", "falha ao criar sessão RDP")
            return False

        u = (self._usuario or "").encode("utf-8")
        p = (self._senha or "").encode("utf-8")
        d = (self._dominio or "").encode("utf-8")
        _lib.rs_definir_credenciais(self._sessao, u, p, d)

        if self._timer_bate is None:
            self._timer_bate = GLib.timeout_add(16, self._bater)
        if self._clip_handler is None:
            self._clip_handler = self._clip.connect(
                "owner-change", self._clip_mudou_no_host)
        self._thread = threading.Thread(
            target=self._rodar, args=(host, int(porta)), daemon=True)
        self._thread.start()
        return True

    def _rodar(self, host, porta):
        """Thread de rede. Nada de GTK aqui — tudo volta por idle_add."""
        sessao = self._sessao
        ok = _lib.rs_conectar(sessao, host.encode("utf-8"), porta)
        if not ok:
            erro_auth = _lib.rs_erro_auth(sessao)
            msg = (_lib.rs_erro_msg(sessao) or b"").decode("utf-8", "replace")
            if erro_auth:
                GLib.idle_add(self._falhou_auth, msg or "autenticação recusada")
            else:
                GLib.idle_add(self._falhou, msg or ("não foi possível conectar "
                                                     "em %s:%s" % (host, porta)))
            return
        GLib.idle_add(self._conectou)

        while not self._parar.is_set():
            n = _lib.rs_esperar(sessao, 100)      # 100ms
            if n < 0:
                break
            if n == 0:
                continue
            if not _lib.rs_processar(sessao):
                break
        GLib.idle_add(self._caiu)

    def desconectar(self):
        self._conectado = False
        self._splash = False
        self._soltar_seat()
        self.soltar_teclas()
        if self._timer_bate is not None:
            try:
                GLib.source_remove(self._timer_bate)
            except Exception:
                pass
            self._timer_bate = None
        if self._timer_splash is not None:
            try:
                GLib.source_remove(self._timer_splash)
            except Exception:
                pass
            self._timer_splash = None
        if self._clip_handler is not None:
            try:
                self._clip.disconnect(self._clip_handler)
            except Exception:
                pass
            self._clip_handler = None
        if self._timer_resize is not None:
            try:
                GLib.source_remove(self._timer_resize)
            except Exception:
                pass
            self._timer_resize = None
        self._ultimo_pedido = None
        self._parar.set()
        t, self._thread = self._thread, None

        with self._lock:
            sessao, self._sessao = self._sessao, None
            self._surface = None
            self._buf = None
        if sessao is None:
            return

        # MESMA REGRA DO VNC: nao destruir com a thread de rede viva. Ver
        # a explicacao longa em vncwidget.py::desconectar.
        if t is None or t is threading.current_thread():
            _lib.rs_destruir(sessao)
            return
        t.join(timeout=0.5)
        if not t.is_alive():
            _lib.rs_destruir(sessao)
            return

        tentativas = [0]

        def _tentar():
            tentativas[0] += 1
            if t.is_alive() and tentativas[0] < 60:
                return True
            try:
                _lib.rs_destruir(sessao)
            except Exception:
                pass
            return False

        GLib.timeout_add(500, _tentar)

    # ------------------------------------------------ vindos do C/thread
    def _c_atualizou(self, _ctx, x, y, w, h):
        """Roda na THREAD DE REDE. Mesma logica do VNC: so anota a area
        suja; quem desenha e o relogio _bater na thread principal."""
        with self._lock_sujo:
            if self._sujo_rect is None:
                self._sujo_rect = [x, y, x + w, y + h]
            else:
                r = self._sujo_rect
                r[0] = min(r[0], x); r[1] = min(r[1], y)
                r[2] = max(r[2], x + w); r[3] = max(r[3], y + h)
            self._splash_pinturas += 1

    def _c_redimensionou(self, _ctx, w, h):
        GLib.idle_add(self._redimensionou, w, h)

    def _c_desconectou(self, _ctx, _motivo):
        pass    # o laco em _rodar ja trata o fim da sessao

    def _perguntar_no_main_thread(self, funcao, *args):
        """Ponte thread de rede -> thread principal para dialogos GTK.

        Os callbacks de certificado do FreeRDP (VerifyCertificateEx e
        VerifyChangedCertificateEx) rodam SINCRONOS dentro do
        freerdp_connect, ou seja, na THREAD DE REDE (_rodar) — e a lib
        espera nosso retorno antes de prosseguir o handshake. Criar um
        Gtk.Dialog fora da thread principal e comportamento indefinido no
        GTK. Por isso agendamos a exibicao real via GLib.idle_add (isso e
        thread-safe: so enfileira) e bloqueamos aqui ate a thread principal
        terminar o dialogo e responder — o handshake so ganha problema se o
        operador demorar para responder, o que e o comportamento certo
        (mesma logica do SSH esperando o "yes" no prompt)."""
        pronto = threading.Event()
        resultado = [False]

        def _mostrar():
            resultado[0] = funcao(*args)
            pronto.set()
            return False

        GLib.idle_add(_mostrar)
        pronto.wait()
        return resultado[0]

    def _c_certificado_novo(self, _ctx, host, porta, nome_comum, assunto,
                            emissor, digital, flags):
        """Certificado novo/autoassinado — perguntamos, no mesmo espirito
        do SSH na primeira conexao a um host ("authenticity of host ...
        can't be established"). NAO aceitamos calado: o parque pode ser
        interno, mas quem decide se aquela impressao digital e a esperada
        e o operador, nao o codigo."""
        host_s = (host or b"").decode("utf-8", "replace")
        digital_s = (digital or b"").decode("utf-8", "replace")
        if self._ao_verificar_certificado is not None:
            aceitar = self._ao_verificar_certificado(host_s, digital_s)
        else:
            aceitar = self._perguntar_no_main_thread(
                self._perguntar_novo, host_s, digital_s)
        return 1 if aceitar else 0

    def _c_certificado_mudou(self, _ctx, host, porta, nome_comum, assunto,
                             emissor, digital_novo, assunto_antigo,
                             emissor_antigo, digital_antigo, flags):
        """O certificado deste host MUDOU desde a ultima conexao aceita.

        Rotina (maquina reinstalada / certificado regerado) OU alguem no
        meio do caminho — o SSH trata a mesma situacao recusando ate
        confirmacao. Perguntamos ao operador; aceitando, devolvemos 1 e a
        propria libfreerdp substitui o certificado salvo."""
        host_s = (host or b"").decode("utf-8", "replace")
        digital_s = (digital_novo or b"").decode("utf-8", "replace")
        if self._ao_verificar_certificado is not None:
            aceitar = self._ao_verificar_certificado(host_s, digital_s)
        else:
            aceitar = self._perguntar_no_main_thread(
                self._perguntar_mudanca, host_s, digital_s)
        return 1 if aceitar else 0

    def _perguntar_novo(self, host, digital):
        """Dialogo estilo SSH na primeira conexao: mostra a impressao
        digital e pede confirmacao antes de guardar o certificado.

        Usa dialogo_ui — o MESMO formato que o resto do app usa para
        confirmacao (avisar/confirmar em acessos.py, cofre.py etc.). Um
        Gtk.MessageDialog cru ignora o CSS do app e destoa visualmente do
        resto da interface; dialogo_ui.confirmar() e um Gtk.Dialog comum
        que obedece a folha de estilo, e se degrada graciosamente (visual
        neutro) quando chamado fora do acessos.py, por exemplo em teste
        isolado deste modulo."""
        dialogo_ui.liberar_grab()
        pai = self.get_toplevel()
        if not isinstance(pai, Gtk.Window):
            pai = None
        linhas = [
            "A autenticidade do host '%s' não pode ser verificada "
            "automaticamente (certificado desconhecido ou autoassinado)."
            % (host or "?"),
            "",
            "Confira a impressão digital abaixo com o administrador antes "
            "de continuar, se não tiver certeza.",
        ]
        if digital:
            linhas += ["", "Impressão digital:", digital]
        return dialogo_ui.confirmar(
            pai, "Confirmar certificado de %s?" % (host or "este host"),
            "\n".join(linhas), ok="Confiar e conectar", cancelar="Cancelar")

    # ------------------------------------------------------- clipboard
    def _clip_mudou_no_host(self, clipboard, _event):
        """Disparado quando o clipboard do HOST muda de dono. So texto por
        enquanto — arquivos/imagens ficam para uma proxima rodada, se
        precisar (o canal CLIPRDR e o mesmo, so falta o resto do
        protocolo)."""
        if self._sessao is None:
            return
        texto = clipboard.wait_for_text()
        if texto is None or texto == self._texto_local_ultimo:
            return
        # o proprio texto que ACABAMOS de colocar no clipboard (vindo do
        # remoto) tambem dispara este sinal — sem o "ultimo" acima
        # entraria em eco: remoto -> host -> "mudou" -> de volta ao remoto.
        codificado = texto.encode("utf-8")
        _lib.rs_clipboard_definir_texto(self._sessao, codificado,
                                        len(codificado))

    def _c_clip_texto(self, _ctx, utf8, tam):
        """Roda na THREAD DE REDE (chamada direto do hook C). So repassa
        para o main loop — nunca mexer em GTK fora dele."""
        try:
            texto = ctypes.string_at(utf8, tam).decode("utf-8", "replace")
        except Exception:
            return
        GLib.idle_add(self._aplicar_clip_do_remoto, texto)

    def _aplicar_clip_do_remoto(self, texto):
        self._texto_local_ultimo = texto
        self._clip.set_text(texto, -1)
        self._clip.store()
        return False

    def _c_disp_pronto(self, _ctx):
        """Canal Display Control terminou o handshake (DisplayControlCaps
        chegou) — roda na THREAD DE REDE, so repassa para o main loop.

        Sem isto o primeiro pedido de ajuste (feito no size-allocate que
        acontece logo ao abrir a aba) chegava CEDO DEMAIS: o canal ainda nao
        tinha ouvido os limites do servidor, rs_pedir_resize devolvia 0
        calado, e nada tentava de novo depois — a sessao ficava presa na
        resolucao negociada na conexao ate o operador mexer manualmente no
        botao 'Ajustar'. Agora, assim que o canal fica pronto, pedimos logo
        o tamanho ATUAL da aba."""
        GLib.idle_add(self._disp_ficou_pronto)

    def _disp_ficou_pronto(self):
        if self._escalar:
            self._agendar_resize()
        return False

    def _perguntar_mudanca(self, host, digital):
        """Mesmo formato dialogo_ui do _perguntar_novo. perigo=True pinta o
        botao de confirmar em vermelho — risco maior que o certificado
        novo, e o estilo (ja usado em toda confirmacao destrutiva do app)
        deixa isso visualmente claro sem precisar de texto extra."""
        dialogo_ui.liberar_grab()
        pai = self.get_toplevel()
        if not isinstance(pai, Gtk.Window):
            pai = None
        linhas = [
            "Isso costuma acontecer quando a máquina é reinstalada ou o "
            "certificado é regerado.",
            "",
            "Mas também é o que se veria se alguém estivesse interceptando "
            "a conexão. Só aceite se você souber o motivo da mudança.",
        ]
        if digital:
            linhas += ["", "Impressão digital nova:", digital]
        return dialogo_ui.confirmar(
            pai, "O certificado de %s mudou" % (host or "este host"),
            "\n".join(linhas), ok="Aceitar novo certificado",
            cancelar="Cancelar", perigo=True)

    # --------------------------------------------- no thread principal
    def _conectou(self):
        self._conectado = True
        if self._timer_splash is not None:
            try:
                GLib.source_remove(self._timer_splash)
            except Exception:
                pass
        self._timer_splash = GLib.timeout_add(self._SPLASH_TETO_MS,
                                              self._splash_teto)
        self.emit("rdp-conectado")
        return False

    def _splash_teto(self):
        """Teto de tempo do splash: cosmetico, entao nao vale a pena
        segurar para sempre esperando N pinturas se o servidor demorar."""
        self._timer_splash = None
        if self._splash:
            self._splash = False
            self.queue_draw()
        return False

    def _falhou(self, msg):
        self.emit("rdp-erro", msg)
        return False

    def _falhou_auth(self, msg):
        self.emit("rdp-erro", "autenticação recusada: %s" % msg)
        return False

    def _caiu(self):
        self._conectado = False
        self.emit("rdp-desconectado")
        return False

    def _redimensionou(self, w, h):
        with self._lock:
            if self._sessao is None:
                return False
            ptr = _lib.rs_framebuffer(self._sessao)
            if not ptr:
                return False
            self._remoto = (w, h)
            stride = _lib.rs_stride(self._sessao) or \
                cairo.ImageSurface.format_stride_for_width(cairo.FORMAT_RGB24, w)
            self._buf = (ctypes.c_char * (stride * h)).from_address(ptr)
            self._surface = cairo.ImageSurface.create_for_data(
                memoryview(self._buf), cairo.FORMAT_RGB24, w, h, stride)
        self.set_size_request(w, h)
        self.queue_draw()
        return False

    def _bater(self):
        """Relogio de redesenho (~60fps), identico em espirito ao do VNC:
        so pinta quando ha area suja, e nao ha flag que possa ficar presa."""
        if not self.get_mapped():
            with self._lock_sujo:
                self._sujo_rect = None
            return True
        with self._lock_sujo:
            r = self._sujo_rect
            self._sujo_rect = None
            pinturas = self._splash_pinturas
        if self._splash and pinturas >= self._SPLASH_MIN_PINTURAS:
            self._splash = False
            if self._timer_splash is not None:
                try:
                    GLib.source_remove(self._timer_splash)
                except Exception:
                    pass
                self._timer_splash = None
            self.queue_draw()
        if r is None:
            return True
        with self._lock:
            surf = self._surface
            if surf is None:
                return True
            lw, lh = self._remoto
            x0, y0, x1, y1 = r
            x0 = max(0, min(x0, lw)); x1 = max(x0, min(x1, lw))
            y0 = max(0, min(y0, lh)); y1 = max(y0, min(y1, lh))
            larg = max(1, x1 - x0); alt = max(1, y1 - y0)
            try:
                surf.mark_dirty_rectangle(x0, y0, larg, alt)
            except Exception:
                try:
                    surf.mark_dirty()
                except Exception:
                    pass
        if self._splash:
            self.queue_draw()          # overlay cobre o widget inteiro
        else:
            self.queue_draw_area(x0, y0, larg, alt)
        return True

    def redesenhar_tudo(self):
        with self._lock:
            surf = self._surface
            if surf is not None:
                try:
                    surf.mark_dirty()
                except Exception:
                    pass
        self.queue_draw()

    # ------------------------------------------------------- desenho
    def _desenhar(self, _w, ctx):
        ctx.set_source_rgb(0, 0, 0)
        ctx.paint()
        if self._splash:
            self._desenhar_splash(ctx)
            return False
        with self._lock:
            surf = self._surface
        if surf is None:
            return False
        ctx.set_source_surface(surf, 0, 0)
        ctx.paint()
        return False

    def _desenhar_splash(self, ctx):
        """Overlay mostrado entre 'rdp-conectado' e o desktop de verdade
        aparecer — ver o comentario em __init__ sobre o BRANCO->PRETO que o
        proprio Windows produz nesse intervalo."""
        alloc = self.get_allocation()
        largura, altura = alloc.width, alloc.height
        texto = "Conectando…"
        ctx.select_font_face("sans-serif", cairo.FONT_SLANT_NORMAL,
                             cairo.FONT_WEIGHT_NORMAL)
        ctx.set_font_size(16)
        extensao = ctx.text_extents(texto)
        ctx.set_source_rgb(0.75, 0.75, 0.75)
        ctx.move_to((largura - extensao.width) / 2.0 - extensao.x_bearing,
                    (altura - extensao.height) / 2.0 - extensao.y_bearing)
        ctx.show_text(texto)

    # -------------------------------------------------------- entrada
    def _botao(self, _w, ev):
        if self._sessao is None:
            return False
        self.grab_focus()
        botao = {1: 1, 2: 2, 3: 3}.get(ev.button, 0)
        if not botao:
            return False
        pressionado = 1 if ev.type == Gdk.EventType.BUTTON_PRESS else 0
        _lib.rs_ponteiro_botao(self._sessao, int(ev.x), int(ev.y), botao,
                               pressionado)
        return True

    def _movimento(self, _w, ev):
        if self._sessao is None:
            return False
        _lib.rs_ponteiro_mover(self._sessao, int(ev.x), int(ev.y))
        return True

    def _roda(self, _w, ev):
        if self._sessao is None:
            return False
        mapa = {Gdk.ScrollDirection.UP: (0, 1), Gdk.ScrollDirection.DOWN: (0, -1),
                Gdk.ScrollDirection.LEFT: (1, -1), Gdk.ScrollDirection.RIGHT: (1, 1)}
        par = mapa.get(ev.direction)
        if not par:
            return False
        eixo, passos = par
        _lib.rs_ponteiro_roda(self._sessao, eixo, passos)
        return True

    def _tecla(self, _w, ev):
        if self._sessao is None:
            return False
        pressionada = ev.type == Gdk.EventType.KEY_PRESS
        codigo = ev.hardware_keycode
        if pressionada:
            self._teclas_presas.add(codigo)
        else:
            self._teclas_presas.discard(codigo)
        _lib.rs_tecla(self._sessao, codigo, 1 if pressionada else 0)
        return True

    def soltar_teclas(self):
        if self._sessao is None:
            self._teclas_presas.clear()
            return
        for codigo in sorted(self._teclas_presas, reverse=True):
            try:
                _lib.rs_tecla(self._sessao, codigo, 0)
            except Exception:
                pass
        self._teclas_presas.clear()

    def _perdeu_foco(self, *_a):
        # soltar o grab junto: teclado preso com a janela em segundo plano
        # deixa o operador sem teclado no resto do sistema (mesma licao do
        # VncWidget)
        self._soltar_seat()
        self.soltar_teclas()
        return False

    def _entrou(self, _w, _ev):
        if not self.has_focus():
            self.grab_focus()
        return False

    # -------------------------------------------------------- consultas
    def conectado(self):
        return self._conectado and self._sessao is not None

    def tamanho_remoto(self):
        return self._remoto

    def focar(self):
        self.grab_focus()

    def definir_escala(self, ligado):
        """Compat com o botao 'Ajustar' da AbaRdpEmbutido.

        Ao ligar, pede logo um resize para o tamanho atual da aba — sem
        esperar o proximo size-allocate, que so vem de um redimensionamento
        de verdade da janela."""
        self._escalar = bool(ligado)
        if self._escalar:
            self._agendar_resize()

    def _realocou(self, _w, alocacao):
        if not self._escalar or not self._conectado:
            return
        self._agendar_resize(alocacao.width, alocacao.height)

    def _agendar_resize(self, largura=None, altura=None):
        """Debounce: durante o arrasto da borda chegam dezenas de
        size-allocate por segundo — pedir resize a cada um inundaria o
        canal Display Control com pedidos que o servidor mal termina de
        processar antes do proximo chegar (mesma licao do gtk-frdp original,
        SELECT_TIMEOUT/redesenho a parte)."""
        if largura is None:
            alocacao = self.get_allocation()
            largura, altura = alocacao.width, alocacao.height
        if largura < 50 or altura < 50:
            return          # ainda sem alocacao util (aba trocando de pagina)
        self._pedido_pendente = (largura, altura)
        if self._timer_resize is not None:
            try:
                GLib.source_remove(self._timer_resize)
            except Exception:
                pass
        self._timer_resize = GLib.timeout_add(350, self._aplicar_resize)

    def _aplicar_resize(self):
        self._timer_resize = None
        if self._sessao is None or not self._escalar:
            return False
        largura, altura = self._pedido_pendente
        if (largura, altura) == self._ultimo_pedido:
            return False    # mesmo tamanho de antes, nao repete o pedido
        if _lib.rs_pedir_resize(self._sessao, largura, altura):
            self._ultimo_pedido = (largura, altura)
        return False

    def set_keyboard_grab(self, ligado):
        """Captura de teclado via GdkSeat — MESMO mecanismo que o
        VncWidget usa (ver vncwidget.py::set_keyboard_grab). Sem isto,
        atalhos como Super e Alt+Tab iam para o gerenciador de janelas do
        HOST em vez de para a sessao remota.

        KEYBOARD apenas: capturar o ponteiro junto prenderia o cursor
        dentro da janela, o que atrapalha em vez de ajudar."""
        self._capturando = bool(ligado)
        if not ligado:
            self._soltar_seat()
            return
        self.grab_focus()
        jan = self.get_window()
        if jan is None:
            return
        try:
            seat = self.get_display().get_default_seat()
        except Exception:
            self._seat_grab = None
            return
        cap = Gdk.SeatCapabilities.KEYBOARD
        # A assinatura de Gdk.Seat.grab varia entre versoes do PyGObject —
        # tentamos as duas formas, como no VncWidget.
        for args in ((jan, cap, True, None, None, None, None),
                     (jan, cap, True, None, None, None)):
            try:
                res = seat.grab(*args)
            except TypeError:
                continue
            except Exception:
                break
            self._seat_grab = seat if res == Gdk.GrabStatus.SUCCESS else None
            return
        self._seat_grab = None

    def _soltar_seat(self):
        seat, self._seat_grab = getattr(self, "_seat_grab", None), None
        if seat is not None:
            try:
                seat.ungrab()
            except Exception:
                pass
