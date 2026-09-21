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
  - **Tela cheia do Gio no Windows parava na ÁREA ÚTIL** (achado em
    2026-09-20, na v2.7.0, com a sessão em janela própria). Ela é
    "maximizada SEM `WS_OVERLAPPEDWINDOW`", e a janela até nasce do
    tamanho do monitor inteiro — quem encolhia era o `WM_NCCALCSIZE` do
    próprio Gio, que grampeia o CLIENTE na `mi.WorkArea` quando a janela
    é sem decoração e está maximizada. Certo para a janela maximizada de
    verdade (senão comeria a barra de tarefas), errado para a tela
    cheia: medido em 1920x1036, o app pintava 1920x996 e sobrava a faixa
    da barra. Corrigido no fork (ver `third_party/gio/PATCH.md`), e
    `cmd/telacheia` mede as quatro transições numa janela de verdade —
    sai 1 se alguma regredir. Nada disso aparece em `go test`: não há
    HWND, nem monitor, nem barra de tarefas.
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

- **PENDENTE: a atualização sem janela do instalador nunca rodou no
  Windows.** A troca de `/SILENT` para `/VERYSILENT` e a passagem do
  fechamento do app para o Restart Manager (`CloseApplications=yes` no
  `scripts/instalador.iss`) foram escritas e conferidas só do lado Linux,
  onde nada disso existe. O checklist dos quatro itens — bateria nativa,
  instalador cru com o app aberto, fluxo completo pelo app e o caminho de
  falha do `cmd.Wait` — está no bloco de comentário logo acima de
  `instalarWindows`, em `internal/atualizador/atualizador.go`.

  **O `AcessosSetup-2.7.1.exe` anexado à release v2.7.1 NÃO é o build da
  2.7.1.** Em 2026-09-21 ele foi substituído, em silêncio, por um build do
  `master` em `5bfb7fe`, que carrega esta mudança; o `SHA256SUMS.txt` foi
  regerado junto e bate (`5d2e1f9a97…`). É esse o instalador a usar no
  teste — ele diz "2.7.1" em Sobre como qualquer outro.

  **Ele serve para os itens 1, 2 e 4, não para o 3.** O item 3 exercita o
  lado do APP que foi mudado, e quem roda o `/VERYSILENT` é o app JÁ
  INSTALADO, não o instalador baixado: um 2.7.0 atualizando para este
  pacote usaria o código velho, com `/SILENT`, e não testaria nada.
  Para o item 3 é preciso uma release MAIS NOVA que a 2.7.1 com um
  `AcessosSetup-*.exe` anexado, com este app instalado por baixo.

  **RETIRAR DEPOIS.** Quando os itens 1, 2 e 4 passarem, publicar a
  release de verdade (versão nova, com `.exe` e, se for o caso, o bundle
  `.flatpak`): ela fecha o item 3 e desfaz esta gambiarra de uma vez. O
  `.exe` original da 2.7.1 não está mais na release — se for preciso, ele
  se refaz a partir da tag `v2.7.1`. Feito isso, apagar este item E o
  bloco de comentário em `instalarWindows`.

- **Compilar no Windows não é o build oficial** (conferido em
  2026-09-20, nesta máquina). O `.exe` entregue sai de
  `scripts/build-windows.sh`, no Linux, e o script monta o sysroot MSYS2
  UMA VEZ e reaproveita (`if [ ! -d "$SYSROOT/lib/pkgconfig" ]`). Quatro
  diferenças, todas medidas:

  - **O sysroot congela a versão das bibliotecas.** A v2.7.0 instalada
    traz FreeRDP/WinPR **3.30.0**; o MSYS2 desta máquina está em
    **3.31.1**. Das 101 DLLs instaladas só essas três diferem — todo o
    resto, `libcrypto-3-x64.dll` inclusive, é byte a byte igual. Um
    `.exe` compilado aqui liga contra a 3.31.1; os 43 símbolos que ele
    importa existem na 3.30.0 (conferido com `objdump -p`), então
    carregaria — mas é variável solta num "só troca o exe". **Para
    substituir só o binário, gerar pelo script, no Linux.**
  - **O `PATH` daqui acha o `gcc` e o `pkg-config` do Strawberry Perl
    antes do MSYS2** — o `pkg-config` é um stub que nem existe
    (`Can't find C:\Strawberry\perl\bin\pkg-config.bat`) e o `gcc` é
    outra ABI. Build nativo só com
    `PATH=/c/msys64/ucrt64/bin:$PATH` e
    `PKG_CONFIG=/c/msys64/ucrt64/bin/pkg-config`.
  - **Duas coisas do script não acontecem sozinhas:**
    `-ldflags "-H=windowsgui"` (sem ele abre um console preto atrás da
    janela) e o `cmd/acessos/recurso_windows.syso` (ícone e versão na
    aba Detalhes), gerado por `windres` e fora do git.
  - **`ossl-modules/legacy.dll` só existe na INSTALAÇÃO.** Um build
    nativo rodado de dentro de `build/` fica sem MD4/RC4 — NTLM e
    autoreconnect quebrados —, e isso é silêncio, não erro (ver
    `cmd/acessos/ossl_windows.go`).

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
