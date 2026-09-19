# Backlog

O que falta para fechar o porte. Ordem de cima para baixo é a ordem de
prioridade acordada; o que já está pronto não mora aqui (o histórico do
git e o metainfo contam essa parte).

A numeração tem buracos de propósito: os itens resolvidos foram removidos
e os que ficaram mantiveram o número, porque é por ele que mensagens de
commit e conversas antigas se referem a eles. O que sobreviveu de um item
resolvido (limitação que continua valendo) está no fim do arquivo, não no
item.

Atualizado em 2026-09-18.

---

## 3d. Busca por atalho global — o que ainda falta

O atalho, a caixa e o serviço estão funcionando (ver README). Estas
pontas continuam abertas:

- **Chave nos Ajustes para desligar o atalho** (padrão ligado), mais o
  aviso na interface quando o atalho ficou **registrado sem tecla**. Isso
  não é conforto: o diálogo do KDE aparece uma vez só por aplicativo, e
  quem o fechar sem querer fica com um atalho morto sem nada na tela
  explicando por quê. Hoje o app só avisa no terminal, que ninguém lê.
  A chave `[geral] atalho_autostart` (ver autostart_linux.go) já existe e
  é editável à mão; falta o mesmo para ligar/desligar o atalho em si, e
  falta a interface das duas.
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

## 4. Ícones no Windows

**Relatado no teste da 2.0.4: os ícones saem errados no Windows.** Falta
detalhar o sintoma (qual ícone, onde) antes de mexer — o que anotar aqui
é onde procurar:

- o ícone do executável e do instalador é um `.ico` multi-resolução
  gerado por [build-windows.sh](scripts/build-windows.sh) a partir do
  mesmo `icones/acessos.svg` do Linux, com `rsvg-convert` + `magick`, e
  embutido como recurso pelo `windres`. Se o problema for este, é
  provável que seja a conversão (fundo, transparência ou tamanho que o
  Explorer escolhe);
- os ícones DENTRO da interface (protocolos, barra de topo, cards) são
  vetores do pacote `gio.tools/icons`, desenhados pelo próprio Gio, e não
  dependem de nada do sistema — se estes estiverem errados no Windows e
  certos no Linux, o assunto é outro (escala ou tema), não o `.ico`.

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

## 7b. Travamento no Windows ao mover a janela entre monitores

**Relatado e diagnosticado em 2026-09-17, sem correção.** A janela congela
e para de aceitar cliques ao ser arrastada de um monitor para o outro.

O que já se sabe, de diagnóstico feito na máquina afetada (Windows 10,
GeForce 210 com driver de 2015, feature level 10_1, dois monitores 1920x1080
no mesmo DPI):

- o endurecimento do resize do D3D11 (item 8 do
  [PATCH.md](third_party/gio/PATCH.md)) **já está** na versão testada, a
  2.3.0. Ele converte ERRO do `ResizeBuffers`/`GetBuffer` em "dispositivo
  perdido", que o Gio recupera recriando o contexto — mas não cobre
  chamada que BLOQUEIA dentro do driver, que é o que o sintoma sugere: no
  Gio, quem desenha é a mesma thread que bombeia as mensagens do Windows;
- não reproduziu em nenhuma tentativa automatizada **sem sessão remota
  ativa** (arraste simulado atravessando os monitores, ~50 verificações de
  responsividade por tentativa, todas respondendo). Reforça que o gatilho
  depende de quadros chegando de verdade no momento da troca de saída de
  vídeo, e/ou de monitores com DPI diferentes;
- o Visualizador de Eventos não tinha nenhum "Hang detected" para o
  processo.

Próximo passo, quando alguém puder mexer na máquina afetada: uma build com
marcação de tempo em volta do `Refresh`/`Present` gravando no `log.txt` que
o app já mantém. Se o log terminar em "entrei" sem o "saí", está provado o
bloqueio no driver; se parar antes, o assunto é outro. Um dump do processo
travado (`procdump -ma`) responde o mesmo com mais precisão, com a vantagem
de que aqui existem os símbolos.

