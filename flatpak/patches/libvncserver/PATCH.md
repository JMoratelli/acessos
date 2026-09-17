# Patches de segurança sobre o LibVNCServer 0.9.15

Aplicados pelo manifesto (`flatpak/org.jj.Acessos.yml`, módulo
`libvncserver`) por cima do tarball da tag `LibVNCServer-0.9.15`.

## Por que existem

Porque **não há versão para onde subir**. A 0.9.15 é de dezembro de 2024 e
continua sendo a última release do projeto; as quatro CVEs abertas do
`libvncclient` são todas posteriores a ela. Isso cria uma armadilha: quem
comparar a versão fixada com a última publicada conclui que está tudo em
dia — e está mesmo, e ao mesmo tempo está sem as correções. Elas só
existem no master do LibVNC, sem release que as contenha.

É por isso que a diretriz de checagem mensal no `CLAUDE.md` manda olhar os
**advisories** do repositório, e não só as tags.

## O que está aplicado

Na ordem cronológica dos commits do upstream, que é a ordem em que aplicam
limpo:

| arquivo | commit upstream | data | CVE | sev. |
|---|---|---|---|---|
| `009008e2.patch` | `009008e2` | 2026-03-22 | CVE-2026-32853 | média |
| `5b270544.patch` | `5b270544` | 2026-05-06 | CVE-2026-44988 | alta |
| `540332be.patch` | `540332be` | 2026-05-29 | CVE-2026-50538 | alta |

- **CVE-2026-32853** — leitura fora dos limites no heap em
  `HandleUltraZipBPP`, por não conferir a contagem de sub-retângulos.
  Toca `src/libvncclient/ultra.c`.
- **CVE-2026-44988** — decodificação *gradient* do Tight permitindo
  escritas fora dos limites, no heap e na pilha, disparadas por um
  servidor malicioso. Toca `include/rfb/rfbclient.h` e
  `src/libvncclient/tight.c`.
- **CVE-2026-50538** — escrita fora dos limites no heap, controlada pelo
  atacante, no decodificador Tight. A correção é a conferência
  `rx + rw > client->width || ry + rh > client->height`, que rejeita
  retângulo maior que o framebuffer. Toca `src/libvncclient/tight.c`.

Uma quarta CVE (**CVE-2026-32854**, NULL deref nos handlers de proxy do
httpd) fica de fora de propósito: é do lado SERVIDOR do LibVNCServer, e
este projeto só usa o cliente. Aplicá-la seria divergir do upstream sem
ganho nenhum.

## Por que só o patch, e não isolar o VNC em processo

Chegou a ser considerado isolar as sessões VNC em processo próprio, como já
é feito com o RDP (ver `internal/telaproc`). Isso **não substitui** estes
patches, e a distinção importa: processo separado contém TRAVAMENTO, mas
estas CVEs são escrita fora dos limites — a classe que vira execução de
código. Um atacante que execute código dentro do processo-filho continua
com o usuário, a sandbox e o acesso a disco do app, e alcança o cofre e o
chaveiro do mesmo jeito. Conter só ajudaria contra bugs que ainda não
conhecemos; corrigir é o que fecha estes.

## Ao mexer aqui

- **Estes arquivos são descartáveis no bom sentido**: quando o LibVNC
  publicar uma release que já contenha as correções, apague os patches e
  suba a versão no manifesto. Fork local é dívida, não patrimônio.
- Se um patch parar de aplicar, é sinal de que o tarball mudou — confira o
  `sha256` do manifesto antes de sair mexendo nos `.patch`.
- Os arquivos são o `.patch` do GitHub sem edição
  (`https://github.com/LibVNC/libvncserver/commit/<sha>.patch`), de
  propósito: manter idênticos ao upstream deixa óbvio o que é deles e
  torna trivial conferir a procedência.
