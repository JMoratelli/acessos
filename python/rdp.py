#!/usr/bin/env python3
"""RdpWidget — RDP embutido na aba, sobre o gtk-frdp.

POR QUE ESTE MODULO EXISTE
--------------------------
A AbaRdp atual lanca o xfreerdp como processo externo e o reparenta num
Gtk.Socket. Isso depende de XEmbed, que SO existe em X11 — em Wayland
nativo o RDP cai em janela separada.

E isso passou a importar: o congelamento da interface que perseguimos por
dias acontecia sob XWayland; em Wayland nativo nao ocorre. Ou seja, a
configuracao boa para o VNC (Wayland nativo) e justamente a que impede o
RDP embutido pelo caminho antigo.

O gtk-frdp resolve o impasse: e um widget GTK3 de verdade (deriva de
GtkDrawingArea), entao embute em qualquer backend, inclusive Wayland.

DEPENDENCIA, E ELA NAO E TRIVIAL
--------------------------------
O gtk-frdp NAO e empacotado no Fedora. Precisa ser compilado:

    meson setup build && ninja -C build

e a biblioteca + typelib precisam estar visiveis:

    GI_TYPELIB_PATH=.../build/src
    LD_LIBRARY_PATH=.../build/src

Se nao estiver disponivel, este modulo apenas informa e a AbaRdp continua
usando o xfreerdp externo. Nada quebra por falta dele.

LIMITACAO CONHECIDA DO gtk-frdp
-------------------------------
Mover a janela emite

    gtk_widget_get_allocated_width: assertion 'GTK_IS_WIDGET (widget)'

E ruido de dentro da biblioteca, nao nosso, e inofensivo em uso normal —
MAS vira abort sob G_DEBUG=fatal-criticals. Ou seja: nao use essa flag de
diagnostico em sessao com RDP embutido.
"""

import os

import gi

try:
    gi.require_version("Gtk", "3.0")
    from gi.repository import Gtk, GLib, GObject
    TEM_GTK = True
except Exception:                                     # pragma: no cover
    Gtk = GLib = GObject = None
    TEM_GTK = False

try:
    if not TEM_GTK:
        raise ImportError("GTK indisponível")
    gi.require_version("GtkFrdp", "0.2")
    from gi.repository import GtkFrdp
    TEM_FRDP, ERRO_FRDP = True, ""
except Exception as e:                                # pragma: no cover
    GtkFrdp, TEM_FRDP, ERRO_FRDP = None, False, str(e)

# Sem GTK o modulo ainda precisa IMPORTAR, para o acessos.py poder checar
# TEM_FRDP e cair no xfreerdp externo sem estourar. Por isso a classe herda
# de object nesse caso, em vez de Gtk.Box.
_BASE = Gtk.Box if TEM_GTK else object


