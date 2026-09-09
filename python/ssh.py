#!/usr/bin/env python3
"""Aba de terminal SSH.

Separada do acessos.py para manutenção: o terminal tem particularidades
próprias (VTE, TTY, TERM, biblioteca de snippets, chave de host) que não se
misturam com o resto.

O acessos.py importa `AbaSsh` daqui e passa os utilitários de que ela
precisa por `configurar()` — assim não há import circular, já que este
módulo é carregado antes de o acessos.py terminar de se definir.
"""

import os
import shutil

import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk, GLib  # noqa: E402

try:
    gi.require_version("Vte", "2.91")
    from gi.repository import Vte
    TEM_VTE, ERRO_VTE = True, ""
except (ValueError, ImportError) as e:                # pragma: no cover
    Vte, TEM_VTE, ERRO_VTE = None, False, str(e)

# ssh: 6 = identificacao do host mudou. O VTE devolve o status bruto do
# waitpid, entao 6 chega como 6 << 8 = 1536.
SSH_CHAVE_MUDOU = 6

# ---------------------------------------------------------------------------
# FABRICA, e nao import direto.
#
# AbaSsh herda de AbaBase, que vive no acessos.py — e o acessos.py precisa
# importar este modulo. Um import mutuo no topo pegaria o outro lado pela
# metade e falharia.
#
# Por isso a classe nasce dentro de construir(): o acessos.py chama assim que
# AbaBase existe, passando tambem os utilitarios de interface. Fica explicito
# quem depende de quem, e o erro aparece alto se algo faltar, em vez de virar
# um AttributeError obscuro la na frente.
# ---------------------------------------------------------------------------
AbaBase = None
add_class = None
carregar_snippets = None
fonte_mono = None
rgba = None
AbaSsh = None


def construir(base, **utilitarios):
    """Cria e devolve a classe AbaSsh. Chamado uma vez pelo acessos.py."""
    global AbaBase, AbaSsh
    AbaBase = base
    globals().update(utilitarios)

    faltando = [n for n in ("add_class", "carregar_snippets", "fonte_mono",
                            "rgba") if globals().get(n) is None]
    if faltando:
        raise RuntimeError("ssh.construir: faltou %s" % ", ".join(faltando))

    AbaSsh = _montar()
    return AbaSsh


