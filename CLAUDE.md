# Diretrizes para trabalhar neste repositório

- Não mencionar Claude, Anthropic ou IA/assistente em nenhum lugar do código,
  comentários, mensagens de commit ou descrições de PR deste projeto.
- Não adicionar linhas de `Co-Authored-By: Claude ...` (ou equivalentes) nos
  commits.
- Na primeira sessão do dia (a primeira vez que eu mexer neste repositório
  num dia novo), checar se há versão nova das bibliotecas externas usadas
  no projeto: FreeRDP e libvncserver (versões fixas no manifesto Flatpak,
  `flatpak/org.jj.Acessos.yml`), o fork do Gio em `third_party/gio` (ver
  `third_party/gio/PATCH.md` para saber contra qual upstream ele foi
  tirado) e o vt10x em `third_party/vt10x`, além de módulos Go relevantes
  (`go list -m -u all`). Não atualizar nada sozinho — só avisar o que
  tem novidade e por quê (changelog/CVE relevante), e perguntar antes de
  subir qualquer versão.
