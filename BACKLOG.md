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

Os números foram refeitos em 2026-09-19, do 1 em diante, e correram duas
vezes em 2026-09-20: quando o item do cursor remoto saiu resolvido, quando
o da busca por atalho global saiu, e quando a tela cheia saiu junto com o
destacamento da sessão. Antes disso a numeração tinha
buracos, porque os itens resolvidos saíam e os que ficavam mantinham o
número — com a lista curta isso deixou de valer a pena. Mensagem de
commit antiga que fale em "item 7d", "item 9", no "item 2" do cursor ou
no "item 1" da busca, se refere à numeração de então; o git conta o que
era.

1. Capturas de tela do metainfo
2. Sessão remota em várias telas — parado, ver o aviso

Atualizado em 2026-09-20.

---

## 1. Capturas de tela do metainfo

As cinco imagens de [screenshots/](screenshots/) são da versão Python.
Decisão sua, de propósito, para não segurar o lançamento — mas a loja
mostra uma interface que não existe mais. Trocar quando a 2.x estiver
assentada.

## 2. Sessão remota em várias telas

Ideia levantada em 2026-09-20: destacar a sessão RDP em janela própria e
poder ESTENDÊ-LA por mais de um monitor. **Parado de propósito, e não por
falta de tempo** — a parte de várias telas esbarra em coisa que não é
nossa:

- **Uma superfície Wayland não se estende por dois monitores.**
  `xdg_toplevel.set_fullscreen` é em UM output, ponto. Cobrir dois é
  abrir DUAS janelas, cada uma cheia no seu monitor, cada uma desenhando
  um recorte do mesmo framebuffer remoto — e o cliente ainda não pode
  posicionar as próprias janelas no Wayland.
- **O Gio não expõe escolha de monitor.** O fork chama
  `xdg_toplevel_set_fullscreen(topLvl, nil)` (ver
  third_party/gio/app/os_wayland.go): o `nil` é o output, ou seja
  "compositor que decida". Nomear um monitor pede mais um patch, e antes
  disso pede enumerar `wl_output`, que o Gio também não expõe.
- **A captura de teclado é singleton por construção.** internal/grab tem
  `onKey`/`onClipboardText` globais, com o comentário dizendo que é de
  propósito ("este app tem uma janela/grab por vez"), e `Start` avisa no
  stderr quando é chamado duas vezes. Cada janela nova precisaria da
  própria captura — dá para resolver movendo os globais para dentro do
  Handle, no mesmo padrão que `internal/cgoregistry` já usa em rdp/vnc,
  mas é trabalho de verdade.
- **Do lado do servidor**, mandar N monitores é negociação:
  `SendMonitorLayout` hoje vai fixo em 1 (rdpshim.c) e o servidor precisa
  anunciar `disp_max_monitores >= N` no DisplayControlCaps. Ninguém
  mediu ainda o que os servidores daqui anunciam — se vier 1, o item
  morre antes de começar. Medir é barato (instrumentar o `RS_LOG` numa
  sessão real) e é o primeiro passo se isto for retomado.

Some ainda o rearranjo de monitor ser da mesma família do resize, que já
derrubou sessão: cada mudança remonta as surfaces gfx no servidor
(RDPGFX_RESET_GRAPHICS) — ver o debounce em rdptab.go.

Trabalho para muito depois. A janela própria SEM várias telas (só
destacar) é bem mais barata e não depende de nada disto além da captura
por janela.
