# Cópia do gioui.org v0.10.3 com um patch

(Rebase em 2026-10-17: era v0.10.2. O patch inteiro — diff da cópia
contra o módulo limpo em `~/go/pkg/mod/gioui.org@v0.10.2` — aplicou em
cima do v0.10.3 com deslocamento de linhas e um único hunk à mão, em
`app/os_windows.go` (`WM_NCCALCSIZE`): o upstream passou a achar o
monitor pelo retângulo com `MONITOR_DEFAULTTONULL` em vez de pelo hwnd, e
o patch da tela cheia só troca o que vai para `Rgrc[0]`. Receita para a
próxima: `diff -ruN -x PATCH.md <módulo limpo da versão atual>
third_party/gio > fork.patch`, copiar a versão nova do module cache,
`patch -p1`, resolver os `.rej`, trocar a versão em `go.mod`, `go mod
tidy`, `go mod vendor`.)

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

Um décimo primeiro patch, em `app/os_wayland.go` (`gio_onPointerButton`,
`gio_onPointerLeave`, `onPointerMotion` e o novo `passouLimiarArrasto`):
**limiar de arrasto na titlebar**. O quarto patch, acima, pedia o
`xdg_toplevel_move` JÁ NO CLIQUE — que é o que o Gio de origem também faz.
Na prática isso deixa a janela sensível demais: um clique curto sem querer
na faixa de título, com a tremida normal da mão, arrancava a janela do
maximizado e a soltava no meio da tela. O patch faz o clique apenas ARMAR
o arrasto; ele só começa quando o ponteiro anda mais que `limiarArrastoDp`
(12dp) a partir do ponto do clique, com o serial daquele clique — que é o
evento de grab implícito que o protocolo exige. De quebra, o press deixou
de ser engolido: a faixa de título passa a receber evento como qualquer
outra área.

No mesmo décimo primeiro patch, uma linha em `gio_onPointerEnter`: o Gio de
origem guarda ali a posição do ponteiro SEM multiplicar pela escala da
janela, ao contrário do `onPointerMotion`. Numa tela com escala 2, a
posição vale metade do certo até o primeiro movimento — o `ActionAt` do
clique pode não reconhecer a faixa de título, e a origem do limiar de
arrasto sairia errada pelo mesmo motivo.

Um décimo segundo patch, em `app/window.go` (novo
`(*Window).PedirTokenAtivacao`): é a metade SÍNCRONA do `TokenAtivacao` do
décimo patch. Juntas, as duas metades daquele método têm exigências
opostas — o pedido precisa sair da goroutine do laço (no Wayland/X11 o
`Window.Run` executa na goroutine de quem chama, e chamar de fora põe o
driver em paralelo com o desenho), e a espera não pode ficar nela (a
resposta chega pelo próprio laço). Quem chama agora pede de dentro do
laço e espera o canal numa goroutine. Ver `cmd/acessos/buscapop.go`.

Um décimo terceiro patch, em `app/os_windows.go` (`Configure`),
`app/internal/windows/windows.go` e `app/window.go`: **`app.Translucent`
passa a valer no WINDOWS também.**

O nono patch implementou a opção só no Wayland, e no Windows ela era
aceita e ignorada — a caixa de busca (`cmd/acessos/buscapop.go`), que é
um cartão de vidro com sombra e cantos arredondados flutuando sobre o
desktop, saía como um retângulo opaco com a MOLDURA DO SISTEMA em volta,
botões de maximizar e fechar inclusive. A causa é sutil: para dar sombra
de sistema a uma janela sem decoração, o Gio chama
`DwmExtendFrameIntoClientArea(-1,-1,-1,-1)`, e essa "folha de vidro" põe
a moldura do DWM ATRÁS do conteúdo. Onde o app pinta opaco ela some; onde
o app pinta com alfa — a margem em volta do cartão — ela aparece. Foi por
isso que a janela principal, que pinta cada pixel, nunca mostrou nada
disso.

O patch, quando `Translucent` está ligado, troca essa chamada por
`DwmEnableBlurBehindWindow` com região de blur VAZIA
(`CreateRectRgn(0,0,-1,-1)`), que é o pedido documentado de "respeite o
alfa por pixel desta janela, sem borrar nada atrás", e zera as margens da
moldura estendida (as duas coisas não convivem: a moldura ganha, e o
branco com os botões volta). A swapchain continua a mesma, amarrada no
HWND — não é preciso DirectComposition. Perde-se a sombra do sistema, que
uma janela translúcida não quer de qualquer forma, porque desenha a dela.

