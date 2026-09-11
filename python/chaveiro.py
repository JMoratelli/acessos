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
"""Chaveiro de credenciais do Acessos.

Dono do cofre (a chave que cifra senhas) e guarda de credenciais NOMEADAS,
reutilizaveis em varias conexoes por alias:

    chaveiro.ini
    ------------
    [cofre]
    salt        = ...
    kdf         = argon2id
    verificador = ...

    [suporte-loja]
    usuario = suporte
    senha   = enc:v1:gAAAAAB...

Uma secao de conexao no conexoes.ini referencia isso com "!nome" no lugar
do usuario e/ou da senha:

    [CAIXA101]
    ssh_usuario = !suporte-loja
    ssh_senha   = !suporte-loja

POR QUE O COFRE MORA AQUI, E NAO NO conexoes.ini
-------------------------------------------------
O chaveiro.ini existe desde a primeira execucao do app (como snippets.ini
ja existe), entao ele e um lugar estavel para o cofre viver — nao depende
de o operador ter alguma senha gravada numa conexao especifica. Isso
tambem deixa o arquivo AUTOSSUFICIENTE: dá pra copiar so o chaveiro.ini
para outra maquina e levar as credenciais compartilhadas junto, sem
arrastar o conexoes.ini inteiro.

conexoes.ini continua podendo cifrar senha/ssh_senha/rdp_senha
diretamente (sem alias) — usando a MESMA chave, que agora vem daqui.
"""

import configparser
import os
import shutil
import sys

import cofre as _cofre
from cofre import (Cofre, ErroCofre, SenhaIncorreta, TEM_CRIPTO,  # noqa: F401
                   senha_do_ambiente, proteger_arquivo, esta_cifrado)

SECAO_COFRE = _cofre.SECAO_COFRE
ALIAS_PREFIXO = "!"


# ---------------------------------------------------------------- alias

def eh_alias(valor):
    return isinstance(valor, str) and valor.startswith(ALIAS_PREFIXO) and len(valor) > 1


def nome_do_alias(valor):
    return valor[len(ALIAS_PREFIXO):] if eh_alias(valor) else None


def resolver(registro, valor, campo):
    """valor: o que estava no campo (pode ou nao ser "!nome").
    campo: nome do campo original (usuario, senha, ssh_usuario, ...).

    Sem alias, devolve o proprio valor. Com alias apontando para uma
    credencial que nao existe (mais), devolve o valor como esta — quem
    chama decide o que fazer com um alias quebrado, em vez de perder o
    dado silenciosamente."""
    nome = nome_do_alias(valor)
    if nome is None:
        return valor
    item = registro.get(nome)
    if item is None:
        return valor
    sub = "usuario" if campo.endswith("usuario") else "senha"
    return item.get(sub, "")


# ------------------------------------------------------------- arquivo

def _ler_cp(caminho):
    """Le o arquivo. Se ele EXISTE mas nao pode ser lido, LEVANTA.

    ConfigParser.read() engole OSError e devolve um parser VAZIO, sem
    avisar. Isso aqui seria catastrofico: um chaveiro.ini ilegivel por um
    instante — permissao trocada, pasta do Drive/Insync no meio de uma
    sincronizacao, disco removivel fora — passaria por "ainda nao existe
    cofre", o app ofereceria criar um NOVO, e a gravacao seguinte
    trocaria o salt. Sem o salt antigo, a mesma senha mestra deriva outra
    chave e TODA senha guardada (aqui e no conexoes.ini) vira lixo.

    Por isso a leitura e explicita, com open(): o erro sobe em vez de
    virar silencio. Arquivo inexistente continua sendo um caso normal —
    e a primeira execucao de verdade.
    """
    cp = configparser.ConfigParser(interpolation=None)
    if not caminho or not os.path.exists(caminho):
        return cp
    try:
        with open(caminho, encoding="utf-8") as f:
            cp.read_file(f, source=caminho)
    except OSError as e:
        raise ErroCofre("não consegui ler %s: %s" % (caminho, e))
    return cp


def _gravar_cp(caminho, cp):
    """Grava de forma atomica, com uma guarda contra apagar o cofre.

    GUARDA: se o arquivo em disco tem [cofre] e o conteudo novo nao tem,
    a gravacao e ABORTADA. E a mesma protecao que o escrever_ini() do
    acessos.py tinha enquanto o cofre morava no conexoes.ini — ela mudou
    de arquivo junto com o cofre. O salt vive aqui agora; um bug que o
    apagasse custaria todas as senhas guardadas, entao ele nao passa.
    """
    if cp.has_section(SECAO_COFRE) is False and os.path.exists(caminho):
        try:
            with open(caminho, encoding="utf-8") as f:
                atual = configparser.ConfigParser(interpolation=None)
                atual.read_file(f, source=caminho)
        except Exception:
            # nao conseguiu conferir: nega, em vez de arriscar
            raise ErroCofre(
                "gravação abortada: não consegui conferir se %s ainda "
                "tem a seção [%s]" % (caminho, SECAO_COFRE))
        if atual.has_section(SECAO_COFRE):
            raise ErroCofre(
                "gravação abortada: o conteúdo novo não tem a seção [%s] "
                "e o arquivo atual tem. Sem o sal as senhas guardadas "
                "ficariam irrecuperáveis." % SECAO_COFRE)

    pasta = os.path.dirname(os.path.abspath(caminho))
    os.makedirs(pasta, exist_ok=True)
    tmp = os.path.join(pasta, ".chaveiro.ini.tmp")
    with open(tmp, "w", encoding="utf-8") as f:
        cp.write(f)
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, caminho)
    proteger_arquivo(caminho)


