# Backlog

O que falta para fechar o porte. Ordem de cima para baixo é a ordem de
prioridade acordada; o que já está pronto não mora aqui (o histórico do
git e o metainfo contam essa parte).

Atualizado em 2026-09-16.

---

## 1. Teclado nas sessões remotas do Windows — RESOLVIDO (2026-09-16)

VNC e RDP testados ao vivo (VNC contra `127.0.0.1`, RDP contra uma
máquina de verdade na rede): digitar, Ctrl+A, Ctrl+V, tudo chegando na
sessão remota. SSH usa o mesmo `Tab.HandleKey` e não foi testado ao vivo
nesta rodada, mas o caminho é idêntico ao do VNC (keysym), então deve
funcionar igual — vale uma conferência quando der.

O caminho ficou em [entrada_outros.go](cmd/acessos/entrada_outros.go)
(`tratarTecladoFrame`, chamado do `main.go` a cada quadro) e
[teclado_outros.go](cmd/acessos/teclado_outros.go) (as tabelas de
tradução). Diferente do que este item dizia antes: não foi preciso ler
scancode cru do `WM_KEYDOWN` — o `key.Event` do Gio no Windows já entrega
`Modifiers` confiável (ao contrário do Wayland, que foi por isso que o
`grab` nasceu) e um `Name` estável por tecla física (não pelo caractere
já deslocado por Shift). Duas pegadinhas que custaram para achar, caso
mexam aqui de novo:

- um `key.Filter{Name: ""}` sem `Optional` só combina com teclas **sem
  nenhum modificador** — Ctrl+V inteiro (incluindo o Ctrl e a V) era
  descartado pelo roteador do Gio antes de chegar no app. É preciso
  `Optional: ModCtrl|ModShift|ModAlt|ModSuper|ModCommand`;
- pedir o foco com `key.FocusCmd` não basta: sem um `event.Op(gtx.Ops,
  tag)` chamado no MESMO quadro, o roteador não marca o alvo como
  "focusable"/"visible" e desfaz o foco sozinho ao fim do quadro,
  silenciosamente.

Limitações conhecidas, aceitas por ora:

- layout assumido é US — símbolos que dependem de outro layout (ex.:
  teclado ABNT2, acentos mortos) não têm tabela ainda
  ([teclado_outros.go](cmd/acessos/teclado_outros.go));
- Ctrl/Alt/Shift sempre viram a variante ESQUERDA (o Windows não
  distingue no `key.Event` sem ir atrás do scancode cru); AltGr não foi
  testado;
- os atalhos do próprio app (F12, Ctrl+W, Ctrl+G) só respondem com uma
  sessão remota em foco — fora dela o app não disputa o foco de teclado
  do Gio com o resto da interface (ver comentário em
  `tratarTecladoFrame`).

## 2. Área de transferência no Windows — RESOLVIDO (2026-09-16)

Testado ao vivo: `Set-Clipboard` local + Ctrl+V numa sessão VNC trouxe o
texto certo do outro lado. A ponte é a mesma função-quadro do item 1
(`tratarClipboardFrame`, também em entrada_outros.go): usa
`clipboard.ReadCmd`/`WriteCmd` do próprio Gio, que no Windows já
resolvem contra a API do sistema — só faltava alguém chamando.

## 3. Terminal SSH: teclas de navegação e cursor — RESOLVIDO PARA LINUX (2026-09-16)

Testado ao vivo contra um servidor de verdade
([sshtab_aovivo_test.go](cmd/acessos/sshtab_aovivo_test.go), desligado por
padrão, ligado por `ACESSOS_SSH_AOVIVO`): seta pra cima recupera comando
do histórico do bash, e um roteiro completo dentro do `nano` — abrir,
descer com seta, ir pro fim da linha com End, digitar, salvar (Ctrl+O
+ Enter), sair (Ctrl+X) — bate exatamente com o que o `cat` lê de volta
do disco depois. `HandleKey -> bytesDaTecla -> stdin -> PTY remoto`
funciona ponta a ponta no caminho Linux/Wayland; **o caminho Windows
continua sem teste ao vivo** (mesma ressalva do item 1).

