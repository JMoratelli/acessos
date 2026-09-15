# Cópia do gioui.org v0.10.2 com um patch

Vendorizado por causa de UMA mudança, em `app/os_wayland.go`, função
`(*window).Configure`: o Gio de origem cria o objeto
`zxdg_toplevel_decoration_v1` e só escuta o `configure` do compositor — ele
nunca envia `set_mode`. Em Wayland/KWin isso faz `app.Decorated(false)` não
ter efeito: o compositor permanece no padrão server-side e desenha a
titlebar dele por cima da barra de topo do app, gastando duas faixas de
altura na mesma janela.

O patch envia `set_mode(CLIENT_SIDE)` quando o app pede
`app.Decorated(false)` (e `SERVER_SIDE` quando pede `true`).

Um terceiro patch, em `app/window.go` (`(*Window).frame`): o Gio limpa o
quadro com BRANCO OPACO fora do js. Com os cantos arredondados do item
anterior, os quatro cantos ficavam fora do recorte do app e apareciam como
bicos brancos — gritantes no tema escuro. Passou a limpar com preto
transparente.

Um quarto patch, em `app/os_wayland.go` (`gio_onPointerButton` e o novo
método `(*window).unmaximizeForDrag`): o Gio de origem só inicia o
`xdg_toplevel_move` quando a janela já está em modo `Windowed`. Como a
titlebar é nossa (CSD), isso fazia arrastar uma janela MAXIMIZADA não
fazer NADA — em qualquer software "padrão" (GNOME, Windows, KDE) isso
restaura a janela pro tamanho de janela e já continua o arrasto seguindo
o cursor. O patch chama `unset_maximized` e já inicia o move com o mesmo
serial do clique; KWin/Mutter aceitam o move interativo mesmo com o
unmaximize ainda pendente de confirmação do compositor.

Um quinto patch, ainda em `app/os_wayland.go` (novo método
`(*window).restoreSize`, usado em `Configure` e em `unmaximizeForDrag`):
o app nasce direto MAXIMIZADO (`app.Maximized.Option()` em main.go, sem
`app.Size`), então `wsize` — o tamanho "de janela" que o Gio guarda pra
restaurar depois — é capturado ainda (0,0), antes de qualquer geometria
real vinda do compositor. Restaurar pra Windowed com wsize=(0,0) manda um
frame de tamanho zero e o Gio dá panic ("zero-sized Draw"), tanto pelo
botão de restaurar quanto arrastando a titlebar (o quarto patch, acima).
`restoreSize` cai pra 75% do tamanho maximizado atual quando não há
wsize salvo.

Um sexto patch, ainda em `app/os_wayland.go` (`newWaylandWindow`, novo
`gtkCursorThemeSize`): o tamanho de cursor caía num fallback fixo de 32px
quando `XCURSOR_SIZE` não estava no ambiente do processo — comum, porque
nem todo compositor exporta essa variável, mesmo com o tamanho
configurado em algum lugar (visto na prática em KDE Plasma/KWin). 32 é
maior que o padrão de praticamente todo desktop Linux atual (24, o
default do libXcursor), e ficava bem visível com qualquer cursor.
`gtkCursorThemeSize` lê `gtk-cursor-theme-size` de
`~/.config/gtk-3.0/settings.ini` (GTK grava esse arquivo também em
sessões KDE, pra manter apps GTK consistentes) e usa isso como
aproximação antes de cair pra 24 fixo.

(Um sexto patch anterior, em `widget/button.go`, dava cursor de mão a
TODO Clickable automaticamente. Revertido a pedido: em cima de card/linha
de lista/aba a mão em qualquer hover ficava irritante — o app agora soma
`pointer.CursorPointer` só nos ícones/botões específicos que fazem
sentido como clicáveis discretos (ver cmd/acessos/topbar.go e
cmd/acessos/tabbar.go), não mais no Clickable genérico do Gio.)

Ligado ao build pelo `replace gioui.org => ./third_party/gio` no `go.mod`.
Ao subir a versão do Gio: recopiar do module cache e reaplicar este trecho
(procure por "patch acessos" no arquivo).
