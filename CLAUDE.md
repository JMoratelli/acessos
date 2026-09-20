# Diretrizes para trabalhar neste repositório

- Não mencionar Claude, Anthropic ou IA/assistente em nenhum lugar do código,
  comentários, mensagens de commit ou descrições de PR deste projeto.
- Não adicionar linhas de `Co-Authored-By: Claude ...` (ou equivalentes) nos
  commits.
- `vendor/` é local e não entra no git (está no `.gitignore`). Sempre que
  `third_party/gio` (ou qualquer outro `replace` local do `go.mod`) for
  editado, rodar `go mod vendor` antes de compilar — senão o build local
  usa a cópia velha em `vendor/gioui.org` e falha com erro de método
  inexistente (ex.: `PedirTokenAtivacao undefined`), tanto no Linux quanto
  no Windows. Já aconteceu mais de uma vez por esquecimento deste passo
  depois de um `git pull` que trouxe patches novos no fork do Gio.
- **Arquivos irmãos (mesma lógica em plataformas/protocolos diferentes)
  saem de sincronia com facilidade — checar o irmão sempre que um deles
  for corrigido.** Já encontrado em auditoria (2026-09-19):
  `WindowsExec.bombear` (executor/windows.go) não sinalizava sessão
  encerrada como `LinuxSSH.bombear` (executor/ssh_pdv.go) já fazia,
  deixando o worker preso até o timeout de 30 min em vez de falhar na
  hora; `invalidar()` foi corrigido em sshtab.go contra uma guarda real
  do Gio mas o fix não foi propagado para sftptab.go, que tem o mesmo
  padrão de goroutine de fundo chamando `Invalidate()`. Lição: ao mexer
  em `_linux.go`/`_windows.go`, `ssh_pdv.go`/`windows.go`,
  `rdptab.go`/`vnctab.go` etc., procurar o equivalente antes de dar a
  tarefa por concluída — e preferir extrair a lógica comum para um lugar
  só quando ela for realmente idêntica, em vez de deixar duas cópias.
- **Pegadinhas do Windows (achadas na rodada de 2026-09-19).** As quatro
  têm a mesma forma: o Linux perdoa e o Windows não, então passam batido
  em quem só testa num dos dois.

  - **Glifo que falta na fonte embutida vira quadradinho vazio.** No
    Linux o shaper cai numa fonte do sistema; no Windows não há em quem
    cair. Símbolo em texto (`▸`, `⟳`, `⌁`) só entra se a IBM Plex tiver —
    senão, ícone vetorial do `gio.tools/icons` ou um caractere
    equivalente que exista. `cmd/acessos/glifos_test.go` quebra o build
    quando entra um novo.
  - **Pixel não pintado numa janela sem decoração mostra a MOLDURA do
    Windows**, botões de fechar/maximizar inclusive: o Gio pede
    `DwmExtendFrameIntoClientArea(-1,-1,-1,-1)` para ter a sombra do
    sistema, e isso põe o frame do DWM atrás do conteúdo. Resolvido para
    quem pede `app.Translucent` (décimo terceiro patch do fork: blur
    behind com região vazia, que liga o alfa por pixel), mas quem
    desenhar com alfa numa janela SEM essa opção continua vendo a
    moldura. Ver o cabeçalho de `cmd/acessos/buscapop.go`.
  - **DLL carregada em tempo de execução não é achada pelo coletor do
    instalador.** O `scripts/dlls-windows.py` percorre a tabela de
    importação do PE; o que o programa carrega pelo nome depois de subir
    (provider do OpenSSL, plugin, addin) não está lá e fica de fora sem
    ninguém perceber — o app abre e só falha na hora de usar. Foi assim
    que o `ossl-modules/legacy.dll` faltou e o FreeRDP ficou sem NTLM.
  - **Janela minimizada não tem quadro.** Qualquer fila drenada de dentro
    do `FrameEvent` fica parada enquanto ela estiver minimizada — foi o
    que fazia escolher máquina no atalho global não abrir nada. Drenar no
    topo do laço, que o `Invalidate` acorda mesmo sem quadro (ver
    `cmd/acessos/filajanela.go`).
  - **`gofmt -l` aqui acusa o repositório inteiro** porque o working tree
    é CRLF (`core.autocrlf=true`) e o gofmt normaliza para LF. NÃO rodar
    `gofmt -w` no repositório: reescreve todos os arquivos. Para conferir
    um arquivo, comparar ignorando o `\r`
    (`diff <(cat f) <(gofmt f)` com `tr -d '\r'` nos dois lados).

    Consequência que já mordeu: como o sinal vem sujo, **desalinhamento de
    verdade passa batido no Windows**. `dashtab.go` e `topbar.go` ficaram
    com campos de struct fora de alinhamento e só apareceram na conferência
    do lado Linux, onde o working tree é LF e o `gofmt -l` é limpo. A
    formatação é, portanto, tarefa do lado LINUX: rodar `gofmt -l cmd/
    internal/` lá antes de fechar a release (o `third_party/vt10x`
    aparece e fica como está — é código de terceiro).

