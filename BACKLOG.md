# Backlog

O que falta para fechar o porte. Ordem de cima para baixo é a ordem de
prioridade acordada; o que já está pronto não mora aqui (o histórico do
git e o metainfo contam essa parte).

Aqui só entra TRABALHO A FAZER. Conferência não mora neste arquivo:
"olhar se tal coisa ficou certa" e "confirmar na outra máquina" viram
tarefa de quem estiver naquela máquina, na hora — o que precisa ser
compilado ou testado no Windows se resolve no Windows, e o que já foi
corrigido aparece no git. Item que só pede um olhar vira ruído e empurra
para baixo o que é para ser feito.

Os números foram refeitos em 2026-09-19, do 1 em diante. Antes disso a
numeração tinha buracos, porque os itens resolvidos saíam e os que
ficavam mantinham o número — com a lista curta isso deixou de valer a
pena. Mensagem de commit antiga que fale em "item 7d", "item 9" e afins
se refere à numeração velha; o git conta o que era.

1. Busca por atalho global — as pontas que ficaram
2. Cursor remoto — conferir contra máscaras de verdade
3. Capturas de tela do metainfo
4. Tela cheia
5. FreeRDP do Windows compilado com flag experimental

Atualizado em 2026-09-19.

---

## 1. Busca por atalho global — as pontas que ficaram

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

## 2. Cursor remoto: conferir contra máscaras de verdade

A classificação de forma foi reescrita em 2026-09-18 (fim da "mão presa" e
do "ocupado" que nunca aparecia) e é exercitada por silhuetas desenhadas à
mão em [cursorforma_test.go](cmd/acessos/cursorforma_test.go), com as
proporções dos cursores reais. O que falta é o passo seguinte, e barato:
capturar as MÁSCARAS de verdade de uma sessão Windows (o `RS_LOG=1` do
rdpshim já imprime tamanho e hotspot de cada uma; falta despejar os bytes)
e virar caso de teste. Sem isso, os limiares continuam calibrados por
proporção, não por amostra.

## 3. Capturas de tela do metainfo

As cinco imagens de [screenshots/](screenshots/) são da versão Python.
Decisão sua, de propósito, para não segurar o lançamento — mas a loja
mostra uma interface que não existe mais. Trocar quando a 2.x estiver
assentada.

## 4. Tela cheia

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
  com o que a sessão remota também usa, mesmo cuidado que F12, Ctrl+W e
  Ctrl+G já tiveram que ter quando foram resolvidos (está no git).

## 5. FreeRDP do Windows compilado com flag experimental

O pacote `freerdp` do MSYS2 (3.31.1-1, o mesmo que o instalador embarca) é
compilado com `WITH_VAAPI_H264_ENCODING=ON`, e a própria libfreerdp avisa
a cada conexão que "[experimental] build options might crash the
application". VA-API é coisa de Linux e o caminho não deve nem ser
exercitado por um cliente no Windows, mas é a única diferença de BUILD
conhecida entre o FreeRDP do Windows e o do Flatpak — e queda "sem
motivo" no Windows é justamente o que se estava caçando.

O trabalho é: ver se uma versão mais nova do pacote sai sem a flag e, se
não sair, compilar o FreeRDP do zero para o sysroot do instalador.
