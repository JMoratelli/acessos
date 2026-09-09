#!/usr/bin/env python3
"""VncWidget — Gtk.DrawingArea que fala VNC via libvncclient.

Substituto do GtkVnc.Display, escrito porque o gtk-vnc apresenta um
congelamento de 2 a 3 segundos apos repinturas grandes (confirmado tambem
no GNOME Connections, que usa a mesma biblioteca, e ausente no Remmina, que
usa libvncclient). Durante o congelamento o processo fica com 0% de CPU, ou
seja, esta ESPERANDO — nao calculando.

ARQUITETURA
-----------
    thread de rede            thread principal (GTK)
    --------------            ----------------------
    WaitForMessage      -->   GLib.idle_add(queue_draw_area)
    HandleRFBServerMsg
    escreve no framebuffer    draw(): set_source_surface + paint
                              (o Cairo aponta para A MESMA memoria,
                               entao nao ha copia por quadro)

O acesso a libvncclient nao e feito por ctypes direto: passa pelo shim em C
(libvncshim.so), porque a struct rfbClient tem blocos condicionais de
compilacao e adivinhar offsets em ctypes causaria segfault. Ver vncshim.c.

Sinais emitidos (mesma ideia dos do gtk-vnc, para facilitar a troca):
    vnc-connected      — TCP + handshake completos
    vnc-initialized    — framebuffer pronto, sessao utilizavel
    vnc-disconnected   — sessao terminou
    vnc-error(str)     — falhou; o texto explica
    vnc-server-cut-text(str) — servidor mandou texto para a area de
                               transferencia
"""

import ctypes
import os
import threading

import cairo
import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk, GLib, GObject  # noqa: E402


# --------------------------------------------------------------- shim
def _carregar_shim():
    """Acha libvncshim.so ao lado deste arquivo, ou no caminho do sistema."""
    aqui = os.path.dirname(os.path.abspath(__file__))
    for caminho in (os.path.join(aqui, "libvncshim.so"),
                    "libvncshim.so"):
        try:
            return ctypes.CDLL(caminho)
        except OSError:
            continue
    raise OSError(
        "libvncshim.so nao encontrada. Compile com ./build.sh — ela precisa "
        "ficar ao lado de vncwidget.py.")


_lib = _carregar_shim()

CB_ATUALIZOU = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_int,
                                ctypes.c_int, ctypes.c_int, ctypes.c_int)
CB_REDIMENSIONOU = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_int,
                                    ctypes.c_int)
CB_TEXTO = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_char_p,
                            ctypes.c_int)

_lib.vs_criar.restype = ctypes.c_void_p
_lib.vs_criar.argtypes = [ctypes.c_void_p, CB_ATUALIZOU, CB_REDIMENSIONOU,
                          CB_TEXTO]
_lib.vs_definir_senha.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
_lib.vs_definir_usuario.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
_lib.vs_conectar.restype = ctypes.c_int
_lib.vs_conectar.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_int]
_lib.vs_esperar.restype = ctypes.c_int
_lib.vs_esperar.argtypes = [ctypes.c_void_p, ctypes.c_int]
_lib.vs_processar.restype = ctypes.c_int
_lib.vs_processar.argtypes = [ctypes.c_void_p]
_lib.vs_framebuffer.restype = ctypes.c_void_p
_lib.vs_framebuffer.argtypes = [ctypes.c_void_p]
_lib.vs_largura.restype = ctypes.c_int
_lib.vs_largura.argtypes = [ctypes.c_void_p]
_lib.vs_altura.restype = ctypes.c_int
_lib.vs_altura.argtypes = [ctypes.c_void_p]
_lib.vs_morto.restype = ctypes.c_int
_lib.vs_morto.argtypes = [ctypes.c_void_p]
_lib.vs_ponteiro.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int,
                             ctypes.c_int]
_lib.vs_tecla.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_int]
_lib.vs_enviar_texto.argtypes = [ctypes.c_void_p, ctypes.c_char_p,
                                 ctypes.c_int]
