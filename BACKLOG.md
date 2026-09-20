# Backlog

O que falta para fechar o porte. Ordem de cima para baixo é a ordem de
prioridade acordada; o que já está pronto não mora aqui (o histórico do
git e o metainfo contam essa parte).

A numeração tem buracos de propósito: os itens resolvidos foram removidos
e os que ficaram mantiveram o número, porque é por ele que mensagens de
commit e conversas antigas se referem a eles.

Atualizado em 2026-09-19.

---

## 10. Conferir no Linux o que foi feito na rodada do Windows

A rodada de 2026-09-19 inteira foi feita e testada na máquina Windows —
lá dá para compilar nativo (MSYS2 ucrt64 tem gcc, freerdp3 e
libvncclient). Nada aqui é suspeita de defeito; é a conferência que a
outra metade do porte exige. Já conferido no Linux, não precisa refazer:
`go mod vendor`, `go build ./...` e `go test ./...`, tudo verde.

- **Rodar os testes AO VIVO no Linux**, com
  `ACESSOS_RDP_AOVIVO`/`ACESSOS_VNC_AOVIVO`/`ACESSOS_SSH_AOVIVO`. Os três
  deixaram de ser `//go:build linux` e a morte do filho passou de
  `syscall.Kill` para `os.Process.Kill` — é o que falta confirmar que não
  mudou nada deste lado.
- **Olhar a caixa do atalho global, e olhar DUAS vezes.** Ela ficou 14dp
  mais alta (`buscaCartaoAlt` foi de 76 para 90): o rodapé "Enter conecta
  · Esc fecha · mais N — refine o termo" NUNCA coube, nos dois sistemas, e
  agora cabe. Conferir no KWin que o cartão continua certo e que a linha
  aparece — **uma vez com a fonte no padrão e outra com o A+ ligado**
  (nível 2), porque a caixa passou a obedecer à escala da interface e
  antes não obedecia: `escalaFonte` era aplicado só na janela principal, e
  o serviço do atalho (processo à parte) nem lia `[geral] fonte`. Os dois
  caminhos agora passam por `aplicarGeral` (persistir.go). O que se quer
  ver: a caixa cresce junto, o rodapé continua dentro e a lista de
  resultados não estoura a tela em 1366x768.
- **X11 ganhou comportamento novo.** A caixa agora pede
  `system.ActionCenter` depois do primeiro quadro. No Wayland a ação é
  ignorada (quem posiciona é o compositor), mas no X11 o Gio a
  implementa de verdade — conferir que ela não briga com o
  posicionamento do gerenciador de janelas.
- **Gerar o instalador do Windows** (`scripts/build-windows.sh
  --instalador`) para fechar duas pontas que só um instalador novo
  confirma: o provider legacy do OpenSSL (item 9) e o ícone do
  executável (item 4). O passo novo copia `ossl-modules/legacy.dll` do
  sysroot e o build FALHA se ele não estiver lá — de propósito.

## 11. Validar ao vivo as correções dos shims C

As seis correções nos shims foram APLICADAS (ver git log), com build e
suíte verdes — mas nenhuma delas pôde ser exercitada contra máquina de
verdade daqui. É isto que falta, e cada uma tem um jeito específico de
mostrar que funcionou:

- **VNC, resize para cima.** Conectar num servidor e mudar a resolução da
  máquina remota de algo pequeno (1024x768) para grande (1920x1080) com a
  sessão aberta. Era o caminho do estouro de leitura; agora o trio
  ponteiro+tamanho sai junto de `vs_capturar_quadro`. O que se quer ver é
  a imagem acompanhar sem queda.
- **VNC, colar texto grande com a tela em movimento.** Era o caminho que
  dessincronizava o fluxo RFB e derrubava a sessão como "conexão
  perdida". Copiar alguns KB de texto e colar na sessão enquanto algo se
  mexe na tela remota.
- **RDP, retângulo de 1px na borda.** O sintoma era pedaço de tela que
  não atualizava. Difícil de provocar de propósito; vale rodar com
  `RS_LOG=1` numa sessão de uso normal e conferir que não há região
  parada.
- **RDP, fechamento de canal.** A trava nova cobre o Display Control e o
  clipboard. Exercitar redimensionando a janela várias vezes e copiando
  texto nos dois sentidos.
- **RDP, desmontagem.** `rs_destruir` agora desconecta sempre e libera o
  contexto. Quem exercita é o `cmd/rdpview` (no app o filho sai por
  `os.Exit`): abrir e fechar várias sessões seguidas e olhar a memória do
  processo.