Dois detalhes que custam se forem esquecidos: o `Translucent` precisou
ser copiado à mão para `w.config` dentro do `Configure` (ali a Config é
copiada campo a campo, mesma armadilha do Wayland), e a chamada TEM de
ser refeita a cada `Configure` — o `SetWindowPos`/`SetWindowLong` logo
abaixo refaz a moldura, e sem repetir o vidro branco volta no primeiro
redimensionamento (visto na prática: a caixa nascia certa e estragava ao
crescer com o primeiro resultado).

Ligado ao build pelo `replace gioui.org => ./third_party/gio` no `go.mod`.
Ao subir a versão do Gio: recopiar do module cache e reaplicar este trecho
(procure por "patch acessos" no arquivo).

Um patch novo, em `app/window.go` (`(*Window).Event`): quando o driver
fecha a janela ele emite DOIS eventos — o `ViewEvent` zerado e, logo
depois, o `DestroyEvent` — e processar o `DestroyEvent` zera `w.driver`.
O cliente que consome o `ViewEvent` e volta ao laço entrava no `Event()`
com `driver == nil` e ganhava uma JANELA NOVA (`w.init()`), em vez do
`DestroyEvent` que já estava na fila.

Essa janela nasce abandonada: na volta seguinte o cliente recebe o
`DestroyEvent`, sai do laço, e ela fica na tela sem ninguém desenhando,
com a própria conexão Wayland aberta — cada janela do Gio abre a sua
(`newWLWindow` chama `newWLDisplay`). Some da lógica do app mas não da
barra de tarefas, e fechá-la pelo compositor derruba o PROCESSO INTEIRO,
porque não há mais laço atendendo os eventos dela.

Aparecia a cada abre-e-fecha de janela secundária: no Acessos, uma
"janela fantasma" por destacar/reacoplar a sessão remota, e o app morria
com SIGABRT quando uma delas era fechada. Medido em `cmd/fantasma`, que
conta os descritores do processo: cinco por ciclo (o socket do
wl_display, o pipe de notificação e o memfd do tema de cursor).

O patch entrega o evento pendente antes de recriar: com `driver == nil` e
um `DestroyEvent` na fila, `Event()` devolve o que está na fila em vez de
abrir janela nova.

Um patch novo, em `app/os_windows.go` (`windowProc`, no `WM_NCCALCSIZE`):
**a tela cheia do Windows cobria a ÁREA ÚTIL do monitor, não o monitor
inteiro** — sobrava na tela justamente a faixa da barra de tarefas.

A tela cheia do Gio no Windows é "maximizada SEM `WS_OVERLAPPEDWINDOW`"
(ver o `case Fullscreen` do `Configure` e a classificação de modo em
`update`). A JANELA nasce certa: maximizar uma janela sem moldura já dá o
retângulo do monitor inteiro. Quem encolhia era o próprio Gio, uma linha
depois: numa janela sem decoração e maximizada ele grampeia o CLIENTE na
`mi.WorkArea`, que é a área útil — o certo para a janela maximizada de
verdade (senão ela comeria a barra de tarefas), e errado para a tela
cheia.

Medido com `cmd/telacheia`, num monitor de 1920x1036 com barra de 40px:

	sem o patch   janela 1920x1036, cliente 1920x996
	com o patch   janela 1920x1036, cliente 1920x1036

Nos dois casos o shell TIRA o `WS_EX_TOPMOST` da barra de tarefas, ou
seja, ele reconhecia a tela cheia e cedia a tela desde sempre — o que
faltava era o app ocupá-la.

O patch escolhe o retângulo pelo estilo da janela, que é o mesmo critério
que o `update()` usa para dizer `Fullscreen`: sem `WS_OVERLAPPEDWINDOW`,
`mi.Monitor`; com ele, `mi.WorkArea` como antes. A janela principal, que
é CSD mas mantém o estilo, não muda em nada.

O jeito canônico (`SetWindowPos` no retângulo do monitor, sem maximizar)
foi medido e funciona igual, mas exigiria mexer no `Configure`, no
`update()` e em guardar/restaurar o `WINDOWPLACEMENT` — três lugares
contra uma condição, para o mesmo pixel.