_lib.vs_destruir.argtypes = [ctypes.c_void_p]


# Combinacoes que o gerenciador de janelas captura antes do widget e que,
# por isso, precisam ser enviadas explicitamente. Os valores sao keysyms X11
# — os mesmos que o RFB usa, entao vao direto sem conversao.
ATALHOS = {
    "ctrl-alt-del": (Gdk.KEY_Control_L, Gdk.KEY_Alt_L, Gdk.KEY_Delete),
    "ctrl-alt-backspace": (Gdk.KEY_Control_L, Gdk.KEY_Alt_L,
                           Gdk.KEY_BackSpace),
    "alt-tab": (Gdk.KEY_Alt_L, Gdk.KEY_Tab),
    "alt-f4": (Gdk.KEY_Alt_L, Gdk.KEY_F4),
    "ctrl-esc": (Gdk.KEY_Control_L, Gdk.KEY_Escape),
    "super": (Gdk.KEY_Super_L,),
    "print": (Gdk.KEY_Print,),
}


class VncWidget(Gtk.DrawingArea):
    __gsignals__ = {
        "vnc-connected": (GObject.SignalFlags.RUN_FIRST, None, ()),
        "vnc-initialized": (GObject.SignalFlags.RUN_FIRST, None, ()),
        "vnc-disconnected": (GObject.SignalFlags.RUN_FIRST, None, ()),
        "vnc-desktop-resize": (GObject.SignalFlags.RUN_FIRST, None,
                               (int, int)),
        "vnc-auth-failure": (GObject.SignalFlags.RUN_FIRST, None, (str,)),
        "vnc-error": (GObject.SignalFlags.RUN_FIRST, None, (str,)),
        "vnc-server-cut-text": (GObject.SignalFlags.RUN_FIRST, None, (str,)),
    }

    def __init__(self):
        super().__init__()
        self._sessao = None
        self._surface = None
        self._buf = None            # mantem a memoria viva enquanto usada
        self._remoto = (0, 0)
        self._escalar = False
        self._ronly = False
        self._botoes = 0
        self._ultimo_ponteiro = (-1, -1)
        self._capturando = False
        self._seat_grab = None
        self._teclas_presas = set()
        self._iniciou = False
        self._conectado = False
        self._manter_proporcao = True
        self._usuario = None
        self._senha = None
        # cursor LOCAL visivel: o shim pede os pseudo-encodings de cursor,
        # entao o servidor manda o ponteiro separado em vez de carimba-lo
        # no framebuffer. Como nao desenhamos a forma remota, sobra apenas
        # o cursor local — que se move sem esperar a rede.
        self._cursor_local = True
        self._parar = threading.Event()
        self._sujo_rect = None          # retangulo envolvente acumulado
        self._lock_sujo = threading.Lock()
        self._timer_bate = None
        self._thread = None
        self._lock = threading.Lock()

        # As instancias de CFUNCTYPE PRECISAM ficar referenciadas em self:
        # se o Python coletar, o C chama endereco morto -> segfault.
        self._cb_at = CB_ATUALIZOU(self._c_atualizou)
        self._cb_rz = CB_REDIMENSIONOU(self._c_redimensionou)
        self._cb_tx = CB_TEXTO(self._c_texto)

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
        # soltar modificadores ao perder foco, senao ficam presos no remoto
        self.connect("focus-out-event", self._perdeu_foco)
        self.connect("realize", self._realizou)
        self.connect("destroy", lambda *_a: self.desconectar())

    # ---------------------------------------------------- ciclo de vida
    def conectar(self, host, porta, senha=None, usuario=None):
        if self._sessao is not None:
            self.desconectar()
        self._parar.clear()
        self._iniciou = False
        self._conectado = False
        self._sessao = _lib.vs_criar(None, self._cb_at, self._cb_rz,
                                     self._cb_tx)
        if not self._sessao:
            self.emit("vnc-error", "falha ao criar sessão VNC")
            return
        if senha:
            _lib.vs_definir_senha(self._sessao, senha.encode("utf-8"))
        if usuario:
            # so usado por esquemas que pedem usuario (VeNCrypt Plain,
            # UltraVNC MSLogon); a autenticacao VNC classica ignora
            _lib.vs_definir_usuario(self._sessao, usuario.encode("utf-8"))
        # relogio de redesenho: 16ms ~= 60 quadros por segundo. So existe
        # enquanto ha sessao, para nao consumir nada com a aba parada.
        if self._timer_bate is None:
            self._timer_bate = GLib.timeout_add(16, self._bater)
        self._thread = threading.Thread(
            target=self._rodar, args=(host, porta), daemon=True)
        self._thread.start()

    def _rodar(self, host, porta):
        """Thread de rede. Nada de GTK aqui — tudo volta por idle_add."""
        sessao = self._sessao
        ok = _lib.vs_conectar(sessao, host.encode("utf-8"), int(porta))
        if not ok:
            GLib.idle_add(self._falhou, "não foi possível conectar em "
                                        "%s:%s" % (host, porta))
            return
        GLib.idle_add(self._conectou)

        while not self._parar.is_set():
            n = _lib.vs_esperar(sessao, 100000)      # 100ms
            if n < 0:
                break
            if n == 0:
                continue
            if not _lib.vs_processar(sessao):
                break
        GLib.idle_add(self._caiu)

    def desconectar(self):
        self._soltar_seat()
        self.soltar_teclas()
        if self._timer_bate is not None:
            try:
                GLib.source_remove(self._timer_bate)
            except Exception:
                pass
            self._timer_bate = None
        self._parar.set()
        t, self._thread = self._thread, None

        # A superficie sai de cena ANTES de qualquer destruicao: assim a
        # thread principal para de pintar a partir do framebuffer.
        with self._lock:
            sessao, self._sessao = self._sessao, None
            self._surface = None
            self._buf = None
        if sessao is None:
            return

        # NAO DESTRUIR COM A THREAD VIVA.
        #
        # vs_destruir libera o rfbClient. Se a thread de rede ainda estiver
        # dentro de HandleRFBServerMessage — e ela pode estar, por exemplo
        # descomprimindo um JPEG grande, o que leva um tempo perceptivel —
        # a estrutura some debaixo dela. E uso-apos-liberacao, corrompe o
        # heap e o sintoma aparece longe da causa.
        #
        # Antes daqui havia um join(timeout=1.0) e a destruicao acontecia
        # MESMO SE O JOIN ESTOURASSE. Agora, se a thread nao terminou,
        # adiamos: um relogio verifica de tempos em tempos e destroi quando
        # for seguro. Segurar memoria por alguns segundos e barato.
        if t is None or t is threading.current_thread():
            _lib.vs_destruir(sessao)
            return

        t.join(timeout=0.5)
        if not t.is_alive():
            _lib.vs_destruir(sessao)
            return

        tentativas = [0]

        def _tentar():
            tentativas[0] += 1
            if t.is_alive() and tentativas[0] < 60:      # ate ~30s
                return True
            try:
                _lib.vs_destruir(sessao)
            except Exception:
                pass
            return False

        GLib.timeout_add(500, _tentar)

    # ------------------------------------------------ vindos do C/thread
    def _c_atualizou(self, _ctx, x, y, w, h):
        """Roda na THREAD DE REDE. So anota a area suja; nao agenda nada.

        NAO chamar GLib.idle_add daqui. Duas razoes, ambas ja custaram um
        travamento:

        1. Cada volta ao Python exige o GIL. Com varias sessoes abertas, as
           threads de rede disputavam o GIL entre si e a thread principal
           ficava sem janela para processar cliques — a interface inteira
           congelava. O shim ja agrupa por lote; aqui reduzimos o resto.

        2. O esquema "agenda um idle e marca pendente" tem um modo de falha
           silencioso: se o idle nao rodar, o flag de pendente fica preso e
           NENHUM redesenho e agendado de novo. A imagem congela para
           sempre com a aplicacao respondendo — dificil de diagnosticar.

        Agora a thread so anota, e um relogio na thread principal (_bater)
        decide quando desenhar. Nao ha estado que possa prender."""
        with self._lock_sujo:
            if self._sujo_rect is None:
                self._sujo_rect = [x, y, x + w, y + h]
            else:
                r = self._sujo_rect
                r[0] = min(r[0], x)
                r[1] = min(r[1], y)
                r[2] = max(r[2], x + w)
                r[3] = max(r[3], y + h)

    def _bater(self):
        """Relogio de redesenho na thread principal (~60 por segundo).

        Desenha so quando ha area suja. Roda sempre, entao nao existe flag
        que possa ficar preso e parar as atualizacoes."""
        # ABA DE FUNDO NAO DESENHA.
        #
        # O framebuffer continua sendo atualizado (a sessao segue viva e
        # pronta ao voltar), mas nao ha por que pedir repintura de algo
        # invisivel. Sem esta checagem, cada sessao aberta pedia repintura
        # a 60 por segundo — com escala ligada, da area INTEIRA. Com meia
        # duzia de abas isso satura o desenho da janela principal: os
        # tooltips (que sao janelas separadas) continuam aparecendo, mas a
        # janela principal para de atualizar e parece congelada.
        #
        # A area suja e descartada de proposito: ao voltar para a aba, o
        # redesenhar() forca um queue_draw completo.
        if not self.get_mapped():
            with self._lock_sujo:
                self._sujo_rect = None
            return True

        with self._lock_sujo:
            r = self._sujo_rect
            self._sujo_rect = None
        if r is None:
            return True
        with self._lock:
            surf = self._surface
            if surf is None:
                return True
            lw, lh = self._remoto
            x0, y0, x1, y1 = r
            # clampa nos limites do framebuffer
            x0 = max(0, min(x0, lw)); x1 = max(x0, min(x1, lw))
            y0 = max(0, min(y0, lh)); y1 = max(y0, min(y1, lh))
            larg = max(1, x1 - x0)
            alt = max(1, y1 - y0)
            # OBRIGATORIO: a libvncclient escreve na memoria da superficie
            # POR FORA do cairo. Sem marcar, ao desenhar numa janela real
            # (xlib/GL) o cairo pode reaproveitar uma copia em cache e a
            # tela fica parada mesmo com dados novos chegando pela rede.
            try:
                surf.mark_dirty_rectangle(x0, y0, larg, alt)
            except Exception:
                try:
                    surf.mark_dirty()
                except Exception:
                    pass
        if self._escalar:
            self.queue_draw()
        else:
            self.queue_draw_area(x0, y0, larg, alt)
        return True

    def _c_redimensionou(self, _ctx, w, h):
        GLib.idle_add(self._redimensionou, w, h)

    def _c_texto(self, _ctx, texto, tam):
        # RFB classico define o cut text em LATIN-1 (ISO 8859-1), nao UTF-8.
        # Decodificar como UTF-8 estraga acentos — comum em texto de PDV.
        try:
            bruto = ctypes.string_at(texto, tam)
        except Exception:
            return
        try:
            t = bruto.decode("latin-1")
        except Exception:
            t = bruto.decode("utf-8", "replace")
        GLib.idle_add(lambda: (self.emit("vnc-server-cut-text", t), False)[1])

    # --------------------------------------------- no thread principal
    def _conectou(self):
        self._conectado = True
        self.emit("vnc-connected")
        # ORDEM IMPORTA. O hook MallocFrameBuffer dispara DENTRO do
        # rfbInitClient, ou seja, antes de vs_conectar() retornar — entao o
        # redimensionamento chega ao idle ANTES deste "conectado". Emitir
        # vnc-initialized la faria o consumidor marcar a sessao como ativa e
        # logo depois sobrescrever com "aguardando handshake", que era o
        # estado que ficava preso na tela. Por isso o initialized so sai
        # aqui, depois do connected.
        if self._surface is not None and not self._iniciou:
            self._iniciou = True
            w, h = self._remoto
            self.emit("vnc-desktop-resize", w, h)
            self.emit("vnc-initialized")
        return False

    def _falhou(self, msg):
        self.emit("vnc-error", msg)
        return False

    def _caiu(self):
        self.emit("vnc-disconnected")
        return False

    def _redimensionou(self, w, h):
        """O framebuffer mudou de tamanho: refaz a superficie Cairo."""
        with self._lock:
            if self._sessao is None:
                return False
            ptr = _lib.vs_framebuffer(self._sessao)
            if not ptr:
                return False
            self._remoto = (w, h)
            stride = cairo.ImageSurface.format_stride_for_width(
                cairo.FORMAT_RGB24, w)
            # A superficie aponta para a MESMA memoria do framebuffer da
            # libvncclient: nao ha copia por quadro. Por isso o buffer
            # precisa continuar vivo enquanto a superficie existir.
            self._buf = (ctypes.c_char * (stride * h)).from_address(ptr)
            self._surface = cairo.ImageSurface.create_for_data(
                memoryview(self._buf), cairo.FORMAT_RGB24, w, h, stride)
        self.queue_draw()
        # se ainda nao anunciamos a conexao, o _conectou() faz os dois
        # anuncios na ordem certa; aqui so tratamos resizes posteriores
        if not self._conectado:
            return False
        self.emit("vnc-desktop-resize", w, h)
        if not self._iniciou:
            self._iniciou = True
            self.emit("vnc-initialized")
        return False

    # ------------------------------------------------------- desenho
    def _fatores(self):
        """Devolve (fx, fy, deslocx, deslocy).

        Com manter_proporcao, usa o MENOR dos dois fatores nos dois eixos e
        centraliza a sobra — o classico letterbox. Sem isso, uma tela remota
        vertical (comum em PDV/self-checkout) esticada num painel horizontal
        fica irreconhecivel: circulos viram elipses e o texto achata.
        """
        rw, rh = self._remoto
        al = self.get_allocation()
        if not rw or not rh or not self._escalar:
            return 1.0, 1.0, 0.0, 0.0
        fx = al.width / rw
        fy = al.height / rh
        if self._manter_proporcao:
            f = min(fx, fy)
            dx = (al.width - rw * f) / 2.0
            dy = (al.height - rh * f) / 2.0
            return f, f, dx, dy
        return fx, fy, 0.0, 0.0

    def definir_manter_proporcao(self, ligado):
        self._manter_proporcao = bool(ligado)
        self.queue_draw()

    def _desenhar(self, _w, ctx):
        with self._lock:
            surf = self._surface
        # fundo preto: com letterbox sobram faixas nas laterais ou em cima
        ctx.set_source_rgb(0, 0, 0)
        ctx.paint()
        if surf is None:
            return False
        fx, fy, dx, dy = self._fatores()
        ctx.translate(dx, dy)
        if fx != 1.0 or fy != 1.0:
            ctx.scale(fx, fy)
            ctx.set_source_surface(surf, 0, 0)
            # BILINEAR e o meio-termo entre nitidez e custo; FAST (vizinho
            # mais proximo) e mais barato porem serrilha texto
            ctx.get_source().set_filter(cairo.FILTER_BILINEAR)
        else:
            ctx.set_source_surface(surf, 0, 0)
        ctx.paint()
        return False

    def redesenhar_tudo(self):
        """Forca repintura completa — usar ao voltar para a aba, ja que em
        segundo plano as areas sujas sao descartadas."""
        with self._lock:
            surf = self._surface
            if surf is not None:
                try:
                    surf.mark_dirty()
                except Exception:
                    pass
        self.queue_draw()

    def definir_escala(self, ligado):
        self._escalar = bool(ligado)
        self.queue_draw()

    def definir_somente_leitura(self, ligado):
        self._ronly = bool(ligado)

    def definir_cursor_local(self, visivel):
        """Mostra ou esconde o cursor do SISTEMA sobre o widget.

        Normalmente nao e preciso mexer: o shim pede os pseudo-encodings
        de cursor, entao o servidor NAO carimba mais o ponteiro no
        framebuffer e fica so o cursor local — sem o efeito de dois
        ponteiros que aparecia antes.

        Se algum servidor ignorar o pedido e continuar carimbando, chame
        com False para esconder o local e ficar so com o remoto.
        """
        self._cursor_local = bool(visivel)
        jan = self.get_window()
        if jan is None:
            return          # ainda nao realizado; _realizou aplica depois
        if visivel:
            jan.set_cursor(None)
        else:
            disp = self.get_display()
            jan.set_cursor(Gdk.Cursor.new_for_display(
                disp, Gdk.CursorType.BLANK_CURSOR))

    def _realizou(self, _w):
        # aplica a preferencia de cursor assim que a GdkWindow existe
        self.definir_cursor_local(self._cursor_local)

    # -------------------------------------------------------- entrada
    def _para_remoto(self, ex, ey):
        """Converte coordenada do widget para coordenada do framebuffer.

        Precisa descontar o deslocamento do letterbox, senao o ponteiro fica
        deslocado em relacao ao que se ve — e o erro cresce conforme a faixa
        preta aumenta."""
        fx, fy, dx, dy = self._fatores()
        if fx == 0 or fy == 0:
            return 0, 0
        rw, rh = self._remoto
        x = int((ex - dx) / fx)
        y = int((ey - dy) / fy)
        # clampa: clique na faixa preta nao deve virar coordenada negativa
        x = max(0, min(rw - 1, x)) if rw else 0
        y = max(0, min(rh - 1, y)) if rh else 0
        return x, y

    def _botao(self, _w, ev):
        if self._ronly or self._sessao is None:
            return False
        self.grab_focus()
        bit = {1: 1, 2: 2, 3: 4}.get(ev.button, 0)
        if ev.type == Gdk.EventType.BUTTON_PRESS:
            self._botoes |= bit
        else:
            self._botoes &= ~bit
        x, y = self._para_remoto(ev.x, ev.y)
        self._ultimo_ponteiro = (x, y)
        _lib.vs_ponteiro(self._sessao, x, y, self._botoes)
        return True

    def _movimento(self, _w, ev):
        if self._ronly or self._sessao is None:
            return False
        x, y = self._para_remoto(ev.x, ev.y)
        # Evita repetir a MESMA coordenada. Mouse de alta taxa dispara
        # centenas de motion por segundo e, apos a conversao para o
        # framebuffer, muitos caem no mesmo pixel — cada um viraria uma
        # escrita no socket, segurando o main loop a toa.
        if (x, y) == self._ultimo_ponteiro:
            return True
        self._ultimo_ponteiro = (x, y)
        # ENVIO IMEDIATO, sem fila: e o ponto do exercicio
        _lib.vs_ponteiro(self._sessao, x, y, self._botoes)
        return True

    def _roda(self, _w, ev):
        if self._ronly or self._sessao is None:
            return False
        mapa = {Gdk.ScrollDirection.UP: 8, Gdk.ScrollDirection.DOWN: 16,
                Gdk.ScrollDirection.LEFT: 32, Gdk.ScrollDirection.RIGHT: 64}
        bit = mapa.get(ev.direction)
        if not bit:
            return False
        x, y = self._para_remoto(ev.x, ev.y)
        _lib.vs_ponteiro(self._sessao, x, y, self._botoes | bit)
        _lib.vs_ponteiro(self._sessao, x, y, self._botoes)
        return True

    def _tecla(self, _w, ev):
        if self._ronly or self._sessao is None:
            return False

        pressionada = ev.type == Gdk.EventType.KEY_PRESS
        keysym = ev.keyval

        # RASTREIO DO QUE ESTA PRESSIONADO.
        #
        # Sem isto, sair da janela com Alt ou Ctrl segurado deixa a tecla
        # presa DO LADO REMOTO: o servidor nunca recebe o key-release,
        # porque o evento vai para outra janela. O sintoma classico e a
        # sessao "endoidar" — cliques viram Ctrl+clique, letras viram
        # atalhos — ate voce apertar e soltar o modificador de novo.
        if pressionada:
            self._teclas_presas.add(keysym)
        else:
            self._teclas_presas.discard(keysym)

        _lib.vs_tecla(self._sessao, keysym, 1 if pressionada else 0)
        return True        # consome: impede o GTK de roubar Tab, F10, setas

    def soltar_teclas(self):
        """Solta no remoto tudo o que ficou pressionado.

        Chamado ao perder o foco. Envia apenas os key-release, na ordem
        inversa, sem tocar no estado local."""
        if self._sessao is None:
            self._teclas_presas.clear()
            return
        for keysym in sorted(self._teclas_presas, reverse=True):
            try:
                _lib.vs_tecla(self._sessao, keysym, 0)
            except Exception:
                pass
        self._teclas_presas.clear()

    def _perdeu_foco(self, *_a):
        # soltar o grab junto: teclado preso com a janela em segundo plano
        # deixa o operador sem teclado no resto do sistema
        self._soltar_seat()
        self.soltar_teclas()
        return False

    def enviar_atalho(self, nome):
        """Envia um atalho pelo nome, ex.: enviar_atalho('ctrl-alt-del')."""
        combo = ATALHOS.get(nome)
        if combo is None:
            raise ValueError("atalho desconhecido: %s (conhecidos: %s)"
                             % (nome, ", ".join(sorted(ATALHOS))))
        self.enviar_combinacao(combo)

    def enviar_combinacao(self, keysyms):
        """Envia uma combinacao inteira, ex.: Ctrl+Alt+Del.

        Necessario porque combinacoes assim sao capturadas pelo gerenciador
        de janelas ANTES de chegarem ao widget — nunca virariam um
        key-press-event aqui. Pressiona na ordem dada e solta na inversa,
        que e o que o lado remoto espera."""
        if self._sessao is None or self._ronly:
            return
        for k in keysyms:
            _lib.vs_tecla(self._sessao, k, 1)
        for k in reversed(keysyms):
            _lib.vs_tecla(self._sessao, k, 0)

    def _entrou(self, _w, _ev):
        if not self.has_focus():
            self.grab_focus()
        return False

    def enviar_texto(self, texto):
        """Envia texto para a area de transferencia da maquina remota."""
        if self._sessao is None or self._ronly or not texto:
            return
        # latin-1 pelo mesmo motivo do _c_texto. Caracteres fora da tabela
        # viram "?" em vez de derrubar o envio inteiro.
        b = texto.encode("latin-1", "replace")
        _lib.vs_enviar_texto(self._sessao, b, len(b))

    # -------------------------------------------------------- consultas
    def tamanho_remoto(self):
        return self._remoto

    def conectado(self):
        return (self._sessao is not None
                and not _lib.vs_morto(self._sessao))

    # ------------------------------------------------------------------
    # CAMADA DE COMPATIBILIDADE com a API do GtkVnc.Display.
    #
    # Existe para que a troca no acessos.py se resuma a instanciar esta
    # classe no lugar de GtkVnc.Display(): os nomes de metodo e de sinal
    # que a AbaVnc ja usa continuam valendo. Sem isto, seriam dezenas de
    # pontos de edicao espalhados — e cada um, uma chance de erro.
    # ------------------------------------------------------------------

    def open_host(self, host, porta):
        self.conectar(host, porta, self._senha, self._usuario)

    def close(self):
        self.desconectar()

    def set_credential(self, tipo, valor):
        """Guarda credenciais para o proximo open_host().

        No gtk-vnc isto era chamado durante o handshake, em resposta ao
        sinal vnc-auth-credential. Aqui a libvncclient pede as credenciais
        por callback ja dentro da thread de rede, entao elas precisam estar
        definidas ANTES de conectar — por isso apenas guardamos."""
        try:
            from gi.repository import GtkVnc
            if tipo == GtkVnc.DisplayCredential.PASSWORD:
                self._senha = valor
            elif tipo == GtkVnc.DisplayCredential.USERNAME:
                self._usuario = valor
        except Exception:
            # sem gtk-vnc instalado: aceita 0=usuario, 1=senha
            if tipo == 1:
                self._senha = valor
            elif tipo == 0:
                self._usuario = valor

    def definir_credenciais(self, usuario=None, senha=None):
        """Forma direta, sem depender dos enums do gtk-vnc."""
        self._usuario = usuario
        self._senha = senha

    def set_read_only(self, valor):
        self.definir_somente_leitura(valor)

    def get_read_only(self):
        return self._ronly

    def set_scaling(self, valor):
        self.definir_escala(valor)

    def set_pointer_local(self, valor):
        self.definir_cursor_local(valor)

    def set_keyboard_grab(self, ligado):
        """Captura o teclado no proprio widget, via GdkSeat.

        E o mesmo mecanismo que o gtk-frdp usa no RDP embutido — e que
        funciona ali. O GrabNativo do acessos.py tenta outro caminho
        (XGrabKeyboard, ou o inibidor de atalhos do Wayland por ctypes) e,
        quando nenhuma das duas bibliotecas carrega, reporta
            captura falhou: nenhum mecanismo de captura disponível
        e o VNC ficava sem captura alguma. O grab de seat nao depende
        daquelas bibliotecas: e API normal do GDK, exposta pelo PyGObject.

        Nao substitui o inibidor do Wayland — Super e Alt+Tab podem
        continuar com o compositor, conforme a politica dele. Mas entrega o
        mesmo comportamento que o RDP ja tem, que era o pedido.
        """
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
        # KEYBOARD apenas: capturar o ponteiro junto prenderia o cursor
        # dentro da janela, o que atrapalha em vez de ajudar.
        #
        # A assinatura de Gdk.Seat.grab varia entre versoes do PyGObject
        # (alguns bindings aceitam o callback e o user_data, outros nao).
        # Errar o numero de argumentos levantaria TypeError e a captura
        # falharia em silencio — por isso tentamos as duas formas.
        cap = Gdk.SeatCapabilities.KEYBOARD
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

    def set_pointer_grab(self, ligado):
        """Idem. O ponteiro ja e entregue ao widget enquanto ele tem foco e
        o cursor esta sobre ele; nao ha grab proprio a fazer."""
        return None

    def client_cut_text(self, texto):
        self.enviar_texto(texto)

    def send_keys(self, teclas):
        self.enviar_combinacao(list(teclas))

    def send_keys_ex(self, teclas, modo=None):
        """No gtk-vnc o modo separa PRESS/RELEASE/CLICK.

        Aqui CLICK (o caso normal) equivale a enviar_combinacao. Para
        PRESS e RELEASE isolados, enviamos so o lado pedido."""
        teclas = list(teclas)
        if modo is None:
            self.enviar_combinacao(teclas)
            return
        nome = getattr(modo, "value_nick", "") or str(modo).lower()
        if "press" in nome and "release" not in nome:
            for k in teclas:
                _lib.vs_tecla(self._sessao, k, 1) if self._sessao else None
        elif "release" in nome:
            for k in teclas:
                _lib.vs_tecla(self._sessao, k, 0) if self._sessao else None
        else:
            self.enviar_combinacao(teclas)

    # Sem efeito aqui, mas mantidos para a AbaVnc nao precisar de hasattr:
    # o dimensionamento e feito pelo proprio widget e pelo Cairo.
    def set_allow_resize(self, _valor):
        pass

    def set_force_size(self, _valor):
        pass

    def set_keep_aspect_ratio(self, valor):
        self.definir_manter_proporcao(valor)

    def set_shared_flag(self, _valor):
        pass

    def set_lossy_encoding(self, _valor):
        pass

    def set_smoothing(self, _valor):
        pass

    def set_depth(self, _valor):
        pass
