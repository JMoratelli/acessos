# Diretrizes para trabalhar neste repositório

- Não mencionar Claude, Anthropic ou IA/assistente em nenhum lugar do código,
  comentários, mensagens de commit ou descrições de PR deste projeto.
- Não adicionar linhas de `Co-Authored-By: Claude ...` (ou equivalentes) nos
  commits.
- Uma vez por mês (não a cada sessão — era diário antes e virou ruído),
  checar se há versão nova das bibliotecas externas usadas no projeto:
  FreeRDP e libvncserver (versões fixas no manifesto Flatpak,
  `flatpak/org.jj.Acessos.yml`), o fork do Gio em `third_party/gio` (ver
  `third_party/gio/PATCH.md` para saber contra qual upstream ele foi
  tirado) e o vt10x em `third_party/vt10x`, além de módulos Go relevantes
  (`go list -m -u all`). Não atualizar nada sozinho — só avisar o que
  tem novidade e por quê (changelog/CVE relevante), e perguntar antes de
  subir qualquer versão.

  Vale checar também os **advisories** dos repositórios, e não só as tags:
  foi assim que apareceram as CVEs do libvncclient abaixo, que nenhuma
  comparação de versão teria mostrado — a última release da biblioteca é
  anterior às falhas, então "estamos na versão mais nova" e "estamos sem
  as correções" eram verdade ao mesmo tempo.

  - **Última checagem: 2026-09-17.** Próxima: a partir de 2026-10-17.
  - Resultado: FreeRDP 3.31.1, Gio v0.10.2, vt10x e dependências Go
    diretas — todos na última versão disponível. libvncserver 0.9.15 é a
    última release existente (dez/2024) e continua sendo: as correções das
    CVEs do `libvncclient` saíram só no master, e por isso viraram patches
    locais no manifesto (ver `flatpak/patches/libvncserver/PATCH.md`).
