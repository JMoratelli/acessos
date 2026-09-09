"""dialogo_ui — formatos de dialogo do Acessos.

Sete dialogos espalhados viraram TRES formatos:

  1. avisar() / confirmar()  — aviso, erro e confirmacao destrutiva
  2. perguntar()             — um campo, duas saidas (senha, grupo, plataforma)
  3. editor()                — casca com headerbar (snippets, conexao, cofre)

O codigo de estilo daqui NAO e novo: foi extraido do cofre.py, onde ja estava
em producao. O cofre redefinia add_class/botao_dialogo por conta propria,
duplicando o que o acessos.py ja tinha; agora os dois usam este modulo.

Convencao: modulos de interface terminam em _ui (massa_ui, dialogo_ui).
"""

import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, GLib  # noqa: E402


# ---------------------------------------------------------------- estilo
# Resolvido em TEMPO DE CHAMADA, nao no import.
#
# O acessos.py importa este modulo no topo, muito antes de definir
# add_class/botao_dialogo/rotulo. Um "from acessos import ..." aqui em cima
# pegaria o modulo pela metade e falharia — e a degradacao seria SILENCIOSA:
# os dialogos apareceriam sem o tema do app e ninguem entenderia por que.
_ESTILO = {}


def _est():
    """Devolve os helpers de estilo do app, ou equivalentes neutros."""
    if _ESTILO:
        return _ESTILO
    try:
        # O app roda como script (./acessos.py), entao ele vive em
        # sys.modules["__main__"], NAO em sys.modules["acessos"]. Um
        # "import acessos" aqui carregaria uma SEGUNDA copia do modulo —
        # outro estado, outros globais — em vez de achar a que esta rodando.
        import sys as _sys
        acessos = _sys.modules.get("__main__")
        if not hasattr(acessos, "botao_dialogo"):
            acessos = _sys.modules.get("acessos")
        if acessos is None or not hasattr(acessos, "botao_dialogo"):
            raise ImportError("acessos ainda nao carregado")
        _ESTILO.update(
            add_class=acessos.add_class,
            botao=acessos.botao_dialogo,
            area=acessos.marcar_area_acao,
            rotulo=acessos.rotulo,
            tem=True)
    except Exception:
        def add_class(w, *nomes):
            for n in nomes:
                w.get_style_context().add_class(n)
            return w

        def botao(dlg, texto, resposta, *classes):
            bt = dlg.add_button(texto, resposta)
            add_class(bt, *classes)
            bt.set_can_focus(False)
            return bt

        def rot(texto, *classes, **kw):
            lb = Gtk.Label(label=texto, xalign=kw.pop("xalign", 0.0))
            return add_class(lb, *classes)

        _ESTILO.update(add_class=add_class, botao=botao,
                       area=lambda _d: None, rotulo=rot, tem=False)
    return _ESTILO


def add_class(w, *nomes):
    return _est()["add_class"](w, *nomes)


def botao_dialogo(dlg, texto, resposta, *classes):
    return _est()["botao"](dlg, texto, resposta, *classes)


def marcar_area_acao(dlg):
    return _est()["area"](dlg)


def rotulo(texto, *classes, **kw):
    return _est()["rotulo"](texto, *classes, **kw)


def liberar_grab():
    """Desfaz gtk_grab_add() pendente — ver liberar_grab_gtk no acessos.py.

    Sem isto, um dialogo aberto enquanto ha sessao RDP ativa aparece mas nao
    aceita clique, porque o grab do GTK entrega todos os eventos a outro
    widget."""
    try:
        for _ in range(8):
            w = Gtk.grab_get_current()
            if w is None:
                break
            Gtk.grab_remove(w)
    except Exception:
        pass


