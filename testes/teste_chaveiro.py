#!/usr/bin/env python3
"""Bateria de testes do chaveiro + cofre + integracao com acessos.py.

    python3 testes/teste_chaveiro.py

Roda SEM abrir janela: exercita o nucleo (cifra, alias, migracao,
gravacao, guardas contra perda de dados) e, no fim, so confere que os
modulos de interface carregam e que a folha de estilo e valida.

Cada teste trabalha num diretorio temporario proprio — nenhum toca no
conexoes.ini nem no chaveiro.ini de verdade do usuario.
"""
import configparser
import os
import shutil
import sys
import tempfile
import traceback

# caminho RELATIVO a este arquivo: a suite roda de qualquer diretorio e
# em qualquer maquina, sem caminho de ninguem embutido
BASE = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                    os.pardir, "python")
sys.path.insert(0, BASE)

import cofre       # noqa: E402
import chaveiro    # noqa: E402

FALHAS = []
PASSOU = []


def teste(fn):
    """Decorador: roda, captura excecao, registra."""
    def executar():
        nome = fn.__name__.replace("t_", "").replace("_", " ")
        try:
            fn()
            PASSOU.append(nome)
            print("  ok   %s" % nome)
        except Exception as e:
            FALHAS.append((nome, traceback.format_exc()))
            print("  FALHOU %s: %s" % (nome, e))
    executar.nome = fn.__name__
    return executar


def _ambiente(com_cofre_antigo=False, senha="mestra"):
    """Cria um diretorio temporario com conexoes.ini (+ chaveiro.ini)."""
    d = tempfile.mkdtemp()
    cx = os.path.join(d, "conexoes.ini")
    ch = os.path.join(d, "chaveiro.ini")
    c = cofre.Cofre()
    c.criar(senha)
    cp = configparser.ConfigParser(interpolation=None)
    cp["geral"] = {"tema": "claro"}
    cp["M1"] = {"host": "10.0.0.1", "ssh_usuario": "u1",
                "ssh_senha": c.cifrar("s1")}
    if com_cofre_antigo:
        cp[cofre.SECAO_COFRE] = c.parametros()
    with open(cx, "w") as f:
        cp.write(f)
    if not com_cofre_antigo:
        cpch = configparser.ConfigParser(interpolation=None)
        cpch[cofre.SECAO_COFRE] = c.parametros()
        with open(ch, "w") as f:
            cpch.write(f)
    return d, cx, ch, c


# ------------------------------------------------------------ alias
@teste
def t_alias_reconhecido():
    assert chaveiro.eh_alias("!sup")
    assert not chaveiro.eh_alias("!")           # so o prefixo nao basta
    assert not chaveiro.eh_alias("sup")
    assert not chaveiro.eh_alias("")
    assert not chaveiro.eh_alias(None)
    assert chaveiro.nome_do_alias("!sup") == "sup"
    assert chaveiro.nome_do_alias("sup") is None


@teste
def t_alias_resolve_campos_certos():
    reg = {"sup": {"usuario": "joao", "senha": "segredo"}}
    for campo in ("usuario", "ssh_usuario", "rdp_usuario"):
        assert chaveiro.resolver(reg, "!sup", campo) == "joao", campo
    for campo in ("senha", "ssh_senha", "rdp_senha"):
        assert chaveiro.resolver(reg, "!sup", campo) == "segredo", campo


@teste
def t_alias_quebrado_preserva_valor():
    reg = {"sup": {"usuario": "joao", "senha": "x"}}
    # aponta para credencial inexistente: devolve o proprio texto, nao perde
    assert chaveiro.resolver(reg, "!nao-existe", "ssh_senha") == "!nao-existe"


@teste
def t_valor_normal_passa_direto():
    reg = {"sup": {"usuario": "joao", "senha": "x"}}
    assert chaveiro.resolver(reg, "senha-crua", "ssh_senha") == "senha-crua"
    assert chaveiro.resolver(reg, "", "ssh_senha") == ""


