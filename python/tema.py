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
"""Tema, paleta e folha de estilo do Acessos.

Aqui mora tudo o que é aparência: as cores de cada tema, as famílias de
fonte e o CSS. Separado do acessos.py porque são 500+ linhas de folha de
estilo — conteúdo que não é lógica de programa e que só se mexe quando o
assunto é interface.

Quem usa:
    tema.gerar_css("escuro")     -> bytes, para o CssProvider
    tema.TEMAS["escuro"]["fundo"]
    tema.rgba("#112233")         -> Gdk.RGBA
    tema.fonte_mono(11)          -> Pango.FontDescription
"""

import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk, Pango  # noqa: E402


# ---------------------------------------------------------------- temas

ACENTOS = ["#4c6ef5", "#0ca678", "#e8590c", "#ae3ec9", "#1098ad", "#d6336c"]

TEMAS = {
    "claro": dict(
        fundo="#d9e2ee", cartao="#ffffff", borda="#e3e8ec", borda2="#cfd6dd",
        texto="#1b232b", sec="#5b6976", fraco="#94a1ad",
        # camadas do fundo: sem fundo variavel, translucidez vira cor solida
        # amplitude MAIOR do que parece necessario numa amostra pequena:
        # numa janela de 1000px de altura um gradiente sutil demais fica
        # indistinguivel de cor lisa nos primeiros 80% e so aparece na
        # faixa final — foi o que aconteceu com #eef2f8 -> #d2dbe8
        fundo1="#e7edf6", fundo2="#d9e2ee", fundo3="#c7d3e3",
        roxo="#6b40d0", azul_fraco="rgba(76,110,245,0.13)",
        verde_fraco="rgba(12,166,120,0.13)", roxo_fraco="rgba(107,64,208,0.13)",
        luz1="rgba(76,110,245,0.10)", luz2="rgba(12,166,120,0.07)",
        # elevacao por luz, nao por cor. cartao continua SOLIDO para entry/lista
        vidro1="rgba(255,255,255,0.70)", vidro2="rgba(255,255,255,0.86)",
        vidro3="rgba(255,255,255,1.00)", luz_b="rgba(17,22,26,0.11)",
        barra="rgba(232,237,243,0.86)",
        hero_luz1="rgba(116,143,252,0.30)", hero_luz2="rgba(56,217,169,0.20)",
        # CROMO SEGUE O TEMA.
        # Antes a titlebar era escura nos DOIS temas — faixa preta no topo
        # de uma interface clara. Agora topo1/topo2 acompanham, e com isso
        # mudam de uma vez a titlebar (pilulas snippets/nova/inicio/INI), a
        # barra de abas e a headerbar dos dialogos: todas usam estes mesmos
        # tokens.
        #
        # O VIDRO INVERTE JUNTO. Sobre barra clara, branco translucido nao
        # aparece — resolve para a propria cor da barra. Quem modula aqui e
        # PRETO translucido. Este e o unico lugar do tema onde os valores de
        # vidro sao escuros.
        topo1="#f2f5f9", topo2="#e6ecf3",
        topo_txt="#11161a", topo_sec="#5b6976",
        vidro="rgba(17,22,26,0.05)", vidro_h="rgba(17,22,26,0.11)",
        vidro_b="rgba(17,22,26,0.12)",
        palco="#0b0f14", term_bg="#0d1117", term_fg="#d7dee6",
        sel="#11161a", sel_txt="#ffffff",
        hover="#eef2f5", campo="#ffffff",
        ok_bg="#dcf5ec", ok_fg="#0b7a63",
        erro_bg="#fde4e4", erro_fg="#b02a37", erro_h="#8d1f2a",
        neutro_bg="#eaeef1", neutro_fg="#5d6b78",
        atencao_bg="#fdf1d8", atencao_fg="#8a5a00",
        acao="#11161a", acao_txt="#ffffff", acao_hover="#2b333c",
        azul="#4c6ef5", azul_h="#3b5bdb", verde="#0ca678",
        hero1="#171c22", hero2="#2b3a4a", hero_txt="#ffffff",
    ),
    "escuro": dict(
        fundo="#10141a", cartao="#171b21", borda="#242a32", borda2="#333b45",
        texto="#d6dde5", sec="#94a1ae", fraco="#68757f",
        fundo1="#1a212b", fundo2="#10141a", fundo3="#080b0f",
        roxo="#b197fc", azul_fraco="rgba(125,149,251,0.16)",
        verde_fraco="rgba(79,209,176,0.16)", roxo_fraco="rgba(177,151,252,0.16)",
        luz1="rgba(116,143,252,0.16)", luz2="rgba(56,217,169,0.10)",
        vidro1="rgba(255,255,255,0.045)", vidro2="rgba(255,255,255,0.075)",
        vidro3="rgba(255,255,255,0.13)", luz_b="rgba(255,255,255,0.10)",
        barra="rgba(16,20,26,0.86)",
        hero_luz1="rgba(116,143,252,0.22)", hero_luz2="rgba(56,217,169,0.14)",
        topo1="#080a0d", topo2="#12171d",
        topo_txt="#ffffff", topo_sec="#9aa6b2",
        vidro="rgba(255,255,255,0.06)", vidro_h="rgba(255,255,255,0.13)",
        vidro_b="rgba(255,255,255,0.10)",
        palco="#06080a", term_bg="#0d1117", term_fg="#d7dee6",
        sel="#e8edf2", sel_txt="#0f1216",
        hover="#1e242b", campo="#171b21",
        ok_bg="#0d2f28", ok_fg="#4fd1b0",
        erro_bg="#341a1e", erro_fg="#c9414d", erro_h="#e05561",
        neutro_bg="#1e242b", neutro_fg="#9aa6b2",
        atencao_bg="#332810", atencao_fg="#e0b458",
        acao="#e8edf2", acao_txt="#0f1216", acao_hover="#ffffff",
        azul="#748ffc", azul_h="#91a7ff", verde="#38d9a9",
        hero1="#0b0e12", hero2="#1c2733", hero_txt="#ffffff",
    ),
}

MONO = '"IBM Plex Mono", "DejaVu Sans Mono", monospace'
SANS = '"IBM Plex Sans", "Inter", "Cantarell", sans-serif'
COND = '"IBM Plex Sans Condensed", "IBM Plex Sans", "Cantarell", sans-serif'