# ---------------------------------------------------------------- casca
def dialogo(titulo, pai, headerbar=True):
    """Casca base. Devolve (dlg, caixa_de_conteudo).

    ATENCAO ao use_header_bar: com ele em True, o Gtk.Dialog move os botoes
    de acao PARA DENTRO da headerbar. Foi o que aconteceu no cofre — Sair,
    Abrir e Trocar senha subiram para a barra de titulo e o dialogo ficou
    deformado. Aqui a titlebar e montada A MAO e o use_header_bar fica em
    False, entao o botao continua embaixo, na area de acao de sempre, e a
    barra de cima leva so o titulo e o X.
    """
    liberar_grab()
    dlg = Gtk.Dialog(title=titulo, transient_for=pai, modal=True)
    add_class(dlg, "acessos-dialogo")
    if headerbar:
        hb = Gtk.HeaderBar()
        # TITULO COMO WIDGET PROPRIO, nao set_title().
        # O set_title() desenha o rotulo dentro do no ".title" do tema, que
        # traz fundo e moldura proprios — era aquela pastilha branca com
        # "Acessos" escrito, que nem era o titulo do dialogo. Com
        # set_custom_title o rotulo e nosso e obedece ao CSS.
        lb = Gtk.Label(label=titulo)
        add_class(lb, "dlg-topo-titulo")
        hb.set_custom_title(lb)
        # BOTAO DE FECHAR PROPRIO, nao o do sistema.
        # O "titlebutton" do Adwaita vem com circulo vermelho, imagem e
        # sombra de icone proprios, e sobrescrever tudo isso por CSS virou
        # briga perdida — o circulo voltava. Um Gtk.Button nosso obedece a
        # folha sem discussao.
        hb.set_show_close_button(False)
        bt_x = Gtk.Button(label="✕")
        add_class(bt_x, "dlg-x")
        bt_x.set_relief(Gtk.ReliefStyle.NONE)
        bt_x.set_tooltip_text("Fechar")
        bt_x.connect("clicked",
                     lambda _b: dlg.response(Gtk.ResponseType.CANCEL))
        hb.pack_end(bt_x)
        # a classe vai na PROPRIA headerbar: depender de
        # ".acessos-dialogo headerbar" nao funcionou — a titlebar nao casa
        # como descendente e a barra ficava com o estilo do sistema
        add_class(hb, "dlg-topo")
        dlg.set_titlebar(hb)
        hb.show_all()
    marcar_area_acao(dlg)
    cx = dlg.get_content_area()
    cx.set_border_width(14)
    cx.set_spacing(6)
    return dlg, cx


def nota(caixa, texto, *classes):
    lb = rotulo(texto, *(classes or ("secundario",)), ellipsize=None)
    lb.set_line_wrap(True)
    lb.set_max_width_chars(52)
    caixa.pack_start(lb, False, False, 0)
    return lb


def campo_senha(caixa, texto_rotulo, dlg=None, resposta=None):
    caixa.pack_start(rotulo(texto_rotulo, "rotulo"), False, False, 0)
    ent = Gtk.Entry()
    ent.set_visibility(False)
    ent.set_input_purpose(Gtk.InputPurpose.PASSWORD)
    if dlg is not None and resposta is not None:
        # Enter ligado direto a resposta, e NAO via activates_default: o
        # padrao do app, ver pedir_senha() no acessos.py
        ent.connect("activate", lambda _e: dlg.response(resposta))
    caixa.pack_start(ent, False, False, 0)
    return ent


def _titulo_e_texto(cx, titulo, texto):
    if titulo:
        cx.pack_start(rotulo(titulo, "dlg-titulo", ellipsize=None),
                      False, False, 0)
    if texto:
        nota(cx, texto, "dlg-texto")


# ------------------------------------------------- formato 1: aviso/confirma
def avisar(pai, texto, titulo=None, erro=False, ok="Fechar", extra=None):
    """Substitui Gtk.MessageDialog.

    O MessageDialog traz o estilo do SISTEMA e ignora boa parte do CSS do
    app — era a causa concreta dos dialogos do cofre parecerem de outro
    programa. Aqui e um Gtk.Dialog normal, que obedece a folha.

    extra: (texto, resposta) para uma acao util alem de fechar, tipo
    "Tentar de novo". Devolve a resposta escolhida.
    """
    dlg, cx = dialogo(titulo or ("Erro" if erro else "Aviso"), pai)
    add_class(dlg, "dlg-erro" if erro else "dlg-aviso")
    _titulo_e_texto(cx, titulo, texto)
    if extra:
        botao_dialogo(dlg, ok, Gtk.ResponseType.CLOSE, "secundaria")
        botao_dialogo(dlg, extra[0], extra[1], "acao")
    else:
        botao_dialogo(dlg, ok, Gtk.ResponseType.CLOSE, "acao")
    dlg.show_all()
    r = dlg.run()
    dlg.destroy()
    return r


