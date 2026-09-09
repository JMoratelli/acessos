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
"""Interface da execução em massa.

Par do massa.py, que é o motor. A divisão é por CAMADA, e de propósito:

    massa.py     conecta, executa, devolve resultado. NÃO importa GTK,
                 então roda em script e em teste, sem display.
    massa_ui.py  esta tela: seleção de alvos, progresso, decisões do
                 operador, relatório.

Juntar os dois faria o motor perder a testabilidade — foi ela que permitiu
validar o laço de leitura do canal SSH sem abrir janela nenhuma.

A classe herda de Gtk.Box, mas usa utilitários de interface do acessos.py
(add_class, chip, rotulo…). Por isso nasce dentro de construir(), chamada
pelo acessos.py quando esses utilitários já existem — mesmo arranjo do
ssh.py, e pelo mesmo motivo: evitar import circular.
"""

import configparser
import threading
import time

import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, GLib, Pango  # noqa: E402

try:
    import massa
    TEM_MASSA, ERRO_MASSA = True, ""
except Exception as e:                                # pragma: no cover
    massa, TEM_MASSA, ERRO_MASSA = None, False, str(e)

# preenchidos por construir()
Snippet = None
add_class = None
agora = None
carregar_snippets = None
chip = None
regua = None
revelar = None
rotulo = None
AbaLote = None

_NECESSARIOS = ("Snippet", "add_class", "agora", "carregar_snippets",
                "chip", "regua", "revelar", "rotulo")


def construir(**utilitarios):
    """Cria e devolve a classe AbaLote. Chamada uma vez pelo acessos.py."""
    global AbaLote
    globals().update(utilitarios)
    faltando = [n for n in _NECESSARIOS if globals().get(n) is None]
    if faltando:
        raise RuntimeError("massa_ui.construir: faltou %s"
                           % ", ".join(faltando))
    AbaLote = _montar()
    return AbaLote