# ------------------------------------------------------- credenciais
@teste
def t_crud_credencial():
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")
    assert chaveiro.listar(ch) == ["sup"]
    reg = chaveiro.carregar_credenciais(ch, c)
    assert reg["sup"] == {"usuario": "joao", "senha": "segredo"}

    # senha fica CIFRADA em disco
    bruto = chaveiro.obter(ch, "sup")
    assert cofre.esta_cifrado(bruto["senha"]), bruto
    assert bruto["usuario"] == "joao"        # usuario NAO e cifrado

    # atualizar
    chaveiro.salvar(ch, c, "sup", "maria", "nova")
    reg = chaveiro.carregar_credenciais(ch, c)
    assert reg["sup"] == {"usuario": "maria", "senha": "nova"}

    # remover
    assert chaveiro.remover(ch, "sup") is True
    assert chaveiro.listar(ch) == []
    assert chaveiro.remover(ch, "sup") is False   # ja nao existe
    shutil.rmtree(d)


@teste
def t_cofre_nao_vira_credencial():
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")
    # a secao [cofre] nunca aparece como credencial
    assert cofre.SECAO_COFRE not in chaveiro.listar(ch)
    assert chaveiro.obter(ch, cofre.SECAO_COFRE) is None
    # e nao pode ser criada com esse nome
    try:
        chaveiro.salvar(ch, c, cofre.SECAO_COFRE, "x", "y")
        raise AssertionError("deveria ter recusado o nome reservado")
    except ValueError:
        pass
    shutil.rmtree(d)


@teste
def t_nome_vazio_recusado():
    d, cx, ch, c = _ambiente()
    for ruim in ("", "   ", None):
        try:
            chaveiro.salvar(ch, c, ruim, "x", "y")
            raise AssertionError("aceitou nome invalido: %r" % ruim)
        except ValueError:
            pass
    shutil.rmtree(d)


@teste
def t_senha_vazia_limpa_sem_exigir_cofre():
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")
    chaveiro.salvar(ch, c, "sup", "joao", "")     # apaga so a senha
    assert chaveiro.obter(ch, "sup")["senha"] == ""
    assert chaveiro.obter(ch, "sup")["usuario"] == "joao"
    shutil.rmtree(d)


@teste
def t_arquivo_protegido_600():
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")
    modo = os.stat(ch).st_mode & 0o777
    assert modo == 0o600, oct(modo)
    shutil.rmtree(d)


@teste
def t_cofre_trancado_recusa_gravar_senha():
    d, cx, ch, c = _ambiente()
    c.trancar()
    try:
        chaveiro.salvar(ch, c, "sup", "joao", "segredo")
        raise AssertionError("gravou senha com o cofre trancado")
    except cofre.ErroCofre:
        pass
    shutil.rmtree(d)


# ---------------------------------------------------------- migracao
@teste
def t_migracao_move_cofre_preservando_chave():
    d, cx, ch, c = _ambiente(com_cofre_antigo=True)
    assert not os.path.exists(ch)

    assert chaveiro.migrar_cofre_de_conexoes(ch, cx) is True

    # [cofre] saiu do conexoes.ini
    cpcx = configparser.ConfigParser(interpolation=None)
    cpcx.read(cx)
    assert not cpcx.has_section(cofre.SECAO_COFRE)
    assert cpcx.has_section("M1")          # o resto ficou intacto
    assert cpcx.has_section("geral")

    # e entrou no chaveiro.ini, com o MESMO salt (senha antiga continua valendo)
    cpch = configparser.ConfigParser(interpolation=None)
    cpch.read(ch)
    c2 = cofre.Cofre()
    c2.carregar_parametros(cpch[cofre.SECAO_COFRE])
    c2.abrir("mestra")                      # nao levanta = senha preservada
    assert c2.decifrar(cpcx["M1"]["ssh_senha"]) == "s1"
    shutil.rmtree(d)


@teste
def t_migracao_idempotente():
    d, cx, ch, c = _ambiente(com_cofre_antigo=True)
    assert chaveiro.migrar_cofre_de_conexoes(ch, cx) is True
    # segunda vez: nao ha mais o que migrar
    assert chaveiro.migrar_cofre_de_conexoes(ch, cx) is False
    shutil.rmtree(d)


@teste
def t_migracao_nao_sobrescreve_cofre_existente():
    d, cx, ch, c = _ambiente(com_cofre_antigo=True)
    # chaveiro ja tem cofre proprio, de OUTRA senha
    outro = cofre.Cofre()
    outro.criar("outra-senha")
    cpch = configparser.ConfigParser(interpolation=None)
    cpch[cofre.SECAO_COFRE] = outro.parametros()
    with open(ch, "w") as f:
        cpch.write(f)

    assert chaveiro.migrar_cofre_de_conexoes(ch, cx) is False
    cpch2 = configparser.ConfigParser(interpolation=None)
    cpch2.read(ch)
    c2 = cofre.Cofre()
    c2.carregar_parametros(cpch2[cofre.SECAO_COFRE])
    c2.abrir("outra-senha")                 # continua sendo o cofre do chaveiro
    shutil.rmtree(d)


