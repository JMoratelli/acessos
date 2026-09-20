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
libvncclient). O lado Linux NÃO foi compilado: falta cross-toolchain
para o cgo. Nada aqui é suspeita de defeito; é a conferência que a outra
metade do porte exige.

- **`go mod vendor` ANTES de compilar.** `third_party/gio` mudou (patch
  décimo terceiro: `app.Translucent` no Windows). Sem isso o build local
  usa a cópia velha em `vendor/gioui.org` e falha com método inexistente
  — é a armadilha que o CLAUDE.md descreve e que já mordeu mais de uma
  vez.
- **`go build ./...` e `go test ./...`.** Os testes do `cmd/acessos`
  mudaram de forma: `memoriaDe` saiu do arquivo de teste ao vivo para
  `memoriaproc_posix_test.go` (o irmão Windows é
  `memoriaproc_win_test.go`), e os três testes ao vivo (RDP, VNC, SSH)
  deixaram de ser `//go:build linux`. Rodar os ao vivo lá também, com
  `ACESSOS_RDP_AOVIVO`/`ACESSOS_VNC_AOVIVO`/`ACESSOS_SSH_AOVIVO`, para
  confirmar que a troca de `syscall.Kill` por `os.Process.Kill` não
  mudou nada no Linux.
- **Olhar a caixa do atalho global.** Ela ficou 14dp mais alta
  (`buscaCartaoAlt` foi de 76 para 90): o rodapé "Enter conecta · Esc
  fecha · mais N — refine o termo" NUNCA coube, nos dois sistemas, e
  agora cabe. Conferir que no KWin o cartão continua certo e que a linha
  aparece.
- **X11 ganhou comportamento novo.** A caixa agora pede
  `system.ActionCenter` depois do primeiro quadro. No Wayland a ação é
  ignorada (quem posiciona é o compositor), mas no X11 o Gio a
  implementa de verdade — conferir que ela não briga com o
  posicionamento do gerenciador de janelas.
- **Gerar o instalador do Windows a partir daí**
  (`scripts/build-windows.sh --instalador`) para fechar duas pontas que
  só um instalador novo confirma: o provider legacy do OpenSSL (item 9)
  e o ícone do executável (item 4). O passo novo copia
  `ossl-modules/legacy.dll` do sysroot e o build FALHA se ele não
  estiver lá — de propósito.

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
relato juntava duas coisas diferentes; uma está fechada, a outra não.

**Fechada (2026-09-19): os "ícones" da interface que não renderizavam.**
Não eram ícones — eram CARACTERES de texto que a IBM Plex embutida não
tem: `▸`/`▾` nos blocos do editor de conexão, `⟳` no recarregar do SFTP,
`⌁` no botão de teclas da barra de sessão. No Linux o shaper cai numa
fonte do sistema e ninguém vê; no Windows não há em quem cair e sai o
quadradinho vazio. As setas viraram ícone vetorial (`setaExpansor`, em
[tema.go](cmd/acessos/tema.go)), o `⟳` virou `↻` (que a Plex tem) e o
`⌁` saiu. [glifos_test.go](cmd/acessos/glifos_test.go) agora quebra o
build se entrar símbolo novo que as fontes embutidas não tenham.

**Aberta: o ícone do executável e do instalador.** Uma causa concreta foi
achada e corrigida em 2026-09-19, no
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

Auditoria pedida depois de queixa de queda/glitch em sessão RDP. Cinco
causas confirmadas já foram corrigidas: ordem invertida de
captura/consumo de dano deixando remendo permanente na tela (comum a
RDP e VNC — ver `enviarQuadro()` em
[telaworker.go](cmd/acessos/telaworker.go)), tempestade de `CmdResize`
ao arrastar a borda da janela sem debounce (só RDP — canal Display
Control não existe no VNC), callbacks da libfreerdp/libvncclient
(`OnCursor`/`OnClipboardText`/`OnDisplayPronto`) escrevendo direto no
socket e podendo travar a sessão inteira esperando o mesmo mutex que um
`EvtQuadro` grande usa, `rs_processar` (rdpshim.c) engolindo uma falha
de `freerdp_check_event_handles` e caindo num laço quente (100% de CPU,
tela congelada, sem reconectar), e — achado em 2026-09-19, efeito
colateral da correção anterior — `OnDisplayPronto` competindo por vaga
com `OnCursor`/`OnClipboardText` na mesma fila de descarte-se-cheia:
como é um evento ÚNICO por sessão (não "estado atual" como os outros
dois), perder essa vaga numa rajada de conexão deixava a aba presa na
resolução padrão do servidor até a janela ser redimensionada na mão.
Ganhou canal próprio (`workerRDP.dispPronto` em
[telaworker_rdp.go](cmd/acessos/telaworker_rdp.go)).

