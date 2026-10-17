# Cópia do github.com/hinshun/vt10x@v0.0.0-20220301184237-5011da428d02 com um patch

Vendorizado por causa de DUAS lacunas.

A primeira: o vt10x não guarda nenhum scrollback — `scrollUp` (em
`state.go`) limpa as linhas que saem por cima da tela ANTES de
descartá-las, então o conteúdo simplesmente desaparece. Era a causa do
terminal SSH do Acessos não ter rolagem nenhuma (histórico), diferente
de qualquer terminal de verdade.

Scrollback, o patch:

- adiciona `historia []line` e `historiaMax int` a `State` (`state.go`),
  com um valor padrão (`historiaPadrao = 10000`) definido em `newState`;
- em `scrollUp`, captura uma CÓPIA das linhas que estão saindo pelo topo
  ANTES do `t.clear(...)` que as apaga — só quando `orig == 0` (é o topo
  da tela real, não uma região de rolagem parcial) e o modo NÃO é
  `ModeAltScreen` (a tela alternativa, que `vim`/`less`/`nano` usam em
  tela cheia, não deve poluir o scrollback do shell — nenhum terminal de
  verdade faz isso);
- expõe `(*State) HistoryLen() int` e `(*State) HistoryCell(x, idx int)
  Glyph` (idx 0 = linha mais antiga), promovidas para a interface `View`
  em `vt.go`;
- adiciona a opção `WithScrollback(n int)` em `vt.go` para quem monta o
  terminal escolher o tamanho (0 desliga; sem a opção vale o padrão).

A segunda: o `CSI ?2004h`/`l` (bracketed paste) caía no `default` de
"unknown private set/reset mode" — o vt10x nunca soube dizer se a
aplicação remota pediu a marcação `\x1b[200~`/`\x1b[201~` em volta de um
colar. Sem isso, o app sempre envolvia todo colar nessa marcação, mesmo
para um programa que não pediu e não entende — a marcação em si vira
texto digitado. Bracketed paste, o patch:

- adiciona `ModeBracketPaste` à enumeração `ModeFlag` em `state.go`;
- liga o `case 2004` do switch de modos privados (a função que já trata
  9/1000/1002/1003/1004/1006/…) a `t.modMode(set, ModeBracketPaste)` —
  mesma função que os outros modos já usavam, só faltava o `case`.

Nenhuma mudança em `vt.go`: `ModeBracketPaste` já sai pela `Mode()`
existente, que promove `t.mode` inteiro — não precisa de getter próprio
como o scrollback precisou.

Ligado ao build pelo `replace github.com/hinshun/vt10x => ./third_party/vt10x`
no `go.mod`. Ao subir a versão do vt10x: recopiar do module cache e
reaplicar este trecho (procure por "patch acessos" nos arquivos).

## Terceira rodada (2026-10-17): telas de ncurses fora do lugar

Sintoma: programa de tela cheia (mc, htop, dialog, telas do PDV) abria
com bordas curtas e tudo à direita delas deslocado, ou perdia a cor no
meio. Cada item abaixo é uma sequência que o terminfo do
`xterm-256color` (que o sshtab pede) anuncia e o vt10x ignorava. Os
casos estão em `acessos_test.go`.

- **REP (`CSI n b`)**: o ncurses usa `rep` para qualquer trecho
  repetido (bordas, linhas de `─`, preenchimento). Sem ele cada borda
  saía com UM caractere. `repetirUltimo` reescreve `ultimoChar` pelo
  mesmo caminho de um caractere comum (`parse`).
- **Modo de inserção (IRM, `CSI 4 h`)**: era só um log "not
  implemented"; agora `parse` empurra a linha com `insertBlanks(1)`.
- **G0/G1 + SO/SI**: `ESC ) 0` era ignorado e `^N`/`^O` também, então
  terminfo que desenha borda com `smacs=^N` (`linux`, `screen`) saía
  em letras. `trantbl[2]`/`charset` guardam os dois conjuntos,
  `aplicarCharset` traduz o ativo para o `attrGfx` que `setChar` lê, e
  DECSC/DECRC salvam/devolvem os dois junto com o cursor.
- **Prefixos `>`, `=`, `<` no CSI**: só o `?` era reconhecido. Um
  `CSI > 4;2 m` (modifyOtherKeys, que o vim manda ao abrir) perdia os
  argumentos no `Atoi` e virava `SGR 0` — zerava a cor no meio da
  tela. Agora `csiEscape.marker` guarda o prefixo, e sequência com
  prefixo diferente de `?` é ignorada — exceto DA2 (`CSI > c`), que
  recebe `\e[>0;0;0c`.
- **DA1 (`CSI c`)**: respondido com `\e[?62;22c`. Programa que
  pergunta e não ouve resposta espera o timeout.
- **`ED 3`** (limpar com scrollback, xterm): limpa a tela e a `historia`.

Bugs do próprio vt10x achados de passagem, corrigidos no mesmo patch:

- `reset()` chamava `clear(0, 0, rows-1, cols-1)` com x e y trocados:
  num 80x24 o RIS limpava só as 24 primeiras colunas.
- `resize()` gravava as paradas de tabulação novas na fatia VELHA
  (`tabs`), nunca em `t.tabs`: alargar o terminal deixava as colunas
  novas sem tab.
- `ED 1` testava `cur.Y > 1` em vez de `> 0`: com o cursor na linha 1 a
  linha 0 ficava sem limpar.
- `parse` fazia `t.logf("%q", string(c))` por rune, alocando a string
  mesmo sem logger; agora só com `DebugLogger`.