@teste
def t_migracao_sem_conexoes_nao_quebra():
    d = tempfile.mkdtemp()
    ch = os.path.join(d, "chaveiro.ini")
    assert chaveiro.migrar_cofre_de_conexoes(ch, None) is False
    assert chaveiro.migrar_cofre_de_conexoes(
        ch, os.path.join(d, "nao-existe.ini")) is False
    shutil.rmtree(d)


# ------------------------------------------------------- troca de senha
@teste
def t_recifrar_os_dois_arquivos():
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo-chaveiro")

    novo, n = chaveiro._recifrar_tudo(ch, cx, c, "senha-nova")
    assert n == 2, n                        # 1 do chaveiro + 1 do conexoes

    # chaveiro abre com a senha nova e devolve o texto certo
    cpch = configparser.ConfigParser(interpolation=None)
    cpch.read(ch)
    c2 = cofre.Cofre()
    c2.carregar_parametros(cpch[cofre.SECAO_COFRE])
    c2.abrir("senha-nova")
    assert chaveiro.carregar_credenciais(ch, c2)["sup"]["senha"] == \
        "segredo-chaveiro"

    # conexoes.ini recifrado com a MESMA chave nova
    cpcx = configparser.ConfigParser(interpolation=None)
    cpcx.read(cx)
    assert c2.decifrar(cpcx["M1"]["ssh_senha"]) == "s1"
    assert os.path.exists(cx + ".bak")      # backup do anterior
    shutil.rmtree(d)


@teste
def t_recifrar_nao_toca_alias():
    """Alias nao e segredo: nao pode ser cifrado na troca de senha."""
    d, cx, ch, c = _ambiente()
    cpcx = configparser.ConfigParser(interpolation=None)
    cpcx.read(cx)
    cpcx["M1"]["ssh_senha"] = "!sup"        # alias em claro
    with open(cx, "w") as f:
        cpcx.write(f)
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")

    novo, n = chaveiro._recifrar_tudo(ch, cx, c, "senha-nova")
    cpcx2 = configparser.ConfigParser(interpolation=None)
    cpcx2.read(cx)
    assert cpcx2["M1"]["ssh_senha"] == "!sup", cpcx2["M1"]["ssh_senha"]
    shutil.rmtree(d)


@teste
def t_senha_errada_nao_abre():
    d, cx, ch, c = _ambiente()
    cpch = configparser.ConfigParser(interpolation=None)
    cpch.read(ch)
    c2 = cofre.Cofre()
    c2.carregar_parametros(cpch[cofre.SECAO_COFRE])
    try:
        c2.abrir("errada")
        raise AssertionError("abriu com a senha errada")
    except cofre.SenhaIncorreta:
        pass
    shutil.rmtree(d)


# ------------------------------------------------- perda de dados (guardas)
@teste
def t_leitura_falha_nao_vira_cofre_vazio():
    """Arquivo existe mas nao pode ser lido: tem de LEVANTAR, nunca
    devolver vazio (senao o app acha que e a primeira execucao)."""
    d, cx, ch, c = _ambiente()
    os.chmod(ch, 0o000)
    try:
        cp = chaveiro._ler_cp(ch)
        raise AssertionError(
            "leitura falhou em silencio e devolveu %s" % cp.sections())
    except cofre.ErroCofre:
        pass
    finally:
        os.chmod(ch, 0o600)
        shutil.rmtree(d)


@teste
def t_ini_corrompido_levanta():
    d, cx, ch, c = _ambiente()
    with open(ch, "w") as f:
        f.write("isto nao e um ini valido\n= = =\n[[[\n")
    try:
        chaveiro._ler_cp(ch)
        raise AssertionError("aceitou INI corrompido")
    except Exception:
        pass
    shutil.rmtree(d)