- **Wayland, tecla presa.** Segurar Ctrl (ou Shift) dentro de uma sessão
  RDP/SSH, trocar de janela com a tecla ainda em baixo, voltar e digitar.
  Antes o modificador ficava preso do lado remoto.

## 12. Conferir no Windows o que saiu pelo lado Linux

Espelho do item 10, e pela mesma razão: tudo isto foi feito e testado no
LINUX (build e suíte verdes, `go vet` limpo), e o Windows não foi
compilado daqui. Nada aqui é suspeita de defeito.

**Os dois shims mudaram** (ver item 11), e eles compilam no Windows
também: o `vncshim.c` ganhou duas travas e uma função nova, e o
`rdpshim.c` ganhou uma trava e mudou a desmontagem. As macros `MUTEX_*`
já tinham os dois caminhos (CRITICAL_SECTION e pthread), e o vncshim
passou a incluir `<windows.h>` no ramo `_WIN32` — é o primeiro lugar a
olhar se o build lá reclamar.

- **A tela remota passou a viajar como RGBA** em vez de NRGBA, do filho
  até o `paint` do Gio. É byte a byte igual enquanto o alfa for sempre
  255, que é o que o laço grava — mas é o caminho do quadro inteiro, e
  merece um olhar: abrir RDP e VNC no Windows e conferir que as cores
  saem certas e que não há transparência onde não devia.
- **O cursor do SSH deixou de ter ticker próprio** e passou a pedir o
  próximo quadro de dentro do desenho. Conferir que ele continua piscando
  numa aba SSH em foco, e que para de piscar quando a aba sai de vista.
- **A caixa do atalho global passou a aplicar o A+.** No Linux há dois
  caminhos (o app e o serviço, que é processo à parte); no Windows só o
  do app. Conferir com o nível 2 que a janela cresce junto com a letra e
  que o rodapé continua dentro.
- **`entrada_outros.go` NÃO recebeu a correção de clipboard que o
  `entrada_linux.go` recebeu**, e isso é deliberado: fora do Linux o
  clipboard é lido de dentro do quadro, onde ler a barra de abas é
  seguro. Está anotado no próprio arquivo — conferir que continua
  verdade antes de propagar.
- **O saneamento do known_hosts cria um arquivo temporário** quando o
  arquivo tem linha ilegível (`os.CreateTemp`, apagado em seguida).
  Conferir no Windows, onde o known_hosts vive em
  `%USERPROFILE%\.ssh\known_hosts`.

## 3d. Busca por atalho global — o que ainda falta

O atalho, a caixa e o serviço estão funcionando (ver README). Estas
pontas continuam abertas:

- **Aviso na interface quando o atalho ficou registrado SEM TECLA.** O
  diálogo do KDE aparece uma vez só por aplicativo, e quem o fechar sem
  querer fica com um atalho morto sem nada na tela explicando por quê.
  Hoje o app só avisa no terminal, que ninguém lê — o lugar natural é a
  mesma linha da caixa nova dos Ajustes (ver abaixo), que já sabe se o
  atalho está ligado mas não se ele pegou tecla.
- **Interface para o `[geral] atalho_autostart`** (ver
  autostart_linux.go): a chave existe e é editável à mão, mas não tem
  caixa nos Ajustes como a do atalho em si já tem.
- **Soltar a captura de atalhos durante sessão remota.** Enquanto uma
  tela remota está em foco o app inibe os atalhos do compositor (de
  propósito: `Super` e `Alt+Tab` têm que chegar na máquina remota), e
  não há como devolvê-los sem trocar de aba. O padrão dos outros
  clientes remotos é uma tecla de soltura — `Ctrl+Alt` — com um aviso
  visível na barra de sessão dizendo que a captura está ligada, sumindo
  sozinho depois de uns segundos. **Vale notar que isto também engole o
  Ctrl+Shift+F12**: com uma sessão remota em foco, a tecla vai para a
  máquina remota, não para o portal.

## 3e. Cursor remoto: conferir contra cursores de verdade

A classificação de forma foi reescrita em 2026-09-18 (fim da "mão presa" e
do "ocupado" que nunca aparecia) e é exercitada por silhuetas desenhadas à
mão em [cursorforma_test.go](cmd/acessos/cursorforma_test.go), com as
proporções dos cursores reais. O que falta é o passo seguinte, e barato:
capturar as MÁSCARAS de verdade de uma sessão Windows (o `RS_LOG=1` do
rdpshim já imprime tamanho e hotspot de cada uma; falta despejar os bytes)
e virar caso de teste. Sem isso, os limiares continuam calibrados por
proporção, não por amostra.