class RdpWidget(_BASE):
    """Casca em volta do GtkFrdp.Display.

    Nao herda do Display de proposito: assim podemos trocar o widget
    interno numa reconexao sem destruir o que esta na arvore da aba — mesmo
    motivo que levou o VNC a ter _novo_display().
    """

    if TEM_GTK:
        __gsignals__ = {
            "rdp-conectado": (GObject.SignalFlags.RUN_FIRST, None, ()),
            "rdp-desconectado": (GObject.SignalFlags.RUN_FIRST, None, ()),
            "rdp-erro": (GObject.SignalFlags.RUN_FIRST, None, (str,)),
        }

    # Nomes conferidos com signal_list_names() na versao 0.2. Os tres
    # ultimos sao PERGUNTAS: o gtk-frdp interrompe a conexao esperando
    # resposta. Sem trata-los, a sessao conecta e CAI logo em seguida —
    # sintoma observado: "conectado" seguido de "desconectado", com a tela
    # em branco porque nenhum quadro chegou a ser enviado.
    _SINAIS = {
        "rdp-connected": "_on_conectado",
        "rdp-disconnected": "_on_caiu",
        "rdp-error": "_on_erro",
        "rdp-auth-failure": "_on_auth",
        "rdp-needs-certificate-verification": "_on_certificado",
        "rdp-needs-certificate-change-verification": "_on_certificado_mudou",
        "rdp-needs-authentication": "_on_precisa_auth",
    }

    def __init__(self):
        if not TEM_GTK:
            raise RuntimeError("GTK indisponível")
        super().__init__(orientation=Gtk.Orientation.VERTICAL)
        self.display = None
        self._conectado = False
        self._escalar = True
        self._cx = None
        self._aberto = False
        self._timer_ajuste = None
        self._timer_realoc = None
        # gancho opcional: se definido, decide sobre o certificado em vez
        # de aceitar automaticamente
        self._ao_verificar_certificado = None

    # ------------------------------------------------------------ ciclo
    def conectar(self, host, porta=3389, usuario=None, senha=None,
                 dominio=None, escalar=False):
        if not TEM_FRDP:
            self.emit("rdp-erro", "gtk-frdp indisponível: %s" % ERRO_FRDP)
            return False
        self.desconectar()
        self._aberto = False
        self._escalar = bool(escalar)
        self._cx = (host, porta)

        d = GtkFrdp.Display()
        # CREDENCIAIS ANTES DO open_host. O handshake nao para para
        # perguntar — mesma licao do VNC, onde definir depois nao adiantava.
        if usuario:
            d.props.username = usuario
        if senha:
            d.props.password = senha
        if dominio:
            d.props.domain = dominio

        # AJUSTE DE TAMANHO: DESLIGADO POR PADRAO. Leia antes de religar.
        #
        # Os dois caminhos que o gtk-frdp 0.2 oferece falharam contra
        # servidor Windows real:
        #
        # 1. set_scaling(True) — calcula, em frdp-session.c:
        #        scale = largura_widget / desktop_width
        #    e produz matriz nao inversivel. A tela fica BRANCA, com
        #        drawing failure ... invalid matrix (not invertible)
        #    Acontece MESMO com a area remota ja visivel, entao nao e so
        #    questao de esperar as dimensoes chegarem.
        #
        # 2. allow-resize — depende do canal DisplayControl. Em
        #    frdp-session.c:483 o redimensionamento so ocorre com
        #        resize_supported && allow_resize
        #    Sem o servidor anunciar suporte, o pedido inicial (feito
        #    quando o widget ainda era pequeno) permanece, e a area remota
        #    fica do tamanho de uma foto 3x4 no canto.
        #
        # Por isso o padrao e 1:1, com barras de rolagem no container. E
        # menos elegante e SEMPRE funciona. Quem quiser tentar, passe
        # escalar=True e observe qual dos dois defeitos aparece.
        #
        # Detalhe abaixo mantido para quem for tentar:
        #
        # O set_scaling do gtk-frdp calcula, em frdp-session.c:
        #     scale = altura_widget / desktop_height
        #     scale = largura_widget / desktop_width
        # Se as dimensoes da area REMOTA ainda nao chegaram, isso e divisao
        # por zero: scale vira inf/NaN, o cairo_scale monta uma matriz nao
        # inversivel e o GTK recusa a pintura — tela BRANCA, com
        #     drawing failure ... invalid matrix (not invertible)
        # Esperar por tempo (400 ms apos conectar) NAO resolve: nada garante
        # que as dimensoes chegaram naquele instante.
        #
        # allow-resize nao divide nada. Ele solta o size_request e pede ao
        # SERVIDOR que adapte a resolucao ao espaco disponivel — equivalente
        # ao /dynamic-resolution que a linha de comando do xfreerdp neste
        # projeto ja usa. Alem de nao ter o bug, o resultado e melhor:
        # imagem nativa em vez de interpolada.
        for sinal, metodo in self._SINAIS.items():
            try:
                d.connect(sinal, getattr(self, metodo))
            except TypeError:
                # NAO silenciar. Um sinal ausente aqui significa versao
                # diferente do gtk-frdp — e se for um dos "needs-*", a
                # conexao vai cair sem explicacao. Melhor o aviso feio no
                # terminal do que caçar tela branca de novo.
                import sys
                sys.stderr.write("rdp: sinal %r ausente nesta build do "
                                 "gtk-frdp\n" % sinal)

        d.set_hexpand(True)
        d.set_vexpand(True)
        d.set_can_focus(True)
        # Renegociar ao redimensionar a janela. Sem isto o servidor acerta a
        # resolucao UMA vez e nao acompanha mais. Com debounce: durante o
        # arrasto da borda chegam dezenas de size-allocate por segundo, e
        # pedir nova resolucao em cada um inunda o servidor.
        d.connect("size-allocate", self._realocou)
        self.display = d
        self.pack_start(d, True, True, 0)
        # SHOW_ALL, nao show(). Um Gtk.Box so exibe os filhos apos
        # show_all() — foi exatamente esta a armadilha do rotulo da aba
        # "inicio" no acessos.py: widget na arvore, visivel para o codigo,
        # e invisivel na tela. Com d.show() sozinho o Display fica sem ser
        # realizado e a area sai BRANCA, mesmo com a sessao conectada.
        self.show_all()

        # ABRIR SO COM O WIDGET MAPEADO.
        #
        # Pelo clique esquerdo a aba conecta enquanto o Notebook ainda esta
        # trocando de pagina: o open_host sai com o widget nao realizado, e o
        # gtk-frdp trava — a aplicacao congela e so um widget chega a abrir.
        # Pelo clique do MEIO nao acontecia, porque ali a conexao ja era
        # adiada ate a aba receber foco.
        #
        # Mesma licao do VNC, onde conectar cedo demais dava tela branca e
        # sessao morta. Aqui esperamos o map-event, que e o momento em que o
        # widget de fato existe na tela.
        alvo = (host, int(porta))
        if d.get_mapped():
            self._aberto = True
            d.open_host(*alvo)
        else:
            def _ao_mapear(_w, *_a):
                if self._aberto or self.display is not d:
                    return False
                self._aberto = True
                d.open_host(*alvo)
                return False
            d.connect("map-event", _ao_mapear)
        return True

    def desconectar(self):
        self._conectado = False
        for nome in ("_timer_ajuste", "_timer_realoc"):
            tid = getattr(self, nome, None)
            if tid is not None:
                try:
                    GLib.source_remove(tid)
                except Exception:
                    pass
                setattr(self, nome, None)
        d, self.display = self.display, None
        if d is None:
            return
        try:
            if hasattr(d, "close"):
                d.close()
        except Exception:
            pass
        try:
            if d.get_parent() is not None:
                self.remove(d)
            d.destroy()
        except Exception:
            pass

    # ----------------------------------------------------------- sinais
    def _on_conectado(self, *_a):
        self._conectado = True
        # allow-resize SO COM ALOCACAO REAL. Definido antes do open_host, o
        # widget ainda nao tem tamanho e a negociacao sai baseada em nada —
        # o servidor obedece e devolve uma area minuscula, que nao acompanha
        # a janela. Aqui esperamos o GTK dar um tamanho de verdade.
        if self._escalar:
            self._timer_ajuste = GLib.timeout_add(120, self._tentar_ajuste)
        self.emit("rdp-conectado")

    def _realocou(self, _w, _al):
        if not self._escalar or not self._conectado:
            return
        if self._timer_realoc is not None:
            try:
                GLib.source_remove(self._timer_realoc)
            except Exception:
                pass
        self._timer_realoc = GLib.timeout_add(350, self._aplicar_realoc)

    def _aplicar_realoc(self):
        self._timer_realoc = None
        d = self.display
        if d is None or not self._conectado or not self._escalar:
            return False
        al = d.get_allocation()
        if al.width < 50 or al.height < 50:
            return False
        try:
            # desligar e religar force a renegociacao com o tamanho novo
            d.props.allow_resize = False
            d.props.allow_resize = True
        except Exception:
            pass
        return False

    def _tentar_ajuste(self):
        """Liga o allow-resize assim que houver alocacao utilizavel."""
        d = self.display
        if d is None or not self._conectado:
            self._timer_ajuste = None
            return False
        al = d.get_allocation()
        if al.width < 50 or al.height < 50:
            return True                  # ainda sem tamanho; tenta de novo
        self._timer_ajuste = None
        try:
            d.props.allow_resize = True
        except Exception:
            pass
        return False

    def _on_caiu(self, *_a):
        self._conectado = False
        self.emit("rdp-desconectado")

    def _on_erro(self, _d, *args):
        msg = str(args[0]) if args else "erro na sessão RDP"
        self.emit("rdp-erro", msg)

    def _on_auth(self, *_a):
        self.emit("rdp-erro", "autenticação recusada")

    def _on_certificado(self, _d, *args):
        import sys
        sys.stderr.write("rdp: certificado novo, aceitando\n")
        """Servidor apresentou certificado autoassinado ou alterado.

        Aceitamos. O parque e interno e os PDVs usam certificado proprio;
        exigir CA valida ali tornaria o RDP inutilizavel. E o equivalente
        ao /cert:ignore que a linha de comando do xfreerdp ja usa neste
        mesmo projeto — ou seja, nao e uma decisao nova, e sim a mesma
        politica, agora explicita.

        Se um dia isso precisar de confirmacao do operador, e aqui que o
        dialogo entra: devolver True aceita, False recusa."""
        if self._ao_verificar_certificado is not None:
            return bool(self._ao_verificar_certificado(args))
        return True

    def _on_certificado_mudou(self, _d, *args):
        import sys
        sys.stderr.write("rdp: certificado MUDOU, sinal recebido "
                         "(%d args)\n" % len(args))
        sys.stderr.flush()
        return self._tratar_mudanca(args)

    def _tratar_mudanca(self, args):
        """O certificado deste host MUDOU desde a ultima conexao.

        Sinal separado do _on_certificado de proposito: os dois casos sao
        diferentes e merecem tratamento diferente.

        Certificado novo (primeira conexao) e rotina num parque interno —
        aceitar calado esta certo. Certificado ALTERADO significa uma de
        duas coisas: a maquina foi reinstalada / o certificado foi regerado
        (comum, e legitimo), ou alguem esta no meio do caminho. O SSH trata
        isso do mesmo jeito, recusando ate o operador confirmar.

        Aceitar automaticamente aqui anularia a protecao. Recusar em
        silencio deixa o tecnico sem saida pela interface: o FreeRDP guarda
        o certificado antigo em
            ~/.var/app/<app-id>/config/freerdp/server/<host>_<porta>.pem
        e recusa a conexao para sempre, sem dizer como resolver.

        Entao perguntamos. Aceitando, apagamos o .pem antigo — equivalente
        ao ssh-keygen -R que o SSH manda rodar."""
        import sys
        if self._ao_verificar_certificado is not None:
            return bool(self._ao_verificar_certificado(args))
        aceitar = self._perguntar_mudanca(args)
        sys.stderr.write("rdp: operador %s o novo certificado\n"
                         % ("ACEITOU" if aceitar else "recusou"))
        if aceitar:
            cam = self._caminho_certificado()
            sys.stderr.write("rdp: removendo %s\n" % cam)
            self._esquecer_certificado()
        return bool(aceitar)

    def _caminho_certificado(self):
        """Onde o FreeRDP guarda o certificado aceito deste host."""
        if not self._cx:
            return None
        host, porta = self._cx
        base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser(
            "~/.config")
        return os.path.join(base, "freerdp", "server",
                            "%s_%s.pem" % (host, porta))

    def _esquecer_certificado(self):
        cam = self._caminho_certificado()
        if not cam:
            return
        try:
            os.remove(cam)
        except OSError:
            pass          # nao existir ja e o estado desejado

    def _perguntar_mudanca(self, args):
        """Dialogo de confirmacao. Sem GTK ou sem janela, recusa."""
        if not TEM_GTK:
            return False
        pai = self.get_toplevel()
        if not isinstance(pai, Gtk.Window):
            pai = None
        host = self._cx[0] if self._cx else "este host"

        # a impressao digital costuma vir nos argumentos do sinal; quando
        # vem, mostramos — e o que permite ao operador conferir de fato
        digital = ""
        for a in args:
            if isinstance(a, str) and len(a) > 20 and ":" in a:
                digital = a
                break

        linhas = [
            "Isso costuma acontecer quando a máquina é reinstalada ou o "
            "certificado é regerado.",
            "",
            "Mas também é o que se veria se alguém estivesse interceptando "
            "a conexão. Só aceite se você souber o motivo da mudança.",
        ]
        if digital:
            linhas += ["", "Impressão digital nova:", digital]

        dlg = Gtk.MessageDialog(
            transient_for=pai, modal=True,
            message_type=Gtk.MessageType.WARNING,
            buttons=Gtk.ButtonsType.NONE,
            text="O certificado de %s mudou" % host)
        dlg.format_secondary_text("\n".join(linhas))
        dlg.add_button("Cancelar", Gtk.ResponseType.CANCEL)
        bt = dlg.add_button("Aceitar novo certificado", Gtk.ResponseType.OK)
        try:
            bt.get_style_context().add_class("destructive-action")
        except Exception:
            pass
        # foco inicial no Cancelar: Enter apressado nao aceita certificado
        dlg.set_default_response(Gtk.ResponseType.CANCEL)
        resp = dlg.run()
        dlg.destroy()
        return resp == Gtk.ResponseType.OK

    def _on_precisa_auth(self, *_a):
        """Servidor pediu credenciais que nao foram informadas.

        As credenciais sao definidas ANTES do open_host, entao chegar aqui
        significa que faltou usuario ou senha na conexao."""
        self.emit("rdp-erro", "servidor exige usuário e senha")

    # --------------------------------------------------------- consultas
    def conectado(self):
        return self._conectado and self.display is not None

    def definir_escala(self, ligado):
        """Ajuste ao tamanho da aba, via allow-resize.

        Deliberadamente NAO chama set_scaling — ver a explicacao da matriz
        nao inversivel em conectar()."""
        self._escalar = bool(ligado)
        d = self.display
        if d is None:
            return
        if ligado and not self._conectado:
            return          # sem sessao nao ha o que negociar
        try:
            d.props.allow_resize = self._escalar
        except Exception:
            pass

    def redesenhar_tudo(self):
        """Forca repintura completa da area remota.

        Necessario ao voltar para a aba: com a aba em segundo plano o
        gtk-frdp descarta os retangulos sujos (ver a correcao do update no
        instalar.sh), entao a tela pode estar defasada."""
        d = self.display
        if d is not None:
            d.queue_draw()

    def set_keyboard_grab(self, ligado):
        """Captura de teclado, no mesmo formato que o VncWidget expoe.

        O gtk-frdp retem os atalhos por conta propria enquanto o widget tem
        FOCO — nao ha grab explicito a pedir. Entao ligar e dar foco, e
        desligar e tirar o foco do display.

        Desligar importa mais do que parece: sem isto, uma aba RDP em
        segundo plano continuava com o teclado e o terminal da aba SSH nao
        recebia tecla alguma."""
        if ligado:
            self.focar()
            return
        d = self.display
        if d is None:
            return
        # devolve o foco ao container, tirando-o do display
        try:
            topo = self.get_toplevel()
            if isinstance(topo, Gtk.Window):
                topo.set_focus(None)
        except Exception:
            pass

    def focar(self):
        if self.display is not None:
            self.display.grab_focus()
