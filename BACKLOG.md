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

## 4. Capturas de tela do metainfo

As cinco imagens de [screenshots/](screenshots/) são da versão Python.
Decisão sua, de propósito, para não segurar o lançamento — mas a loja
mostra uma interface que não existe mais. Trocar quando a 2.x estiver
assentada.

## 5. Assinatura do executável do Windows

O instalador não é assinado, então o SmartScreen avisa em toda máquina
nova. Para distribuição interna é aceitável (o aviso passa com "Mais
informações"); para distribuir fora, não. Precisa de um certificado de
code signing — custo e decisão sua, não técnica.

## 6. Crash do app inteiro num disconnect abrupto de RDP — CONTIDO (2026-09-16)

O crash em si continua existindo dentro do FreeRDP; o que mudou é que ele
deixou de derrubar o app. **Cada sessão RDP agora roda em processo
próprio** — o terceiro caminho que esta lista descrevia, e o mais
estrutural. Falta VALIDAR AO VIVO contra o servidor que reproduziu o
crash: o esperado é a aba marcar "CAIU" e religar sozinha enquanto as
outras seguem intocadas.

Como ficou:

- [internal/telaproc](internal/telaproc/) é o canal entre os dois
  processos: socket TCP em 127.0.0.1 com token de 32 bytes, moldura de
  `tipo + tamanho + corpo`. Não é stdin/stdout de propósito — a libfreerdp
  escreve no stdout/stderr do processo (WLog) e corromperia o fluxo;
- o app **reexecuta a si mesmo** (`acessos -tela-worker rdp <addr>
  <token>`, ver [telaworker.go](cmd/acessos/telaworker.go)). Nada muda no
  Flatpak nem no instalador do Windows, e não há como as duas metades
  saírem de versão;
- [rdptab.go](cmd/acessos/rdptab.go) virou a ponta que manda entrada e
  recebe retângulos de tela. Crash do filho, desconexão limpa e queda de
  rede chegam aqui como a MESMA coisa (o socket fecha), e caem no backoff
  de reconexão que já existia;
- ao desconectar, o filho sai com `os.Exit` **sem** desmontar a sessão: a
  desmontagem (`dvcman_channel_close`) é justamente onde mora o crash, e
  não há nada a liberar que o fim do processo não libere melhor.

Dois ganhos que vieram junto, por o desenho obrigar a rastrear região
suja:

- a conversão BGRX→NRGBA pixel a pixel saiu da thread que desenha e
  passou a cobrir só o retângulo que mudou. Antes ela rodava sobre a tela
  INTEIRA a cada quadro da interface, mesmo sem nada ter mudado na sessão
  remota;
- há controle de fluxo por crédito: o filho só manda um quadro quando o
  processo principal diz que consumiu o anterior. Uma sessão muito ativa
  não enche mais a fila do socket mais rápido do que a interface desenha.

Ainda em aberto neste item:

- **VNC continua in-process.** O transporte já nasceu agnóstico de
  protocolo; falta escrever o `vncworker.go` e virar a chave no
  [vnctab.go](cmd/acessos/vnctab.go). Foi deixado de fora de propósito,
  para não dobrar a área de teste ao vivo numa rodada só;
- reportar o bug upstream ao FreeRDP continua valendo — conter não é
  corrigir, e quem usa `dvcman_channel_close` fora daqui segue exposto.

## 7. `unsafe.Pointer` mal usado nos handles de VNC e RDP

`go vet ./...` acusa duas linhas, as duas anteriores a qualquer coisa
desta lista: [rdp.go:94](internal/rdp/rdp.go) e
[vnc.go:62](internal/vnc/vnc.go). Os dois pacotes usam um CONTADOR (1, 2,
3…) convertido para `unsafe.Pointer` como contexto opaco dos callbacks em
C. Na prática funciona — o valor nunca é desreferenciado do lado Go, só
vai ao C e volta para virar chave de mapa —, mas é ilegal pelas regras do
`unsafe.Pointer`, e o `checkptr` (que o `-race` liga junto) aborta o
processo ao ver a conversão.

O efeito concreto hoje: o teste que sobe o processo-filho RDP de verdade
precisa ficar fora do `-race`
([telaworker_filho_test.go](cmd/acessos/telaworker_filho_test.go)).

Correção provável, pequena nos dois pacotes: usar o endereço de um objeto
de verdade alocado no heap como handle, guardando-o dentro da própria
`Session` para o coletor não o levar. Não foi feito nesta rodada por
tocar em dois pacotes testados ao vivo, e o item aqui é para essa decisão
ser sua e não minha.

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