Atenção a uma armadilha: um diagnóstico de caixa-preta feito por strings do
binário concluiu que o app é Rust com egui/wgpu/winit. Não é — é Go com
Gio, e no Windows o Gio desenha por Direct3D 11. As recomendações daquele
relatório apontam para APIs que não existem aqui.

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

## Limitações conhecidas, que NÃO estão no plano de corrigir

Ficam registradas para ninguém "descobrir" de novo. Boa parte veio de
itens que já foram resolvidos e saíram daqui — o que sobrou deles é isto:

- **Terminal SSH**: sem busca no scrollback (o scrollback em si existe,
  20000 linhas — é busca DENTRO dele que não tem, tipo o Ctrl+Shift+F de
  um gnome-terminal). O relato de mouse para `htop`/`less`/TUIs existe
  desde 2026-09-16; o que continua de fora é o modo "qualquer movimento"
  (1003) sem nenhum botão apertado, que o Gio não entrega nesta pilha (só
  chega evento de arrasto com botão). A seleção com o mouse é da TELA
  VISÍVEL: não acompanha o conteúdo se o programa remoto redesenhar por
  baixo. (Tela alternativa — o modo que `vim`/`less`/`nano` usam — já é
  tratada pela biblioteca de terminal por baixo.)
- **DECSCUSR**: a aplicação remota não consegue escolher a forma do
  cursor do terminal (é assim que o vim vira barra no modo de inserção).
  O vt10x vendorizado não interpreta esse código; o cursor daqui é sempre
  barra. Se um dia importar, é mais um patch local no mesmo espírito do
  scrollback/bracketed-paste.
- **Teclado não-US**: o layout assumido é US. Símbolos que dependem de
  outro layout (ABNT2, acentos mortos) e o AltGr não têm tabela em
  [teclado_outros.go](cmd/acessos/teclado_outros.go). No Windows,
  Ctrl/Alt/Shift sempre viram a variante ESQUERDA — o `key.Event` do Gio
  não distingue sem ir atrás do scancode cru.
- **Atalhos do app com sessão remota fora de foco**: F12, Ctrl+W e Ctrl+G
  só respondem com uma sessão remota em foco; fora dela o app não disputa
  o foco de teclado do Gio com o resto da interface (ver o comentário em
  `tratarTecladoFrame`).
- **Teto de sessões simultâneas**: `telaproc.MaxSessoes`, hoje 12. RDP e
  VNC rodam cada um em processo próprio, e o número saiu de medição real
  do custo de um filho (`TestAoVivoCustoDeUmFilho`, em
  [processo.go](internal/telaproc/processo.go)) — **não é chute e não deve
  ser mexido por estimativa**: refaça a medição antes de mudar.
- **Cursor remoto é aproximação, não cópia.** Nem RDP nem VNC dizem QUE
  cursor é — mandam um bitmap. O que o app faz é medir a silhueta e o
  ponto quente e escolher o cursor nomeado mais próximo do Gio (ver
  [cursorforma.go](cmd/acessos/cursorforma.go), com os casos cobertos em
  `cursorforma_test.go`). "Não permitido" (anel com risco) cai em
  ocupado e "ajuda" (seta + ?) cai em seta+ocupado, de propósito: os dois
  são raros em sessão remota e errar neles custa menos que um falso
  positivo nos comuns.
- **Trabalho vindo de outra janela espera um quadro.** O que a caixa de
  busca escolhe é executado pelo laço da janela principal
  ([filajanela.go](cmd/acessos/filajanela.go)). Com a janela grande
  minimizada, o compositor pode segurar os quadros, e a aba só abre
  quando ela voltar. A caixa em si NÃO depende disso — ela tem janela e
  laço próprios, e é por isso que o atalho responde de qualquer jeito.
- **Sem reportar upstream ao FreeRDP** a variante residual do
  `dvcman_channel_close` (vizinhança da
  [CVE-2026-56297](https://github.com/FreeRDP/FreeRDP/security/advisories/GHSA-3mv2-5q57-2v8h))
  que derrubava o processo num disconnect abrupto. Aqui ela deixou de ser
  fatal — cada sessão RDP roda em processo próprio —, mas segue sendo bug
  deles.