# ------------------------------------------------------------ credenciais

def listar(caminho):
    """Nomes das credenciais guardadas, em ordem alfabetica."""
    cp = _ler_cp(caminho)
    return sorted(s for s in cp.sections() if s != SECAO_COFRE)


def obter(caminho, nome):
    """{'usuario':..., 'senha':...(cifrada)} ou None."""
    cp = _ler_cp(caminho)
    if nome == SECAO_COFRE or not cp.has_section(nome):
        return None
    return dict(cp[nome])


def carregar_credenciais(caminho, cofre):
    """{nome: {'usuario':..., 'senha':...}} com a senha JA DECIFRADA.

    Mesma filosofia de acessos.carregar(): o resto do programa (resolver)
    trabalha com texto claro em memoria, sem saber que existe cifra."""
    cp = _ler_cp(caminho)
    reg = {}
    for nome in cp.sections():
        if nome == SECAO_COFRE:
            continue
        sec = cp[nome]
        senha = sec.get("senha", "")
        if senha and cofre is not None and not cofre.trancado():
            try:
                senha = cofre.decifrar(senha)
            except Exception as e:
                sys.stderr.write("chaveiro: %s em [%s]\n" % (e, nome))
                senha = ""
        reg[nome] = {"usuario": sec.get("usuario", ""), "senha": senha}
    return reg


def salvar(caminho, cofre, nome, usuario, senha):
    """Cria ou atualiza uma credencial. senha="" apaga a senha gravada
    (fica so o usuario), sem exigir o cofre destrancado nesse caso."""
    nome = (nome or "").strip()
    if not nome:
        raise ValueError("nome da credencial não pode ser vazio")
    if nome == SECAO_COFRE:
        raise ValueError("'%s' é um nome reservado" % SECAO_COFRE)
    cp = _ler_cp(caminho)
    if not cp.has_section(nome):
        cp.add_section(nome)
    cp[nome]["usuario"] = usuario or ""
    if senha:
        if cofre is None or cofre.trancado():
            raise ErroCofre("chaveiro trancado")
        cp[nome]["senha"] = cofre.cifrar(senha)
    else:
        cp[nome]["senha"] = ""
    _gravar_cp(caminho, cp)


def remover(caminho, nome):
    cp = _ler_cp(caminho)
    if cp.has_section(nome) and nome != SECAO_COFRE:
        cp.remove_section(nome)
        _gravar_cp(caminho, cp)
        return True
    return False


# ------------------------------------------------------------- migracao

def _remover_secao_bruta(caminho, secao):
    """Apaga uma secao por edicao de linha, preservando o resto do
    arquivo a mao — mesmo espirito de apagar_chave/escrever_ini do
    acessos.py, mas usado aqui uma unica vez, na migracao."""
    try:
        with open(caminho, encoding="utf-8") as f:
            linhas = f.readlines()
    except OSError:
        return False
    ini = fim = None
    for i, ln in enumerate(linhas):
        s = ln.strip()
        if s.startswith("[") and s.endswith("]"):
            if s[1:-1].strip() == secao:
                ini = i
            elif ini is not None and fim is None:
                fim = i
    if ini is None:
        return False
    if fim is None:
        fim = len(linhas)
    del linhas[ini:fim]
    tmp = caminho + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.writelines(linhas)
        f.flush()
        os.fsync(f.fileno())
    proteger_arquivo(tmp)         # 600 ANTES de virar o arquivo real
    os.replace(tmp, caminho)
    # o conexoes.ini continua guardando senhas (cifradas, mas guardadas):
    # sem isto ele saia desta edicao com a umask padrao, 644, legivel por
    # qualquer usuario da maquina
    proteger_arquivo(caminho)
    return True


