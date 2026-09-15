# Cópia do github.com/hinshun/vt10x@v0.0.0-20220301184237-5011da428d02 com um patch

Vendorizado por causa de UMA lacuna: o vt10x não guarda nenhum
scrollback — `scrollUp` (em `state.go`) limpa as linhas que saem por
cima da tela ANTES de descartá-las, então o conteúdo simplesmente
desaparece. Era a causa do terminal SSH do Acessos não ter rolagem
nenhuma (histórico), diferente de qualquer terminal de verdade.

O patch:

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

Ligado ao build pelo `replace github.com/hinshun/vt10x => ./third_party/vt10x`
no `go.mod`. Ao subir a versão do vt10x: recopiar do module cache e
reaplicar este trecho (procure por "patch acessos" nos arquivos).