## 4. Ícone do EXECUTÁVEL no Windows — confirmar com instalador novo

**Relatado no teste da 2.0.4: os ícones saem errados no Windows.** O
relato juntava duas coisas; a metade que era glifo de fonte na interface
foi resolvida em 2026-09-19 (ver `glifos_test.go`). Sobra o ícone do
executável e do instalador.

Uma causa concreta foi achada e corrigida no
[build-windows.sh](scripts/build-windows.sh): o `.ico` multi-resolução
era montado com `magick "$tmp"/*.png`, e o glob ordena por NOME. O
arquivo saía na ordem **128, 16, 24, 256, 32, 48, 64** — ou seja, com a
imagem de 128px como PRIMEIRA entrada, que é a que o Windows trata como
padrão em vários lugares (Explorer, Alt+Tab, instalador). A lista agora é
montada à mão, em ordem crescente, e o `.ico` foi conferido com
`magick identify`.

Falta **confirmar na máquina Windows**, com um instalador novo, se era só
isso. Se continuar errado, o próximo suspeito é a conversão em si (fundo
ou transparência do `rsvg-convert`) — e o sintoma precisa ser detalhado
(qual ícone, onde).

## 5. Capturas de tela do metainfo

As cinco imagens de [screenshots/](screenshots/) são da versão Python.
Decisão sua, de propósito, para não segurar o lançamento — mas a loja
mostra uma interface que não existe mais. Trocar quando a 2.x estiver
assentada.

## 6. Assinatura do executável do Windows

