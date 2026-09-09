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
"""atualizador — checa releases no GitHub e reinstala o pacote .flatpak novo.

Feature EXCLUSIVA do Flatpak: fora dele (instalacao nativa via instalar.sh,
ou "python3 acessos.py" direto em desenvolvimento) nao ha o que reinstalar,
entao rodando_em_flatpak() bloqueia tudo o resto na porta de entrada —
quem chama nem precisa se preocupar com o caso "instalado nativo".

NAO usa 'flatpak update': os releases sao publicados como um arquivo
.flatpak solto anexado ao release do GitHub (ver build.sh, 'flatpak
build-bundle' sem --repo-url), nao como um repositorio OSTree hospedado.
Sem um repo remoto configurado, 'flatpak update' nao acha nada para
buscar e devolve sucesso sem mudar nada — pareceria ter atualizado e na
verdade so reabriria a mesma versao antiga. Por isso o fluxo real e:
baixar o .flatpak do release e 'flatpak install --reinstall' por cima.

Tres papeis:

  versao_instalada()      le a versao da PROPRIA release do metainfo.xml
                           instalado (/app/share/metainfo/...). Fonte unica
                           de verdade: e o mesmo arquivo que ja se atualiza
                           a cada release, tanto para o Flathub quanto para
                           a distribuicao por fora — nao ha uma segunda
                           constante de versao no codigo Python para
                           esquecer de bumpar.

  checar_async()           so LE a API do GitHub, numa thread, e devolve o
                           resultado na main loop do GTK via GLib.idle_add.
                           Nunca toca em rede na thread principal.

  atualizar_e_reiniciar() baixa o .flatpak do release, reinstala por cima
                           do app atual e, se der certo, sobe uma nova
                           instancia ja DESACOPLADA deste processo — quem
                           chama e quem decide encerrar a janela atual
                           (self.destroy()) depois que isto retornar sem
                           excecao.

flatpak-spawn --host e o unico jeito de um processo dentro do sandbox pedir
ao host para rodar 'flatpak install' nele mesmo; exige --talk-name=
org.freedesktop.Flatpak no manifest. O .flatpak baixado precisa ficar num
caminho que o HOST tambem enxergue — daqui o uso do cache do proprio app
(~/.var/app/<id>/cache), que e um bind-mount real do $HOME, nao um tmpfs
privado do sandbox.
"""

import json
import os
import subprocess
import threading
import urllib.request
import xml.etree.ElementTree as ET

from gi.repository import GLib

REPO = "JMoratelli/acessos"
APP_ID = "org.jj.Acessos"
URL_RELEASE_MAIS_RECENTE = "https://api.github.com/repos/%s/releases/latest" % REPO
CAMINHO_METAINFO = "/app/share/metainfo/%s.metainfo.xml" % APP_ID
TIMEOUT_REDE = 8
TIMEOUT_DOWNLOAD = 300


class FalhaAtualizacao(Exception):
    """Erro ao baixar ou instalar a atualizacao — mensagem ja pronta para dialogo."""


def rodando_em_flatpak():
    return os.path.exists("/.flatpak-info")


def versao_instalada():
    """Versao da release mais recente listada no metainfo.xml instalado, ou
    None se o arquivo nao existe ou nao tem release nenhuma (nao deveria
    acontecer dentro do Flatpak, mas a checagem de atualizacao nao e algo
    que valha travar o app por causa disso)."""
    try:
        raiz = ET.parse(CAMINHO_METAINFO).getroot()
        release = raiz.find("releases/release")
        return release.get("version") if release is not None else None
    except Exception:
        return None


def _versao_tupla(v):
    v = (v or "").strip().lower().lstrip("v")
    partes = []
    for pedaco in v.split("."):
        digitos = "".join(ch for ch in pedaco if ch.isdigit())
        partes.append(int(digitos) if digitos else 0)
    return tuple(partes) or (0,)


def mais_nova(versao_remota, versao_local):
    return _versao_tupla(versao_remota) > _versao_tupla(versao_local)


