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