O que os testes tentaram reproduzir NÃO era o bug: era outro, achado no
caminho. `copiarSelecao()` (Ctrl+Shift+C sem seleção nenhuma, "copia a
tela inteira") fazia `t.term.Lock(); t.term.String(); t.term.Unlock()` —
mas o `String()` do vt10x **tranca o mutex por conta própria**, ao
contrário de `Cell`/`Cursor`/`Size`/`HistoryCell` (que exigem o chamador
travar). Travar duas vezes o mesmo mutex não-reentrante na mesma goroutine
trava pra sempre. Ou seja: **Ctrl+Shift+C sem nada selecionado no SSH
congelava o app inteiro**, silenciosamente, desde sempre. Corrigido
(removido o Lock/Unlock redundante); o vt10x segue sendo a única API do
pacote que se comporta assim — vale desconfiar dela de novo se aparecer
outro `.String()` em volta de Lock/Unlock.

Cursor virou barra piscante (2dp, cor `tema.Azul` sólida, sem alfa —
ver item da paleta clara abaixo), com o piscar suspenso por
`piscarPeriodo` (530ms) sempre que uma tecla acabou de ser mandada, pra
não "sumir" bem no instante em que a pessoa está olhando pra ele.

O que ficou de fora, de propósito:

- **captura física do teclado no Wayland** ([internal/grab](internal/grab/))
  não tem como ser exercitada por um teste — não existe compositor nem
  teclado físico pra simular daqui. Revisão estática do C não achou nada
  suspeito para as setas especificamente;
- **AltGr e layouts não-US** continuam sem tabela (ver item 1);
- **DECSCUSR** (a aplicação remota escolher a forma do cursor — é assim
  que o vim vira barra no modo de inserção) não é suportado; o vt10x
  vendorizado não interpreta esse código. O cursor daqui é sempre barra,
  não segue pedido da aplicação. Se um dia isso importar, é mais um
  patch local no mesmo espírito do scrollback/bracketed-paste abaixo.

## 3b. Terminal SSH: relato de mouse e colar condicional — RESOLVIDO (2026-09-16)

Duas lacunas do terminal que o ficaram de fora da 2.x original, ambas
testadas ao vivo contra um `htop` de verdade.

**Relato de mouse** (clique/roda dentro de `htop`/`less`/`mc`): o vt10x
já rastreava os modos (`ModeMouseButton`, `ModeMouseSgr` etc.) desde
sempre, só que ninguém olhava pra eles. Agora `HandlePointer` checa
`t.term.Mode()` — com algum modo de mouse ligado, clique/arrasto/roda
viram sequência xterm (SGR quando disponível, senão o formato legado de 1
byte por campo, limitado a 223 colunas/linhas por protocolo, não por
escolha nossa) em vez de virar seleção/scrollback local. Shift força o
comportamento local mesmo com o modo ligado — a mesma válvula de escape
que xterm/gnome-terminal têm, pra copiar um pedaço de tela mesmo dentro de
um TUI que capturou o mouse. Testado contra `htop 3.0.5` real: ele liga
SGR ao abrir, e seis "rodas pra baixo" mandadas por `HandlePointer` fazem
a lista de processos rolar de verdade (visível no antes/depois da tela).
Onde mora: [sshtab_mouse.go](cmd/acessos/sshtab_mouse.go) (a montagem dos
bytes, pura, testada em [sshtab_mouse_test.go](cmd/acessos/sshtab_mouse_test.go))
e o `HandlePointer` em [sshtab.go](cmd/acessos/sshtab.go).

**Bracketed paste condicional**: antes, todo Ctrl+Shift+V envolvia o
texto em `\x1b[200~`/`\x1b[201~` incondicionalmente, mesmo quando o
programa remoto não pediu (`CSI ?2004h`) nem entende a marcação — nesse
caso os bytes de abertura/fechamento chegavam como se tivessem sido
digitados. O vt10x vendorizado ganhou `ModeBracketPaste` (mais um patch
local, documentado em [third_party/vt10x/PATCH.md](third_party/vt10x/PATCH.md),
mesmo espírito do patch de scrollback) e `colarDoSistema` só envolve
quando o modo está de fato ligado.

Consequência: a linha "sem relato de mouse" na lista de limitações
conhecidas no fim deste arquivo não vale mais — removida.

## 3c. Tema claro no terminal SSH — RESOLVIDO (2026-09-16)

`TermBg`/`TermFg` e a paleta ANSI de 16 cores eram fixos e escuros nos
dois temas — o resto da interface trocava com `tema.Escuro`, o terminal
não. Agora `Tema` carrega os tokens do terminal (`TermBg`, `TermFg`,
`TermSel`, `Ansi [16]color.NRGBA`) e cada tema define a paleta inteira já
calibrada pro próprio fundo, em vez de uma paleta única com o fundo
trocado por baixo — o claro precisou escurecer bem mais o amarelo (o pior
caso de contraste em terminal claro) e parar de usar branco puro pro
índice 15, que sumiria contra o fundo.

Decisão de propósito: **seleção e cursor viraram cor sólida, sem alfa em
tempo de desenho** (`TermSel` por tema, cursor usa `tema.Azul` direto). O
valor antigo era `tema.Azul` com alfa calculada em cima de um fundo fixo
(`#0d1117`); virar tema deixaria essa conta errada por design — o mesmo
alfa sobre um fundo bem mais claro lava quase invisível, e transparência
calculada por cima de fundo variável é frágil de mexer depois. Os tons
sólidos de `TermSel` foram pré-calculados a partir do que a transparência
antiga resolvia visualmente, não escolhidos no escuro.

Onde mora: `Tema` em [tema.go](cmd/acessos/tema.go) (`temaClaro`/
`temaEscuro`); consumido em `corVT`/`desenharGrade` em
[sshtab.go](cmd/acessos/sshtab.go).

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

## 7. Crash do app inteiro num disconnect abrupto de RDP — RESOLVIDO PARA RDP (2026-09-16)

O diagnóstico continua valendo e vale a pena guardar: o servidor derruba a
sessão (`ERRINFO_RPC_INITIATED_DISCONNECT`) e o processo inteiro morre com
SIGSEGV dentro do **próprio FreeRDP** (`dvcman_channel_close`, chamado de
`drdynvc_order_recv`, na thread do canal dinâmico), não no
[rdpshim.c](internal/rdp/rdpshim.c). É a vizinhança da
[CVE-2026-56297](https://github.com/FreeRDP/FreeRDP/security/advisories/GHSA-3mv2-5q57-2v8h),
cuja correção catalogada já está no 3.31.1 que vendorizamos — ou seja, é uma
variante residual: `dvcman_channel_close` mexe em
`channel->state`/`channel->channel_callback` sem o `channel->lock` que a
struct já tem.

Dos três caminhos listados aqui antes, foi feito o terceiro, que era o
estrutural: **cada sessão RDP roda em processo próprio**. O app reexecuta a
si mesmo (`acessos -tela-worker rdp <endereço> <token>`) e conversa com o
filho por socket em 127.0.0.1. Agora um SIGSEGV dentro da libfreerdp mata o
FILHO; o processo principal só vê o canal fechar, marca a aba como "CAIU" e
o backoff de reconexão que já existia religa aquela aba. As outras abas nem
ficam sabendo, porque deixaram de compartilhar o mesmo heap C.

Onde mora:

- [internal/telaproc](internal/telaproc/) — protocolo e transporte, Go puro
  (sem cgo, compila em qualquer plataforma). O porquê de socket TCP local
  em vez de stdin/stdout está no cabeçalho de `protocolo.go`: a libfreerdp
  escreve nos descritores padrão (WLog), e no Windows o próprio app os
  redireciona para o arquivo de log — qualquer byte dela corromperia o
  fluxo binário;
- [telaworker.go](cmd/acessos/telaworker.go) — o app rodando como filho:
  sem janela, biblioteca C de um lado e socket do outro;
- [rdptab.go](cmd/acessos/rdptab.go) — a aba, que agora manda entrada e
  recebe retângulos de tela em vez de chamar `internal/rdp` direto.

Provado ao vivo contra servidor real
([telaworker_aovivo_test.go](cmd/acessos/telaworker_aovivo_test.go),
desligado por padrão, ligado por `ACESSOS_RDP_AOVIVO`): `SIGKILL` no filho
no meio da sessão, o principal percebe como EOF e religa num processo novo.
O `ERRINFO_RPC_INITIATED_DISCONNECT` original chegou a acontecer durante os
testes — e matou só o filho.

De quebra, a aba ficou mais leve. Antes ela copiava a tela remota inteira do
C e convertia BGRX→NRGBA pixel a pixel A CADA QUADRO DA INTERFACE, mesmo
sem nada ter mudado do lado remoto. Agora o filho manda só o retângulo sujo,
já convertido, e a thread de desenho só encosta em pixel quando chega quadro
novo. Medido ao vivo numa sessão de 1024x768: com a tela calma, 1.792 bytes
por quadro contra 3.145.752 da tela cheia.

O que ficou de fora, de propósito:

- **VNC continua dentro do processo principal.** O transporte já nasceu
  agnóstico para receber os dois; falta o worker de VNC e reescrever a
  `vnctab.go` do mesmo jeito. Ao fazer isso, releia o teto de sessões
  abaixo — ele foi calibrado só com RDP em processo próprio;
- **teto de sessões simultâneas**: `telaproc.MaxSessoes` (hoje 12). Cada
  sessão passou a custar um processo, e a medição real de um filho é RSS
  118 MiB / PSS 94 MiB / **privada 87 MiB** — o que manda é a privada. 12
  deixa o pior caso em ~1 GiB. **Esse número não é chute e não deve ser
  mexido por estimativa**: `TestAoVivoCustoDeUmFilho` mede um filho de
  verdade, e é ela que precisa ser refeita antes de mudar o teto. O
  raciocínio inteiro está no comentário da constante em
  [processo.go](internal/telaproc/processo.go);
- **reportar upstream ao FreeRDP** continua valendo, e agora com menos
  pressa: o bug deixou de ser fatal aqui, mas segue sendo bug deles.

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

Ficam registradas para ninguém "descobrir" de novo:

- **Terminal SSH**: sem busca no scrollback (o scrollback em si existe,
  20000 linhas — é busca DENTRO dele que não tem, tipo o Ctrl+Shift+F de
  um gnome-terminal). Relato de mouse para `htop`/`less`/TUIs em geral
  passou a existir em 2026-09-16 (ver item 3b) — o que continua de fora é
  o modo "qualquer movimento" (1003) sem nenhum botão apertado, que o Gio
  não entrega nesta pilha (só chega evento de arrasto com botão). A
  seleção com o mouse existe desde a 2.0.4, mas é da TELA VISÍVEL: não
  acompanha o conteúdo se o programa remoto redesenhar por baixo.
  (Tela alternativa — o modo que `vim`/`less`/`nano` usam para tela
  cheia — já é tratada pela biblioteca de terminal por baixo; testado
  com `nano` sem problema.)
- **Atalhos globais**: não existem, por pedido explícito.
