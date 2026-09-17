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

Um sétimo patch, em `app/os_wayland.go` (`gio_onToplevelConfigure`): o Gio
de origem recebia o array de estados (`states *C.struct_wl_array`) desse
callback do `xdg_toplevel` e o descartava por completo, só lendo
width/height. Na prática: arrastar a titlebar até o topo/canto da tela
aciona o snap-to-maximize do KWin, que manda um `configure` com
`XDG_TOPLEVEL_STATE_MAXIMIZED` no array de estados — só que, como o Gio
ignorava esse array, `w.config.Mode` nunca saía de `Windowed`. O app
ficava do tamanho da tela mas achando que estava em modo janela: o ícone
de maximizar/restaurar da nossa topbar não trocava, e um clique nele
mandava `ActionMaximize` de novo numa janela que já estava maximizada. O
patch lê o array (um `wl_array` de `uint32_t`, valores do enum
`xdg_toplevel_state`) e atualiza `w.config.Mode` (`Maximized`/
`Fullscreen`/`Windowed`) e dispara `ConfigEvent` quando muda. Confirmado
na prática: o KWin manda `[MAXIMIZED, ACTIVATED]` nesse array ao encostar
a janela no topo da tela.

Um oitavo patch, em `app/d3d11_windows.go` (`(*d3d11Context).Refresh`): o
resize do swapchain D3D11 só tratava `DXGI_ERROR_DEVICE_RESET`/
`_DEVICE_REMOVED` como recuperável — qualquer outro erro do
`ResizeBuffers`/`GetBuffer`/`CreateRenderTargetView` (visto na prática:
`DXGI_ERROR_INVALID_CALL` ao minimizar ou trocar de monitor/DPI no meio de
um redesenho pesado) subia cru e `window.go` derrubava a janela — a
travada relatada no Windows ao minimizar e ao mover entre telas. O patch
(1) pula o resize e devolve `errOutOfDate` quando a janela está 0x0
(minimizada: não há o que redesenhar, e o tamanho de cliente muda de novo
assim que ela volta) e (2) faz qualquer erro do resize passar por
`recoverableErr`, que vira `gpu.ErrDeviceLost` — `window.go` já sabe reagir
a isso destruindo e recriando o contexto D3D11 no quadro seguinte, em vez
de propagar o erro cru e matar a janela. Não tem equivalente nos caminhos
GL/EGL do Linux (`egl_wayland.go`, `egl_x11.go`): o resize ali
(`wl_egl_window_resize`/`glViewport`) não falha desse jeito, então não há
o que hardening aqui traria para lá.

Um nono patch, em `app/os.go`, `app/window.go` e `app/os_wayland.go`
(`updateOpaqueRegion` e o `Configure`): a opção nova `app.Translucent`.
O Gio declara a superfície inteira como OPACA para o compositor, o que é
ganho de desempenho numa janela comum mas impede translucidez — o
compositor pula a composição e o que o app desenhou com alfa aparece como
lixo de memória. A caixa de busca (`cmd/acessos/buscapop.go`) é um cartão
de vidro com sombra e cantos arredondados flutuando sobre o desktop, e
sem isso os cantos saíam quebrados e a margem, suja. Com `Translucent`
a região opaca fica VAZIA. O campo precisou ser copiado à mão dentro do
`Configure`, porque ali o Gio copia campo a campo para `w.config` e um
campo novo que ninguém copia nunca chega em quem o consome.

Um décimo patch, em `app/os_wayland.go`, `app/os_wayland.c`,
`app/window.go` e os arquivos gerados `app/wayland_xdg_activation.{c,h}`:
suporte a **xdg-activation**, com dois métodos novos em `app.Window` —
`TokenAtivacao()` e `AtivarCom(token)`.

No Wayland um cliente não pode se trazer para a frente sozinho (senão
qualquer programa em segundo plano pularia na frente de quem está
trabalhando). O que o protocolo permite é a janela QUE TEM O FOCO pedir
um token ao compositor e ceder a vez a outra. É exatamente o caso de dois
janelas do mesmo app: a caixa de busca tem o foco, o usuário escolhe uma
máquina, e quem deve ficar à frente é a janela principal, onde a aba
nasceu. O Gio de origem não conhece o protocolo, e implementa
`system.ActionRaise` só em Windows, X11 e macOS — no Wayland ele é
silêncio.

O pedido do token é ASSÍNCRONO de propósito: o `done` do compositor chega
pelo laço de eventos da própria janela, então esperar por ele de dentro
do laço seria esperar por uma mensagem que só é processada depois de a
espera acabar. `IniciarTokenAtivacao` devolve um canal e quem chama
espera de fora, com teto de 2s.

Os arquivos `wayland_xdg_activation.{c,h}` são gerados por
`wayland-scanner` a partir de
`staging/xdg-activation/xdg-activation-v1.xml`, do mesmo jeito que o Gio
já faz com xdg-shell e xdg-decoration (o XML vem do SDK do Freedesktop
que o Flatpak já traz — não é preciso instalar `wayland-protocols-devel`
na máquina). A tag `//go:build` no `.c` é acrescentada à mão depois de
gerar, seguindo o que os `//go:generate` do próprio Gio fazem.

Ligado ao build pelo `replace gioui.org => ./third_party/gio` no `go.mod`.
Ao subir a versão do Gio: recopiar do module cache e reaplicar este trecho
(procure por "patch acessos" no arquivo).
