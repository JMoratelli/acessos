# Backlog

O que falta para fechar o porte. Ordem de cima para baixo é a ordem de
prioridade acordada; o que já está pronto não mora aqui (o histórico do
git e o metainfo contam essa parte).

Atualizado em 2026-09-14, com a 2.0.6 publicada.

---

## 1. Teclado nas sessões remotas do Windows — BLOQUEANTE

**Estado: não existe.** No Windows, VNC, RDP e o terminal SSH são hoje
**só leitura**: dá para ver a tela e usar o mouse, e nenhuma tecla chega
do outro lado.

Não é "falta mapear tecla", é que o caminho inteiro é do Linux:
`Tab.HandleKey` só é chamado de [entrada_linux.go](cmd/acessos/entrada_linux.go),
que lê o teclado direto do Wayland (`internal/grab`). Em
[entrada_outros.go](cmd/acessos/entrada_outros.go) a função é um corpo
vazio — e o comentário dela, dizendo que "o teclado vem do próprio Gio",
descreve uma intenção, não o que acontece.

O que precisa ser feito:

- consumir `key.Event` do Gio na aba ativa (hoje ninguém registra
  `event.Op` de teclado nas abas), e decidir o que fazer com o fato,
  já documentado em `main.go`, de que o `key.Event` do Gio **não entrega
  o "soltou" de Ctrl/Alt/Shift sozinhos** nesta pilha — foi por isso que
  o `grab` nasceu. Modificador preso é sessão remota inutilizável;
- traduzir para o que cada protocolo quer: VNC quer keysym X11, RDP quer
  scancode PS/2. No Windows o scancode vem de graça (é o que o
  `WM_KEYDOWN` já entrega) — é o caminho que o shim do Carlos usa, com a
  lista estática de teclas estendidas do lado da interface;
- o terminal SSH tem um encoder próprio ([keyencode.go](cmd/acessos/keyencode.go))
  que hoje só recebe keysym; ele também precisa de entrada nova.

Sem isto o app no Windows serve para olhar, não para operar.

## 2. Área de transferência no Windows

**Estado: não existe.** `currentGrab` é sempre nil fora do Linux, então
`SetClipboardText` não faz nada e nada é recebido do sistema. Copiar e
colar entre a máquina local e a sessão remota não funciona — nem no VNC,
nem no RDP, nem no SSH.

O motor dos dois lados já está pronto e é multiplataforma (os canais
CLIPRDR/VNC cut-text estão em `internal/rdp` e `internal/vnc`): falta só
a ponte com o clipboard do sistema. O ponto de entrada é o mesmo do item
1 — o Gio tem `clipboard.ReadCmd`/`WriteCmd`, que no Windows resolvem
sozinhos, e a publicação já está serializada no laço de quadro
(ver [clipboard.go](cmd/acessos/clipboard.go)).

## 3. Ícones no Windows

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

## 4. Diálogo "Sobre"

**Estado: não existe.** A versão aparece no rodapé e nos Ajustes, mas não
há uma tela dizendo o que é o programa, a licença (GPLv3) e o link do
repositório. O metainfo embutido já tem tudo isso — é montar a tela.

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

---

## Limitações conhecidas, que NÃO estão no plano de corrigir

Ficam registradas para ninguém "descobrir" de novo:

- **Terminal SSH**: sem busca no scrollback (não existe scrollback:
  o que sai da tela sai) e sem relato de mouse (programas que capturam
  clique/scroll dentro do terminal, tipo `htop` ou um menu TUI, não
  recebem o evento). É o escopo que foi combinado para o v1 do terminal.
  A seleção com o mouse existe desde a 2.0.4, mas é da TELA VISÍVEL: não
  acompanha o conteúdo se o programa remoto redesenhar por baixo.
  (Tela alternativa — o modo que `vim`/`less`/`nano` usam para tela
  cheia — já é tratada pela biblioteca de terminal por baixo; testado
  com `nano` sem problema.)
- **Atalhos globais**: não existem, por pedido explícito.