def confirmar(pai, titulo, texto, ok="Confirmar", cancelar="Cancelar",
              perigo=False, contagem=0):
    """Confirmacao. Devolve True se o usuario confirmou.

    contagem: segundos em que o botao de confirmar nasce DESABILITADO,
    contando para tras no proprio rotulo. Serve para acao destrutiva —
    encerrar sessoes, excluir conexao, executar em N maquinas. Nao e
    frescura: o custo de um clique reflexo aqui e alto e nao tem desfazer.
    """
    dlg, cx = dialogo(titulo, pai)
    _titulo_e_texto(cx, titulo, texto)
    botao_dialogo(dlg, cancelar, Gtk.ResponseType.CANCEL, "perigo")
    bt = botao_dialogo(dlg, ok, Gtk.ResponseType.OK,
                       "perigo" if perigo else "acao")

    fontes = []
    if contagem > 0:
        restante = [int(contagem)]
        bt.set_sensitive(False)
        bt.set_label("%s (%ds)" % (ok, restante[0]))

        def _tick():
            restante[0] -= 1
            if restante[0] <= 0:
                bt.set_label(ok)
                bt.set_sensitive(True)
                return False
            bt.set_label("%s (%ds)" % (ok, restante[0]))
            return True

        fontes.append(GLib.timeout_add_seconds(1, _tick))

    dlg.show_all()
    r = dlg.run()
    # o timer sobrevive ao destroy e dispara em widget morto se nao remover
    for f in fontes:
        try:
            GLib.source_remove(f)
        except Exception:
            pass
    dlg.destroy()
    return r == Gtk.ResponseType.OK


# ------------------------------------------------- formato 2: entrada unica
def perguntar(pai, titulo, texto_rotulo, valor="", senha=False,
              ok="Confirmar", dica=None, validar=None):
    """Um campo, duas saidas. Devolve o texto, ou None se cancelou.

    validar: funcao que recebe o texto e devolve None (ok) ou uma mensagem
    de erro. O erro aparece INLINE sob o campo — nunca um segundo dialogo
    por cima do primeiro, que era o padrao antigo e empilhava janela.
    """
    dlg, cx = dialogo(titulo, pai)
    if titulo:
        cx.pack_start(rotulo(titulo, "dlg-titulo", ellipsize=None),
                      False, False, 0)
    cx.pack_start(rotulo(texto_rotulo, "rotulo"), False, False, 0)

    ent = Gtk.Entry()
    ent.set_text(valor or "")
    if senha:
        ent.set_visibility(False)
        ent.set_input_purpose(Gtk.InputPurpose.PASSWORD)
    ent.connect("activate", lambda _e: dlg.response(Gtk.ResponseType.OK))
    cx.pack_start(ent, False, False, 0)

    lb_dica = rotulo(dica or "Enter confirma · Esc cancela", "dlg-dica",
                     ellipsize=None)
    lb_dica.set_line_wrap(True)
    cx.pack_start(lb_dica, False, False, 0)

    botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
    botao_dialogo(dlg, ok, Gtk.ResponseType.OK, "acao")
    dlg.show_all()
    ent.grab_focus()

    try:
        while True:
            if dlg.run() != Gtk.ResponseType.OK:
                return None
            txt = ent.get_text().strip()
            erro = validar(txt) if validar else None
            if not erro:
                return txt
            # valida sem fechar: o dialogo continua de pe e o campo mantem
            # o que foi digitado, em vez de sumir e obrigar a redigitar
            lb_dica.set_text(erro)
            add_class(lb_dica, "dlg-dica-erro")
            ent.grab_focus()
    finally:
        dlg.destroy()


# ------------------------------------------------- formato 3: editor
def editor(titulo, pai, ok="Salvar", cancelar="Cancelar",
           largura=640, altura=480):
    """Casca de editor com headerbar. Devolve (dlg, caixa, bt_ok).

    ok=None / cancelar=None omitem os respectivos botoes; bt_ok vem None
    quando omitido.

    Quem chama monta o conteudo em `caixa`, chama dlg.show_all() e trata o
    run(). A headerbar leva cancelar a esquerda e a acao primaria a direita.
    """
    liberar_grab()
    dlg = Gtk.Dialog(title=titulo, transient_for=pai, modal=True,
                     use_header_bar=True)
    add_class(dlg, "acessos-dialogo", "dlg-editor")
    marcar_area_acao(dlg)
    dlg.set_default_size(largura, altura)
    # a headerbar se estiliza AQUI, nao por chamada externa.
    # Antes cada tela precisava lembrar de chamar estilizar_headerbar(); a
    # que esqueceu (Ajustes) nasceu com a barra branca do sistema e o X
    # dentro de uma caixinha. Formato que depende de quem usa lembrar de
    # algo nao e formato.
    estilizar_headerbar(dlg, titulo)

    # Cancelar e vermelho: regra da interface, destrutivo usa a cor de
    # erro sempre — descartar o que foi digitado conta como destrutivo.
    #
    # cancelar=None ou "" omite o botao: em telas onde cada acao ja se
    # aplica sozinha (ajustes, por exemplo) nao ha o que cancelar, e um
    # botao vermelho ali sugeriria que ha.
    if cancelar:
        botao_dialogo(dlg, cancelar, Gtk.ResponseType.CANCEL, "perigo")
    # ok=None omite a acao primaria. Em painel onde cada opcao ja se aplica
    # sozinha nao ha o que confirmar: o X da barra ja fecha, e um botao
    # "Fechar" ao lado dele seria a mesma acao escrita duas vezes.
    bt_ok = (botao_dialogo(dlg, ok, Gtk.ResponseType.OK, "acao")
             if ok else None)

    cx = dlg.get_content_area()
    cx.set_border_width(14)
    cx.set_spacing(8)
    return dlg, cx, bt_ok