def migrar_cofre_de_conexoes(caminho_chaveiro, caminho_conexoes):
    """Se conexoes.ini ainda tiver [cofre] e o chaveiro nao tiver o seu
    proprio ainda, MOVE a secao tal como esta: mesmo salt, mesmo
    verificador — nenhuma senha e recifrada, so muda de arquivo.

    Devolve True se migrou algo."""
    if not caminho_conexoes or not os.path.exists(caminho_conexoes):
        return False
    cp_cx = configparser.ConfigParser(interpolation=None)
    cp_cx.read(caminho_conexoes, encoding="utf-8")
    if not cp_cx.has_section(SECAO_COFRE):
        return False

    cp_ch = _ler_cp(caminho_chaveiro)
    if cp_ch.has_section(SECAO_COFRE):
        return False          # chaveiro ja tem cofre proprio; nao sobrescreve

    # CÓPIA ANTES DE QUALQUER ESCRITA. Esta edicao nao passa pelo
    # escrever_ini() do app, entao nao ganha a copia em historico/ que
    # toda gravacao normal ganha — e e justamente a operacao que mexe no
    # sal. Se algo der errado no meio, o .bak tem o arquivo com o [cofre]
    # ainda no lugar.
    try:
        shutil.copy2(caminho_conexoes, caminho_conexoes + ".bak")
        proteger_arquivo(caminho_conexoes + ".bak")
    except OSError as e:
        raise ErroCofre("não consegui guardar a cópia de segurança antes "
                        "de migrar o cofre: %s" % e)

    parametros = dict(cp_cx[SECAO_COFRE])
    cp_ch[SECAO_COFRE] = parametros

    # ORDEM IMPORTA: grava o destino PRIMEIRO e so entao apaga a origem.
    # Se _gravar_cp falhar, a excecao sobe aqui e o conexoes.ini fica
    # intacto, com o [cofre] onde sempre esteve — o pior caso e a
    # migracao nao acontecer, nunca o sal sumir dos dois lados.
    _gravar_cp(caminho_chaveiro, cp_ch)

    # CONFERE ANTES DE APAGAR A ORIGEM. Le de volta do disco e compara: a
    # remocao no conexoes.ini e irreversivel na pratica (o sal so existe
    # em um lugar), entao ela so acontece depois de o destino estar
    # comprovadamente gravado e igual. Um bug daqui — uma linha perdida
    # numa edicao, o disco cheio na hora errada — custaria todas as
    # senhas guardadas; a conferencia transforma isso em "a migracao nao
    # aconteceu", que e recuperavel.
    conferido = _ler_cp(caminho_chaveiro)
    if (not conferido.has_section(SECAO_COFRE)
            or dict(conferido[SECAO_COFRE]) != parametros):
        raise ErroCofre(
            "o cofre não foi gravado corretamente em %s — o conexoes.ini "
            "fica como está, sem perder o sal." % caminho_chaveiro)

    _remover_secao_bruta(caminho_conexoes, SECAO_COFRE)
    return True


# ------------------------------------------------------------ recifragem

def _do_app(nome):
    """Mesma armadilha documentada em cofre.py: o app roda como script,
    entao "from acessos import x" carregaria uma SEGUNDA copia do
    modulo. Busca a funcao no modulo que ja esta rodando."""
    for chave in ("__main__", "acessos"):
        mod = sys.modules.get(chave)
        if mod is not None and hasattr(mod, nome):
            return getattr(mod, nome)
    raise ErroCofre("função %s indisponível (app não carregado)" % nome)


class RecifragemParcial(ErroCofre):
    """Falhou DEPOIS de comecar a trocar os arquivos.

    Existe para o aviso ao operador poder ser honesto: "nada foi
    alterado" e verdade quando a falha e na preparacao, e mentira
    perigosa quando um dos dois arquivos ja foi trocado."""


def _escrever_tmp(caminho, cp):
    """Escreve o ConfigParser num temporario ao lado do destino.

    No MESMO diretorio de proposito: os.replace so e atomico dentro do
    mesmo sistema de arquivos."""
    tmp = os.path.join(os.path.dirname(os.path.abspath(caminho)),
                       "." + os.path.basename(caminho) + ".novo")
    with open(tmp, "w", encoding="utf-8") as f:
        cp.write(f)
        f.flush()
        os.fsync(f.fileno())          # sem isto o replace pode publicar
    proteger_arquivo(tmp)             # um arquivo ainda vazio numa queda
    return tmp