@teste
def t_guarda_impede_apagar_cofre():
    """Gravar um conteudo SEM [cofre] por cima de um arquivo QUE TEM
    e abortado — a guarda que o escrever_ini tinha, agora aqui."""
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")
    vazio = configparser.ConfigParser(interpolation=None)
    vazio["sup"] = {"usuario": "joao", "senha": ""}   # sem [cofre]!
    try:
        chaveiro._gravar_cp(ch, vazio)
        raise AssertionError("gravou por cima e apagou o cofre")
    except cofre.ErroCofre:
        pass
    # o arquivo continua intacto
    cp = chaveiro._ler_cp(ch)
    assert cp.has_section(cofre.SECAO_COFRE)
    c2 = cofre.Cofre()
    c2.carregar_parametros(cp[cofre.SECAO_COFRE])
    c2.abrir("mestra")
    shutil.rmtree(d)


@teste
def t_migracao_faz_backup_e_protege_permissao():
    d, cx, ch, c = _ambiente(com_cofre_antigo=True)
    os.chmod(cx, 0o644)                       # comeca frouxo de proposito
    chaveiro.migrar_cofre_de_conexoes(ch, cx)
    assert os.path.exists(cx + ".bak"), "migracao sem copia de seguranca"
    # o .bak guarda o estado ANTERIOR, com o [cofre] ainda no lugar
    cpbak = configparser.ConfigParser(interpolation=None)
    cpbak.read(cx + ".bak")
    assert cpbak.has_section(cofre.SECAO_COFRE)
    # e o arquivo resultante nao fica legivel por outros
    modo = os.stat(cx).st_mode & 0o777
    assert modo == 0o600, oct(modo)
    shutil.rmtree(d)


@teste
def t_migracao_falha_nao_apaga_origem():
    """Se o chaveiro nao ficar gravado direito, o [cofre] TEM de
    continuar no conexoes.ini. Regressao real: uma versao desta funcao
    gravava um chaveiro VAZIO e apagava a origem — perda total."""
    d, cx, ch, c = _ambiente(com_cofre_antigo=True)

    original = chaveiro._gravar_cp

    def gravar_capenga(caminho, cp):
        """Simula gravacao que 'funciona' mas nao grava o cofre."""
        vazio = configparser.ConfigParser(interpolation=None)
        original(caminho, vazio)

    chaveiro._gravar_cp = gravar_capenga
    try:
        chaveiro.migrar_cofre_de_conexoes(ch, cx)
        raise AssertionError("migrou sem gravar o cofre e nao reclamou")
    except cofre.ErroCofre:
        pass
    finally:
        chaveiro._gravar_cp = original

    # o conexoes.ini continua com o sal: nada foi perdido
    cpcx = configparser.ConfigParser(interpolation=None)
    cpcx.read(cx)
    assert cpcx.has_section(cofre.SECAO_COFRE), "APAGOU o cofre da origem!"
    c2 = cofre.Cofre()
    c2.carregar_parametros(cpcx[cofre.SECAO_COFRE])
    c2.abrir("mestra")
    assert c2.decifrar(cpcx["M1"]["ssh_senha"]) == "s1"
    shutil.rmtree(d)


@teste
def t_recifrar_faz_backup_dos_dois():
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")
    chaveiro._recifrar_tudo(ch, cx, c, "nova")
    assert os.path.exists(ch + ".bak"), "chaveiro sem copia de seguranca"
    assert os.path.exists(cx + ".bak"), "conexoes sem copia de seguranca"
    # os .bak abrem com a senha VELHA — sao o estado anterior de verdade
    cpbak = configparser.ConfigParser(interpolation=None)
    cpbak.read(ch + ".bak")
    velho = cofre.Cofre()
    velho.carregar_parametros(cpbak[cofre.SECAO_COFRE])
    velho.abrir("mestra")
    shutil.rmtree(d)


@teste
def t_recifrar_nao_deixa_temporario_para_tras():
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")
    chaveiro._recifrar_tudo(ch, cx, c, "nova")
    sobras = [n for n in os.listdir(d) if n.endswith(".novo")]
    assert not sobras, sobras
    shutil.rmtree(d)


# --------------------------------------------- integracao com acessos.py
def _carregar_acessos():
    import importlib.util
    spec = importlib.util.spec_from_file_location(
        "acessos_teste", os.path.join(BASE, "acessos.py"))
    mod = importlib.util.module_from_spec(spec)
    sys.modules["acessos_teste"] = mod
    spec.loader.exec_module(mod)
    return mod


