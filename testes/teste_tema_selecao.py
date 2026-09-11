#!/usr/bin/env python3
"""Contraste do item SELECIONADO da lista, nos dois temas.

    python3 testes/teste_tema_selecao.py

POR QUE ISTO EXISTE: o fundo da linha selecionada e INVERTIDO de proposito
(quase preto no tema claro, quase branco no escuro). O texto so fica legivel
se inverter junto. Como o rotulo dentro da linha tem classe propria com
"color", e cor no proprio node vence heranca, bastava alguem dar cor a um
rotulo para o item selecionado virar preto-no-preto (tema claro) ou
branco-no-branco (escuro) — foi exatamente o que aconteceu no seletor
"Usar do chaveiro". Isto le a cor JA RESOLVIDA pelo CSS, sem depender do olho.
"""
import os
import sys

import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk                        # noqa: E402

AQUI = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(AQUI, os.pardir, "python"))
from tema import gerar_css, TEMAS                         # noqa: E402

MINIMO = 0.4       # diferenca de luminancia abaixo disso e ilegivel


def _luminancia(c):
    return 0.2126 * c.red + 0.7152 * c.green + 0.0722 * c.blue


def _checar(nome_tema):
    prov = Gtk.CssProvider()
    prov.load_from_data(gerar_css(nome_tema))
    tela = Gdk.Screen.get_default()
    Gtk.StyleContext.add_provider_for_screen(
        tela, prov, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

    # mesma arvore de chaveiro.escolher(): ListBox.lista > row > label
    jan = Gtk.OffscreenWindow()
    caixa = Gtk.ListBox()
    caixa.set_selection_mode(Gtk.SelectionMode.SINGLE)
    caixa.get_style_context().add_class("lista")
    row = Gtk.ListBoxRow()
    rot = Gtk.Label(label="credencial")
    rot.get_style_context().add_class("opcao-txt")
    row.add(rot)
    caixa.add(row)
    jan.add(caixa)
    jan.show_all()
    caixa.select_row(row)
    while Gtk.events_pending():
        Gtk.main_iteration()

    fundo = row.get_style_context().get_background_color(
        Gtk.StateFlags.SELECTED)
    texto = rot.get_style_context().get_color(Gtk.StateFlags.SELECTED)
    d = abs(_luminancia(fundo) - _luminancia(texto))
    ok = d >= MINIMO
    print("  %-7s fundo=%-18s texto=%-18s contraste=%.2f  %s" % (
        nome_tema, fundo.to_string(), texto.to_string(), d,
        "ok" if ok else "ILEGIVEL"))

    Gtk.StyleContext.remove_provider_for_screen(tela, prov)
    jan.destroy()
    return ok


def main():
    if not Gdk.Screen.get_default():
        print("sem display: pulando (rode com Xvfb se precisar em CI)")
        return 0
    print("item selecionado da lista:")
    falhas = [t for t in TEMAS if not _checar(t)]
    if falhas:
        print("\ntexto ilegivel sobre a selecao em: %s" % ", ".join(falhas))
        return 1
    print("\ntudo conforme o esperado")
    return 0


if __name__ == "__main__":
    sys.exit(main())
