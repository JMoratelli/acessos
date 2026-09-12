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

Ligado ao build pelo `replace gioui.org => ./third_party/gio` no `go.mod`.
Ao subir a versão do Gio: recopiar do module cache e reaplicar este trecho
(procure por "patch acessos" no arquivo).