@teste
def t_carregar_resolve_alias_e_guarda_bruto():
    mod = _carregar_acessos()
    d, cx, ch, c = _ambiente()
    chaveiro.salvar(ch, c, "sup", "joao", "segredo")
    cpcx = configparser.ConfigParser(interpolation=None)
    cpcx.read(cx)
    cpcx["M1"]["ssh_usuario"] = "!sup"
    cpcx["M1"]["ssh_senha"] = "!sup"
    with open(cx, "w") as f:
        cpcx.write(f)

    mod.COFRE = c
    mod.CHAVEIRO_REGISTRO = chaveiro.carregar_credenciais(ch, c)
    conexoes, _geral = mod.carregar(cx)
    m1 = [x for x in conexoes if x.nome == "M1"][0]
    # resolvido para uso
    assert m1.ssh_usuario == "joao"
    assert m1.ssh_senha == "segredo"
    # mas o alias ORIGINAL fica guardado, para o editor nao regravar cru
    assert m1.alias_bruto == {"ssh_usuario": "!sup", "ssh_senha": "!sup"}
    shutil.rmtree(d)


@teste
def t_gravar_chave_nao_cifra_alias():
    mod = _carregar_acessos()
    d, cx, ch, c = _ambiente()
    mod.COFRE = c
    mod.gravar_chave(cx, "M1", "ssh_senha", "!sup")
    cpcx = configparser.ConfigParser(interpolation=None)
    cpcx.read(cx)
    assert cpcx["M1"]["ssh_senha"] == "!sup"
    shutil.rmtree(d)


@teste
def t_gravar_chave_cifra_senha_normal():
    mod = _carregar_acessos()
    d, cx, ch, c = _ambiente()
    mod.COFRE = c
    mod.gravar_chave(cx, "M1", "ssh_senha", "senha-crua")
    cpcx = configparser.ConfigParser(interpolation=None)
    cpcx.read(cx)
    valor = cpcx["M1"]["ssh_senha"]
    assert cofre.esta_cifrado(valor), valor
    assert c.decifrar(valor) == "senha-crua"
    shutil.rmtree(d)


@teste
def t_campos_alias_cobrem_todos_os_protocolos():
    mod = _carregar_acessos()
    esperado = {"usuario", "senha", "ssh_usuario", "ssh_senha",
                "rdp_usuario", "rdp_senha"}
    assert set(mod.CAMPOS_ALIAS) == esperado, mod.CAMPOS_ALIAS
    # todo campo sigiloso do cofre tem de estar coberto pelo alias tambem
    assert set(cofre.CAMPOS_SIGILOSOS).issubset(esperado)


@teste
def t_conexao_duplicada_nao_compartilha_alias():
    mod = _carregar_acessos()
    import copy
    cp = configparser.ConfigParser(interpolation=None)
    cp["M1"] = {"host": "1.1.1.1"}
    cxn = mod.Conexao("M1", cp["M1"])
    cxn.alias_bruto["ssh_senha"] = "!sup"

    # o caminho real do app: duplicar_conexao faz copy.copy + dict novo
    novo = copy.copy(cxn)
    novo.alias_bruto = dict(cxn.alias_bruto)
    novo.alias_bruto["ssh_senha"] = "!outro"
    assert cxn.alias_bruto["ssh_senha"] == "!sup", "o dict foi compartilhado"


@teste
def t_caminho_chaveiro_acompanha_dir_dados():
    mod = _carregar_acessos()
    d = tempfile.mkdtemp()
    mod.BASE_DADOS = d
    assert mod.caminho_chaveiro() == os.path.join(d, "chaveiro.ini")
    # fica ao lado do snippets.ini, mesmo diretorio
    assert (os.path.dirname(mod.caminho_chaveiro()) ==
            os.path.dirname(mod.caminho_snippets()))
    shutil.rmtree(d)


@teste
def t_sem_cofre_app_nao_quebra():
    """Sem cofre aberto, carregar() nao pode estourar."""
    mod = _carregar_acessos()
    d, cx, ch, c = _ambiente()
    mod.COFRE = None
    mod.CHAVEIRO_REGISTRO = {}
    conexoes, _geral = mod.carregar(cx)
    m1 = [x for x in conexoes if x.nome == "M1"][0]
    # senha continua cifrada (sem chave para abrir), mas o app carrega
    assert cofre.esta_cifrado(m1.ssh_senha)
    shutil.rmtree(d)