def _montar():
    class AbaLote(Gtk.Box):
        """Execucao de comandos em lote, por SSH.

        Fluxo: escolher snippets -> canario sequencial -> decisao -> resto.

        O canario e OBRIGATORIO e nao pode ser pulado: e dele que saem os
        timeouts. Nunca disparar as cegas em 200 maquinas."""

        tipo = "lote"
        CANARIO = 3          # padrao do documento
        PARALELISMO = 1      # sequencial: a garantia de "parar no erro" so e
                             # exata com paralelismo 1

        def __init__(self, janela, conexoes):
            super().__init__(orientation=Gtk.Orientation.VERTICAL)
            self.janela = janela
            self.alvos = list(conexoes)
            self.fechando = False
            self.rodando = False
            # DUAS variaveis distintas, de proposito:
            #   decisao   -> resposta pontual a UMA pergunta (continuar/pular/…),
            #                zerada a cada nova pergunta
            #   cancelado -> sinal GLOBAL de "pare tudo", so liga, nunca desliga
            # Eu tinha colapsado as duas: um canario que falhava marcava
            # decisao="abortar" para parar a fila DAQUELE PDV, e isso derrubava
            # a execucao inteira. E, se ja tivesse passado da checagem, a
            # pergunta seguinte zerava a decisao e a espera travava para sempre
            # em "Executando…".
            self.decisao = None
            self.cancelado = False
            self.sempre_continuar = False
            self.timeouts = {}
            self.linhas = {}
            # log separado por maquina, alem do geral: clicar numa linha da
            # tabela filtra a saida para aquele PDV so
            import threading as _th
            self._lock_decisao = _th.Lock()
            self._avulsos = []
            self.log_pdv = {}
            self.log_geral = []
            self.filtro_pdv = None
            self.senha_root = ""
            self._timers = set()

            # --- barra de estado
            self.barra = Gtk.Box(spacing=6)
            self.barra.set_border_width(4)
            self.chip_estado = chip("PRONTO", "neutro")
            self.lb_estado = rotulo("%d máquina(s) selecionada(s)" % len(self.alvos),
                                    "secundario")
            self.barra.pack_start(self.chip_estado, False, False, 0)
            self.barra.pack_start(self.lb_estado, True, True, 0)

            self.bt_iniciar = add_class(Gtk.Button(label="Iniciar canário"), "acao")
            self.bt_iniciar.connect("clicked", lambda _b: self.iniciar())
            self.barra.pack_end(self.bt_iniciar, False, False, 0)

            # Paralelismo: teto de 5 de proposito. Acima disso o ganho encolhe
            # (o gargalo passa a ser a rede da filial) e o risco cresce: cada
            # sessao extra e mais uma conexao simultanea saindo pelo mesmo link.
            # MenuButton, nao ComboBoxText: o combo do GTK abre o popup no
            # botao PRESSIONADO e fecha ao soltar, obrigando a clicar, segurar
            # e arrastar ate a opcao. Ja tinha trocado o combo de selecao por
            # isso e repeti o erro aqui.
            self.paralelismo = self.PARALELISMO
            self.bt_par = Gtk.MenuButton(label="1 por vez  ▾")
            add_class(self.bt_par, "secundaria")
            self.bt_par.set_tooltip_text(
                "Quantas máquinas ao mesmo tempo na fase 2.\n\n"
                "Com mais de 1, 'parar no primeiro erro' deixa de ser exato: "
                "quando um PDV falha, os outros já estão no ar. Para garantia "
                "real, use 1.\n\n"
                "O canário é sempre sequencial — é dele que saem os timeouts.")
            menu_par = Gtk.Menu()
            for n in range(1, 6):
                rot = "1 por vez (sequencial)" if n == 1 else "%d simultâneas" % n
                mi = Gtk.MenuItem(label=rot)
                mi.connect("activate", lambda _m, v=n: self._trocar_paralelismo(v))
                menu_par.append(mi)
            menu_par.show_all()
            self.bt_par.set_popup(menu_par)
            self.barra.pack_end(self.bt_par, False, False, 0)

            self.bt_cancelar = add_class(Gtk.Button(label="Cancelar"), "perigo")
            self.bt_cancelar.set_no_show_all(True)
            self.bt_cancelar.set_tooltip_text(
                "Interrompe a execução; o PDV em andamento termina o comando atual")
            self.bt_cancelar.connect("clicked", lambda _b: self.cancelar())
            self.barra.pack_end(self.bt_cancelar, False, False, 0)

            bt_destacar = add_class(Gtk.Button(label="⧉"), "secundaria",
                                    "tog-glifo")
            bt_destacar.set_tooltip_text("Abrir esta execução em janela própria")
            bt_destacar.connect("clicked", lambda _b: self.destacar())
            self.barra.pack_end(bt_destacar, False, False, 0)

            self.pack_start(self.barra, False, False, 0)

            # --- contadores agregados
            # Com 31 maquinas a tabela responde "o que aconteceu com a
            # CAIXA107", mas nao responde "quantas ja foram". Isso obrigava a
            # contar linha a linha. Os quatro numeros usam a tipografia do
            # hero de proposito: e a mesma pergunta ("quanto de que"), entao e
            # a mesma forma.
            self.contadores = Gtk.Box(spacing=14)
            self.contadores.set_border_width(3)
            self.ct = {}
            for chave, cap, classe in (("alvos", "ALVOS", ""),
                                       ("ok", "OK", "massa-ok"),
                                       ("erro", "FALHA", "massa-err"),
                                       ("fila", "NA FILA", "massa-fila")):
                col = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
                lb = rotulo("0", "massa-num", *( [classe] if classe else [] ))
                self.ct[chave] = lb
                col.pack_start(lb, False, False, 0)
                col.pack_start(rotulo(cap, "massa-cap"), False, False, 0)
                self.contadores.pack_start(col, False, False, 0)
            self.lb_resumo = rotulo("", "massa-cap", xalign=1.0)
            self.contadores.pack_end(self.lb_resumo, True, True, 0)
            self.pack_start(self.contadores, False, False, 0)

            self.pack_start(regua(janela.cor("borda"), 1), False, False, 0)

            # --- barra de decisao (embutida, NAO modal: a tabela e a saida
            # precisam continuar visiveis enquanto o operador decide)
            self.faixa = add_class(Gtk.Box(spacing=8), "faixa-decisao")
            self.faixa.set_border_width(5)
            self.faixa.set_no_show_all(True)
            self.lb_decisao = rotulo("", "secundario")
            self.lb_decisao.set_line_wrap(True)
            self.faixa.pack_start(self.lb_decisao, True, True, 0)
            # os botoes sao montados na hora, porque as opcoes mudam conforme
            # a pergunta: a decisao do canario e "seguir ou nao", enquanto um
            # erro em PDV oferece continuar/pular/continuar tudo/abortar
            self.faixa_botoes = Gtk.Box(spacing=6)
            self.faixa.pack_end(self.faixa_botoes, False, False, 0)
            self.pack_start(self.faixa, False, False, 0)

            painel = Gtk.Paned(orientation=Gtk.Orientation.VERTICAL)
            self.pack_start(painel, True, True, 0)

            # --- escolha de snippets + tabela
            topo = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
            topo.set_border_width(8)
            cab_cmd = Gtk.Box(spacing=8)
            cab_cmd.pack_start(rotulo("COMANDOS A EXECUTAR", "titulo-secao"),
                               False, False, 0)
            bt_avulso = add_class(Gtk.Button(label="＋"), "secundaria", "tog-glifo")
            bt_avulso.set_tooltip_text(
                "Comando avulso: roda só nesta execução, não vai para a "
                "biblioteca de snippets")
            bt_avulso.connect("clicked", lambda _b: self._linha_avulsa())
            cab_cmd.pack_end(bt_avulso, False, False, 0)
            topo.pack_start(cab_cmd, False, False, 0)

            # linha de digitacao do comando avulso, escondida ate o "+"
            self.caixa_avulso = Gtk.Box(spacing=6)
            self.caixa_avulso.set_no_show_all(True)
            self.ent_avulso = Gtk.Entry()
            self.ent_avulso.set_placeholder_text(
                "comando a executar uma única vez (Enter adiciona)")
            self.ent_avulso.connect("activate", lambda _e: self._add_avulso())
            self.caixa_avulso.pack_start(self.ent_avulso, True, True, 0)

            self.cb_avulso_win = Gtk.CheckButton(label="Windows")
            add_class(self.cb_avulso_win, "marca-lote")
            self.cb_avulso_win.set_tooltip_text(
                "Marcado: PowerShell, só nas máquinas Windows.\n"
                "Desmarcado: shell, só nas máquinas Linux.")
            self.caixa_avulso.pack_start(self.cb_avulso_win, False, False, 0)

            self.cb_avulso_root = Gtk.CheckButton(label="root")
            add_class(self.cb_avulso_root, "marca-lote")
            self.cb_avulso_root.set_tooltip_text(
                "Elevar com su antes de executar (Linux). A senha é pedida "
                "no início da execução e nunca é gravada.")
            self.caixa_avulso.pack_start(self.cb_avulso_root, False, False, 0)

            b_ok = add_class(Gtk.Button(label="Adicionar"), "acao")
            b_ok.connect("clicked", lambda _b: self._add_avulso())
            self.caixa_avulso.pack_end(b_ok, False, False, 0)
            b_x = add_class(Gtk.Button(label="Cancelar"), "perigo")
            b_x.connect("clicked", lambda _b: self.caixa_avulso.set_visible(False))
            self.caixa_avulso.pack_end(b_x, False, False, 0)
            topo.pack_start(self.caixa_avulso, False, False, 0)
            self.store_sn = Gtk.ListStore(bool, str, str, str)  # on, desc, plat, chave
            tv_sn = Gtk.TreeView(model=self.store_sn)
            tv_sn.set_headers_visible(True)
            add_class(tv_sn, "lista")
            rend = Gtk.CellRendererToggle()
            rend.connect("toggled", self._alternar_snippet)
            tv_sn.append_column(Gtk.TreeViewColumn("", rend, active=0))
            tv_sn.append_column(Gtk.TreeViewColumn(
                "Comando", Gtk.CellRendererText(), text=1))
            tv_sn.append_column(Gtk.TreeViewColumn(
                "Plataforma", Gtk.CellRendererText(), text=2))
            rol_sn = Gtk.ScrolledWindow()
            rol_sn.set_size_request(-1, 130)
            rol_sn.add(tv_sn)
            add_class(rol_sn, "cartao")
            topo.pack_start(rol_sn, False, False, 0)

            topo.pack_start(rotulo("MÁQUINAS", "titulo-secao"), False, False, 4)
            # IP, filial, status, tempo, detalhe
            self.store_pdv = Gtk.ListStore(str, str, str, str, str, str)
            self.tabela = Gtk.TreeView(model=self.store_pdv)
            add_class(self.tabela, "lista")
            for i, titulo in enumerate(("Máquina", "Host", "Status", "Tempo",
                                        "Detalhe")):
                rend = Gtk.CellRendererText()
                if titulo == "Detalhe":
                    rend.set_property("ellipsize", Pango.EllipsizeMode.END)
                col = Gtk.TreeViewColumn(titulo, rend, text=i)
                col.add_attribute(rend, "foreground", 5)   # cor por situacao
                col.set_resizable(True)
                self.tabela.append_column(col)
            self.tabela.set_tooltip_text(
                "Clique numa máquina para ver só o log dela")
            self.tabela.get_selection().connect("changed", self._clicou_pdv)
            self.rol_tab = Gtk.ScrolledWindow()
            self.rol_tab.add(self.tabela)
            add_class(self.rol_tab, "cartao")
            topo.pack_start(self.rol_tab, True, True, 0)
            painel.pack1(topo, True, False)

            # --- saida
            baixo = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=4)
            baixo.set_border_width(8)
            cab_saida = Gtk.Box(spacing=8)
            cab_saida.pack_start(rotulo("SAÍDA", "titulo-secao"), False, False, 0)
            self.lb_filtro = rotulo("", "card-meta", xalign=1.0)
            cab_saida.pack_end(self.lb_filtro, True, True, 0)
            baixo.pack_start(cab_saida, False, False, 0)
            self.buf_saida = Gtk.TextBuffer()
            tv_out = Gtk.TextView(buffer=self.buf_saida)
            tv_out.set_editable(False)
            tv_out.set_monospace(True)
            add_class(tv_out, "log")
            rol_out = Gtk.ScrolledWindow()
            rol_out.add(tv_out)
            baixo.pack_start(rol_out, True, True, 0)
            painel.pack2(baixo, True, False)
            painel.set_position(420)

            self._montar_listas()

        # ---- preparo
        def _montar_listas(self):
            """Snippets que servem a PELO MENOS uma das maquinas escolhidas."""
            vistos = set()
            for sn in carregar_snippets():
                if not any(sn.serve_para(c) for c in self.alvos):
                    continue
                if sn.chave in vistos:
                    continue
                vistos.add(sn.chave)
                self.store_sn.append([False, sn.descricao, sn.plataforma,
                                      sn.chave])
            self._snippets = {s.chave: s for s in carregar_snippets()}
            GLib.idle_add(self._recontar)

            cor = self.janela.cor("sec")
            for cx in self.alvos:
                plat = "Windows" if cx.windows else "Linux"
                self.linhas[cx.nome] = self.store_pdv.append(
                    [cx.nome, cx.host, "aguardando", "", plat, cor])

        def _linha_avulsa(self):
            """Abre a linha de digitacao e ja poe o cursor nela.

            show() em cada filho, um por um: a caixa tem no_show_all(True) —
            que e o que a mantem escondida no show_all() geral da aba — e isso
            faz o show_all() DELA nao propagar. Sem isto a linha abria vazia,
            sem campo nem botoes, e o "+" parecia nao ter funcao. Mesmo defeito
            que a barra de decisao teve."""
            revelar(self.caixa_avulso)
            self.ent_avulso.grab_focus()

        def _add_avulso(self):
            """Comando que vive so nesta execucao.

            Nao vai para o snippets.ini de proposito: e para o caso de
            'preciso rodar isto uma vez', que nao merece virar item de
            biblioteca. Some quando a aba fecha."""
            texto = self.ent_avulso.get_text().strip()
            if not texto:
                return
            plat = "windows" if self.cb_avulso_win.get_active() else "linux"

            # interpolation=None pelo mesmo motivo de carregar_snippets(): este
            # SectionProxy vira o armazenamento do Snippet, e o comando pode ter %
            cp = configparser.ConfigParser(interpolation=None)
            chave = "avulso_%d" % (len(self._avulsos) + 1)
            cp.add_section(chave)
            sn = Snippet(chave, cp[chave])
            sn.descricao = "⚡ %s" % (texto if len(texto) <= 60
                                     else texto[:57] + "…")
            sn.plataforma = plat
            sn.root = self.cb_avulso_root.get_active()
            sn.comando = texto

            self._avulsos.append(sn)
            self._snippets[chave] = sn
            # ja entra MARCADO: quem digitou quer executar
            self.store_sn.append([True, sn.descricao, plat + " · avulso", chave])

            self.ent_avulso.set_text("")
            self.caixa_avulso.set_visible(False)
            self.reg("comando avulso adicionado (%s): %s" % (plat, texto))

        def _alternar_snippet(self, _r, caminho):
            self.store_sn[caminho][0] = not self.store_sn[caminho][0]

        def escolhidos(self):
            return [self._snippets[l[3]] for l in self.store_sn if l[0]]

        def reg(self, txt, cx=None):
            GLib.idle_add(self._reg_ui, txt, cx.nome if cx else None)

        def _reg_ui(self, txt, nome=None):
            linha = "%s  %s" % (agora(), txt)
            if nome:
                self.log_pdv.setdefault(nome, []).append(linha)
            else:
                self.log_geral.append(linha)
            # so escreve na tela o que pertence ao filtro atual
            if self.filtro_pdv is None or self.filtro_pdv == nome:
                self.buf_saida.insert(self.buf_saida.get_end_iter(), linha + "\n")
            return False

        def _clicou_pdv(self, sel):
            """Clicar numa maquina filtra a saida para ela."""
            modelo, it = sel.get_selected()
            if not it:
                return
            nome = modelo[it][0]
            self.filtro_pdv = None if self.filtro_pdv == nome else nome
            self._repintar_saida()

        def _repintar_saida(self):
            self.buf_saida.set_text("")
            if self.filtro_pdv is None:
                todas = list(self.log_geral)
                for linhas in self.log_pdv.values():
                    todas.extend(linhas)
                todas.sort()
                self.lb_filtro.set_text("")
            else:
                todas = self.log_pdv.get(self.filtro_pdv, [])
                self.lb_filtro.set_text(
                    "filtrado: %s  (clique de novo para ver tudo)"
                    % self.filtro_pdv)
            self.buf_saida.set_text("\n".join(todas) + ("\n" if todas else ""))

        def _estado(self, chip_txt, tipo, txt):
            GLib.idle_add(self._estado_ui, chip_txt, tipo, txt)

        def _estado_ui(self, chip_txt, tipo, txt):
            self.chip_estado.set_text(chip_txt)
            ctx = self.chip_estado.get_style_context()
            for c in ("chip-ok", "chip-erro", "chip-neutro", "chip-atencao"):
                ctx.remove_class(c)
            ctx.add_class("chip-" + tipo)
            self.lb_estado.set_text(txt)
            return False

        def _linha(self, cx, status=None, tempo=None, detalhe=None, cor=None):
            GLib.idle_add(self._linha_ui, cx.nome, status, tempo, detalhe, cor)

        def _linha_ui(self, nome, status, tempo, detalhe, cor):
            it = self.linhas.get(nome)
            if it is None:
                return False
            if status is not None:
                self.store_pdv[it][2] = status
            if tempo is not None:
                self.store_pdv[it][3] = tempo
            if detalhe is not None:
                self.store_pdv[it][4] = detalhe
            if cor is not None:
                self.store_pdv[it][5] = cor
            self._recontar()
            return False

        def _recontar(self):
            """Deriva os contadores da propria tabela.

            De proposito NAO mantem contador incrementado a parte: qualquer
            caminho que escreva na tabela (canario, fase 2, pular, abortar)
            passa por aqui, entao nao existe estado paralelo para
            dessincronizar."""
            if not hasattr(self, "ct"):
                return
            c_ok = self.janela.cor("ok_fg")
            c_err = self.janela.cor("erro_fg")
            n = {"alvos": len(self.store_pdv), "ok": 0, "erro": 0, "fila": 0}
            for lin in self.store_pdv:
                cor, status = lin[5], (lin[2] or "")
                if cor == c_err:
                    n["erro"] += 1
                elif cor == c_ok:
                    n["ok"] += 1
                elif status == "aguardando":
                    n["fila"] += 1
            for k, lb in self.ct.items():
                lb.set_text(str(n[k]))
            feitos = n["ok"] + n["erro"]
            self.lb_resumo.set_text(
                "%d/%d concluídas" % (feitos, n["alvos"]) if feitos else "")

        def destacar(self):
            """Move esta aba para uma janela propria."""
            if self.get_parent() is None:
                return
            jan = Gtk.Window(title="Acessos — execução em lote")
            jan.set_default_size(980, 700)
            pai = self.get_parent()
            pai.remove(self)
            jan.add(self)
            jan.connect("destroy", lambda *_a: self.janela.fechar("::lote"))
            jan.show_all()
            self.janela.abas.pop("::lote", None)

        def conectar(self):
            pass

        def desconectar(self):
            self.fechando = True

        # ---- execucao
        def iniciar(self):
            if self.rodando:
                return
            if not TEM_MASSA:
                self.janela.avisar("Motor ausente", ERRO_MASSA)
                return
            if not massa.TEM_PARAMIKO:
                self.janela.avisar("Falta o paramiko",
                                   "sudo dnf install python3-paramiko  (Fedora)\n"
                                   "sudo pacman -S python-paramiko     (Arch)\n\n"
                                   + massa.ERRO_PARAMIKO)
                return
            cmds = self.escolhidos()
            if not cmds:
                self.janela.avisar("Nenhum comando",
                                   "Marque ao menos um snippet para executar.")
                return
            if any(c.root for c in cmds) and not self.senha_root:
                senha = self.janela.pedir_senha("Elevação root",
                                                "usada só nesta execução")
                if senha is None:
                    return
                # NUNCA gravada: vive so na memoria desta execucao
                self.senha_root = senha

            # zerar o estado da execucao ANTERIOR: sem isto, depois de abortar
            # uma vez, toda nova execucao ja comecava com cancelado=True e
            # terminava na primeira checagem, sem sair de "Executando…"
            self.cancelado = False
            self.decisao = None
            self.sempre_continuar = False
            self.timeouts = {}

            self.rodando = True
            self.bt_iniciar.set_sensitive(False)
            self.bt_iniciar.set_label("Executando…")
            self.bt_cancelar.set_visible(True)
            self.faixa.set_visible(False)
            import threading
            threading.Thread(target=self._rodar, args=(cmds,), daemon=True).start()

        def _rodar(self, cmds):
            # O finally garante que a interface NUNCA fique presa em
            # "Executando…": antes o _finalizar() estava espalhado por cinco
            # pontos de saida, e bastava um caminho novo esquecer a chamada
            # para o botao ficar travado para sempre.
            try:
                self._rodar_interno(cmds)
            except Exception as e:
                self.reg("ERRO INTERNO: %s" % e)
                self._estado("ERRO", "erro", str(e)[:60])
            finally:
                self._finalizar()

        def _rodar_interno(self, cmds):
            if True:
                canarios = self.alvos[:self.CANARIO]
                resto = self.alvos[self.CANARIO:]

                self._estado("CANÁRIO", "atencao",
                             "fase 1: %d máquina(s), sequencial" % len(canarios))
                self.reg("=== FASE 1 - canario em %d PDV(s), sequencial ==="
                         % len(canarios))
                medidas = {}
                for n, cx in enumerate(canarios, 1):
                    # so o cancelamento GLOBAL interrompe. Canario que falha
                    # entra no relatorio e a fase continua: a fase 1 termina
                    # quando os tres terminam, com sucesso ou nao.
                    if self.fechando or self.cancelado:
                        self._estado("CANCELADO", "erro", "interrompido")
                        return
                    self._estado("CANÁRIO", "atencao",
                                 "fase 1: %d de %d — %s" % (n, len(canarios),
                                                            cx.nome))
                    self._executar_em(cx, cmds, medidas, calibrando=True)

                # calibracao pelo PIOR tempo, nao a media
                for i, cmd in enumerate(cmds):
                    pior = max(medidas.get(i, [None]) or [None],
                               key=lambda x: (x is None, x))
                    self.timeouts[i] = massa.calcular_timeout(pior)
                sem_medida = [i for i in range(len(cmds)) if not medidas.get(i)]

                resumo = ["", "=== timeouts calculados ==="]
                for i, cmd in enumerate(cmds):
                    marca = " (SEM MEDIDA — timeout generoso)" if i in sem_medida \
                            else ""
                    resumo.append("  comando %d: %ds%s"
                                  % (i + 1, self.timeouts[i], marca))
                self.reg("\n".join(resumo))

                if not resto:
                    self._estado("CONCLUÍDO", "ok",
                                 "fase de canário concluída (%d máquina(s))"
                                 % len(canarios))
                    return

                self._estado("DECISÃO", "atencao",
                             "confira o canário antes de seguir")
                self._perguntar("Canário terminado — confira os tempos e os "
                                "timeouts acima. Seguir para as %d máquina(s) "
                                "restantes?" % len(resto), self.OPC_CANARIO)
                d = self._esperar_decisao()
                if d in ("abortar", None) or self.cancelado:
                    self.cancelado = True
                    self._estado("ABORTADO", "erro", "interrompido pelo operador")
                    return

                self._estado("EXECUTANDO", "atencao",
                             "fase 2: %d máquina(s)" % len(resto))
                self.reg("\n=== FASE 2 - %d PDV(s) ===" % len(resto))
                par = self.paralelismo
                if par <= 1:
                    for n, cx in enumerate(resto, 1):
                        if self.fechando or self.cancelado:
                            break
                        self._estado("EXECUTANDO", "atencao",
                                     "fase 2: %d de %d — %s"
                                     % (n, len(resto), cx.nome))
                        self._executar_em(cx, cmds, {}, calibrando=False)
                else:
                    self._rodar_paralelo(resto, cmds, par)
                self._estado("CONCLUÍDO", "ok", "execução terminada")

        def _rodar_paralelo(self, alvos, cmds, par):
            """Fase 2 com varias sessoes ao mesmo tempo.

            Uma sessao por thread — o paramiko e bloqueante, entao thread e o
            modelo natural. O que exige cuidado aqui e o DIALOGO: com varios
            workers, dois PDVs podem falhar no mesmo instante e empilhar
            perguntas na tela. O _lock_decisao serializa: enquanto um worker
            espera resposta, os outros que falharem ficam na fila."""
            from concurrent.futures import ThreadPoolExecutor
            feitos = [0]
            total = len(alvos)

            def um(cx):
                if self.fechando or self.cancelado:
                    return
                self._executar_em(cx, cmds, {}, calibrando=False)
                feitos[0] += 1
                self._estado("EXECUTANDO", "atencao",
                             "fase 2: %d de %d (%d simultâneos)"
                             % (feitos[0], total, par))

            self.reg("=== fase 2 com %d execucoes simultaneas ===" % par)
            self.reg("    atencao: com paralelismo, 'parar no erro' nao e exato")
            with ThreadPoolExecutor(max_workers=par) as pool:
                list(pool.map(um, alvos))

        def _executar_em(self, cx, cmds, medidas, calibrando):
            cores = {"ok": self.janela.cor("ok_fg"),
                     "erro": self.janela.cor("erro_fg"),
                     "aviso": self.janela.cor("atencao_fg")}
            self._linha(cx, status="conectando", cor=cores["aviso"])
            self.reg("=== Inicio da execucao no IP: %s (%s) ==="
                     % (cx.host, cx.grupo), cx)
            t0 = time.monotonic()
            sessao = None
            try:
                sessao = massa.abrir_sessao(cx)
                if any(c.root for c in cmds) and not cx.windows:
                    sessao.elevar(self.senha_root)
                    self.reg("[abertura] elevado a root", cx)
            except massa.ErroConexao as e:
                # NUNCA abre dialogo: vai para o relatorio e segue
                self._linha(cx, status="sem conexão", detalhe=str(e)[:120],
                            cor=cores["erro"])
                self.reg("=== Fim: %s | status: SEM CONEXAO | %s ==="
                         % (cx.host, e), cx)
                return
            except massa.ErroShell as e:
                self._linha(cx, status="shell inutilizável",
                            detalhe=str(e)[:120], cor=cores["erro"])
                self.reg("=== Fim: %s | status: SHELL | %s ===" % (cx.host, e), cx)
                if sessao:
                    sessao.fechar()
                return

            status_final = "OK"
            for i, sn in enumerate(cmds):
                if self.fechando:
                    break
                if not sn.serve_para(cx):
                    self.reg("--- comando %d pulado (plataforma) ---" % (i + 1), cx)
                    continue
                limite = self.timeouts.get(i, 600 if calibrando else 1800)
                try:
                    saida, codigo, ms = sessao.executar(sn.comando, limite)
                except (massa.ErroTimeout, massa.ErroShell) as e:
                    status_final = "TIMEOUT"
                    self._linha(cx, status="timeout", detalhe=str(e)[:120],
                                cor=cores["erro"])
                    self.reg("--- comando %d: %s ---" % (i + 1, e), cx)
                    if not self._tratar_erro(cx, "timeout no comando %d: %s"
                                             % (i + 1, e)):
                        break
                    continue
                if calibrando:
                    medidas.setdefault(i, []).append(ms / 1000.0)
                self.reg("--- comando %d (%dms) ---\n%s\n%s\n[exit %d]"
                         % (i + 1, ms, sn.comando, saida, codigo), cx)
                if codigo != 0 and not sn.ignorar_exit and \
                        not massa.tolera_exit(sn.comando):
                    status_final = "EXIT %d" % codigo
                    self._linha(cx, status="exit %d" % codigo,
                                detalhe=saida[:120], cor=cores["erro"])
                    if not self._tratar_erro(cx, "comando %d devolveu exit %d"
                                             % (i + 1, codigo)):
                        break

            sessao.fechar()
            dt = "%.1fs" % (time.monotonic() - t0)
            cor = cores["ok"] if status_final == "OK" else cores["erro"]
            self._linha(cx, status=status_final, tempo=dt, cor=cor)
            self.reg("=== Fim da execucao no IP: %s | status: %s ===\n"
                     % (cx.host, status_final), cx)

        def _tratar_erro(self, cx, msg):
            """Devolve True para continuar a fila deste PDV.

            O lock e o que impede varias perguntas de se atropelarem quando
            mais de um worker falha ao mesmo tempo."""
            if self.sempre_continuar or self.cancelado:
                return not self.cancelado
            with self._lock_decisao:
                # relido DENTRO do lock: enquanto este worker esperava a vez,
                # o operador pode ter escolhido "continuar tudo" ou "abortar"
                # respondendo a outro PDV
                if self.sempre_continuar:
                    return True
                if self.cancelado:
                    return False
                self._perguntar("%s — %s" % (cx.nome, msg))
                d = self._esperar_decisao()
                if d == "tudo":
                    self.sempre_continuar = True
                    return True
                if d == "abortar":
                    self.cancelado = True   # o operador pediu parar tudo
                    return False
                return d == "continuar"

        # opcoes por tipo de pergunta: (rotulo, valor, classe)
        OPC_CANARIO = [("Continuar", "continuar", "acao"),
                       ("Cancelar", "abortar", "perigo")]
        OPC_ERRO = [("Continuar este", "continuar", "secundaria"),
                    ("Pular este", "pular", "secundaria"),
                    ("Continuar tudo", "tudo", "secundaria"),
                    ("Abortar", "abortar", "perigo")]

        def _perguntar(self, texto, opcoes=None):
            self.decisao = None
            GLib.idle_add(self._mostrar_faixa, texto, opcoes or self.OPC_ERRO)

        def _mostrar_faixa(self, texto, opcoes):
            for f in self.faixa_botoes.get_children():
                self.faixa_botoes.remove(f)
            for rot, val, classe in opcoes:
                b = add_class(Gtk.Button(label=rot), classe)
                b.connect("clicked", lambda _b, v=val: self._decidir(v))
                self.faixa_botoes.pack_start(b, False, False, 0)
                # show() em CADA botao: a faixa tem no_show_all(True) — que e o
                # que a mantem escondida no show_all() geral da aba — mas isso
                # faz o show_all() dela NAO propagar aos filhos. Os botoes
                # nasciam invisiveis e a barra aparecia vazia, sem nenhuma
                # forma de continuar apos o canario.
                b.show()
            self.lb_decisao.set_text(texto)
            revelar(self.faixa)
            self.faixa_botoes.show()
            return False

        def _decidir(self, valor):
            self.decisao = valor
            self.faixa.set_visible(False)

        def _esperar_decisao(self):
            while self.decisao is None and not self.fechando and not self.cancelado:
                time.sleep(0.1)
            return self.decisao or ("abortar" if self.cancelado else None)

        def _trocar_paralelismo(self, n):
            self.paralelismo = n
            self.bt_par.set_label("1 por vez  ▾" if n == 1
                                  else "%d simultâneas  ▾" % n)

        def cancelar(self):
            """Liga o sinal global e destrava qualquer pergunta pendente."""
            self.cancelado = True
            self.decisao = "abortar"
            self.reg("cancelado pelo operador")

        def _finalizar(self):
            self.rodando = False
            GLib.idle_add(self._reset_botoes)

        def _reset_botoes(self):
            self.bt_iniciar.set_sensitive(True)
            self.bt_iniciar.set_label("Executar de novo")
            self.bt_cancelar.set_visible(False)
            self.faixa.set_visible(False)
            return False
            GLib.idle_add(self._gravar_relatorio)

        def _gravar_relatorio(self):
            """Relatorio por loja, ordenado."""
            por_loja = {}
            for cx in self.alvos:
                it = self.linhas.get(cx.nome)
                if it is None:
                    continue
                status = self.store_pdv[it][2]
                loja = cx.caminho_grupo[0]
                ok, tot = por_loja.get(loja, (0, 0))
                por_loja[loja] = (ok + (1 if status == "OK" else 0), tot + 1)
            linhas = ["", "=" * 38, "RELATORIO DE SUCESSO POR LOJA", "=" * 38]
            tot_ok = tot_all = 0
            for loja in sorted(por_loja):
                ok, tot = por_loja[loja]
                tot_ok += ok
                tot_all += tot
                linhas.append("%s: %d de %d" % (loja, ok, tot))
            linhas += ["-" * 38, "TOTAL: %d de %d" % (tot_ok, tot_all)]
            self._reg_ui("\n".join(linhas))
            return False
    return AbaLote
