# Backlog

O que falta para fechar o porte. Ordem de cima para baixo é a ordem de
prioridade acordada; o que já está pronto não mora aqui (o histórico do
git e o metainfo contam essa parte).

Atualizado em 2026-09-12, com a 2.0.3 publicada.

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

## 3. Atualizador do Windows

**Estado: não existe.** O [internal/atualizador](internal/atualizador/atualizador.go)
de hoje é só do Flatpak: `EmFlatpak()` desliga tudo fora dele, e
`Instalar` chama `flatpak-spawn --host flatpak install`.

A mecânica no Windows é mais simples que a do Flatpak, e o instalador já
foi feito pensando nela — o `AppId` do Inno Setup é fixo, então instalar
por cima é atualizar, não duplicar:

- ler a release mais nova na API do GitHub (a função `Checar` já faz, e
  a comparação de versão já corrige o bug do `atualizador.py` com
  `2.0.0-rc1`) e procurar o anexo `AcessosSetup-X.Y.Z.exe`;
- baixar para `%LOCALAPPDATA%\Temp`, **conferir o sha256** publicado
  junto da release, e só então executar com `/SILENT /NORESTART`;
- o instalador fecha o app, substitui e reabre.

Falta também publicar o sha256 na release (hoje o `build-windows.sh` não
gera nenhum). Enquanto isso não existe, atualizar no Windows é baixar o
instalador na mão.

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

- **Terminal SSH**: sem tela alternativa (`vim`/`less` em tela cheia
  podem não desenhar direito), sem busca no scrollback e sem relato de
  mouse. É o escopo que foi combinado para o v1 do terminal.
- **Seleção de texto no terminal**: copiar manda a tela inteira, não uma
  seleção de mouse.
- **Atalhos globais**: não existem, por pedido explícito.
- **RDP no Wine**: não conecta, e isso é limitação do Wine
  (`ucrtbase._aligned_recalloc` não implementada, e a própria FreeRDP a
  usa). No Windows de verdade funciona — validado.