def _recifrar_tudo(caminho_chaveiro, caminho_conexoes, cofre_velho, senha_nova):
    """Troca a senha mestra: decifra credenciais do chaveiro E campos
    sigilosos do conexoes.ini com a chave velha, gera cofre novo, regrava
    os dois arquivos com a chave nova.

    DOIS ARQUIVOS, UMA CHAVE SO — e o ponto delicado desta funcao. Se um
    deles ficasse com a chave nova e o outro com a velha, o que sobrasse
    para tras viraria lixo indecifravel: o sal antigo nao existe mais em
    lugar nenhum. Por isso, nesta ordem:

      1. decifra TUDO em memoria (falha aqui = nada foi tocado);
      2. copia os DOIS arquivos para .bak;
      3. escreve os dois em TEMPORARIOS, com fsync — toda a parte
         demorada e arriscada (disco cheio, permissao) acontece aqui,
         ainda sem publicar nada;
      4. so entao os dois os.replace, um atras do outro.

    A janela entre os dois replaces e a unica exposicao real que resta —
    duas trocas de nome, sem I/O de conteudo no meio. Se ainda assim uma
    falhar, levanta RecifragemParcial e os .bak sao a saida.
    """
    cp_ch = _ler_cp(caminho_chaveiro)
    claros_ch = {}
    for nome in cp_ch.sections():
        if nome == SECAO_COFRE:
            continue
        valor = cp_ch[nome].get("senha")
        if valor:
            claros_ch[nome] = cofre_velho.decifrar(valor)

    cp_cx = None
    claros_cx = {}
    if caminho_conexoes and os.path.exists(caminho_conexoes):
        cp_cx = configparser.ConfigParser(interpolation=None)
        cp_cx.read(caminho_conexoes, encoding="utf-8")
        for secao in cp_cx.sections():
            for campo in _cofre.CAMPOS_SIGILOSOS:
                valor = cp_cx[secao].get(campo)
                # alias "!nome" nao e segredo e nao entra na recifragem:
                # esta_cifrado() ja o deixa de fora, e ele fica como esta
                if valor and esta_cifrado(valor):
                    claros_cx[(secao, campo)] = cofre_velho.decifrar(valor)

    novo = Cofre()
    novo.criar(senha_nova)

    for nome, claro in claros_ch.items():
        cp_ch[nome]["senha"] = novo.cifrar(claro) if claro else ""
    cp_ch[SECAO_COFRE] = novo.parametros()
    if cp_cx is not None:
        for (secao, campo), claro in claros_cx.items():
            cp_cx[secao][campo] = novo.cifrar(claro) if claro else ""

    # ---- 2. copias de seguranca dos DOIS, antes de qualquer troca
    for origem in (caminho_chaveiro, caminho_conexoes):
        if origem and os.path.exists(origem):
            try:
                shutil.copy2(origem, origem + ".bak")
                proteger_arquivo(origem + ".bak")
            except OSError as e:
                raise ErroCofre("não consegui guardar a cópia de segurança "
                                "de %s: %s" % (origem, e))

    # ---- 3. temporarios (nada publicado ainda)
    tmps = []
    try:
        tmp_ch = _escrever_tmp(caminho_chaveiro, cp_ch)
        tmps.append(tmp_ch)
        tmp_cx = _escrever_tmp(caminho_conexoes, cp_cx) if cp_cx else None
        if tmp_cx:
            tmps.append(tmp_cx)
    except Exception as e:
        for t in tmps:
            try:
                os.remove(t)
            except OSError:
                pass
        raise ErroCofre("falha ao preparar os arquivos novos: %s" % e)

    # ---- 4. publicacao: so trocas de nome daqui para baixo
    os.replace(tmp_ch, caminho_chaveiro)
    proteger_arquivo(caminho_chaveiro)
    if tmp_cx:
        try:
            os.replace(tmp_cx, caminho_conexoes)
            proteger_arquivo(caminho_conexoes)
        except OSError as e:
            raise RecifragemParcial(
                "o chaveiro já foi gravado com a senha nova, mas o "
                "conexoes.ini não: %s\n\nAs cópias .bak dos dois arquivos "
                "estão ao lado deles." % e)

    return novo, len(claros_ch) + len(claros_cx)


def _limpar_tudo(caminho_chaveiro, caminho_conexoes):
    """Usado so na reinicializacao: o que estava cifrado com a chave
    antiga virou lixo indecifravel. Esvazia senha de todas as
    credenciais do chaveiro e os campos sigilosos do conexoes.ini."""
    cp_ch = _ler_cp(caminho_chaveiro)
    mudou = False
    for nome in cp_ch.sections():
        if nome == SECAO_COFRE:
            continue
        if cp_ch[nome].get("senha"):
            cp_ch[nome]["senha"] = ""
            mudou = True
    if mudou:
        _gravar_cp(caminho_chaveiro, cp_ch)

    if caminho_conexoes and os.path.exists(caminho_conexoes):
        cp_cx = configparser.ConfigParser(interpolation=None)
        cp_cx.read(caminho_conexoes, encoding="utf-8")
        _gs = _do_app("gravar_secao")
        for secao in cp_cx.sections():
            pares = [(campo, "") for campo in _cofre.CAMPOS_SIGILOSOS
                     if cp_cx[secao].get(campo)]
            if pares:
                _gs(caminho_conexoes, secao, pares)


# ============================================================== interface
# Reaproveita os dialogos genericos ja existentes em cofre.py — eles
# recebem uma instancia de Cofre e um "pai", sem saber (nem precisar
# saber) de qual arquivo essa instancia veio.

try:
    import gi  # noqa: E402
    gi.require_version("Gtk", "3.0")
    from gi.repository import Gtk, Gdk  # noqa: E402,F401
    TEM_GTK = True
except Exception:                                     # pragma: no cover
    Gtk, Gdk, TEM_GTK = None, None, False

