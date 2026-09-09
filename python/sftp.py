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
"""Aba de transferencia de arquivos por SFTP.

Isolado de proposito: o acessos.py so instancia AbaSftp e chama
`selecionar(cx)`. Tudo o que diz respeito a transferencia vive aqui.

POR QUE SFTP E NAO TFTP
-----------------------
O SFTP roda DENTRO da conexao SSH que ja existe e ja funciona: nada a
instalar no PDV (o sshd ja traz o subsistema), mesma autenticacao,
trafego cifrado, TCP confiavel, listagem de diretorio e permissoes. O TFTP
exigiria subir um servidor em cada maquina, sem autenticacao nem cifra,
sobre UDP e sem sequer listar diretorio.

DESENHO
-------
Um Gtk.Paned com dois navegadores iguais (local e remoto). No meio, os
botoes de enviar/baixar. Toda operacao de rede roda em thread; a interface
so e tocada por GLib.idle_add.
"""

import os
import stat
import threading
import time

import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk, GLib, Pango  # noqa: E402

try:
    import paramiko
    TEM_PARAMIKO, ERRO_PARAMIKO = True, ""
except Exception as e:                                # pragma: no cover
    paramiko, TEM_PARAMIKO, ERRO_PARAMIKO = None, False, str(e)


# ESTILO: resolvido em tempo de chamada, nao no import.
#
# O acessos.py importa este modulo no topo, antes de definir add_class e
# rotulo. Um "from acessos import ..." aqui em cima pegaria o modulo pela
# metade e cairia no fallback em silencio — os dialogos e botoes ficariam
# sem o tema e ninguem entenderia por que. Ver a mesma nota no cofre.py.
_ESTILO = {}


def _est():
    if _ESTILO:
        return _ESTILO
    import sys as _sys
    # o app roda como script, entao vive em __main__, nao em "acessos"
    mod = _sys.modules.get("__main__")
    if not hasattr(mod, "add_class"):
        mod = _sys.modules.get("acessos")
    if mod is not None and hasattr(mod, "add_class"):
        _ESTILO.update(add_class=mod.add_class, rotulo=mod.rotulo,
                       botao=mod.botao_dialogo, area=mod.marcar_area_acao)
    else:
        def add_class(w, *nomes):
            for n in nomes:
                w.get_style_context().add_class(n)
            return w

        def rot(texto, *classes, **kw):
            lb = Gtk.Label(label=texto, xalign=kw.pop("xalign", 0.0))
            return add_class(lb, *classes)

        def botao(dlg, texto, resposta, *classes):
            bt = dlg.add_button(texto, resposta)
            add_class(bt, *classes)
            return bt

        _ESTILO.update(add_class=add_class, rotulo=rot, botao=botao,
                       area=lambda _d: None)
    return _ESTILO


def add_class(w, *nomes):
    return _est()["add_class"](w, *nomes)


def rotulo(texto, *classes, **kw):
    return _est()["rotulo"](texto, *classes, **kw)


def botao_dialogo(dlg, texto, resposta, *classes):
    return _est()["botao"](dlg, texto, resposta, *classes)


def marcar_area_acao(dlg):
    return _est()["area"](dlg)


def _humano(n):
    if n is None:
        return ""
    for u in ("B", "KB", "MB", "GB"):
        if n < 1024 or u == "GB":
            return "%d %s" % (n, u) if u == "B" else "%.1f %s" % (n, u)
        n /= 1024.0
    return ""


def _data(ts):
    # localtime(None) devolve a hora ATUAL, entao um mtime ausente viraria
    # uma data plausivel e errada. Melhor vazio.
    if ts is None:
        return ""
    try:
        return time.strftime("%d/%m/%y %H:%M", time.localtime(ts))
    except Exception:
        return ""