def estilizar_headerbar(dlg, titulo):
    """Poe a headerbar de um Gtk.Dialog(use_header_bar=True) no padrao.

    Serve para EditorConexao/EditorSnippets, que criam a propria headerbar
    com os botoes de acao dentro dela (ali isso e correto: e o formato 3).
    Aplica a classe, troca o titulo por widget proprio — o no ".title" do
    tema traz fundo e moldura, que era a pastilha branca em volta de "Nova
    conexao" — e devolve a headerbar.
    """
    hb = None
    for getter in ("get_header_bar", "get_titlebar"):
        try:
            hb = getattr(dlg, getter)()
        except Exception:
            hb = None
        if hb is not None:
            break
    if hb is None:
        return None
    add_class(hb, "dlg-topo")
    lb = Gtk.Label(label=titulo)
    add_class(lb, "dlg-topo-titulo")
    lb.show()
    hb.set_custom_title(lb)

    # X PROPRIO, como no cofre.
    # O "titlebutton" do sistema vem com caixa, imagem e sombra do tema, e
    # nao se deixa domar por CSS — fica visivelmente fora do padrao ao lado
    # dos nossos botoes. Um Gtk.Button obedece a folha sem discussao.
    try:
        hb.set_show_close_button(False)
        bt_x = Gtk.Button(label="✕")
        add_class(bt_x, "dlg-x")
        bt_x.set_relief(Gtk.ReliefStyle.NONE)
        bt_x.set_valign(Gtk.Align.CENTER)
        bt_x.set_tooltip_text("Fechar")
        # CANCEL e nao close(): num editor, fechar pelo X significa sair sem
        # salvar, e quem chama ja trata essa resposta
        bt_x.connect("clicked",
                     lambda _b: dlg.response(Gtk.ResponseType.CANCEL))
        hb.pack_end(bt_x)
        bt_x.show()
    except Exception:
        pass
    return hb


# ------------------------------------------------- blocos de painel
def secao(caixa, titulo, primeira=False):
    """Titulo de secao dentro de um painel, com regua acima (menos na 1a)."""
    if not primeira:
        caixa.pack_start(add_class(Gtk.Box(), "regua-h"), False, False, 8)
    caixa.pack_start(rotulo(titulo, "rotulo"), False, False, 0)


def interruptor(caixa, texto, ativo, ao_mudar, descricao=None):
    """Linha de opcao liga/desliga. Devolve o Gtk.Switch.

    ao_mudar recebe o novo estado (bool). Quem chama decide o que fazer —
    este modulo nao guarda estado de aplicacao.
    """
    linha = Gtk.Box(spacing=10)
    sw = Gtk.Switch()
    sw.set_valign(Gtk.Align.CENTER)
    sw.set_active(bool(ativo))
    sw.connect("notify::active", lambda w, _p: ao_mudar(w.get_active()))
    linha.pack_start(sw, False, False, 0)
    lb = rotulo(texto, "opcao-txt", ellipsize=None)
    lb.set_line_wrap(True)
    linha.pack_start(lb, False, False, 0)
    caixa.pack_start(linha, False, False, 0)
    if descricao:
        nota(caixa, descricao, "dlg-dica")
    return sw


def linha_botoes(caixa, botoes):
    """Fila de botoes de uma opcao. botoes: (texto, classe, callback)."""
    linha = Gtk.Box(spacing=8)
    saida = []
    for texto, classe, fn in botoes:
        b = add_class(Gtk.Button(label=texto), classe)
        b.connect("clicked", lambda _b, f=fn: f())
        linha.pack_start(b, False, False, 0)
        saida.append(b)
    caixa.pack_start(linha, False, False, 4)
    return saida