def _montar():


    class AbaSsh(AbaBase):
        tipo = "ssh"

        def __init__(self, conexao, janela):
            super().__init__(conexao, janela)
            self.pid = None
            # usado UMA vez logo apos remover a chave antiga, para o ssh aceitar a
            # nova sem prompt. Sem isso ele volta a sair com 6 e a oferta se repete.
            self.aceitar_nova_chave = False
            self.perguntando_chave = False

            self.bt_auto.set_active(self.cx.ssh_auto)
            self._ligar_auto("ssh_auto")

            bt_rec = add_class(Gtk.Button(label="Reconectar"), "secundaria")
            bt_rec.connect("clicked", lambda _b: self.reconectar(manual=True))
            self.barra.pack_end(bt_rec, False, False, 0)

            # biblioteca de snippets dentro do proprio shell, filtrada pela
            # plataforma DESTA maquina: um PDV Linux nao ve comando PowerShell
            self.bt_snip = Gtk.MenuButton(label="⌘  snippets")
            add_class(self.bt_snip, "secundaria")
            self.bt_snip.set_tooltip_text(
                "Inserir um comando da biblioteca. Ele é COLADO no terminal, "
                "não executado — você revisa e aperta Enter.")
            self.barra.pack_end(self.bt_snip, False, False, 0)
            self._montar_menu_snippets()

            if self.cx.tem_vnc:
                bt = add_class(Gtk.Button(label="Tela"), "secundaria")
                bt.connect("clicked", lambda _b: self.janela.abrir(self.cx, "vnc"))
                self.barra.pack_end(bt, False, False, 0)

            # ARQUIVOS: abre a aba unica de SFTP ja apontando para esta maquina.
            # Fica aqui, e nao numa aba propria por conexao, porque o SFTP
            # aproveita as credenciais SSH desta conexao — o atalho contextual
            # poupa o operador de reescolher a maquina no seletor.
            bt_arq = add_class(Gtk.Button(label="📁  Arquivos"), "secundaria")
            bt_arq.set_tooltip_text("Enviar e baixar arquivos desta máquina "
                                    "(SFTP, pela mesma conexão SSH)")
            bt_arq.connect("clicked", lambda _b: self.janela.abrir_sftp(self.cx))
            self.barra.pack_end(bt_arq, False, False, 0)

            self.term = Vte.Terminal()
            self.term.set_scrollback_lines(20000)
            self.term.set_mouse_autohide(True)
            self.term.set_font(fonte_mono(11))

            # ---- ajustes de uso diario, todos protegidos: a API do VTE
            # varia entre versoes e um metodo ausente nao pode derrubar a aba
            for metodo, valor in (
                    # sino audivel num app com varias sessoes vira barulho
                    ("set_audible_bell", False),
                    # rolar automaticamente quando chega saida nova, mas NAO
                    # quando o operador subiu o historico para ler algo
                    ("set_scroll_on_output", False),
                    ("set_scroll_on_keystroke", True),
                    # caminhos e URLs selecionaveis com duplo clique inteiros
                    ("set_word_char_exceptions", "-A-Za-z0-9,./?%&#:_=+@~"),
                    # deixa o texto respirar; ajuda em sessao longa
                    ("set_cursor_shape", None),
            ):
                if valor is None or not hasattr(self.term, metodo):
                    continue
                try:
                    getattr(self.term, metodo)(valor)
                except Exception:
                    pass
            for nome in ("set_cell_width_scale", "set_cell_height_scale"):
                if hasattr(self.term, nome):
                    try:
                        getattr(self.term, nome)(1.0)
                    except Exception:
                        pass
            try:
                self.term.set_cursor_blink_mode(Vte.CursorBlinkMode.ON)
                self.term.set_colors(rgba(janela.cor("term_fg")),
                                     rgba(janela.cor("term_bg")), None)
            except Exception:
                pass
            self.term.connect("child-exited", self._on_saiu)
            self.term.connect("button-press-event", self._on_botao)
            self.term.connect("key-press-event", self._on_tecla)

            # CTRL+RODA ajusta o corpo da fonte. Numa sessao de leitura de
            # log isso vale mais do que parece, e e o gesto que todo
            # terminal tem — a falta dele incomoda sem que se saiba por que.
            self._corpo = 11
            self.term.add_events(Gdk.EventMask.SCROLL_MASK
                                 | Gdk.EventMask.SMOOTH_SCROLL_MASK)
            self.term.connect("scroll-event", self._on_roda)

            # URLs CLICAVEIS. Sem isto, copiar um link de dentro de um log
            # exige selecionar caractere a caractere.
            try:
                regex = Vte.Regex.new_for_match(
                    r"(https?|ftp)://[-A-Za-z0-9+&@#/%?=~_|!:,.;]*"
                    r"[-A-Za-z0-9+&@#/%=~_|]", -1, 0x00000400)
                self._tag_url = self.term.match_add_regex(regex, 0)
                self.term.match_set_cursor_name(self._tag_url, "pointer")
            except Exception:
                self._tag_url = -1

            sw = Gtk.ScrolledWindow()
            sw.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
            sw.add(self.term)
            self.pack_start(sw, True, True, 0)
            self.pack_start(self.exp_log, False, False, 0)

        def _url_em(self, ev):
            """Devolve a URL sob o ponteiro, se houver.

            A API mudou de nome entre versoes do VTE — match_check_event nas
            novas, match_check nas antigas —, por isso as duas tentativas."""
            for nome in ("match_check_event", "match_check"):
                fn = getattr(self.term, nome, None)
                if fn is None:
                    continue
                try:
                    achado = fn(ev) if nome == "match_check_event" else None
                except Exception:
                    continue
                if achado and achado[0]:
                    return achado[0]
            return None

        def _on_roda(self, _w, ev):
            """Ctrl+roda muda o corpo da fonte; sem Ctrl, rolagem normal."""
            if not (ev.state & Gdk.ModifierType.CONTROL_MASK):
                return False
            if ev.direction == Gdk.ScrollDirection.UP:
                passo = 1
            elif ev.direction == Gdk.ScrollDirection.DOWN:
                passo = -1
            elif ev.direction == Gdk.ScrollDirection.SMOOTH:
                ok, _dx, dy = ev.get_scroll_deltas()
                if not ok or abs(dy) < 0.1:
                    return True
                passo = -1 if dy > 0 else 1
            else:
                return False
            # limites: abaixo de 6 fica ilegivel, acima de 32 cabe pouco
            self._corpo = max(6, min(32, self._corpo + passo))
            try:
                self.term.set_font(fonte_mono(self._corpo))
            except Exception:
                pass
            return True

        def _montar_menu_snippets(self):
            """Monta o menu com o que serve a ESTA maquina.

            Reconstruido a cada abertura do menu: se voce editar a biblioteca
            no ✎ snippets com a aba aberta, o menu ja reflete."""
            menu = Gtk.Menu()
            try:
                itens = [sn for sn in carregar_snippets()
                         if sn.serve_para(self.cx)]
            except Exception as e:
                itens = []
                self.reg("não consegui ler os snippets: %s" % e)

            if not itens:
                mi = Gtk.MenuItem(label="nenhum snippet para %s"
                                  % ("Windows" if self.cx.windows else "Linux"))
                mi.set_sensitive(False)
                menu.append(mi)
            for sn in itens:
                rot = sn.descricao
                if sn.root and not self.cx.windows:
                    rot += "   (precisa de root)"
                mi = Gtk.MenuItem(label=rot)
                mi.set_tooltip_text(sn.comando[:300])
                mi.connect("activate", lambda _m, c=sn.comando: self._colar(c))
                menu.append(mi)

            menu.append(Gtk.SeparatorMenuItem())
            mi = Gtk.MenuItem(label="Editar biblioteca…")
            mi.connect("activate", lambda _m: self._editar_snippets())
            menu.append(mi)
            menu.show_all()
            self.bt_snip.set_popup(menu)

        def _editar_snippets(self):
            self.janela.abrir_snippets()
            self._montar_menu_snippets()      # reflete o que mudou

        def _colar(self, comando):
            """Cola no terminal SEM executar.

            Quem vai rodar um comando revisa antes — principalmente se ele veio
            de uma biblioteca escrita noutro dia. O Enter fica por sua conta.
            Comando multilinha entra sem o \n final pelo mesmo motivo: senao a
            ultima linha dispararia sozinha."""
            if not comando:
                return
            texto = comando.rstrip("\n")
            try:
                # a assinatura de feed_child mudou entre versoes do VTE
                try:
                    self.term.feed_child(texto.encode("utf-8"))
                except TypeError:
                    self.term.feed_child(texto, len(texto))
            except Exception as e:
                self.reg("não consegui colar no terminal: %s" % e)
                return
            self.term.grab_focus()
            self.reg("snippet inserido (revise e aperte Enter)")

        # ---- copiar / colar
        def copiar(self):
            if not self.term.get_has_selection():
                return
            if hasattr(self.term, "copy_clipboard_format"):
                self.term.copy_clipboard_format(Vte.Format.TEXT)
            else:
                self.term.copy_clipboard()

        def colar(self):
            self.term.paste_clipboard()

        def _on_tecla(self, _w, ev):
            # O VTE nao traz atalho de fabrica: Ctrl+Shift+C/V so existem se voce
            # ligar na mao. Nao e limitacao do terminal remoto.
            ctrl = ev.state & Gdk.ModifierType.CONTROL_MASK
            shift = ev.state & Gdk.ModifierType.SHIFT_MASK
            if not (ctrl and shift):
                return False
            nome = Gdk.keyval_name(Gdk.keyval_to_lower(ev.keyval)) or ""
            if nome == "c":
                self.copiar()
                return True
            if nome == "v":
                self.colar()
                return True
            return False

        # ---- ciclo
        def _argv(self):
            alvo = "%s@%s" % (self.cx.ssh_usuario or os.environ.get("USER") or "root",
                              self.cx.host)
            ssh = shutil.which("ssh") or "/usr/bin/ssh"
            # -t FORCA A ALOCACAO DE TTY no lado remoto.
            #
            # Sem TTY, programas de tela cheia (nano, vim, htop) nao recebem as
            # dimensoes nem o SIGWINCH ao redimensionar, e desenham errado — a
            # tela some, fica sem bordas ou embaralhada.
            #
            # Normalmente o ssh aloca sozinho quando ve um terminal na entrada,
            # mas com o sshpass na frente essa deteccao falha. Forcar e o certo
            # aqui: este canal E interativo, sempre.
            base = [ssh, "-t", "-p", str(self.cx.ssh_porta),
                    "-o", "ConnectTimeout=8",
                    "-o", "ServerAliveInterval=30"]
            if self.aceitar_nova_chave:
                # accept-new grava a chave nova sem perguntar, mas continua
                # recusando host cuja chave MUDOU — por isso a remocao vem antes.
                base += ["-o", "StrictHostKeyChecking=accept-new"]
            if self.cx.ssh_senha:
                sshpass = shutil.which("sshpass")
                if not sshpass:
                    self.reg("senha definida mas sshpass ausente — "
                             "o ssh vai pedir no terminal")
                    return base + [alvo]
                base += ["-o", "PreferredAuthentications=password,keyboard-interactive"]
                return [sshpass, "-p", self.cx.ssh_senha] + base + [alvo]
            return base + [alvo]

        def conectar(self):
            # sem GdkWindow o VTE nao consegue dimensionar o PTY e reclama a cada
            # consulta de geometria
            if not self.term.get_realized():
                try:
                    self.term.realize()
                except Exception:
                    pass
            argv = self._argv()
            self._estado("AGUARDE", "neutro", "abrindo %s…" % self.cx.destino_ssh)
            self.reg("exec: %s" % " ".join(
                "***" if (self.cx.ssh_senha and a == self.cx.ssh_senha) else a
                for a in argv))
            # TERM PRECISA SER DEFINIDO POR NOS.
            #
            # Antes o ambiente era copiado inteiro do processo, TERM incluso. Ao
            # abrir o Acessos pelo menu de aplicativos nao ha terminal nenhum, e
            # o TERM vem ausente ou errado — programas de tela cheia como nano,
            # htop e vim entao desenham com a capacidade errada e a tela sai
            # embaralhada, sem bordas, ou simplesmente em branco.
            #
            # O VTE emula xterm-256color; e isso que o lado remoto tem de ver.
            # COLUMNS e LINES continuam de fora de proposito: quem informa o
            # tamanho e o proprio pty, e valores fixos ali brigariam com o
            # redimensionamento da janela.
            env = ["%s=%s" % (k, v) for k, v in os.environ.items()
                   if k not in ("COLUMNS", "LINES", "TERM")]
            env.append("TERM=xterm-256color")
            lar = os.path.expanduser("~")
            try:
                self.term.spawn_async(
                    Vte.PtyFlags.DEFAULT, lar, argv, env,
                    GLib.SpawnFlags.DEFAULT, None, None, -1, None,
                    self._on_spawn, None)
            except TypeError:
                try:
                    ok, pid = self.term.spawn_sync(
                        Vte.PtyFlags.DEFAULT, lar, argv, env,
                        GLib.SpawnFlags.DEFAULT, None, None, None)
                    self._on_spawn(self.term, pid if ok else -1, None, None)
                except Exception as e:
                    self._estado("ERRO", "erro", "falha ao iniciar ssh")
                    self.reg("spawn_sync falhou: %s" % e)

        def _on_spawn(self, _t, pid, erro, _d):
            if self.fechando:
                return
            if erro or pid == -1:
                self._estado("ERRO", "erro", "falha ao iniciar ssh")
                self.reg("spawn: %s" % (erro or "pid -1"))
                return
            self.pid = pid
            self._estado("ATIVO", "ok", self.cx.destino_ssh)
            self.reg("ssh iniciado (pid %s)" % pid)
            # a flag vale para uma tentativa apenas
            self.aceitar_nova_chave = False
            self.sucesso()

        def _on_saiu(self, _t, status):
            self.pid = None
            if self.fechando:
                return
            codigo = status >> 8 if status > 255 else status
            if status == 0:
                self._estado("ENCERRADO", "neutro", "sessão finalizada")
                return          # saida limpa nao religa: foi o operador que saiu
            self._estado("ENCERRADO", "erro", "saiu com código %s" % codigo)
            self.reg("ssh terminou, status bruto %s (código %s)" % (status, codigo))
            if codigo == SSH_CHAVE_MUDOU or self._texto_acusa_chave():
                if not self.perguntando_chave:
                    self.perguntando_chave = True
                    GLib.idle_add(self._oferecer_chave)
                return          # chave errada nao se resolve insistindo
            self._agendar_auto("código %s" % codigo)

        def _texto_acusa_chave(self):
            try:
                if hasattr(self.term, "get_text_format"):
                    txt = self.term.get_text_format(Vte.Format.TEXT)
                else:
                    txt = self.term.get_text(None, None)[0]
            except Exception:
                return False
            if not txt:
                return False
            alto = txt.upper()
            return ("REMOTE HOST IDENTIFICATION HAS CHANGED" in alto
                    or "HOST KEY VERIFICATION FAILED" in alto)

        def _oferecer_chave(self):
            """Sem botao avulso: a pergunta so aparece quando o ssh de fato
            reclamou da identificacao do host."""
            try:
                if not self.janela.confirmar(
                        "A identificação de %s mudou" % self.cx.host,
                        "O ssh recusou a conexão porque a chave do host não bate "
                        "com a registrada em known_hosts.\n\n"
                        "Isso é esperado se a máquina foi reinstalada. Se não foi, "
                        "pode indicar que você está falando com outro equipamento.\n\n"
                        "Remover a entrada antiga e tentar de novo?",
                        ok="Remover e reconectar"):
                    self.reg("operador manteve a chave antiga")
                    return False
                if not self._remover_chave():
                    self.janela.avisar(
                        "Não foi possível remover",
                        "O ssh-keygen não conseguiu limpar a entrada. Verifique "
                        "as permissões de ~/.ssh/known_hosts.")
                    return False
                # sem isto o ssh seguinte pararia no prompt de aceitar a nova chave
                # e voltaria a sair com 6, repetindo a oferta em laco.
                self.aceitar_nova_chave = True
                self.tentativa = 0
                self.reg("chave removida; reconectando com accept-new")
                self.reconectar()
            finally:
                self.perguntando_chave = False
            return False

        def _remover_chave(self):
            alvos = [self.cx.host]
            if str(self.cx.ssh_porta) != "22":
                alvos.append("[%s]:%s" % (self.cx.host, self.cx.ssh_porta))
            kh = shutil.which("ssh-keygen")
            if not kh:
                self.reg("ssh-keygen nao encontrado")
                return False
            algum = False
            for alvo in alvos:
                try:
                    # spawn_sync bloqueia o laco principal. E rapido, mas se o
                    # known_hosts estiver num NFS lento a interface congela.
                    ok, saida, err, _st = GLib.spawn_sync(
                        None, [kh, "-R", alvo], None,
                        GLib.SpawnFlags.SEARCH_PATH, None)
                    msg = (err or saida or b"").decode(errors="replace").strip()
                    self.reg("ssh-keygen -R %s → %s" % (alvo, msg or "ok"))
                    algum = algum or bool(ok)
                except Exception as e:
                    self.reg("ssh-keygen -R %s falhou: %s" % (alvo, e))
            return algum

        def desconectar(self):
            super().desconectar()
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
            try:
                self.term.reset(True, True)
            except Exception:
                pass
            self._agendar(300, lambda: (self.conectar(), False)[1])

        def _on_botao(self, _w, ev):
            # CLIQUE NO LINK abre no navegador. Ctrl+clique, como nos
            # terminais em geral, para nao disparar sem querer ao selecionar.
            if ev.button == 1 and (ev.state & Gdk.ModifierType.CONTROL_MASK):
                url = self._url_em(ev)
                if url:
                    try:
                        Gtk.show_uri_on_window(self.get_toplevel(), url,
                                               Gdk.CURRENT_TIME)
                    except Exception as e:
                        self.reg("não consegui abrir %s: %s" % (url, e))
                    return True
            if ev.button != 3:
                return False
            menu = Gtk.Menu()

            def item(texto, fn, sensivel=True):
                mi = Gtk.MenuItem(label=texto)
                mi.set_sensitive(sensivel)
                mi.connect("activate", lambda _m: fn())
                menu.append(mi)

            # No terminal Ctrl+C e SIGINT e Ctrl+V e literal-next: por isso aqui
            # o padrao e Ctrl+Shift. Nas abas de tela nao ha esse conflito.
            item("Copiar   Ctrl+Shift+C", self.copiar, self.term.get_has_selection())
            item("Colar   Ctrl+Shift+V", self.colar)
            menu.append(Gtk.SeparatorMenuItem())

            # os mesmos snippets do botao da barra, tambem aqui: e onde muita
            # gente procura primeiro num terminal
            sub = Gtk.Menu()
            try:
                disponiveis = [sn for sn in carregar_snippets()
                               if sn.serve_para(self.cx)]
            except Exception:
                disponiveis = []
            if disponiveis:
                for sn in disponiveis:
                    mi = Gtk.MenuItem(label=sn.descricao)
                    mi.set_tooltip_text(sn.comando[:300])
                    mi.connect("activate", lambda _m, c=sn.comando: self._colar(c))
                    sub.append(mi)
            else:
                mi = Gtk.MenuItem(label="nenhum para %s"
                                  % ("Windows" if self.cx.windows else "Linux"))
                mi.set_sensitive(False)
                sub.append(mi)
            mi_snip = Gtk.MenuItem(label="Snippets")
            mi_snip.set_submenu(sub)
            menu.append(mi_snip)
            menu.append(Gtk.SeparatorMenuItem())

            item("Reconectar", lambda: self.reconectar(manual=True))
            if self.cx.tem_vnc:
                item("Abrir tela (VNC)", lambda: self.janela.abrir(self.cx, "vnc"))
            menu.show_all()
            menu.popup_at_pointer(ev)
            return True
    return AbaSsh