class Painel(Gtk.Box):
    """Um lado do navegador. Nao sabe se e local ou remoto — quem sabe e o
    provedor de listagem passado no construtor."""

    def __init__(self, titulo, ao_navegar, ao_ativar):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=4)
        self._ao_navegar = ao_navegar
        self.caminho = "/"
        self._itens = []          # o que veio do disco/servidor, sem filtro

        topo = Gtk.Box(spacing=4)
        topo.pack_start(rotulo(titulo, "titulo-secao"), False, False, 0)
        self.lb_conta = rotulo("", "secundario", xalign=1.0)
        topo.pack_end(self.lb_conta, False, False, 0)
        self.pack_start(topo, False, False, 0)

        cam = Gtk.Box(spacing=4)
        bt_up = add_class(Gtk.Button(label="↑"), "secundaria", "tog-glifo")
        bt_up.set_tooltip_text("Pasta acima")
        bt_up.set_can_focus(False)
        bt_up.connect("clicked", lambda _b: self.subir())
        cam.pack_start(bt_up, False, False, 0)

        self.ent = Gtk.Entry()
        self.ent.set_tooltip_text("Caminho — Enter para ir")
        self.ent.connect("activate",
                         lambda _e: self._ao_navegar(self.ent.get_text()))
        cam.pack_start(self.ent, True, True, 0)

        bt_rec = add_class(Gtk.Button(label="⟳"), "secundaria", "tog-glifo")
        bt_rec.set_tooltip_text("Atualizar")
        bt_rec.set_can_focus(False)
        bt_rec.connect("clicked", lambda _b: self._ao_navegar(self.caminho))
        cam.pack_start(bt_rec, False, False, 0)
        self.pack_start(cam, False, False, 0)

        # BUSCA. Escondida por padrao; aparece ao digitar sobre a lista ou
        # com Ctrl+F, e Esc limpa e esconde — o gesto do Nautilus, sem tirar
        # o campo de caminho do caminho (digitar ali continua funcionando,
        # porque o atalho so vale com a LISTA em foco).
        self.busca = Gtk.SearchEntry()
        self.busca.set_placeholder_text("filtrar nesta pasta…")
        self.busca.set_no_show_all(True)
        self.busca.connect("search-changed", lambda _e: self._aplicar_filtro())
        self.busca.connect("stop-search", lambda _e: self.esconder_busca())
        self.pack_start(self.busca, False, False, 0)

        # nome, tamanho, data, e_pasta, nome_cru
        self.store = Gtk.ListStore(str, str, str, bool, object)
        self.tree = Gtk.TreeView(model=self.store)
        self.tree.set_headers_visible(True)
        # a busca embutida do TreeView brigaria com a nossa
        self.tree.set_enable_search(False)
        self.tree.get_selection().set_mode(Gtk.SelectionMode.MULTIPLE)
        self.tree.connect("row-activated",
                          lambda _t, p, _c: ao_ativar(self, p))
        self.tree.connect("key-press-event", self._tecla_na_lista)
        add_class(self.tree, "lista")

        rend = Gtk.CellRendererText()
        rend.set_property("ellipsize", Pango.EllipsizeMode.MIDDLE)
        col = Gtk.TreeViewColumn("Nome", rend, text=0)
        col.set_expand(True)
        self.tree.append_column(col)
        for i, (t, larg) in enumerate((("Tamanho", 90), ("Modificado", 130)),
                                      start=1):
            r = Gtk.CellRendererText()
            if i == 1:
                r.set_property("xalign", 1.0)
            c = Gtk.TreeViewColumn(t, r, text=i)
            c.set_min_width(larg)
            self.tree.append_column(c)

        rol = Gtk.ScrolledWindow()
        rol.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
        rol.add(self.tree)
        add_class(rol, "cartao")
        self.pack_start(rol, True, True, 0)

    # ---- busca
    def _tecla_na_lista(self, _w, ev):
        """Digitar sobre a lista abre a busca, como no Nautilus."""
        nome = Gdk.keyval_name(ev.keyval) or ""
        if nome == "Escape":
            self.esconder_busca()
            return True
        ctrl = ev.state & Gdk.ModifierType.CONTROL_MASK
        if ctrl and nome in ("f", "F"):
            self.mostrar_busca()
            return True
        if ctrl or (ev.state & Gdk.ModifierType.MOD1_MASK):
            return False
        # so caractere imprimivel abre a busca: setas, Enter, Delete e
        # afins continuam navegando a lista
        ch = Gdk.keyval_to_unicode(ev.keyval)
        if ch and chr(ch).isprintable() and not chr(ch).isspace():
            self.mostrar_busca()
            self.busca.set_text(chr(ch))
            self.busca.set_position(-1)
            return True
        return False

    def mostrar_busca(self):
        self.busca.set_visible(True)
        self.busca.grab_focus()

    def esconder_busca(self):
        tinha = bool(self.busca.get_text())
        self.busca.set_text("")
        self.busca.set_visible(False)
        self.tree.grab_focus()
        if tinha:
            self._aplicar_filtro()

    def _aplicar_filtro(self):
        self._preencher()

    # ---- estado
    def mostrar(self, caminho, itens):
        """itens: lista de (nome, tamanho, mtime, e_pasta)."""
        # trocou de pasta: o filtro antigo nao faz sentido aqui
        if caminho != self.caminho:
            self.busca.set_text("")
            self.busca.set_visible(False)
        self.caminho = caminho
        self.ent.set_text(caminho)
        self._itens = list(itens)
        self._preencher()

    def _preencher(self):
        """Repovoa a lista aplicando o filtro atual.

        O filtro age SO no que ja esta listado — a pasta corrente. Busca
        recursiva em SFTP significaria percorrer a arvore inteira pela rede,
        cara e lenta; nao e o que se espera ao digitar numa lista."""
        termo = self.busca.get_text().strip().lower()
        itens = self._itens
        if termo:
            itens = [i for i in itens if termo in i[0].lower()]
        self.store.clear()
        pastas = sorted([i for i in itens if i[3]],
                        key=lambda i: i[0].lower())
        arquivos = sorted([i for i in itens if not i[3]],
                          key=lambda i: i[0].lower())
        for nome, tam, mt, ehpasta in pastas + arquivos:
            self.store.append([
                ("📁  " if ehpasta else "        ") + nome,
                "" if ehpasta else _humano(tam),
                _data(mt), ehpasta, nome])
        # contador: com filtro ativo mostra "achados de total"
        total = len(self._itens)
        if termo:
            self.lb_conta.set_text("%d de %d" % (len(itens), total))
        else:
            self.lb_conta.set_text("%d item%s" % (total,
                                                  "" if total == 1 else "s"))

    def subir(self):
        pai = os.path.dirname(self.caminho.rstrip("/")) or "/"
        self._ao_navegar(pai)

    def selecionados(self):
        """Devolve [(nome, e_pasta)] das linhas marcadas."""
        modelo, linhas = self.tree.get_selection().get_selected_rows()
        return [(modelo[l][4], modelo[l][3]) for l in linhas]

    def nome_da_linha(self, path):
        return self.store[path][4], self.store[path][3]