CSS_MOLDE = """
/* ---------------- fundo com luz ----------------
   O fundo deixou de ser cor chapada. Isto NAO e decoracao: sem um fundo
   que varie, todo widget translucido resolve para uma cor solida e o
   efeito de vidro simplesmente nao existe. As duas luzes vem de ACENTOS.
   Camadas comecam pela de cima; a linear e a base. */
/* SO a window leva o gradiente.
   ".fundo" tambem e usado em Box internas (a coluna do EditorConexao, por
   exemplo). Com o gradiente aplicado nelas, cada Box ganhava a PROPRIA
   copia das camadas, dimensionada a si mesma — e o spacing entre os blocos
   opacos virava uma tira clara atravessando o dialogo. Parecia barra de
   rolagem; era o fundo aparecendo na emenda. Box interna fica em cor lisa. */
/* TRANSPARENTE, nao cor lisa.
   Esta classe e usada em Box internas (a home, a coluna do EditorConexao).
   Com o gradiente aplicado nelas, cada Box ganhava a propria copia das
   camadas e o spacing entre blocos virava tira clara. Mas com COR LISA ela
   pinta por cima do gradiente da window e o vidro some — foi o que
   aconteceu na home. Transparente resolve os dois: nao duplica camada e
   deixa o fundo da janela aparecer. */
.fundo { background-color: transparent; color: %(texto)s; }

/* "window.background", nao so "window".
   O GTK adiciona a classe .background a toda janela de topo, e o Adwaita
   traz ".background { background-color: @theme_bg_color; }". Seletor de
   CLASSE (0,1,0) vence seletor de ELEMENTO (0,0,1): a regra do tema do
   sistema ganhava da nossa e o gradiente nunca chegava a ser pintado — o
   que se via era a cor lisa do Adwaita. Casar a classe empata a
   especificidade e, por vir depois, a nossa prevalece. */
window, window.background {
    background-color: %(fundo)s;
    background-image:
        linear-gradient(150deg, %(luz1)s 0%%, transparent 55%%),
        linear-gradient(15deg,  %(luz2)s 0%%, transparent 45%%),
        linear-gradient(170deg, %(fundo1)s 0%%, %(fundo2)s 52%%, %(fundo3)s 100%%);
    color: %(texto)s;
}

/* ---------------- titlebar de vidro ----------------
   O GTK3 nao tem backdrop-filter. O efeito vem de camadas translucidas
   sobre um gradiente escuro: sem sombra, sem relevo, so luz. */
headerbar.integrada {
    background-image: linear-gradient(180deg, %(topo1)s 0%%, %(topo2)s 100%%);
    background-color: %(topo1)s;
    border: none;
    box-shadow: none;
    min-height: 20px;
    padding: 0px 3px;
}
headerbar.integrada > * { box-shadow: none; text-shadow: none; }

.marca-topo { color: %(topo_txt)s; font-family: """ + COND + """; font-size: 13px; font-weight: 600; }
.marca-sub  { color: %(topo_sec)s; font-family: """ + MONO + """; font-size: 10px; }

/* UM padding so. Havia dois nesta regra e o segundo vencia, entao os
   ajustes no primeiro nao surtiam efeito nenhum. A fonte fica em 10px: o
   que encolhe a pilula e o padding, nao o texto. */
.btn-topo {
    background-image: none;
    background-color: %(vidro)s;
    color: %(topo_sec)s;
    border: 1px solid %(vidro_b)s;
    border-radius: 5px;
    padding: 0px 5px;
    margin: 0px;
    /* altura PROPRIA, menor que a barra. So funciona junto com o
       set_valign(CENTER) no widget: sem ele o botao estica e este valor e
       ignorado. */
    min-height: 18px; min-width: 0px;
    box-shadow: none; text-shadow: none;
    font-family: """ + MONO + """; font-size: 10px;
    transition: background-color 140ms ease, color 140ms ease;
}
.btn-topo:hover   { background-color: %(vidro_h)s; color: %(topo_txt)s; }

/* Botoes de janela (minimizar/maximizar/fechar) igualados a pilula POR
   CONSTRUCAO, em vez de eu chutar um numero que "parece" igual: mesmas
   metricas, mesmo raio, mesma margem. Assim a area de hover deles e a
   pilula ficam na mesma linha, sempre. */
headerbar.integrada button.titlebutton {
    min-height: 18px; min-width: 18px;
    padding: 0px 6px; margin: 0px;
    border-radius: 5px;
    background-image: none; background-color: transparent;
    border: 1px solid transparent; box-shadow: none;
    color: %(topo_sec)s;
}
headerbar.integrada button.titlebutton:hover {
    background-color: %(vidro_h)s; color: %(topo_txt)s;
}
headerbar.integrada button.titlebutton.close:hover {
    background-color: #d13438; color: #ffffff;
}
.btn-topo:active,
.btn-topo:checked { background-color: %(vidro_h)s; color: %(topo_txt)s; border-color: %(topo_txt)s; }

.btn-janela {
    background-image: none; background-color: transparent;
    color: %(topo_sec)s; border: none; border-radius: 6px;
    padding: 2px 9px; min-height: 0px; min-width: 0px;
    box-shadow: none; text-shadow: none;
    font-family: """ + MONO + """; font-size: 12px;
    transition: background-color 140ms ease;
}
.btn-janela:hover { background-color: %(vidro_h)s; color: %(topo_txt)s; }
.btn-fechar:hover { background-color: #d13438; color: #ffffff; }

/* ---------------- texto ---------------- */
.rotulo     { color: %(sec)s;   font-family: """ + MONO + """; font-size: 10px; }
.secundario { color: %(sec)s;   font-family: """ + MONO + """; font-size: 11px; }
.fraco      { color: %(fraco)s; font-family: """ + MONO + """; font-size: 10px; }
.mono       { color: %(texto)s; font-family: """ + MONO + """; font-size: 12px; }
.titulo-secao {
    color: %(fraco)s; font-family: """ + MONO + """;
    font-size: 10px; font-weight: 600;
}

.cartao { background-color: %(cartao)s; border: 1px solid %(borda)s; border-radius: 6px; }

/* dialogos: sem isto o corpo fica com a cor crua do tema do sistema e o
   conjunto vira uma colcha de retalhos */
/* GTK3: GtkDialog tem no CSS chamado "window" com classe ".dialog" — nao
   existe elemento de tipo "dialog". Selecionar por "dialog X" nunca casava
   com nada; o fallback era sempre o tema do sistema. Por isso a classe
   propria .acessos-dialogo, aplicada a mao em cada Gtk.Dialog criado. */
/* corpo, blocos e campos na MESMA cor: a separacao vem das bordas, nao de
   tres tons diferentes competindo entre si */
dialog, .acessos-dialogo box, .acessos-dialogo .fundo, .acessos-dialogo scrolledwindow, .acessos-dialogo viewport {
    background-color: %(cartao)s; color: %(texto)s;
}
.acessos-dialogo label { color: %(texto)s; }
.acessos-dialogo entry {
    min-height: 28px;
    background-color: %(cartao)s;
    color: %(texto)s;
    border: 1px solid %(borda2)s;
}
.acessos-dialogo entry:focus { border-color: %(azul)s; }

/* cabecalho do dialogo: liso, mesma cor do corpo. Um gradiente proprio aqui
   destoava de tudo e parecia um enxerto de outro programa. */
headerbar.dlg-topo {
    background-image: none;
    background-color: %(cartao)s;
    border: none; border-bottom: 1px solid %(borda)s;
    box-shadow: none; min-height: 40px; padding: 0px 8px;
}
.acessos-dialogo headerbar .title, .acessos-dialogo headerbar > box > label {
    color: %(texto)s;
    font-family: """ + COND + """; font-size: 15px; font-weight: 600;
}

/* O GTK aplica seu proprio estilo a botoes dentro de headerbar e vence a
   classe .acao: o fundo ficava escuro com a letra tambem escura. Estas
   regras sao mais especificas e reescrevem cor de fundo E de texto,
   inclusive a do label interno. */
headerbar.dlg-topo button {
    background-image: none;
    box-shadow: none; text-shadow: none;
}
headerbar.dlg-topo button.acao,
.acessos-dialogo button.acao {
    background-color: %(azul)s;
    border: 1px solid %(azul)s;
}
headerbar.dlg-topo button.acao label,
.acessos-dialogo button.acao label {
    color: #ffffff;
    background-color: transparent;
}
headerbar.dlg-topo button.acao:hover,
.acessos-dialogo button.acao:hover { background-color: %(azul_h)s; border-color: %(azul_h)s; }

headerbar.dlg-topo button:not(.acao),
.acessos-dialogo .area-acao button:not(.acao) {
    background-color: %(cartao)s;
    border: 1px solid %(borda2)s;
}
.acessos-dialogo .area-acao { background-color: %(cartao)s; border-top: 1px solid %(borda)s; }
headerbar.dlg-topo button:not(.acao) label,
.acessos-dialogo .area-acao button:not(.acao) label { color: %(texto)s; }
headerbar.dlg-topo button:not(.acao):hover,
.acessos-dialogo .area-acao button:not(.acao):hover { background-color: %(hover)s; }

/* botoes na area de acao (dialogos sem headerbar, como o de confirmacao) */
.acessos-dialogo .area-acao button {
    background-image: none; box-shadow: none;
}

/* combo e switch dentro do dialogo */
.acessos-dialogo combobox, .acessos-dialogo combobox box, .acessos-dialogo combobox entry {
    background-color: %(cartao)s; color: %(texto)s;
}
.acessos-dialogo combobox button {
    background-image: none; background-color: %(cartao)s;
    color: %(texto)s; border: 1px solid %(borda2)s; border-radius: 6px;
    box-shadow: none; min-height: 28px;
}
.acessos-dialogo combobox button label { color: %(texto)s; }
.acessos-dialogo combobox entry { border-right-width: 0px; }
/* O container nao precisa de moldura: com corpo e blocos na mesma cor, a
   borda externa so criava um retangulo solto. Quem delimita e o campo. */
.bloco {
    background-color: transparent;
    border: 1px solid transparent;
    border-radius: 8px;
}
.bloco entry {
    background-color: %(cartao)s;
    border: 1px solid %(borda2)s;
    border-radius: 6px;
}
.bloco-cab {
    color: %(sec)s; font-family: """ + MONO + """;
    font-size: 10px; font-weight: 600;
}
.bloco-off { opacity: 0.45; }
.dica { color: %(fraco)s; font-family: """ + MONO + """; font-size: 9px; }
.regua  { min-height: 2px; }

/* ---------------- chips ---------------- */
/* padding VERTICAL menor, fonte intacta: a barra de sessao herdava a
   altura do chip, nao do texto */
.chip {
    font-family: """ + MONO + """; font-size: 10px; font-weight: 600;
    border-radius: 4px; padding: 0px 7px;
}
.chip-ok      { background-color: %(ok_bg)s;      color: %(ok_fg)s; }
.chip-erro    { background-color: %(erro_bg)s;    color: %(erro_fg)s; }
.chip-neutro  { background-color: %(neutro_bg)s;  color: %(neutro_fg)s; }
.chip-atencao { background-color: %(atencao_bg)s; color: %(atencao_fg)s; }

/* A faixa de decisao do canario e uma PERGUNTA que trava o lote: ficava
   com a cor de cartao comum, igual a tudo. Ambar e a cor de "precisa de
   voce" no resto da interface. */
.faixa-decisao {
    background-color: %(atencao_bg)s;
    border: 1px solid %(atencao_fg)s;
    border-radius: 8px;
}
.faixa-decisao label { color: %(atencao_fg)s; }

/* ---------------- botoes ---------------- */
/* Em qualquer botao com filho Label o GTK pinta o LABEL, e a cor da classe
   no botao e ignorada — foi o que produziu preto sobre preto. Toda regra de
   cor aqui vale para o botao E para o label interno. */
.acao {
    background-image: none; background-color: %(acao)s; color: %(acao_txt)s;
    border: 1px solid %(acao)s; border-radius: 6px;
    padding: 5px 12px; min-height: 0px; box-shadow: none;
    font-family: """ + SANS + """; font-size: 12px; font-weight: 600;
    transition: background-color 140ms ease;
}
.acao label            { color: %(acao_txt)s; background-color: transparent; }
/* O anel de foco do GTK3 e um outline TRACEJADO desenhado sobre o rotulo.
   "outline: none" nao basta em todos os temas: quem desliga de fato e
   outline-style, e ele precisa valer tambem para o label interno e para o
   estado de foco. */
button, button label,
button:focus, button:focus label,
button:hover, button:active, button:checked,
.acessos-dialogo button, .acessos-dialogo button label,
.acessos-dialogo button:focus, .acessos-dialogo button:focus label {
    outline-style: none;
    outline-width: 0px;
    outline-color: transparent;
    outline-offset: 0px;
    -gtk-outline-radius: 0px;
    /* O tema (Adwaita) desenha um text-shadow sutil no texto de botoes com
       fundo solido — e o "borrao"/sombra suave no "Tela"/"RDP" dos cards.
       So tinha sido zerado para botoes DE DIALOGO ("ULTIMA PALAVRA" mais
       abaixo); faltava aqui, no bloco que vale para o app inteiro. */
    text-shadow: none;
}
/* Alem do outline, o tema desenha um box-shadow inset no botao com foco —
   e ele que produz o retangulo escuro DENTRO do azul. */
button:focus, button:hover, button:active, button:checked,
.acessos-dialogo button, .acessos-dialogo button:focus, .acessos-dialogo button:hover,
.acessos-dialogo button:active, headerbar.dlg-topo button:focus {
    box-shadow: none;
}
/* O botao de resposta padrao ganha a moldura propria do tema — e ela que
   sobrava em volta de "Salvar" enquanto "Cancelar" ficava limpo.
   ATENCAO: no GTK3 isto e a CLASSE .default, nao a pseudo-classe :default,
   que nao existe e faz o CssProvider recusar a folha inteira. */
button.default, button.default label,
.acessos-dialogo button.default, .acessos-dialogo button.default label,
headerbar.dlg-topo button.default {
    box-shadow: none;
    outline-style: none;
    outline-width: 0px;
    outline-color: transparent;
}
.acessos-dialogo button.acao.default,
headerbar.dlg-topo button.acao.default {
    background-color: %(azul)s;
    border: 1px solid %(azul)s;
}
.acessos-dialogo button.acao:focus,
.acessos-dialogo button.acao.default:focus { border-color: %(azul)s; }

/* mesma geometria em todos os botoes de dialogo: sem isto o primario da
   headerbar usava padding diferente do .perigo e um parecia maior */
headerbar.dlg-topo button, .acessos-dialogo .area-acao button, .acessos-dialogo button {
    padding: 5px 12px;
    border-radius: 6px;
    min-height: 0px;
    font-family: """ + SANS + """; font-size: 12px; font-weight: 600;
}

/* negacao e desfazimento: vermelho com texto legivel */
.perigo {
    background-image: none;
    background-color: %(erro_fg)s;
    border: 1px solid %(erro_fg)s;
    border-radius: 6px;
    padding: 5px 12px; min-height: 0px; box-shadow: none;
    font-family: """ + SANS + """; font-size: 12px; font-weight: 600;
    transition: background-color 140ms ease;
}
.perigo.default, .perigo.default label { box-shadow: none; outline-style: none; }
.perigo label          { color: #ffffff; background-color: transparent; }
.perigo:hover          { background-color: %(erro_h)s; border-color: %(erro_h)s; }
/* variante de contorno: repouso discreto, vermelho cheio no hover — o
   aviso chega ANTES do clique, que e o unico momento em que ele serve */
.perigo-leve, .perigo-leve label {
    background-color: %(erro_bg)s; color: %(erro_fg)s;
    background-image: none; box-shadow: none;
}
.perigo-leve { border: 1px solid %(erro_fg)s; }
.perigo-leve:hover, .perigo-leve:hover label {
    background-color: %(erro_fg)s; color: #ffffff;
}
.perigo:hover label    { color: #ffffff; }
.perigo:focus          { box-shadow: none; }   /* nao mexe em borda: mudaria o tamanho */
.acessos-dialogo button.perigo,
.acessos-dialogo .area-acao button.perigo,
headerbar.dlg-topo button.perigo {
    background-color: %(erro_fg)s;
    border: 1px solid %(erro_fg)s;
}
.acessos-dialogo button.perigo label,
.acessos-dialogo .area-acao button.perigo label,
headerbar.dlg-topo button.perigo label { color: #ffffff; }
.acessos-dialogo button.perigo:hover,
.acessos-dialogo .area-acao button.perigo:hover,
headerbar.dlg-topo button.perigo:hover {
    background-color: %(erro_h)s; border-color: %(erro_h)s;
}
.acao:hover            { background-color: %(acao_hover)s; }  /* so cor */

/* aviso de atualizacao no rodape: ambar e a cor de "precisa de voce" no
   resto da interface (ver .faixa-decisao acima) — o botao usa a mesma
   linguagem visual para o mesmo tipo de aviso. */
.btn-atualizacao, .btn-atualizacao label {
    background-color: %(atencao_bg)s; color: %(atencao_fg)s;
    background-image: none; box-shadow: none;
}
.btn-atualizacao {
    border: 1px solid %(atencao_fg)s; border-radius: 6px;
    padding: 2px 10px; min-height: 0px;
    font-family: """ + SANS + """; font-size: 11px; font-weight: 600;
}
.btn-atualizacao:hover, .btn-atualizacao:hover label {
    background-color: %(atencao_fg)s; color: %(atencao_bg)s;
}

/* estado de repouso do mesmo botao: so a versao instalada, discreta, no
   canto inferior esquerdo — vira .btn-atualizacao quando ha novidade. */
.btn-versao, .btn-versao label {
    background-color: transparent; color: %(fraco)s;
    background-image: none; box-shadow: none;
}
.btn-versao {
    border: 1px solid transparent; border-radius: 6px;
    padding: 2px 10px; min-height: 0px;
    font-family: """ + MONO + """; font-size: 11px;
}
.btn-versao:hover, .btn-versao:hover label {
    background-color: %(hover)s; color: %(texto)s;
}
.acao:hover label      { color: %(acao_txt)s; }
.acao:disabled         { background-color: %(borda)s; border-color: %(borda)s; color: %(fraco)s; }
.acao:disabled label   { color: %(fraco)s; }

.secundaria label      { color: %(texto)s; background-color: transparent; }
.secundaria:disabled label { color: %(fraco)s; }
.tog label             { color: inherit; background-color: transparent; }
.seg label             { color: inherit; background-color: transparent; }
.btn-topo label        { color: inherit; background-color: transparent; }
.btn-janela label      { color: inherit; background-color: transparent; }

.secundaria {
    background-image: none; background-color: %(cartao)s; color: %(texto)s;
    border: 1px solid %(borda2)s; border-radius: 5px;
    padding: 1px 9px; min-height: 0px; box-shadow: none;
    font-family: """ + MONO + """; font-size: 11px;
    transition: background-color 140ms ease;
}
.secundaria:hover    { background-color: %(hover)s; }
.secundaria:disabled { color: %(fraco)s; }

/* toggles semanticos: apagado = inativo */
.tog {
    background-image: none; background-color: %(cartao)s;
    border: 1px solid %(borda2)s; border-radius: 5px;
    /* barra de sessao e informativa: padding vertical ZERO e altura
       travada em 20px. O texto continua em 11px — o que encolhe e a caixa. */
    padding: 0px 8px; min-height: 20px; color: %(fraco)s; box-shadow: none;
    font-family: """ + MONO + """; font-size: 11px;
    font-weight: 600;   /* fixo: variar por estado mudava a metrica do texto
                           e podia disparar laco de realocacao */
    transition: background-color 140ms ease, color 140ms ease;
}
.tog:hover { background-color: %(hover)s; }
/* glifo isolado precisa de corpo maior que o texto, mas sem inflar a barra */
/* altura FIXA, nao apenas minima: este botao troca de glifo entre estados
   e glifos de emoji tem alturas diferentes — sem travar, a barra de sessao
   inteira mudava de altura a cada clique */
.tog-glifo { font-size: 13px; padding: 0px 7px; min-height: 20px; }
.tog-bloq:checked, .tog-bloq:checked:hover {
    background-color: %(erro_bg)s; border-color: %(erro_fg)s;
    color: %(erro_fg)s;
}
.tog-ok:checked, .tog-ok:checked:hover {
    background-color: %(ok_bg)s; border-color: %(ok_fg)s;
    color: %(ok_fg)s;
}

/* ---------------- segmentado (substitui o ComboBox) ---------------- */
.seg {
    background-image: none; background-color: %(cartao)s;
    color: %(sec)s; border: 1px solid %(borda2)s;
    border-radius: 0px; padding: 0px 9px; min-height: 20px;
    box-shadow: none; font-family: """ + MONO + """; font-size: 11px;
    font-weight: 600;   /* fixo: variar por estado mudava a metrica do texto
                           e disparava laco de realocacao dentro de dialogos */
    transition: background-color 140ms ease, color 140ms ease;
}
.seg:hover { background-color: %(hover)s; }
.seg:checked, .seg:checked:hover {
    background-color: %(acao)s; color: %(acao_txt)s; border-color: %(acao)s;
}
.seg-ini { border-top-left-radius: 6px; border-bottom-left-radius: 6px; }
.seg-fim { border-top-right-radius: 6px; border-bottom-right-radius: 6px; }
.seg-meio { border-left-width: 0px; border-right-width: 0px; }

/* ---------------- areas ---------------- */
.palco { background-color: %(palco)s; }
.log, .log text {
    background-color: %(term_bg)s; color: %(term_fg)s;
    font-family: """ + MONO + """; font-size: 11px;
    /* caret-color NAO herda de `color` no GTK3: sem declarar, o cursor
       fica na cor do tema (escura) sobre o fundo escuro do terminal e
       some. Aparecia como "campo sem cursor" ao editar o comando. */
    caret-color: %(term_fg)s;
    -gtk-secondary-caret-color: %(term_fg)s;
}
/* ---------------- listas (TreeView) ----------------
   Antes so a cor de fundo era definida; cabecalho, linhas e bordas ficavam
   com o Adwaita puro. Era isso que fazia o massa_ui parecer de outro app:
   duas TreeView com cabecalho cinza de sistema no meio da interface. */
.lista { background-color: %(cartao)s; color: %(texto)s; }
/* os quadros de COMANDOS e MAQUINAS pareciam soltos: cartao branco puro
   sobre fundo azulado, sem raio e sem borda. Agora seguem o mesmo cartao
   translucido dos tiles. */
.cartao {
    background-color: %(vidro2)s;
    border: 1px solid %(luz_b)s;
    border-radius: 8px;
}
.titulo-secao {
    font-family: """ + MONO + """; font-size: 9px;
    letter-spacing: 1px; color: %(fraco)s;
}
.lista:selected { background-color: %(sel)s; color: %(sel_txt)s; }

.lista header button {
    background-image: none;
    background-color: %(cartao)s;
    color: %(fraco)s;
    font-family: """ + MONO + """;
    font-size: 10px;
    font-weight: normal;
    border: none;
    border-bottom: 1px solid %(borda)s;
    border-radius: 0px;
    box-shadow: none;
    text-shadow: none;
    padding: 6px 8px;
    min-height: 0px;
}
.lista header button:hover { background-color: %(hover)s; color: %(sec)s; }

/* linha zebrada de leve: com 31 maquinas o olho perde a linha sem isso */
.lista:hover { background-color: %(hover)s; }
entry {
    background-image: none; background-color: %(campo)s; color: %(texto)s;
    border: 1px solid %(borda2)s; border-radius: 6px; box-shadow: none;
}
/* ---------------- containers transparentes ----------------
   Todo container entre a window e o conteudo precisa deixar o gradiente
   passar. Um unico opaco no meio tampa o fundo inteiro, e o sintoma nao
   parece com a causa: vira "gradiente que nao pegou".

   Ha versao por ELEMENTO e por CLASSE de proposito. O Adwaita estiliza
   esses widgets por classe, e classe vence elemento na especificidade —
   entao a regra generica sozinha perde. A classe "transparente" e aplicada
   no ScrolledWindow da home pelo acessos.py.

   (Este bloco ja existiu e foi apagado sem querer por uma edicao que
   substituiu um intervalo de texto grande demais. Se sumir de novo, o
   sintoma e exatamente este: fundo chapado com o gradiente aparecendo so
   numa faixa fora do ScrolledWindow.) */
notebook, notebook > stack { background-color: transparent; }
scrolledwindow, viewport, flowbox { background-color: transparent; }
scrolledwindow > viewport > box { background-color: transparent; }

.transparente,
.transparente > viewport,
.transparente > viewport > box,
scrolledwindow.transparente,
scrolledwindow.transparente > viewport {
    background-color: transparent;
    background-image: none;
    border: none;
    box-shadow: none;
}

/* BARRA DE ABAS ESCURA.
   Era aqui que estava a diferenca para a proposta: com a barra em
   %(fundo)s (claro), pastilha de vidro branco fica da mesma cor da aba
   ativa e todas parecem selecionadas. A barra usa o mesmo gradiente da
   titlebar, e ai o vidro tem o que modular. */
notebook > header {
    background-image: linear-gradient(180deg, %(topo1)s 0%%, %(topo2)s 100%%);
    background-color: %(topo1)s;
    border: none;
    border-bottom: 1px solid %(borda2)s;
}
notebook > header > tabs { padding: 0px; }
notebook > header > tabs > tab {
    background-color: %(vidro)s; color: %(topo_sec)s;
    border: 1px solid transparent;
    border-radius: 7px;
    padding: 1px 7px; min-height: 0px; margin: 2px 1px;
    box-shadow: none;
}
notebook > header > tabs > tab:hover { background-color: %(vidro_h)s; color: %(topo_txt)s; }
/* ABA ATIVA — UMA forma so.
   Antes o tab:checked pintava um retangulo arredondado E o Box interno
   desenhava o trilho: duas formas concentricas, com o trilho sem alcancar
   as bordas do retangulo. Parecia aba dentro de aba.
   Agora o proprio tab leva fundo E trilho; o Box interno nao desenha nada. */
/* Aba ativa: vidro mais forte + trilho inset. box-shadow nao ocupa espaco
   de layout, entao a altura NAO muda entre os estados — com border a aba
   ganhava 2px e "descia". */
notebook > header > tabs > tab:checked {
    background-color: %(vidro_h)s;
    color: %(topo_txt)s;
    border-color: %(vidro_b)s;
    box-shadow: inset 0px -2px 0px %(azul)s;
}

.aba-nome { font-family: """ + MONO + """; font-size: 11px; color: inherit; }

/* O selo de texto (VNC/SSH/RDP) virou icone.
   A palavra se repetia em toda aba e nao conversava com nada; o icone e o
   MESMO do card, na mesma cor, entao a aba deixa de ser lida e passa a ser
   reconhecida. Fundo tenue da cor do protocolo, glifo na cor cheia. */
/* caixa FIXA para todos: sem isto o icone de lista (que tem proporcao
   diferente do monitor) desalinhava a aba inteira em relacao as outras */
.aba-tipo {
    font-family: """ + MONO + """; font-size: 9px; font-weight: 600;
    border-radius: 5px; padding: 0px;
    min-width: 18px; min-height: 18px;
}
.aba-vnc  { color: %(azul)s;       background-color: %(azul_fraco)s; }
.aba-ssh  { color: %(verde)s;      background-color: %(verde_fraco)s; }
.aba-rdp  { color: %(roxo)s;       background-color: %(roxo_fraco)s; }
.aba-sftp { color: %(atencao_fg)s; background-color: %(atencao_bg)s; }
.aba-massa{ color: %(atencao_fg)s; background-color: %(atencao_bg)s; }

/* TRILHO DA ABA ATIVA.
   Com icone colorido em toda aba, a cor ja esta ocupada identificando o
   protocolo — marcar selecao com mais cor competiria com essa leitura. Por
   isso a selecao usa POSICAO: um trilho de 2px embaixo, na cor do protocolo.

   A largura da borda entra e sai, mas o padding compensa: a altura total
   nao muda. Se a aba crescesse ao ser escolhida, clicar numa empurraria as
   outras de lugar — insuportavel com seis sessoes abertas. */
/* O trilho por protocolo teve que sair: a cor dele so pode viver no no
   "tab", e o GTK/CSS nao deixa um filho pintar a borda do pai. Cor de
   protocolo continua no icone, que e onde ela informa; o trilho da aba
   ativa e um acento unico. Perde-se pouco e some a forma dupla. */
.aba-cab { padding-bottom: 0px; }

/* barra de filtro: fica sobre a lista rolando, entao usa vidro e nao a cor
   do fundo — com fundo chapado ela sumiria dentro da pagina */
/* ---------------- icones-acao do card ----------------
   Indicador e botao no mesmo controle: cor = configurado, apagado = nao
   configurado e nao clicavel. Estado muda so COR — nunca dimensao. */
.card-ico {
    background-image: none;
    background-color: %(vidro)s;
    border: 1px solid %(luz_b)s;
    border-radius: 7px;
    padding: 0px;
    min-height: 26px; min-width: 0px;
    box-shadow: none; text-shadow: none;
}
.card-ico:hover { background-color: %(hover)s; border-color: %(borda2)s; }
.card-ico-vnc  { color: %(azul)s; }
.card-ico-ssh  { color: %(verde)s; }
.card-ico-rdp  { color: %(roxo)s; }
.card-ico-sftp { color: %(atencao_fg)s; }
.card-ico-off  { color: %(fraco)s; background-color: transparent; }
.card-ico-off:hover { background-color: transparent; border-color: %(luz_b)s; }
.card-ico-txt  { font-family: """ + MONO + """; font-size: 9px; }

/* Icone do cabecalho de bloco: MESMAS cores dos icones-acao do card e dos
   selos de aba. Um protocolo tem uma cor so em toda a interface. */
.bloco-ico-vnc  { color: %(azul)s; }
.bloco-ico-ssh  { color: %(verde)s; }
.bloco-ico-rdp  { color: %(roxo)s; }
.bloco-ico-off  { color: %(fraco)s; }
.bloco-ico      { min-width: 18px; min-height: 18px; }

/* cabecalho do bloco: o botao INTEIRO e o alvo, sem relevo em repouso */
.bloco-cabbt {
    background-image: none; background-color: transparent;
    border: 1px solid transparent; border-radius: 7px;
    box-shadow: none; text-shadow: none;
    padding: 3px 6px; min-height: 0px;
}
.bloco-cabbt:hover { background-color: %(hover)s; border-color: %(borda)s; }
.bloco-aberto { background-color: %(vidro2)s; border-color: %(borda2)s; }

/* X proprio do dialogo: sem circulo, sem imagem do tema, vermelho so no
   hover — o "titlebutton" do sistema nao aceitava ser domado por CSS */
/* PRECISA ser "headerbar.dlg-topo button.dlg-x" (0,1,2) e nao ".dlg-x"
   (0,1,0): a regra generica "headerbar.dlg-topo button" acima tem 0,1,1 e
   venceria, deixando o X com a cara dos demais botoes da barra. */
headerbar.dlg-topo button.dlg-x {
    background-image: none; background-color: transparent;
    border: 1px solid transparent; box-shadow: none; text-shadow: none;
    border-radius: 6px; padding: 2px 8px;
    min-height: 22px; min-width: 26px;
    font-family: """ + SANS + """; font-size: 12px; font-weight: 700;
    color: %(topo_sec)s;
}
/* o label fica SEMPRE transparente; quem pinta e o botao. Pintando o label
   o vermelho cobria so a caixa do glifo — aquela tirinha minuscula. */
headerbar.dlg-topo button.dlg-x label { color: %(topo_sec)s; background-color: transparent; }
/* hover VERMELHO. Estava cinza porque a regra pintava so o botao: em
   qualquer botao com filho Label o GTK pinta o LABEL e ignora a cor da
   classe no botao — a mesma armadilha ja anotada nos botoes normais. */
/* HOVER: vermelho no GLIFO, nao branco sobre vermelho.
   Branco sobre fundo vermelho depende de DUAS regras acertarem ao mesmo
   tempo — a do botao e a do label. Se a do label falhar, sobra X branco em
   fundo claro, ou seja, invisivel. Colorindo o glifo, uma regra so ja
   resolve e nao ha como sumir. */
headerbar.dlg-topo button.dlg-x:hover,
headerbar.dlg-topo button.dlg-x:hover label {
    background-color: %(erro_bg)s;
    color: %(erro_fg)s;
    background-image: none;
}
headerbar.dlg-topo button.dlg-x:hover {
    border-color: %(erro_fg)s;
}
headerbar.dlg-topo button.dlg-x:hover label {
    background-color: transparent;
}
/* pressionado: ai sim vermelho cheio, com o glifo branco por cima */
headerbar.dlg-topo button.dlg-x:active,
headerbar.dlg-topo button.dlg-x:active label {
    background-color: %(erro_fg)s;
    color: #ffffff;
}
headerbar.dlg-topo button.dlg-x:active label {
    background-color: transparent;
}

.barra-filtro {
    background-color: %(barra)s;
    border-bottom: 1px solid %(borda)s;
}

/* ---------------- contadores do massa_ui ----------------
   Mesma tipografia dos contadores do hero: e a mesma pergunta ("quanto de
   que"), entao e a mesma forma. Cor so nos que carregam juizo — ok, falha,
   fila; o total fica neutro. */
.massa-num {
    font-family: """ + COND + """;
    font-size: 20px;
    font-weight: 600;
    color: %(texto)s;
}
.massa-ok   { color: %(ok_fg)s; }
.massa-err  { color: %(erro_fg)s; }
.massa-fila { color: %(atencao_fg)s; }
.massa-cap {
    font-family: """ + MONO + """;
    font-size: 9px;
    color: %(fraco)s;
    padding-top: 1px;
}

/* HEADERBAR DO DIALOGO.
   O seletor antigo exigia a classe (.dlg-topo) e nao pegava — o resultado
   era headerbar branca do sistema com um X circular cinza por cima. Aqui a
   regra vale para QUALQUER headerbar dentro de .acessos-dialogo, com a
   classe apenas reforcando.

   O X e "button.titlebutton" no Adwaita atual, nao "button.close": era por
   isso que ele continuava com o fundo redondo do tema. */
headerbar.dlg-topo {
    background-image: linear-gradient(180deg, %(topo1)s 0%%, %(topo2)s 100%%);
    background-color: %(topo1)s;
    border: none; box-shadow: none; min-height: 34px; padding: 0px 6px;
}
headerbar.dlg-topo > * { box-shadow: none; text-shadow: none; }
headerbar.dlg-topo label, .dlg-topo-titulo {
    font-family: """ + COND + """; font-size: 13px; font-weight: 600;
    color: %(topo_txt)s; background-color: transparent;
}
/* o no ".title" do tema traz fundo e moldura; por isso o titulo e um
   Gtk.Label nosso via set_custom_title, e este no fica neutralizado */
headerbar.dlg-topo .title {
    background-color: transparent; background-image: none;
    border: none; box-shadow: none; padding: 0px;
}
headerbar.dlg-topo button,
headerbar.dlg-topo button.titlebutton {
    background-image: none; background-color: transparent;
    border: none; box-shadow: none; border-radius: 6px;
    padding: 2px 8px; min-height: 0px; min-width: 0px;
    color: %(topo_sec)s;
}
headerbar.dlg-topo button label,
headerbar.dlg-topo button.titlebutton label {
    color: %(topo_sec)s; background-color: transparent;
    font-family: """ + MONO + """; font-size: 11px; font-weight: normal;
}
headerbar.dlg-topo button:hover,
headerbar.dlg-topo button.titlebutton:hover {
    background-color: %(vidro_h)s;
}
headerbar.dlg-topo button.titlebutton:hover { background-color: #d13438; }
headerbar.dlg-topo button.titlebutton:hover label { color: #ffffff; }
/* a borda inferior da titlebar so aparece quando ela e clara; no escuro o
   proprio gradiente ja separa */
headerbar.dlg-topo { border-bottom: 1px solid %(borda2)s; }

/* ---------------- formatos de dialogo (dialogo_ui.py) ----------------
   Diálogo fica OPACO de proposito. Vidro aqui atrapalha: e modal, fica sobre
   a janela, e o conteudo aparecendo por tras do texto que voce precisa ler
   e exatamente o que nao se quer. Peso vem da sombra, nao da transparencia. */
.dlg-titulo {
    font-family: """ + COND + """;
    font-size: 15px;
    font-weight: 600;
    color: %(texto)s;
    padding-bottom: 2px;
}
.dlg-texto { color: %(sec)s; font-size: 12px; }
.dlg-dica {
    font-family: """ + MONO + """;
    font-size: 10px;
    color: %(fraco)s;
    padding-top: 4px;
}
/* validacao inline: o erro nasce no lugar da dica, sob o campo, e o dialogo
   continua de pe — nunca um segundo dialogo empilhado sobre o primeiro */
.dlg-dica-erro { color: %(erro_fg)s; }

.dlg-erro   .dlg-titulo { color: %(erro_fg)s; }
.dlg-aviso  .dlg-titulo { color: %(atencao_fg)s; }

.rodape-info { color: %(fraco)s; font-family: """ + MONO + """; font-size: 11px; }

/* ---------------- dashboard ---------------- */
/* Faixa de topo: sem raio e sem margem, encosta nas bordas da area de
   conteudo. Um cartao flutuante ali deixava a pagina sem ancora. */
.hero {
    background-image:
        linear-gradient(160deg, %(hero_luz1)s 0%%, transparent 60%%),
        linear-gradient(20deg,  %(hero_luz2)s 0%%, transparent 50%%),
        linear-gradient(115deg, %(hero1)s 0%%, %(hero2)s 100%%);
    background-color: %(hero1)s;
    border-radius: 0px;
    border-bottom: 1px solid %(hero1)s;
}
.hero-eyebrow {
    color: %(topo_sec)s;
    font-family: """ + MONO + """; font-size: 10px; font-weight: 600;
}
.hero-titulo { color: %(hero_txt)s; font-family: """ + COND + """; font-size: 32px; font-weight: 600; }
.hero-sub    { color: %(topo_sec)s; font-family: """ + MONO + """; font-size: 10px; }
/* credito: mesma altura de linha do caminho do INI, mais apagado ainda —
   presente sem disputar atencao com a informacao util */
.hero-credito {
    color: %(topo_sec)s;
    font-family: """ + MONO + """;
    font-size: 10px;
    opacity: 0.55;
}
.hero-num    { color: %(hero_txt)s; font-family: """ + COND + """; font-size: 27px; font-weight: 600; }
.hero-cap    { color: %(topo_sec)s; font-family: """ + MONO + """; font-size: 9px; }
.hero-risco  { opacity: 0.14; }

/* EventBox com janela propria precisa de fundo explicito, senao pinta a cor
   crua do tema por baixo dos cards */
.evt { background-color: transparent; }

/* Elevacao por luz, nao por cor: repouso = vidro2, hover = vidro3. Como o
   fundo por baixo tem gradiente, o mesmo card nao fica identico no topo e
   na base da lista — e isso que o olho le como vidro. A borda e de LUZ
   (luz_b), nao a borda cinza: borda cinza sobre translucido denuncia o
   truque. Estado muda so COR, nunca dimensao. */
.card {
    background-color: %(vidro2)s;
    border: 1px solid %(luz_b)s;
    border-radius: 9px;
}
.card:hover { border-color: %(borda2)s; background-color: %(vidro3)s; }

/* card fantasma da conexao instantanea: borda tracejada diz "isto nao esta
   no INI" sem precisar de texto */
.card-efemero {
    border: 1px dashed %(atencao_fg)s;
    background-color: %(atencao_bg)s;
}
.card-nome  { color: %(texto)s; font-family: """ + COND + """; font-size: 17px; font-weight: 600; }
.card-host  { color: %(sec)s;   font-family: """ + MONO + """; font-size: 12px; }
.card-meta  { color: %(fraco)s; font-family: """ + MONO + """; font-size: 9px; }

/* caixa de selecao do lote: discreta, some no fundo do card ate ser
   marcada — nao deve competir com o nome da maquina */
.marca-lote check {
    min-height: 13px; min-width: 13px;
    border-radius: 3px;
    border: 1px solid %(borda2)s;
    background-image: none;
    background-color: %(cartao)s;
    box-shadow: none;
}
.marca-lote check:checked {
    background-color: %(azul)s;
    border-color: %(azul)s;
    color: #ffffff;
}
.trilho     { border-radius: 2px; min-width: 3px; }

/* Linha de vida ao lado do trilho do grupo. Neutra ate a sonda responder —
   cinza NAO quer dizer offline, quer dizer "ainda nao sei", e essa
   diferenca importa: pintar de vermelho antes de checar seria mentira. */
/* cinza VISIVEL: com %(borda)s a linha sumia dentro do card branco e
   parecia que o indicador nao existia */
.trilho-vida { background-color: %(fraco)s; }

/* linha de opcao em painel: texto no corpo, nao em mono */
.opcao-txt { color: %(texto)s; font-size: 12.5px; }

/* separador horizontal entre secoes de um dialogo. borda2, nao borda: a
   borda fraca e quase invisivel sobre o cartao (#e3e8ec no branco), e as
   secoes do painel de Ajustes pareciam jogadas sem nenhuma linha entre
   elas — borda2 e a mesma cor ja usada nas bordas que realmente precisam
   aparecer (cards, campos, rodape da headerbar). */
.regua-h { background-color: %(borda2)s; min-height: 1px; }
.vida-on     { background-color: %(ok_fg)s; }
.vida-off    { background-color: %(erro_fg)s; }
.grupo-titulo    { color: %(texto)s; font-family: """ + COND + """; font-size: 18px; font-weight: 600; }
.subgrupo-titulo { color: %(sec)s;   font-family: """ + COND + """; font-size: 15px; font-weight: 600; }
.grupo-cont   { color: %(fraco)s; font-family: """ + MONO + """; font-size: 10px; }

/* ---------------------------------------------------------------------
   ULTIMA PALAVRA SOBRE OS BOTOES DE DIALOGO

   Regra de ouro daqui para baixo: GEOMETRIA e declarada UMA vez, num
   seletor SEM pseudo-classe. Estados (:hover, :focus, :active, :backdrop)
   so podem mexer em COR.

   Foi a mistura dos dois que fez a janela inteira "dar zoom" ao passar o
   mouse: um estado trazia padding ou borda diferente, o botao mudava de
   tamanho, e o dialogo inteiro era realocado em cascata.
   --------------------------------------------------------------------- */

/* --- geometria (sem estado) --- */
.acessos-dialogo button,
headerbar.dlg-topo button,
.acessos-dialogo .area-acao button {
    border: 1px solid transparent;
    border-radius: 6px;
    padding: 5px 12px;
    min-height: 0px;
    min-width: 0px;
    margin: 0px;
    background-image: none;
    box-shadow: none;
    text-shadow: none;
    outline-style: none;
    outline-width: 0px;
    outline-offset: 0px;
    -gtk-outline-radius: 0px;
    font-family: """ + SANS + """;
    font-size: 12px;
    font-weight: 600;
}
.acessos-dialogo button label,
headerbar.dlg-topo button label,
.acessos-dialogo .area-acao button label {
    border: 0px solid transparent;
    background-color: transparent;
    background-image: none;
    box-shadow: none;
    outline-style: none;
    font-size: 12px;
    font-weight: 600;
}

/* --- so cor daqui para baixo --- */
.acessos-dialogo button.acao,
headerbar.dlg-topo button.acao,
.acessos-dialogo .area-acao button.acao            { background-color: %(azul)s; }
.acessos-dialogo button.acao:hover,
headerbar.dlg-topo button.acao:hover,
.acessos-dialogo .area-acao button.acao:hover      { background-color: %(azul_h)s; }
.acessos-dialogo button.acao:active,
headerbar.dlg-topo button.acao:active,
.acessos-dialogo .area-acao button.acao:active     { background-color: %(azul_h)s; }
.acessos-dialogo button.acao:backdrop,
headerbar.dlg-topo button.acao:backdrop,
.acessos-dialogo .area-acao button.acao:backdrop   { background-color: %(azul)s; }
.acessos-dialogo button.acao label,
headerbar.dlg-topo button.acao label,
.acessos-dialogo .area-acao button.acao label      { color: #ffffff; }

.acessos-dialogo button.perigo,
headerbar.dlg-topo button.perigo,
.acessos-dialogo .area-acao button.perigo          { background-color: %(erro_fg)s; }
.acessos-dialogo button.perigo:hover,
headerbar.dlg-topo button.perigo:hover,
.acessos-dialogo .area-acao button.perigo:hover    { background-color: %(erro_h)s; }
.acessos-dialogo button.perigo:active,
headerbar.dlg-topo button.perigo:active,
.acessos-dialogo .area-acao button.perigo:active   { background-color: %(erro_h)s; }
.acessos-dialogo button.perigo:backdrop,
headerbar.dlg-topo button.perigo:backdrop,
.acessos-dialogo .area-acao button.perigo:backdrop { background-color: %(erro_fg)s; }
.acessos-dialogo button.perigo label,
headerbar.dlg-topo button.perigo label,
.acessos-dialogo .area-acao button.perigo label    { color: #ffffff; }

.acessos-dialogo button:not(.acao):not(.perigo)          { background-color: %(cartao)s; }
.acessos-dialogo button:not(.acao):not(.perigo):hover    { background-color: %(hover)s; }
.acessos-dialogo button:not(.acao):not(.perigo):backdrop { background-color: %(cartao)s; }
.acessos-dialogo button:not(.acao):not(.perigo) label    { color: %(texto)s; }
"""

def gerar_css(tema):
    """Folha pronta para o CssProvider, já com as cores do tema."""
    return (CSS_MOLDE % TEMAS[tema]).encode()


def rgba(h):
    """'#rrggbb' -> Gdk.RGBA."""
    c = Gdk.RGBA()
    c.parse(h)
    return c


def fonte_mono(tamanho=11):
    """Nunca peça uma família que pode não existir.

    O Pango cai num fallback proporcional, e o VTE — que dimensiona a célula
    pela largura do caractere — estica o terminal inteiro. Por isso
    perguntamos ao Pango o que existe antes de escolher.
    """
    ctx = Gtk.Label().get_pango_context()
    fams = {f.get_name(): f for f in ctx.list_families()}
    for familia in ("IBM Plex Mono", "DejaVu Sans Mono"):
        f = fams.get(familia)
        if f is not None and f.is_monospace():
            return Pango.FontDescription("%s %d" % (familia, tamanho))
    return Pango.FontDescription("monospace %d" % tamanho)