De quebra, `rs_processar`/`rs_esperar` agora guardam o motivo específico
do FreeRDP (`freerdp_get_last_error_string`) antes de marcar a sessão
como caída, e `Run()` devolve esse motivo em vez do genérico "conexão
perdida" — foi o que permitiu identificar, no mesmo dia, que uma queda
recorrente contra um host específico era `ERRINFO_RPC_INITIATED_
DISCONNECT` (ferramenta administrativa NO SERVIDOR derrubando a sessão
a cada ~35s) — nada a corrigir aqui, é comportamento do servidor.

**Causa nova, achada e corrigida em 2026-09-19 testando ao vivo a partir
do Windows: o provider "legacy" do OpenSSL nunca ia no instalador.** A
libcrypto empacotada vem do MSYS2 e traz compilado o MODULESDIR
`/ucrt64/lib/ossl-modules`, que não existe fora de quem tem o MSYS2; e o
`scripts/dlls-windows.py` não podia pegá-lo, porque percorre a tabela de
importação do PE e provider é carregado em tempo de execução, pelo nome.
Sem ele não há MD4 nem RC4 — o FreeRDP perde NTLM e o cookie de
autoreconnect. Traduzindo: login recusado contra servidor que não fecha
por Kerberos (o caso normal ao conectar por IP), e queda passageira que
não se resolve sozinha. As duas assinaturas estavam no freerdp.log desta
máquina. O build passa a copiar `ossl-modules/legacy.dll` para o lado do
.exe e o app aponta o `OPENSSL_MODULES` para lá no start (ver
cmd/acessos/ossl_windows.go). **Vale um instalador novo para confirmar na
prática** — aqui foi conferido com o layout do dist montado à mão, com
controle negativo.

**Sobra um suspeito no mesmo lugar**: o pacote `freerdp` do MSYS2
(3.31.1-1, o mesmo que o instalador embarca) é compilado com
`WITH_VAAPI_H264_ENCODING=ON`, e a própria libfreerdp avisa a cada
conexão que "[experimental] build options might crash the application".
VA-API é coisa de Linux e o caminho não deve nem ser exercitado por um
cliente no Windows, mas é a única diferença de BUILD conhecida entre o
FreeRDP do Windows e o do Flatpak — e queda "sem motivo" no Windows é
justamente o que se está caçando. Conferir se uma versão mais nova do
pacote sai sem a flag antes de considerar compilar o FreeRDP do zero para
o sysroot.

**Os testes ao vivo já rodam no Windows** (soltos da tag `linux` em
2026-09-19) e foram exercitados contra máquinas de verdade a partir
daqui: RDP 1024x768 em 60 quadros seguidos sem queda, gastando 30% do
que seria mandar tela cheia toda vez; VNC em 150 quadros, 9%; morte do
filho a ferro não derruba o principal, nos dois protocolos. O que sobra
abaixo continua sobrando — mas agora dá para validar no sistema onde as
quedas foram relatadas.

O que ficou de fora, por ser mais arriscado de mexer sem um teste ao
vivo (`ACESSOS_RDP_AOVIVO`) validando cada mudança:

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
  ao recortar em Go) — em 4K é ~100MB de churn por quadro, contribuindo
  pro vigia de memória (`internal/telaproc/vigia.go`, teto de 768MiB)
  matar a sessão sob carga, o que aparece como queda "sem motivo".