class AbaSftp(Gtk.Box):
    """Aba unica de transferencia. Troca de maquina pelo seletor."""

    tipo = "sftp"

    def __init__(self, janela):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        self.janela = janela
        self.cx = None
        self.cli = None          # paramiko.SSHClient
        self.sftp = None
        self.ocupado = False
        self.set_border_width(8)

        # ---- barra superior
        barra = Gtk.Box(spacing=6)
        self.cb = Gtk.ComboBoxText()
        self.cb.set_tooltip_text("Máquina")
        self.cb.connect("changed", self._trocou_maquina)
        barra.pack_start(self.cb, False, False, 0)

        self.bt_conectar = add_class(Gtk.Button(label="Conectar"), "acao")
        self.bt_conectar.connect("clicked", lambda _b: self._conectar())
        barra.pack_start(self.bt_conectar, False, False, 0)

        self.lb_estado = rotulo("desconectado", "secundario")
        barra.pack_start(self.lb_estado, True, True, 6)
        self.pack_start(barra, False, False, 0)

        # ---- paineis
        self.local = Painel("LOCAL", self._ir_local, self._ativou)
        self.remoto = Painel("REMOTO", self._ir_remoto, self._ativou)

        meio = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        meio.set_valign(Gtk.Align.CENTER)
        self.bt_env = add_class(Gtk.Button(label="→"), "acao", "tog-glifo")
        self.bt_env.set_tooltip_text("Enviar para a máquina remota")
        self.bt_env.connect("clicked", lambda _b: self._enviar())
        self.bt_bai = add_class(Gtk.Button(label="←"), "acao", "tog-glifo")
        self.bt_bai.set_tooltip_text("Baixar para esta máquina")
        self.bt_bai.connect("clicked", lambda _b: self._baixar())
        self.bt_del = add_class(Gtk.Button(label="🗑"), "perigo", "tog-glifo")
        self.bt_del.set_tooltip_text("Excluir no remoto")
        self.bt_del.connect("clicked", lambda _b: self._excluir())
        self.bt_pasta = add_class(Gtk.Button(label="+📁"), "secundaria", "tog-glifo")
        self.bt_pasta.set_tooltip_text("Nova pasta no remoto")
        self.bt_pasta.connect("clicked", lambda _b: self._nova_pasta())
        for b in (self.bt_env, self.bt_bai, self.bt_del, self.bt_pasta):
            b.set_can_focus(False)
            meio.pack_start(b, False, False, 0)

        painel = Gtk.Paned(orientation=Gtk.Orientation.HORIZONTAL)
        esq = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        esq.pack_start(self.local, True, True, 0)
        dir_ = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        dir_.pack_start(self.remoto, True, True, 0)
        painel.pack1(esq, True, False)
        painel.pack2(dir_, True, False)

        corpo = Gtk.Box(spacing=6)
        corpo.pack_start(painel, True, True, 0)
        corpo.pack_start(meio, False, False, 0)
        self.pack_start(corpo, True, True, 0)

        # ---- rodape
        self.barra_prog = Gtk.ProgressBar()
        self.barra_prog.set_show_text(True)
        self.barra_prog.set_no_show_all(True)
        self.pack_start(self.barra_prog, False, False, 0)

        self._ir_local(os.path.expanduser("~"))
        self._habilitar(False)

    # ------------------------------------------------ conexoes disponiveis
    def recarregar_conexoes(self, conexoes):
        """conexoes: lista de objetos com .nome e credenciais ssh."""
        self._conexoes = list(conexoes)
        atual = self.cb.get_active_text()
        self.cb.remove_all()
        for cx in self._conexoes:
            self.cb.append_text(cx.nome)
        if atual:
            self.selecionar_por_nome(atual)

    def selecionar_por_nome(self, nome):
        for i, cx in enumerate(getattr(self, "_conexoes", [])):
            if cx.nome == nome:
                self.cb.set_active(i)
                return True
        return False

    def selecionar(self, cx):
        """Chamado pelo acessos.py quando o operador clica na pasta da aba
        SSH: troca para aquela maquina e ja conecta."""
        if not self.selecionar_por_nome(cx.nome):
            return
        if self.cx is not None and self.cx.nome == cx.nome and self.sftp:
            return                     # ja esta nela e conectada
        self._conectar()

    def _trocou_maquina(self, _cb):
        i = self.cb.get_active()
        if i < 0:
            return
        nova = self._conexoes[i]
        if self.cx is not None and nova.nome != self.cx.nome:
            self._desconectar()
        self.cx = nova

    # ------------------------------------------------------- conexao SFTP
    def _diz(self, txt):
        self.lb_estado.set_text(txt)

    def _habilitar(self, ligado):
        for b in (self.bt_env, self.bt_bai, self.bt_del, self.bt_pasta):
            b.set_sensitive(ligado)

    def _conectar(self):
        if not TEM_PARAMIKO:
            self._erro("paramiko ausente: %s" % ERRO_PARAMIKO)
            return
        i = self.cb.get_active()
        if i < 0:
            self._diz("escolha uma máquina")
            return
        self.cx = self._conexoes[i]
        self._desconectar()
        self._diz("conectando em %s…" % self.cx.nome)
        self.bt_conectar.set_sensitive(False)

        cx = self.cx

        def trabalho():
            try:
                cli = paramiko.SSHClient()
                cli.set_missing_host_key_policy(paramiko.AutoAddPolicy())
                cli.connect(
                    cx.host,
                    port=int(getattr(cx, "ssh_porta", 22) or 22),
                    username=(getattr(cx, "ssh_usuario", None)
                              or os.environ.get("USER") or "root"),
                    password=getattr(cx, "ssh_senha", None) or None,
                    timeout=8, allow_agent=True, look_for_keys=True)
                sftp = cli.open_sftp()
                inicial = sftp.normalize(".")
            except Exception as e:
                GLib.idle_add(self._falhou, str(e))
                return
            GLib.idle_add(self._conectou, cli, sftp, inicial)

        threading.Thread(target=trabalho, daemon=True).start()

    def _conectou(self, cli, sftp, inicial):
        self.cli, self.sftp = cli, sftp
        self.bt_conectar.set_sensitive(True)
        alvo = getattr(self.cx, "destino", None) or self.cx.nome
        self._diz("conectado — %s" % alvo)
        self._habilitar(True)
        self._ir_remoto(inicial)
        return False

    def _falhou(self, msg):
        self.bt_conectar.set_sensitive(True)
        self._erro(msg)
        return False

    def _erro(self, msg):
        self._diz("erro: %s" % msg)
        self._habilitar(False)

    def _desconectar(self):
        for obj in (self.sftp, self.cli):
            try:
                if obj is not None:
                    obj.close()
            except Exception:
                pass
        self.sftp = self.cli = None
        self.remoto.store.clear()
        self._habilitar(False)
        self._diz("desconectado")

    # --------------------------------------------------------- navegacao
    def _ir_local(self, caminho):
        try:
            caminho = os.path.abspath(os.path.expanduser(caminho))
            itens = []
            for nome in os.listdir(caminho):
                p = os.path.join(caminho, nome)
                try:
                    st = os.stat(p)
                    itens.append((nome, st.st_size, st.st_mtime,
                                  stat.S_ISDIR(st.st_mode)))
                except OSError:
                    continue
            self.local.mostrar(caminho, itens)
        except Exception as e:
            self._diz("local: %s" % e)

    def _ir_remoto(self, caminho):
        if self.sftp is None:
            return
        sftp = self.sftp

        def trabalho():
            try:
                alvo = sftp.normalize(caminho)
                itens = [(a.filename, a.st_size, a.st_mtime,
                          stat.S_ISDIR(a.st_mode))
                         for a in sftp.listdir_attr(alvo)]
            except Exception as e:
                # ARMADILHA DO PYTHON: `except ... as e` APAGA `e` ao sair
                # do bloco. Como este lambda so roda depois, la no idle, ele
                # encontrava o nome ja destruido e levantava NameError.
                # Por isso a mensagem e convertida para texto agora e
                # amarrada por valor no argumento padrao.
                msg = str(e)
                GLib.idle_add(
                    lambda m=msg: (self._diz("remoto: %s" % m), False)[1])
                return
            GLib.idle_add(lambda: (self.remoto.mostrar(alvo, itens), False)[1])

        threading.Thread(target=trabalho, daemon=True).start()

    def _ativou(self, painel, path):
        nome, ehpasta = painel.nome_da_linha(path)
        if not ehpasta:
            return
        novo = os.path.join(painel.caminho, nome)
        if painel is self.local:
            self._ir_local(novo)
        else:
            self._ir_remoto(novo)

    # ------------------------------------------------------ transferencia
    def _progresso(self, feito, total, rotulo):
        def aplicar():
            self.barra_prog.set_visible(True)
            fr = (feito / total) if total else 0.0
            self.barra_prog.set_fraction(min(1.0, fr))
            self.barra_prog.set_text("%s — %s / %s"
                                     % (rotulo, _humano(feito),
                                        _humano(total)))
            return False
        GLib.idle_add(aplicar)

    def _fim(self, msg, atualizar_remoto=True):
        self.ocupado = False
        self.barra_prog.set_visible(False)
        self._diz(msg)
        self._habilitar(True)
        if atualizar_remoto:
            self._ir_remoto(self.remoto.caminho)
        self._ir_local(self.local.caminho)
        return False

    def _iniciar(self, alvo, *args):
        if self.ocupado:
            self._diz("aguarde a transferência em andamento")
            return
        if self.sftp is None:
            self._diz("não conectado")
            return
        self.ocupado = True
        self._habilitar(False)
        threading.Thread(target=alvo, args=args, daemon=True).start()

    def _enviar(self):
        itens = [n for n, ehp in self.local.selecionados() if not ehp]
        if not itens:
            self._diz("selecione arquivos no painel local")
            return
        origem, destino, sftp = self.local.caminho, self.remoto.caminho, \
            self.sftp
        # conflitos sao verificados ANTES de comecar: perguntar no meio da
        # fila deixaria arquivos ja copiados e outros nao
        itens = self._resolver(
            itens, self._conflitos_remoto(itens, origem, destino), destino)
        if not itens:
            return

        def trabalho():
            enviados = 0
            for nome in itens:
                lp = os.path.join(origem, nome)
                rp = destino.rstrip("/") + "/" + nome
                try:
                    sftp.put(lp, rp,
                             callback=lambda f, t, n=nome:
                                 self._progresso(f, t, "enviando " + n))
                    enviados += 1
                except Exception as e:
                    GLib.idle_add(self._fim, "falha em %s: %s" % (nome, e))
                    return
            GLib.idle_add(self._fim, "%d arquivo(s) enviado(s)" % enviados)

        self._iniciar(trabalho)

    def _baixar(self):
        itens = [n for n, ehp in self.remoto.selecionados() if not ehp]
        if not itens:
            self._diz("selecione arquivos no painel remoto")
            return
        origem, destino, sftp = self.remoto.caminho, self.local.caminho, \
            self.sftp
        itens = self._resolver(
            itens, self._conflitos_local(itens, origem, destino), destino)
        if not itens:
            return

        def trabalho():
            baixados = 0
            for nome in itens:
                rp = origem.rstrip("/") + "/" + nome
                lp = os.path.join(destino, nome)
                try:
                    sftp.get(rp, lp,
                             callback=lambda f, t, n=nome:
                                 self._progresso(f, t, "baixando " + n))
                    baixados += 1
                except Exception as e:
                    GLib.idle_add(self._fim, "falha em %s: %s" % (nome, e))
                    return
            GLib.idle_add(self._fim, "%d arquivo(s) baixado(s)" % baixados)

        self._iniciar(trabalho)

    # ---------------------------------------------------- sobrescrita
    def _decidir_conflitos(self, existentes, destino):
        """Pergunta UMA vez o que fazer com os arquivos que ja existem.

        Estilo "merge": o que nao conflita vai sempre; para o que conflita,
        o operador escolhe. Perguntar arquivo por arquivo seria insuportavel
        num envio de dezenas, e sobrescrever calado e pior ainda — o
        paramiko faz isso por padrao, sem avisar."""
        self._liberar_grab()
        dlg = Gtk.Dialog(title="Arquivos já existem",
                         transient_for=self.get_toplevel(), modal=True)
        add_class(dlg, "acessos-dialogo")
        marcar_area_acao(dlg)
        botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
        botao_dialogo(dlg, "Pular existentes", Gtk.ResponseType.NO,
                      "secundaria")
        botao_dialogo(dlg, "Sobrescrever", Gtk.ResponseType.YES, "acao")
        dlg.set_default_response(Gtk.ResponseType.NO)

        cx = dlg.get_content_area()
        cx.set_border_width(10)
        cx.set_spacing(6)
        cab = Gtk.Label()
        cab.set_xalign(0)
        cab.set_markup(
            "<b>%d de %d arquivo(s) já existem em %s</b>"
            % (len(existentes), self._total_conflito, GLib.markup_escape_text(
                destino)))
        cx.pack_start(cab, False, False, 0)

        store = Gtk.ListStore(str, str, str)
        for nome, tam_o, tam_d in existentes:
            store.append([nome, _humano(tam_o), _humano(tam_d)])
        tv = Gtk.TreeView(model=store)
        for i, t in enumerate(("Arquivo", "Origem", "Destino")):
            r = Gtk.CellRendererText()
            if i:
                r.set_property("xalign", 1.0)
            tv.append_column(Gtk.TreeViewColumn(t, r, text=i))
        rol = Gtk.ScrolledWindow()
        rol.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
        rol.set_min_content_height(160)
        rol.add(tv)
        cx.pack_start(rol, True, True, 0)

        rod = Gtk.Label()
        rod.set_xalign(0)
        rod.set_markup(
            "<small>Os demais arquivos são transferidos de qualquer "
            "forma.</small>")
        cx.pack_start(rod, False, False, 0)

        dlg.show_all()
        resp = dlg.run()
        dlg.destroy()
        if resp == Gtk.ResponseType.YES:
            return "sobrescrever"
        if resp == Gtk.ResponseType.NO:
            return "pular"
        return None

    def _conflitos_remoto(self, nomes, origem, destino):
        """(existentes, ok) para envio local -> remoto."""
        existentes = []
        for nome in nomes:
            try:
                a = self.sftp.stat(destino.rstrip("/") + "/" + nome)
            except Exception:
                continue
            try:
                to = os.path.getsize(os.path.join(origem, nome))
            except OSError:
                to = None
            existentes.append((nome, to, a.st_size))
        return existentes

    def _conflitos_local(self, nomes, origem, destino):
        """(existentes) para download remoto -> local."""
        existentes = []
        mapa = {}
        try:
            for a in self.sftp.listdir_attr(origem):
                mapa[a.filename] = a.st_size
        except Exception:
            pass
        for nome in nomes:
            lp = os.path.join(destino, nome)
            if os.path.exists(lp):
                try:
                    td = os.path.getsize(lp)
                except OSError:
                    td = None
                existentes.append((nome, mapa.get(nome), td))
        return existentes

    def _resolver(self, nomes, existentes, destino):
        """Aplica a decisao do operador. Devolve a lista final ou None."""
        if not existentes:
            return nomes
        self._total_conflito = len(nomes)
        escolha = self._decidir_conflitos(existentes, destino)
        if escolha is None:
            self._diz("transferência cancelada")
            return None
        if escolha == "sobrescrever":
            return nomes
        conflitantes = {n for n, _o, _d in existentes}
        restantes = [n for n in nomes if n not in conflitantes]
        if not restantes:
            self._diz("nada a transferir: todos já existiam")
            return None
        return restantes

    # ------------------------------------------------------- destrutivas
    def _liberar_grab(self):
        """Ver liberar_grab_gtk no acessos.py: com sessao RDP ativa ha um
        gtk_grab_add pendente, e o dialogo nao receberia clique."""
        try:
            for _ in range(8):
                w = Gtk.grab_get_current()
                if w is None:
                    break
                Gtk.grab_remove(w)
        except Exception:
            pass

    def _confirmar(self, titulo, texto, espera=0):
        """Confirmacao com espera obrigatoria no botao de confirmar.

        Com `espera` > 0 o botao nasce desabilitado e conta regressiva. E
        proposital: exclusao recursiva num PDV nao tem desfazer, e o custo
        de tres segundos e irrisorio perto de apagar a pasta errada por
        reflexo — o "clicar em OK sem ler" e um risco real quando o dialogo
        aparece no meio do trabalho.

        O foco inicial vai para Cancelar, entao Enter apressado cancela."""
        self._liberar_grab()
        dlg = Gtk.MessageDialog(
            transient_for=self.get_toplevel(), modal=True,
            message_type=Gtk.MessageType.WARNING,
            buttons=Gtk.ButtonsType.NONE, text=titulo)
        dlg.format_secondary_text(texto)
        add_class(dlg, "acessos-dialogo")
        marcar_area_acao(dlg)
        botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "secundaria")
        bt_ok = botao_dialogo(dlg, "Excluir", Gtk.ResponseType.OK, "perigo")
        dlg.set_default_response(Gtk.ResponseType.CANCEL)

        timer = {"id": None}
        if espera > 0:
            restante = {"n": int(espera)}
            bt_ok.set_sensitive(False)
            bt_ok.set_label("Excluir (%d)" % restante["n"])

            def tique():
                restante["n"] -= 1
                if restante["n"] <= 0:
                    bt_ok.set_label("Excluir")
                    bt_ok.set_sensitive(True)
                    timer["id"] = None
                    return False
                bt_ok.set_label("Excluir (%d)" % restante["n"])
                return True

            timer["id"] = GLib.timeout_add_seconds(1, tique)

        resp = dlg.run()
        if timer["id"] is not None:
            # o operador pode fechar antes do fim da contagem
            try:
                GLib.source_remove(timer["id"])
            except Exception:
                pass
        dlg.destroy()
        return resp == Gtk.ResponseType.OK

    def _levantar(self, base, nome, ehpasta):
        """Conta o que sera apagado. Devolve (arquivos, pastas)."""
        alvo = base.rstrip("/") + "/" + nome
        if not ehpasta:
            return 1, 0
        arq, pas = 0, 1        # a propria pasta ja conta
        pilha = [alvo]
        while pilha:
            atual = pilha.pop()
            try:
                entradas = self.sftp.listdir_attr(atual)
            except Exception:
                continue
            for a in entradas:
                if stat.S_ISDIR(a.st_mode):
                    pas += 1
                    pilha.append(atual.rstrip("/") + "/" + a.filename)
                else:
                    arq += 1
        return arq, pas

    def _apagar_arvore(self, sftp, alvo):
        """Remove pasta e todo o conteudo, de baixo para cima.

        O rmdir do SFTP so aceita pasta vazia, entao a ordem importa:
        esvaziamos primeiro e removemos as pastas na volta."""
        pilha = [alvo]
        ordem = []
        while pilha:
            atual = pilha.pop()
            ordem.append(atual)
            for a in sftp.listdir_attr(atual):
                filho = atual.rstrip("/") + "/" + a.filename
                if stat.S_ISDIR(a.st_mode):
                    pilha.append(filho)
                else:
                    sftp.remove(filho)
        for pasta in reversed(ordem):     # das folhas para a raiz
            sftp.rmdir(pasta)

    def _excluir(self):
        itens = self.remoto.selecionados()
        if not itens:
            self._diz("selecione o que excluir no painel remoto")
            return
        base, sftp = self.remoto.caminho, self.sftp

        # levantamento antes de perguntar: o operador precisa saber o
        # TAMANHO do estrago, nao so quantos itens marcou
        tot_arq = tot_pas = 0
        for nome, ehpasta in itens:
            a, p = self._levantar(base, nome, ehpasta)
            tot_arq += a
            tot_pas += p

        nomes = ", ".join(n for n, _ in itens[:5])
        if len(itens) > 5:
            nomes += " e mais %d" % (len(itens) - 5)
        detalhe = "%s\n\n" % nomes
        if tot_pas:
            detalhe += ("Serão apagados %d arquivo(s) e %d pasta(s), "
                        "incluindo todo o conteúdo.\n" % (tot_arq, tot_pas))
        else:
            detalhe += "Serão apagados %d arquivo(s).\n" % tot_arq
        detalhe += ("\nIsto acontece na MÁQUINA REMOTA (%s) e não tem "
                    "desfazer." % self.cx.nome)

        if not self._confirmar("Excluir em %s?" % base, detalhe, espera=3):
            return

        def trabalho():
            n = 0
            for nome, ehpasta in itens:
                alvo = base.rstrip("/") + "/" + nome
                try:
                    if ehpasta:
                        self._apagar_arvore(sftp, alvo)
                    else:
                        sftp.remove(alvo)
                    n += 1
                except Exception as e:
                    GLib.idle_add(self._fim, "falha em %s: %s" % (nome, e))
                    return
            GLib.idle_add(self._fim, "%d item(ns) excluído(s)" % n)

        self._iniciar(trabalho)

    def _nova_pasta(self):
        if self.sftp is None:
            return
        dlg = Gtk.Dialog(title="Nova pasta", transient_for=self.get_toplevel(),
                         modal=True)
        add_class(dlg, "acessos-dialogo")
        marcar_area_acao(dlg)
        botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
        botao_dialogo(dlg, "Criar", Gtk.ResponseType.OK, "acao")
        ent = Gtk.Entry()
        ent.set_activates_default(True)
        cx = dlg.get_content_area()
        cx.set_border_width(10)
        cx.pack_start(Gtk.Label(label="Nome da pasta em %s:"
                                % self.remoto.caminho), False, False, 4)
        cx.pack_start(ent, False, False, 4)
        dlg.set_default_response(Gtk.ResponseType.OK)
        dlg.show_all()
        resp = dlg.run()
        nome = ent.get_text().strip()
        dlg.destroy()
        if resp != Gtk.ResponseType.OK or not nome:
            return
        try:
            self.sftp.mkdir(self.remoto.caminho.rstrip("/") + "/" + nome)
            self._ir_remoto(self.remoto.caminho)
        except Exception as e:
            self._diz("erro ao criar: %s" % e)

    # --------------------------------------------------------- encerrar
    def desconectar(self):
        """Nome igual ao das outras abas, para o acessos.py fechar igual."""
        self._desconectar()