def _icone(nome_symbolic, fallback_texto, px=13, classe=None):
    """Icone "symbolic" do tema (linha fina, recolorivel via CSS
    "color") com um glifo de texto como fallback. Emoji como "🗑" tem cor
    PROPRIA gravada no glifo (fonte de emoji colorido) e ignora o
    "color" do CSS — e por isso o botao de remover nao ficava vermelho
    so mudando a folha de estilo; o icone symbolic resolve isso porque
    e desenhado como mascara de UMA cor so.

    `classe`, quando dado, vai DIRETO na imagem — nao basta pintar o
    botao-pai e esperar heranca. E o mesmo padrao de icone_acao()
    (acessos.py): la a cor tambem e aplicada direto no Gtk.Image, nunca
    no container."""
    try:
        tema_ic = Gtk.IconTheme.get_default()
        if tema_ic.has_icon(nome_symbolic):
            img = Gtk.Image.new_from_icon_name(nome_symbolic,
                                               Gtk.IconSize.MENU)
            img.set_pixel_size(px)
            if classe:
                add_class(img, classe)
            return img
    except Exception:
        pass
    return Gtk.Label(label=fallback_texto)


def _clicavel(conteudo, ao_clicar, classe_extra=None, tooltip=None):
    """EventBox com hover 100% manual — SEM Gtk.Button.

    O Gtk.Button (mesmo com relief NONE, mesmo zerando toda propriedade
    de CSS que se pensa em zerar) mostrou um retangulo branco solido ao
    passar o mouse nesta maquina, e piscava ao clicar — o suspeito e o
    efeito de destaque nativo do tema sobre o icone (prelight), que essa
    maquina desenha errado com o carregador de pixbuf quebrado (aviso no
    log: "Could not load a pixbuf from icon theme"). Um EventBox comum
    nao tem esse efeito embutido nenhum: so pinta o que a classe CSS
    "icone-hover" manda, ligada e desligada por nos mesmos.

    NotifyType.INFERIOR: o evento que o GDK manda quando o ponteiro cruza
    para uma JANELA FILHA (a da propria imagem, por exemplo) — sem
    filtrar isso, sair/entrar na imagem dentro do EventBox contava como
    sair/entrar no EventBox tambem, e o hover PISCAVA a cada pixel de
    fronteira."""
    caixa = Gtk.EventBox()
    if classe_extra:
        add_class(caixa, classe_extra)
    add_class(caixa, "icone-clicavel")
    caixa.add(conteudo)
    if tooltip:
        caixa.set_tooltip_text(tooltip)

    def _entrar(w, ev):
        if ev.detail == Gdk.NotifyType.INFERIOR:
            return False
        w.get_style_context().add_class("icone-hover")
        return False

    def _sair(w, ev):
        if ev.detail == Gdk.NotifyType.INFERIOR:
            return False
        w.get_style_context().remove_class("icone-hover")
        return False

    def _clique(_w, ev):
        if ev.button == 1:
            ao_clicar()
        return False

    caixa.connect("enter-notify-event", _entrar)
    caixa.connect("leave-notify-event", _sair)
    caixa.connect("button-release-event", _clique)
    return caixa


from dialogo_ui import (avisar as _avisar, confirmar as _confirmar,  # noqa: E402
                        add_class, botao_dialogo, rotulo as _rotulo,
                        dialogo as _dialogo, campo_senha as _campo_senha,
                        editor as _editor, nota as _nota)


def _erro(pai, texto):
    _avisar(pai, texto, erro=True)


def _aviso(pai, texto):
    _avisar(pai, texto)