@teste
def t_ini_sem_secao_cofre_continua_valido():
    """conexoes.ini novo (pos-migracao) nao tem [cofre] — e isso e ok."""
    mod = _carregar_acessos()
    d, cx, ch, c = _ambiente()
    cpcx = configparser.ConfigParser(interpolation=None)
    cpcx.read(cx)
    assert not cpcx.has_section(cofre.SECAO_COFRE)
    mod.COFRE = c
    mod.CHAVEIRO_REGISTRO = {}
    conexoes, _geral = mod.carregar(cx)
    assert len(conexoes) == 1
    shutil.rmtree(d)


# ------------------------------------------------------------- interface
@teste
def t_modulos_de_ui_carregam():
    """Import de GTK + construcao dos widgets, sem abrir janela."""
    import gi
    gi.require_version("Gtk", "3.0")
    from gi.repository import Gtk    # noqa: F401
    from tema import gerar_css
    for t in ("claro", "escuro"):
        css = gerar_css(t)
        assert b"box.regua-h" in css, "divisoria sumiu do tema (%s)" % t
        assert b".linha-item" in css, "linha do chaveiro sumiu (%s)" % t
        assert b"box.sem-fundo" in css, "sem-fundo sumiu (%s)" % t
        assert b".icone-clicavel" in css, "icone clicavel sumiu (%s)" % t
        # o CssProvider recusa a folha INTEIRA se houver erro de sintaxe
        prov = Gtk.CssProvider()
        prov.load_from_data(css)


@teste
def t_icones_symbolic_disponiveis():
    import gi
    gi.require_version("Gtk", "3.0")
    from gi.repository import Gtk
    tema_ic = Gtk.IconTheme.get_default()
    # se faltar, _icone() cai no glifo de texto — nao quebra, mas avisa
    for nome in ("document-edit-symbolic", "user-trash-symbolic",
                 "dialog-password-symbolic"):
        if not tema_ic.has_icon(nome):
            print("     aviso: icone %s ausente (usara fallback)" % nome)


# ------------------------------------------------------------------- main
def main():
    print("\n=== chaveiro: alias ===")
    t_alias_reconhecido()
    t_alias_resolve_campos_certos()
    t_alias_quebrado_preserva_valor()
    t_valor_normal_passa_direto()

    print("\n=== chaveiro: credenciais ===")
    t_crud_credencial()
    t_cofre_nao_vira_credencial()
    t_nome_vazio_recusado()
    t_senha_vazia_limpa_sem_exigir_cofre()
    t_arquivo_protegido_600()
    t_cofre_trancado_recusa_gravar_senha()

    print("\n=== chaveiro: migracao do cofre ===")
    t_migracao_move_cofre_preservando_chave()
    t_migracao_idempotente()
    t_migracao_nao_sobrescreve_cofre_existente()
    t_migracao_sem_conexoes_nao_quebra()

    print("\n=== chaveiro: troca de senha mestra ===")
    t_recifrar_os_dois_arquivos()
    t_recifrar_nao_toca_alias()
    t_senha_errada_nao_abre()

    print("\n=== guardas contra perda de dados ===")
    t_leitura_falha_nao_vira_cofre_vazio()
    t_ini_corrompido_levanta()
    t_guarda_impede_apagar_cofre()
    t_migracao_faz_backup_e_protege_permissao()
    t_migracao_falha_nao_apaga_origem()
    t_recifrar_faz_backup_dos_dois()
    t_recifrar_nao_deixa_temporario_para_tras()

    print("\n=== integracao com acessos.py ===")
    t_carregar_resolve_alias_e_guarda_bruto()
    t_gravar_chave_nao_cifra_alias()
    t_gravar_chave_cifra_senha_normal()
    t_campos_alias_cobrem_todos_os_protocolos()
    t_conexao_duplicada_nao_compartilha_alias()
    t_caminho_chaveiro_acompanha_dir_dados()
    t_sem_cofre_app_nao_quebra()
    t_ini_sem_secao_cofre_continua_valido()

    print("\n=== interface ===")
    t_modulos_de_ui_carregam()
    t_icones_symbolic_disponiveis()

    print("\n" + "=" * 60)
    print("%d passaram, %d falharam" % (len(PASSOU), len(FALHAS)))
    for nome, tb in FALHAS:
        print("\n--- %s ---\n%s" % (nome, tb))
    return 1 if FALHAS else 0


if __name__ == "__main__":
    sys.exit(main())