def checar_async(ao_concluir):
    """Consulta a release mais recente do GitHub numa thread separada, se e
    so se isto e um Flatpak com metainfo legivel — do contrario nao ha nem
    versao local para comparar, nem pacote para reinstalar depois.

    ao_concluir(tag_ou_none, url_bundle_ou_none) e chamado de volta na main
    loop do GTK (GLib.idle_add) — nunca direto da thread de rede, que nao
    pode mexer em widgets. Ambos vem None se nao ha versao mais nova, se a
    checagem falhou (sem rede, API fora do ar, etc.), ou se a release mais
    nova nao tem um .flatpak anexado — nesses casos nao ha nada a oferecer."""
    if not rodando_em_flatpak():
        return
    versao_atual = versao_instalada()
    if not versao_atual:
        return

    def trabalho():
        tag = None
        url_bundle = None
        try:
            pedido = urllib.request.Request(
                URL_RELEASE_MAIS_RECENTE,
                headers={"Accept": "application/vnd.github+json",
                         "User-Agent": "Acessos-Atualizador"})
            with urllib.request.urlopen(pedido, timeout=TIMEOUT_REDE) as resp:
                dado = json.loads(resp.read().decode("utf-8"))
            candidata = dado.get("tag_name")
            if candidata and mais_nova(candidata, versao_atual):
                ativo = next((a for a in dado.get("assets", [])
                             if a.get("name", "").endswith(".flatpak")), None)
                if ativo:
                    tag = candidata
                    url_bundle = ativo["browser_download_url"]
        except Exception:
            tag = None
        GLib.idle_add(ao_concluir, tag, url_bundle)
    threading.Thread(target=trabalho, daemon=True).start()


def _rodar_no_host(args):
    return subprocess.run(["flatpak-spawn", "--host"] + args,
                          capture_output=True, text=True)


def atualizar_e_reiniciar(url_bundle):
    """Baixa o .flatpak do release e reinstala por cima, desacoplando uma
    nova instancia do app antes de retornar.

    So e chamada a partir do clique no aviso de atualizacao, que so aparece
    quando rodando_em_flatpak() ja deu True e ha um url_bundle valido — nao
    precisa reverificar nenhum dos dois aqui.

    Levanta FalhaAtualizacao com uma mensagem pronta para dialogo se o
    download ou o 'flatpak install' falharem. Se retornar sem excecao, uma
    nova instancia do app ja foi lancada e quem chamou deve encerrar a
    janela atual."""
    # ~/.var/app/<id>/cache: cache do proprio app, mas e um caminho REAL do
    # $HOME do host (que o Flatpak so bind-monta dentro do sandbox) — tanto
    # este processo quanto o 'flatpak install' rodado no host, mais abaixo,
    # enxergam o mesmo arquivo por este mesmo caminho.
    destino = os.path.join(GLib.get_user_cache_dir(), "atualizacao.flatpak")
    try:
        os.makedirs(os.path.dirname(destino), exist_ok=True)
        pedido = urllib.request.Request(
            url_bundle, headers={"User-Agent": "Acessos-Atualizador"})
        with urllib.request.urlopen(pedido, timeout=TIMEOUT_DOWNLOAD) as resp, \
                open(destino, "wb") as arq:
            while True:
                bloco = resp.read(1 << 20)
                if not bloco:
                    break
                arq.write(bloco)
    except Exception as e:
        raise FalhaAtualizacao("Falha ao baixar a atualização: %s" % e)

    r = _rodar_no_host(["flatpak", "install", "--user", "-y", "--noninteractive",
                        "--reinstall", destino])
    try:
        os.remove(destino)
    except OSError:
        pass
    if r.returncode != 0:
        msg = (r.stderr or r.stdout or "").strip()
        raise FalhaAtualizacao(
            msg or "flatpak install terminou com codigo %d" % r.returncode)

    # start_new_session: desacopla do processo atual, que esta prestes a
    # sair — sem isto, a nova instancia morreria junto com este processo.
    subprocess.Popen(["flatpak-spawn", "--host", "flatpak", "run", APP_ID],
                      start_new_session=True)