def destrancar(caminho_chaveiro, caminho_conexoes=None, pai=None):
    """Ponto de entrada unico, chamado no arranque — no lugar do antigo
    cofre.destrancar(). O chaveiro existe desde a primeira execucao:
    ao contrario do cofre antigo, ele NAO pergunta "quer proteger?" —
    so oferece criar a senha mestra na primeira vez.

    Devolve (cofre, seguir), mesmo contrato de cofre.destrancar."""
    if not TEM_CRIPTO:
        return None, True

    try:
        migrou = migrar_cofre_de_conexoes(caminho_chaveiro, caminho_conexoes)
    except Exception as e:
        _erro(pai, "Falha ao migrar o cofre do conexoes.ini: %s" % e)
        migrou = False

    # CHAVEIRO ILEGIVEL = PARAR, nao seguir em frente. O arquivo existe
    # mas nao abre (permissao, INI corrompido, pasta sincronizada no meio
    # de uma escrita): seguir daqui significaria tratar como "ainda nao
    # ha cofre" e oferecer criar um novo — com sal novo, apagando o
    # acesso a tudo que ja estava guardado. Melhor o app nao abrir e o
    # operador poder consertar o arquivo (ou restaurar o .bak).
    try:
        cp = _ler_cp(caminho_chaveiro)
    except Exception as e:
        _erro(pai, "O chaveiro existe mas não pôde ser lido:\n\n%s\n\n"
                   "O aplicativo não vai abrir para não arriscar criar um "
                   "cofre novo por cima. Verifique o arquivo:\n%s"
                   % (e, caminho_chaveiro))
        return None, False
    c = Cofre()

    # ---- primeira vez: nao ha cofre nem no chaveiro nem (mais) no conexoes
    if not cp.has_section(SECAO_COFRE):
        senha = _cofre.dialogo_criar(pai)
        if senha is None:
            return None, True             # segue sem cofre, por escolha
        c.criar(senha)
        cp[SECAO_COFRE] = c.parametros()
        _gravar_cp(caminho_chaveiro, cp)
        return c, True

    c.carregar_parametros(cp[SECAO_COFRE])
    if migrou:
        _aviso(pai, "O cofre de senhas foi movido do conexoes.ini para o "
                    "chaveiro.ini. A senha mestra continua a mesma.")

    senha_amb = senha_do_ambiente()
    if senha_amb:
        try:
            c.abrir(senha_amb)
            return c, True
        except Exception:
            pass                           # cai para o dialogo

    while True:
        r = _cofre.dialogo_abrir(c, pai)
        if r is None:
            return None, False
        if r == "ok":
            return c, True
        if r == "reiniciar":
            senha = _cofre.dialogo_criar(pai)
            if senha is None:
                continue
            c2 = Cofre()
            c2.criar(senha)
            try:
                _limpar_tudo(caminho_chaveiro, caminho_conexoes)
                cp2 = _ler_cp(caminho_chaveiro)
                cp2[SECAO_COFRE] = c2.parametros()
                _gravar_cp(caminho_chaveiro, cp2)
            except Exception as e:
                _erro(pai, "Falha ao reinicializar: %s" % e)
                continue
            _aviso(pai, "Chaveiro reinicializado. As senhas anteriores "
                        "foram apagadas.")
            return c2, True
        # trocar senha, direto da primeira janela
        nova = _cofre.dialogo_trocar(c, pai)
        if nova is None:
            continue
        try:
            c2, n = _recifrar_tudo(caminho_chaveiro, caminho_conexoes, c, nova)
        except RecifragemParcial as e:
            # NAO dizer "nada foi alterado" aqui: um dos arquivos ja
            # mudou, e mandar o operador seguir como se nada tivesse
            # acontecido e o caminho mais curto para ele perder as senhas
            # por cima do estrago.
            _erro(pai, "A troca de senha falhou pela METADE.\n\n%s\n\n"
                       "Feche o aplicativo e restaure as cópias .bak "
                       "antes de usá-lo de novo." % e)
            return None, False
        except Exception as e:
            _erro(pai, "Falha ao trocar a senha: %s\n\nNada foi "
                       "alterado." % e)
            c = Cofre()
            c.carregar_parametros(cp[SECAO_COFRE])
            continue
        _aviso(pai, "Senha mestra trocada. %d valor(es) recifrado(s)." % n)
        return c2, True


# ---------------------------------------------------------- gerenciador

def _dialogo_credencial(pai, nome="", usuario="", senha="", travar_nome=False):
    """Formulario de uma credencial. Devolve (nome, usuario, senha) ou
    None se cancelou.

    A senha vem PRE-PREENCHIDA com o valor atual (decifrado) ao editar:
    assim, campo vazio ao salvar significa "apagar a senha", uma escolha
    deliberada, e nao "nao mexi nisso"."""
    titulo = "Editar credencial" if travar_nome else "Nova credencial"
    dlg, cx = _dialogo(titulo, pai)
    # largura minima: o placeholder do NOME ("identificador — usado como
    # !nome") nao cabia na largura natural do dialogo pequeno de _dialogo()
    dlg.set_default_size(360, -1)
    botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
    botao_dialogo(dlg, "Salvar", Gtk.ResponseType.OK, "acao")

    cx.pack_start(_rotulo("NOME", "rotulo"), False, False, 0)
    ent_nome = Gtk.Entry()
    ent_nome.set_text(nome)
    ent_nome.set_sensitive(not travar_nome)
    ent_nome.set_placeholder_text("identificador — usado como !nome")
    cx.pack_start(ent_nome, False, False, 0)

    cx.pack_start(_rotulo("USUÁRIO", "rotulo"), False, False, 0)
    ent_usuario = Gtk.Entry()
    ent_usuario.set_text(usuario)
    cx.pack_start(ent_usuario, False, False, 0)

    ent_senha = _campo_senha(cx, "SENHA", dlg, Gtk.ResponseType.OK)
    ent_senha.set_text(senha)

    cx.show_all()
    (ent_usuario if travar_nome else ent_nome).grab_focus()

    try:
        while True:
            if dlg.run() != Gtk.ResponseType.OK:
                return None
            nm = ent_nome.get_text().strip()
            if not nm:
                _erro(dlg, "O nome não pode ser vazio.")
                continue
            if nm == SECAO_COFRE:
                _erro(dlg, "'%s' é um nome reservado." % SECAO_COFRE)
                continue
            return nm, ent_usuario.get_text().strip(), ent_senha.get_text()
    finally:
        dlg.destroy()