- **Pegadinhas de MAIS DE UMA JANELA (achadas em 2026-09-20, ao pôr a
  sessão remota em janela própria).** Todas têm a mesma forma: com uma
  janela só elas não existem, e a segunda janela as acorda de uma vez.
  Quatro das seis derrubam o PROCESSO INTEIRO, não só a janela nova.

  - **Um `material.Theme` POR JANELA.** O `text.Shaper` dentro dele é
    cache sem trava, e dois laços de quadro no mesmo Theme dão
    `concurrent map read and map write` — que é erro FATAL do runtime,
    não pânico: `recover()` não pega e leva todas as sessões junto. Já
    estava escrito em `tema.go` (o `temaBusca` existe por isso) e mesmo
    assim aconteceu. A aba carrega o Theme da janela em que está (campo
    `tha`, em rdptab.go); quem for destacar VNC/SSH/SFTP precisa do
    mesmo.
  - **Ícone de `gio.tools/icons` é objeto de PACOTE**, um por ícone, com
    cache de rasterização sem trava (`op`, `imgSize`, `imgColor`). Duas
    janelas desenhando o mesmo ícone rasgam o `paint.ImageOp`. Por isso
    `icone()` (tema.go) tem mutex e é o ÚNICO `ic.Layout` do app —
    desenhar ícone por fora dele recria o problema.
  - **Cada janela do Gio abre a PRÓPRIA conexão Wayland**
    (`newWLWindow` → `newWLDisplay`), e o `close()` dela enfileira o
    `DestroyEvent` e EM SEGUIDA desconecta: quando o laço lê o evento, a
    conexão JÁ CAIU. Tocar em `wl_proxy` dali é use-after-free. É por
    isso que `internal/grab` tem `Stop` (solta tudo, só com o display
    vivo) e `Liberar` (só memória local, seguro depois do fechamento).
  - **`Window.Event()` recria a janela quando `driver == nil`**, e o
    `DestroyEvent` zera o driver. O cliente que consome o `ViewEvent`
    zerado do `close()` e volta ao laço ganhava uma JANELA NOVA em vez do
    `DestroyEvent` pendente — janela fantasma, abandonada, que derrubava
    o app ao ser fechada. Patch no fork; ver `third_party/gio/PATCH.md`.
  - **O KWin impõe `SERVER_SIDE` em tela cheia**, mesmo recebendo
    `set_mode(CLIENT_SIDE)`, e o Gio grava a RESPOSTA do compositor no
    mesmo campo em que guarda o PEDIDO do app (`Config.Decorated`). Sem
    reafirmar `app.Decorated(false)` a cada troca de modo, a janela volta
    de tela cheia com a moldura do sistema por cima da nossa — e fica
    assim. Ver `pedirModo`, em janelasessao.go.
  - **`wl_keyboard.release` só existe da versão 3** do protocolo. O seat
    é vinculado com versão 1, então abaixo disso é `wl_proxy_destroy`;
    chamar `release` dá erro de protocolo, que é FATAL para a conexão.

  As três ferramentas de campo que acharam isso ficaram no repositório e
  valem mais que teste unitário aqui, porque nada disso aparece num
  `go test` (não há `wl_display`, nem compositor): `cmd/grabtest`
  (capturas simultâneas e desmonte), `cmd/fantasma` (conta descritores do
  processo para achar janela que não morreu, e mede o que o compositor
  responde sobre decoração) e `cmd/cursorcap` (máscaras de cursor de
  sessão real).

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