O instalador não é assinado, então o SmartScreen avisa em toda máquina
nova. Para distribuição interna é aceitável (o aviso passa com "Mais
informações"); para distribuir fora, não. Precisa de um certificado de
code signing — custo e decisão sua, não técnica.

## 7d. Clipboard: o `write()` bloqueante congela a janela

**Diagnosticado em 2026-09-17, sem correção.** Não é corrida — é bloqueio, e
é anterior a qualquer mudança recente.

Quando outro programa pede o nosso clipboard, o compositor chama
`fonte_enviar` (`internal/grab/grab_wayland.c`), que escreve o texto num pipe
com `write()` em laço até terminar. Esse callback roda de dentro do dispatch
do Wayland, ou seja, **na goroutine do laço de eventos**. Se quem está lendo
do outro lado for lento (ou parar de ler), o `write()` bloqueia no pipe cheio
e leva a interface inteira junto: sem clique, sem hover, sem redesenho.

Quanto mais texto, mais fácil de ver — colar um clipboard grande de uma
sessão RDP num programa lento é o caso plausível.

Instrução básica para quando for corrigir: o `fd` recebido em `fonte_enviar`
não pode ser escrito ali. O caminho é entregar esse descritor para fora do
dispatch — uma thread própria (ou uma goroutine, devolvendo o `fd` ao lado Go
como já é feito na LEITURA, em `goClipOferta`) que escreve e fecha por conta
própria. O `fd` é do processo e continua válido depois que o callback retorna;
o texto já é copiado sob `clip_m` antes do write, então a cópia pode viajar
junto sem nova trava. Cuidado com dois detalhes: garantir o `close(fd)` em
todo caminho de saída, e não deixar acumular uma thread por pedido se algum
consumidor nunca ler.

## 8. Tela cheia

Pedido em 2026-09-16: tela cheia estilo F11 do Chrome / cliente RDP da
Microsoft / Remmina (a REFERÊNCIA de comportamento, não o atalho) — some a
decoração, fica só o conteúdo da aba ativa ocupando a tela inteira, com
uma barrinha FINA sempre visível no topo (não a tira de abas nem a barra
de título de hoje inteiras) mostrando só status e quem está conectado.

**Entrada é só por BOTÃO, sem atalho de teclado nenhum** — decisão
explícita: dentro de uma sessão remota qualquer F-key pode colidir com o
programa do outro lado (F11 especificamente é usado por vim em alguns
binds, tmux, e diverge por distro), e a barra de sessão já tem lugar pros
botões (`ControlesSessao` em [sshtab.go](cmd/acessos/sshtab.go) tem o
padrão: `botaoSessao`/`toggleSessao`). Saída ainda em aberto — Ctrl+F11
foi cogitado, mas o padrão exato (tecla, ou também um botão/X na
barrinha) fica pra decidir na hora, não travar agora.

Por onde entra:

- **modo de janela**: o fork do Gio em `third_party/gio/app` já expõe
  `WindowMode` (`Fullscreen`/`Windowed`) nas quatro plataformas que
  importam aqui — `os_wayland.go`, `os_x11.go`, `os_windows.go` têm a
  implementação nativa, não é preciso inventar nada na camada de SO;
- a decoração de hoje é **desenhada pelo próprio app** (CSD — ver
  `janelaRaio`, `sistemaDecora`, `fundoTitulo` em
  [tema.go](cmd/acessos/tema.go)/[main.go](cmd/acessos/main.go)), então
  entrar em tela cheia não pode só pedir o modo ao Gio: precisa TROCAR a
  faixa de título + tira de abas de hoje pela barrinha fina pedida,
  condicionado a um novo estado tipo `telaCheia bool`;
- a barrinha reaproveita o que já existe: cada aba remota já implementa
  `EstadoSessao() estadoSessao` (chip ATIVO/CAIU/AGUARDE + texto tipo
  `usuario@host:porta`) — é exatamente o "status e quem está conectado"
  pedido, sem inventar campo novo;
- se a saída acabar usando alguma tecla (Ctrl+F11 ou o que for decidido),
  ela só pode disparar quando `telaCheia` já está true — nunca competir
  com o que a sessão remota também usa, mesmo cuidado que F12/Ctrl+W/
  Ctrl+G já tiveram que ter (ver itens 1 e 3 deste arquivo).

## 9. Estabilidade RDP/VNC — sobras da auditoria de 2026-09-19

A auditoria (pedida depois de queixa de queda/glitch em sessão RDP)
fechou seis causas, a última delas o provider legacy do OpenSSL que nunca
ia junto no instalador do Windows — todas no histórico do git. Uma queda
recorrente contra um host específico ficou explicada e NÃO é defeito
nosso: `ERRINFO_RPC_INITIATED_DISCONNECT`, ferramenta administrativa NO
SERVIDOR derrubando a sessão a cada ~35s. Como o `Run()` passou a devolver
o motivo específico do FreeRDP em vez do genérico "conexão perdida", esse
tipo de queda agora se identifica pelo log.

Para validar o que sobra: os testes ao vivo (`ACESSOS_RDP_AOVIVO`,
`ACESSOS_VNC_AOVIVO`) rodam nos dois sistemas desde 2026-09-19 — inclusive
no Windows, onde as quedas foram relatadas.

**Suspeito em aberto**: o pacote `freerdp` do MSYS2 (3.31.1-1, o mesmo que
o instalador embarca) é compilado com `WITH_VAAPI_H264_ENCODING=ON`, e a
própria libfreerdp avisa a cada conexão que "[experimental] build options
might crash the application". VA-API é coisa de Linux e o caminho não deve
nem ser exercitado por um cliente no Windows, mas é a única diferença de
BUILD conhecida entre o FreeRDP do Windows e o do Flatpak — e queda "sem
motivo" no Windows é justamente o que se está caçando. Conferir se uma
versão mais nova do pacote sai sem a flag antes de considerar compilar o
FreeRDP do zero para o sysroot.

O que ficou de fora da auditoria, por ser mais arriscado de mexer sem um
teste ao vivo (`ACESSOS_RDP_AOVIVO`) validando cada mudança:

- **Cópia do framebuffer sem lock contra a pintura.** `fb_lock` em
  rdpshim.c só protege `gdi_resize` e a própria captura — a pintura de
  verdade acontece dentro de `rs_processar` sem essa trava, e as duas
  goroutines de leitura (`rdp.go`) só usam `RLock`. Pode causar quadro
  rasgado (metade novo, metade velho) com os canais gfx do RDP, que
  fazem blits grandes.
- **Retângulo de dano cortado a zero é descartado.** Em
  [telaworker.go](cmd/acessos/telaworker.go), depois de um shrink, um
  retângulo que não cabe mais na tela é jogado fora mesmo já tendo sido
  consumido de `dano`. Hoje é coberto por `dano.tudo()` no `OnResize`,
  mas a ordem entre os dois não é garantida.
- **Duas cópias de tela inteira por quadro** (uma em C ao capturar, outra
  ao recortar em Go) — em 4K é ~66MB de churn por quadro, contribuindo
  pro vigia de memória (`internal/telaproc/vigia.go`, teto de 768MiB)
  matar a sessão sob carga, o que aparece como queda "sem motivo". Eram
  TRÊS: a do `Quadro.Codificar` saiu, e o buffer de saída da conversão
  passou a ser reaproveitado entre quadros. Estas duas são mais caras de
  tirar — a de C precisaria entregar o ponteiro sob trava para o Go, e a
  do Go some junto com ela.