def abrir_gerenciador(caminho, cofre, pai=None):
    """Tela de listar/adicionar/editar/remover credenciais.

    Gtk.ListBox, NAO Gtk.TreeView: o TreeView deixava selecao/hover sem
    nenhum feedback visual nesta tela (mesmo com CSS ":selected" correto
    e prioridade certa — algo na estrutura de nodes desta versao do GTK
    nao respondia) e sobrava espaco vazio de sobra quando a lista tinha
    poucas linhas. ListBoxRow tem :selected/:hover que realmente pintam,
    e cada linha carrega seus PROPRIOS botoes de editar/remover — sem
    exigir selecionar antes de agir, e sem um botao "secundaria" solto
    que nao pegava contraste nenhum ao lado dos coloridos."""
    if cofre is None or cofre.trancado():
        _erro(pai, "O chaveiro está trancado.")
        return

    dlg, cx, _ok = _editor("Chaveiro de senhas", pai, ok=None, cancelar=None,
                           largura=480, altura=-1)
    # resize(1,1) em redesenhar() pede o MENOR tamanho possivel — sem
    # piso, ele aceitava de volta so a altura do cabecalho, uma tira fina
    # de janela. set_size_request trava um minimo que o resize(1,1) nao
    # atravessa, mas ainda deixa crescer normalmente com mais conteudo.
    dlg.set_size_request(480, 260)

    cx.pack_start(_rotulo("CREDENCIAIS", "bloco-cab"), False, False, 0)
    _nota(cx, "Aponte para elas no conexoes.ini com \"!nome\" no usuário "
              "e/ou na senha.", "dlg-dica")

    # Gtk.Box comum, NAO Gtk.ListBox: o ListBoxRow carrega estado interno
    # proprio (foco, prelight, "boxed-list") que o CSS deste tema nao
    # conseguia neutralizar de forma confiavel — cada tentativa resolvia
    # um retangulo branco e revelava outro em outro lugar (linha, botao,
    # texto). Aqui cada linha e um Gtk.EventBox comum, e o hover e ligado
    # e desligado A MAO (enter/leave-notify), com UMA classe CSS nossa —
    # sem pseudo-estado nativo do GTK envolvido, sem surpresa.
    caixa_lista = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
    add_class(caixa_lista, "cartao")
    # ScrolledWindow so entra em jogo com MUITAS credenciais — com poucas,
    # propagate_natural_height deixa a janela do tamanho do conteudo, sem
    # sobrar area vazia nem crescer sem fim.
    rol = Gtk.ScrolledWindow()
    rol.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
    rol.set_max_content_height(320)
    rol.set_propagate_natural_height(True)
    # SEM overlay scrolling: a barra flutuante por cima do conteudo e o
    # padrao do GTK3, e ela cobria o icone de remover, na borda direita.
    # Com overlay desligado a barra ganha espaco PROPRIO, reservado, e
    # nao sobrepoe mais nada.
    rol.set_overlay_scrolling(False)
    rol.add(caixa_lista)
    cx.pack_start(rol, False, False, 0)

    registro = {}

    def _editar(nome):
        dados = registro.get(nome, {})
        r = _dialogo_credencial(dlg, nome=nome,
                                usuario=dados.get("usuario", ""),
                                senha=dados.get("senha", ""),
                                travar_nome=True)
        if r is None:
            return
        _, usu, sen = r
        try:
            salvar(caminho, cofre, nome, usu, sen)
        except Exception as e:
            _erro(dlg, "Falha ao salvar: %s" % e)
            return
        redesenhar()

    def _remover(nome):
        if not _confirmar(
                dlg, "Remover '%s'?" % nome,
                "Conexões que apontam \"!%s\" ficam com um alias quebrado "
                "até serem editadas." % nome,
                ok="Remover", perigo=True):
            return
        remover(caminho, nome)
        redesenhar()

    def _linha(nome, dados):
        # "sem-fundo": a regra generica ".acessos-dialogo box" (tema.py)
        # pinta QUALQUER Gtk.Box de branco solido — sem essa classe, esta
        # caixa interna (que embrulha o texto+icones) ficava branca por
        # cima do cinza do hover da linha, um retangulo exatamente do
        # tamanho do conteudo. Mesmo mecanismo que ja acertou a divisoria
        # "regua-h" antes: precisa de "box.<classe>" para empatar a
        # especificidade e vencer por ordem.
        linha = add_class(Gtk.Box(spacing=6), "sem-fundo")
        linha.set_border_width(6)
        usuario = dados.get("usuario") or "sem usuário"
        lb = _rotulo("%s  ·  %s" % (nome, usuario), "opcao-txt",
                    ellipsize=None)
        lb.set_hexpand(True)
        linha.pack_start(lb, True, True, 0)

        ic_editar = _icone("document-edit-symbolic", "✎")
        linha.pack_start(
            _clicavel(ic_editar, lambda n=nome: _editar(n),
                     tooltip="Editar"),
            False, False, 0)

        ic_remover = _icone("user-trash-symbolic", "🗑", classe="icone-perigo")
        linha.pack_start(
            _clicavel(ic_remover, lambda n=nome: _remover(n),
                     classe_extra="icone-perigo", tooltip="Remover"),
            False, False, 0)

        def _entrar(w, ev):
            if ev.detail == Gdk.NotifyType.INFERIOR:
                return False
            w.get_style_context().add_class("linha-hover")
            return False

        def _sair(w, ev):
            if ev.detail == Gdk.NotifyType.INFERIOR:
                return False
            w.get_style_context().remove_class("linha-hover")
            return False

        caixa = Gtk.EventBox()
        add_class(caixa, "linha-item")
        caixa.add(linha)
        caixa.connect("enter-notify-event", _entrar)
        caixa.connect("leave-notify-event", _sair)
        return caixa

    def redesenhar(encolher=True):
        for filho in list(caixa_lista.get_children()):
            caixa_lista.remove(filho)
        registro.clear()
        registro.update(carregar_credenciais(caminho, cofre))
        linhas = sorted(registro)
        for i, nome in enumerate(linhas):
            caixa_lista.pack_start(_linha(nome, registro[nome]),
                                   False, False, 0)
            if i < len(linhas) - 1:
                caixa_lista.pack_start(add_class(Gtk.Box(), "regua-h"),
                                       False, False, 0)
        if not registro:
            caixa_lista.pack_start(
                _rotulo("nenhuma credencial ainda", "dlg-dica"),
                False, False, 8)
        caixa_lista.show_all()
        # o GTK3 so CRESCE a janela sozinho, nunca encolhe por conta
        # propria quando o conteudo diminui (removeu credencial, lista
        # ficou mais curta) — sem isto sobrava borda em branco embaixo,
        # do tamanho da lista de antes. resize(1,1) pede o menor tamanho
        # possivel e o GTK recalcula pelo natural do conteudo atual.
        #
        # SO nas chamadas POSTERIORES (encolher=True): na primeira, antes
        # do dialogo ainda ter sido mostrado/mapeado, resize(1,1) nao
        # respeitava o minimo de set_size_request e a janela nascia como
        # uma tira de 1 linha — o floor so vale depois que a janela ja
        # existe de verdade na tela.
        if encolher:
            dlg.resize(1, 1)

    def _novo():
        r = _dialogo_credencial(dlg)
        if r is None:
            return
        nm, usu, sen = r
        if nm in registro:
            _erro(dlg, "Já existe uma credencial com o nome '%s'." % nm)
            return
        try:
            salvar(caminho, cofre, nm, usu, sen)
        except Exception as e:
            _erro(dlg, "Falha ao salvar: %s" % e)
            return
        redesenhar()

    bt_novo = add_class(Gtk.Button(label="+ Nova credencial"), "acao")
    bt_novo.connect("clicked", lambda _b: _novo())
    cx.pack_start(bt_novo, False, False, 4)

    redesenhar(encolher=False)
    dlg.show_all()
    dlg.run()
    dlg.destroy()