Patch do rebase para v0.10.3 (2026-10-17), em `app/os_wayland.go`
(`(*window).ReadClipboard`): **colar em campo do Gio morria no primeiro
Ctrl+V sem clipboard legível**. Quando `readClipboard()` não tem o que
ler (seleção ainda não chegou, clipboard vazio ou com imagem) o Gio de
origem só retornava — e isso deixava DUAS coisas presas: o
`readClipClose`, que a guarda do começo da função usa para descartar
pedidos enquanto há um em andamento (nenhuma goroutine ia fechá-lo), e a
tag do pedinte no `receivers` do roteador (`io/input/clipboard.go`,
`ProcessReadClipboard`), que só é solta por um `DataEvent` e sem isso
nunca mais gera pedido. Resultado medido com `cmd/colartest` nesta
máquina (KDE/KWin): 19 pedidos, zero entregas, mesmo com o clipboard
mudando — o grab do app via as mudanças, o Gio não. O patch zera o
`readClipClose` e entrega um `DataEvent` vazio pelo mesmo `clipReads`,
o que solta a tag; depois disso cada pedido volta a ler o clipboard de
verdade (mesmo teste: 16 de 16 entregas, inclusive após limpar o
clipboard no meio).

Dois patches de cursor, em `app/os_wayland.go` (2026-10-17):

- **Tema do cursor do desktop.** `XCURSOR_THEME` raramente chega ao
  processo de um app Wayland (KDE guarda o tema em `kcminputrc`, o GTK em
  `settings.ini`, nenhum exporta a variável), e sem nome a
  libwayland-cursor carrega o "default" do sistema (Adwaita aqui) — a
  seta do app saía diferente da do resto do desktop.
  `temaCursorDoAmbiente` lê `gtk-cursor-theme-name` do settings.ini e,
  na falta, `cursorTheme` do kcminputrc; só então cai no padrão.
- **Cursor animado.** `setCursor` mostrava só `images[0]`, então o
  "carregando" (seta com anel/ampulheta) ficava parado no primeiro
  quadro. Agora `wl_cursor_frame_and_duration` escolhe o quadro pelo
  tempo desde que o cursor foi posto (`cursor.inicio`) e, quando o
  quadro tem duração, um `time.AfterFunc` por display acorda o laço
  (`cursorTick` + `wakeup`) e o `dispatch` troca o quadro na thread
  certa. Cursor estático não arma timer nenhum.

Dois patches de vidro fosco (2026-10-18):

- **Imagem ancorada na janela** — `paint.ImageOp.Tela` (`op/paint/paint.go`),
  um byte a mais no op de imagem (`internal/ops/ops.go`, `TypeImageLen`) e
  a leitura dele no renderizador (`gpu/gpu.go`: `imageOpData.tela`,
  `decodeImageOp` e o caso `ops.TypePaint`). Com `Tela > 0` a pintura da
  imagem troca a transformação corrente por uma escala pura a partir da
  origem da JANELA: o pixel (0,0) da imagem cai no (0,0) da janela e
  cada pixel dela cobre `Tela`×`Tela` pixels. O recorte continua o
  corrente. É o que deixa o card pintar, dentro do próprio molde, o
  pedaço exato do fundo desfocado que está atrás dele — com rolagem,
  hover e tudo — sem saber onde está na tela (o Gio não expõe a posição
  absoluta durante o layout: o Flex só posiciona o filho depois de
  desenhá-lo). Só o `paint.go` escreve esse op e só o `gpu.go` lê;
  `Tela` zero é o comportamento de origem. `gpu/internal/rendertest`
  passa. Quem usa: `cmd/acessos/vidro.go`.
- **Desfoque do compositor (ext-background-effect-v1)** — protocolo
  padrão do Wayland (staging) para a janela pedir que o compositor
  desfoque o que está ATRÁS dela; o KWin 6 expõe, o GNOME não.
  `app/wayland_ext_background_effect.{c,h}` saíram do
  `wayland-scanner 1.26.0` (`client-header` e `private-code`) a partir do
  XML do `wayland-protocols` (`staging/ext-background-effect`), com a
  linha de build do `.c` igual à dos outros gerados. Em `os_wayland.c`, o
  listener da capacidade; em `os_wayland.go`, o bind do global, a
  capacidade "blur" (`desfoqueOk`), `(*window).DefinirDesfoque` (monta a
  região em coordenadas de SUPERFÍCIE, com os cantos arredondados em
  faixas de uma linha, arredondando para dentro) e a destruição do
  objeto do efeito ANTES da superfície. Em `window.go`,
  `(*Window).DesfocarFundo(r, raio) bool`, que confere a interface do
  driver antes do `Run` — fora do Wayland devolve false sem despachar
  nada para a thread da janela. Quem usa: a caixa do atalho global
  (`cmd/acessos/buscapop.go`), que engrossa a tinta quando ouve false.
  Os campos novos ficam no FIM das structs de propósito, para o gofmt
  não realinhar os campos de origem e o diff contra o upstream continuar
  só de acréscimos.