def escolher(caminho, pai=None, titulo="Usar do chaveiro"):
    """Seletor simples de uma credencial existente. Devolve o nome
    escolhido, ou None se cancelou/nao ha nenhuma.

    Gtk.ListBox, NAO ComboBoxText nem TreeView: o popup do combo exige
    clique-e-segura sob boa parte do Wayland deste app (mesma familia de
    encrenca de grab documentada em GrabNativo/liberar_grab), e o
    TreeView nesta versao do GTK nao mostrava NENHUM feedback ao
    selecionar (ver abrir_gerenciador). ListBoxRow com SelectionMode.
    SINGLE realmente pinta a linha selecionada."""
    nomes = listar(caminho)
    if not nomes:
        _aviso(pai, "Nenhuma credencial cadastrada ainda. Abra o "
                    "chaveiro (🔑 na barra) para criar uma.")
        return None

    dlg, cx = _dialogo(titulo, pai)
    botao_dialogo(dlg, "Cancelar", Gtk.ResponseType.CANCEL, "perigo")
    botao_dialogo(dlg, "Usar", Gtk.ResponseType.OK, "acao")

    cx.pack_start(_rotulo("CREDENCIAL", "rotulo"), False, False, 0)
    caixa_lista = Gtk.ListBox()
    caixa_lista.set_selection_mode(Gtk.SelectionMode.SINGLE)
    add_class(caixa_lista, "lista")
    linhas = {}
    for nm in nomes:
        row = Gtk.ListBoxRow()
        row.add(_rotulo(nm, "opcao-txt", ellipsize=None))
        caixa_lista.add(row)
        linhas[row] = nm
    caixa_lista.select_row(caixa_lista.get_row_at_index(0))
    caixa_lista.connect("row-activated",
                        lambda *_a: dlg.response(Gtk.ResponseType.OK))

    # SEM ScrolledWindow/Viewport — ver o comentario em abrir_gerenciador.
    add_class(caixa_lista, "cartao")
    cx.pack_start(caixa_lista, False, False, 0)
    cx.show_all()
    caixa_lista.grab_focus()

    r = dlg.run()
    escolhido = None
    if r == Gtk.ResponseType.OK:
        row = caixa_lista.get_selected_row()
        if row is not None:
            escolhido = linhas.get(row)
    dlg.destroy()
    return escolhido
