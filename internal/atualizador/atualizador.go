// Package atualizador checa se há versão nova no GitHub e instala por
// cima, reabrindo o app — bundle .flatpak no Linux, instalador Inno
// Setup no Windows.
//
// Mecânica portada do atualizador.py, com as mesmas decisões de fundo:
//
//  1. No Flatpak, só age DENTRO do sandbox. Fora dele não há nem versão
//     instalada para comparar nem pacote para reinstalar — quem instalou
//     por outro caminho atualiza por aquele caminho. No Windows não há
//     essa distinção: qualquer instalação feita pelo instalador serve.
//  2. Não usa "flatpak update": os releases são bundles soltos anexados à
//     release do GitHub, não um repositório OSTree. No Windows, da mesma
//     forma, o anexo é o `.exe` do Inno Setup, não um feed de update.
//  3. A release do GitHub marcada como "latest" NÃO serve de ponto de
//     partida: uma versão pode sair só de um lado (só o .exe, só o
//     bundle) e, quando sai, a "latest" fica sem pacote para a outra
//     plataforma e o app de lá para de enxergar atualização — inclusive
//     as anteriores, que tinham pacote. Aconteceu na v2.7.1, publicada
//     só com o instalador do Windows: o Linux ficou preso na 2.5.2 com
//     a 2.7.0 disponível e ninguém avisado. Por isso a checagem varre a
//     LISTA de releases e fica com a mais nova que tenha pacote PARA
//     ESTA plataforma.
//  4. No Flatpak o download vai para o cache do app, um caminho REAL do
//     $HOME do host — tanto este processo quanto o "flatpak install"
//     rodado no host enxergam o mesmo arquivo pelo mesmo caminho. No
//     Windows o download vai para a pasta temporária do usuário e é
//     conferido por sha256 antes de rodar, já que ali não há sandbox
//     nenhum policiando o que se executa.
package atualizador

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	AppID       = "org.jj.Acessos"
	tempoRede   = 8 * time.Second
	tempoBaixar = 5 * time.Minute
)

// var, não const, só para o teste conseguir apontar para um servidor
// local — a decodificação da lista é a parte que nenhum teste de unidade
// de escolher() cobre.
var urlReleases = "https://api.github.com/repos/JMoratelli/acessos/releases?per_page=30"

// Release é o que a interface precisa mostrar e usar.
type Release struct {
	Tag    string
	Bundle string // URL do .flatpak (Linux) ou do AcessosSetup-X.Y.Z.exe (Windows)
	Sha256 string // só no Windows: hash publicado junto do instalador
	Notas  string
}

// EmFlatpak diz se estamos dentro do sandbox: /.flatpak-info é criado
// pelo próprio Flatpak e é a checagem recomendada.
func EmFlatpak() bool {
	_, err := os.Stat("/.flatpak-info")
	return err == nil
}

// Suportado diz se este pacote sabe agir na plataforma atual: dentro do
// Flatpak no Linux, ou em qualquer instalação no Windows — lá não existe
// sandbox para checar, o instalador silencioso resolve sozinho.
func Suportado() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return EmFlatpak()
}

// releaseGitHub e assetGitHub são o recorte da API que interessa aqui.
type releaseGitHub struct {
	Tag      string        `json:"tag_name"`
	Corpo    string        `json:"body"`
	Rascunho bool          `json:"draft"`
	Previa   bool          `json:"prerelease"`
	Assets   []assetGitHub `json:"assets"`
}

type assetGitHub struct {
	Nome string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Checar procura a versão mais nova que tenha pacote para esta
// plataforma. Devolve nil quando não há nada a oferecer: nenhuma release
// mais nova que a instalada, ou nenhuma delas com o pacote daqui.
func Checar(versaoAtual string) (*Release, error) {
	lista, err := listar()
	if err != nil {
		return nil, err
	}
	return escolher(lista, versaoAtual, runtime.GOOS, acharSoma)
}

// listar traz as releases publicadas, da mais recente para a mais
// antiga. As 30 do topo cobrem com folga a distância entre dois pacotes
// da mesma plataforma; quem ficar para trás disso reinstala na mão.
func listar() ([]releaseGitHub, error) {
	req, err := http.NewRequest("GET", urlReleases, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Acessos-Atualizador")

	cli := &http.Client{Timeout: tempoRede}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub respondeu %s", resp.Status)
	}

	var lista []releaseGitHub
	if err := json.NewDecoder(resp.Body).Decode(&lista); err != nil {
		return nil, err
	}
	return lista, nil
}

// escolher fica com a primeira release, da mais nova para a mais velha,
// que seja mais nova que versaoAtual E tenha pacote para o sistema so.
// somaDe é o leitor do SHA256SUMS, separado para o teste não precisar de
// rede.
//
// Um erro ao ler o SHA256SUMS de uma candidata não interrompe a varredura
// — a release seguinte ainda pode servir —, mas é guardado e devolvido se
// no fim não sobrar nada, para a falha de rede não passar por "está tudo
// atualizado".
func escolher(lista []releaseGitHub, versaoAtual, so string, somaDe func(url, nome string) (string, error)) (*Release, error) {
	// A API ordena por data de criação. Reordenar por versão evita que uma
	// tag republicada, ou uma correção de linha antiga lançada depois,
	// esconda o que na verdade veio antes dela.
	ordem := make([]releaseGitHub, len(lista))
	copy(ordem, lista)
	sort.SliceStable(ordem, func(i, j int) bool { return MaisNova(ordem[i].Tag, ordem[j].Tag) })

	var primeiroErro error
	for i := range ordem {
		r := &ordem[i]
		if r.Tag == "" || r.Rascunho || r.Previa {
			continue
		}
		if !MaisNova(r.Tag, versaoAtual) {
			// Lista ordenada: daqui para baixo só vem coisa ainda mais velha.
			break
		}
		rel, err := montar(r, so, somaDe)
		if err != nil {
			if primeiroErro == nil {
				primeiroErro = err
			}
			continue
		}
		if rel != nil {
			return rel, nil
		}
	}
	return nil, primeiroErro
}

// montar traduz uma release no que o sistema so consegue instalar, ou
// nil quando aquela release não trouxe pacote para cá.
func montar(r *releaseGitHub, so string, somaDe func(url, nome string) (string, error)) (*Release, error) {
	if so == "windows" {
		var exeNome, exeURL, somasURL string
		for _, a := range r.Assets {
			if strings.HasPrefix(a.Nome, "AcessosSetup-") && strings.HasSuffix(a.Nome, ".exe") {
				exeNome, exeURL = a.Nome, a.URL
			}
			if strings.HasPrefix(a.Nome, "SHA256SUMS") && strings.HasSuffix(a.Nome, ".txt") {
				somasURL = a.URL
			}
		}
		if exeURL == "" || somasURL == "" {
			return nil, nil
		}
		soma, err := somaDe(somasURL, exeNome)
		if err != nil {
			return nil, err
		}
		if soma == "" {
			// Sem hash publicado para este arquivo não há como conferir o
			// instalador antes de rodar — melhor não oferecer do que
			// baixar um .exe às cegas.
			return nil, nil
		}
		return &Release{Tag: r.Tag, Bundle: exeURL, Sha256: soma, Notas: r.Corpo}, nil
	}

	for _, a := range r.Assets {
		if strings.HasSuffix(a.Nome, ".flatpak") {
			return &Release{Tag: r.Tag, Bundle: a.URL, Notas: r.Corpo}, nil
		}
	}
	return nil, nil
}

// acharSoma lê o SHA256SUMS.txt anexado à release — uma linha por
// arquivo, no formato do próprio `sha256sum` ("hash  nome", opcionalmente
// com "*" antes do nome para modo binário) — e devolve o hash da linha
// que casa com nomeArquivo. String vazia (sem erro) quando o arquivo não
// aparece ali.
func acharSoma(url, nomeArquivo string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Acessos-Atualizador")
	cli := &http.Client{Timeout: tempoRede}
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub respondeu %s", resp.Status)
	}
	corpo, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	for _, linha := range strings.Split(string(corpo), "\n") {
		campos := strings.Fields(linha)
		if len(campos) != 2 {
			continue
		}
		if strings.TrimPrefix(campos[1], "*") == nomeArquivo {
			return strings.ToLower(campos[0]), nil
		}
	}
	return "", nil
}

// MaisNova compara versões tolerando "v" na frente e sufixo de
// pré-lançamento ("2.0.0-rc1").
//
// Cada parte é lida até o primeiro caractere que não é dígito. O
// atualizador.py removia TODOS os não-dígitos e juntava o resto, o que
// fazia "2.0.0-rc1" virar 2.0.1 e ser oferecido como mais novo que o
// próprio 2.0.0 final. Aqui o sufixo é ignorado: um rc vale como a versão
// que ele antecede, então nunca é empurrado por cima do final.
func MaisNova(remota, local string) bool {
	a, b := partes(remota), partes(local)
	for i := 0; i < len(a) || i < len(b); i++ {
		va, vb := 0, 0
		if i < len(a) {
			va = a[i]
		}
		if i < len(b) {
			vb = b[i]
		}
		if va != vb {
			return va > vb
		}
	}
	return false
}

func partes(v string) []int {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	var out []int
	for _, pedaco := range strings.Split(v, ".") {
		fim := 0
		for fim < len(pedaco) && pedaco[fim] >= '0' && pedaco[fim] <= '9' {
			fim++
		}
		n, _ := strconv.Atoi(pedaco[:fim])
		out = append(out, n)
	}
	return out
}

// Instalar baixa o pacote da release e instala por cima.
//
// progresso recebe a fração já baixada, de 0 a 1. instalando é chamado
// uma vez, quando o download acaba e a instalação começa: dali em diante
// não há progresso nenhum para ler, e quem chama troca o aviso na tela em
// vez de deixar uma barra parada em 100%.
//
// Voltar sem erro quer dizer "instalado, e a instância nova já está
// subindo" — quem chama fecha a janela atual. No Windows o normal é NÃO
// voltar: o instalador encerra este processo no meio da espera, de
// propósito (ver instalarWindows).
func Instalar(r *Release, progresso func(float64), instalando func()) error {
	if runtime.GOOS == "windows" {
		return instalarWindows(r, progresso, instalando)
	}
	return instalarFlatpak(r, progresso, instalando)
}

func avisar(f func()) {
	if f != nil {
		f()
	}
}

func instalarFlatpak(r *Release, progresso func(float64), instalando func()) error {
	destino := filepath.Join(cacheDir(), "atualizacao.flatpak")
	if err := os.MkdirAll(filepath.Dir(destino), 0o700); err != nil {
		return err
	}
	if err := baixar(r.Bundle, destino, progresso); err != nil {
		return fmt.Errorf("falha ao baixar a atualização: %w", err)
	}
	defer os.Remove(destino)

	avisar(instalando)
	saida, err := noHost("flatpak", "install", "--user", "-y",
		"--noninteractive", "--reinstall", destino)
	if err != nil {
		if saida != "" {
			return fmt.Errorf("%s", saida)
		}
		return err
	}

	// A instância nova precisa sobreviver a esta, que vai sair em seguida.
	return exec.Command("flatpak-spawn", "--host", "flatpak", "run", AppID).Start()
}

// =====================================================================
// PENDENTE DE TESTE NO WINDOWS - APAGAR ESTE BLOCO QUANDO PASSAR
// =====================================================================
// Nada do que está abaixo foi exercitado numa máquina Windows.
// /VERYSILENT, o Restart Manager fechando o app e o código de saída do
// Inno não existem no Linux e não aparecem em `go test` - o que está
// verde hoje é compilação, vet e os testes de seleção de release.
//
// Apague este bloco INTEIRO (e o item correspondente no CLAUDE.md) assim
// que os quatro passarem. Se algum falhar, o que está escrito aqui é o
// ESPERADO, não o observado: corrija o código, não o comentário.
//
// 1. Bateria nativa: `go test ./...`. Lembrar do PATH do CLAUDE.md
//    (PATH=/c/msys64/ucrt64/bin:$PATH e
//    PKG_CONFIG=/c/msys64/ucrt64/bin/pkg-config), senão pega o gcc e o
//    pkg-config do Strawberry Perl.
//
// 2. Instalador cru. É onde mora o risco desta mudança, e NÃO precisa de
//    release nenhuma: com o acessos.exe ABERTO, rodar
//    "AcessosSetup-X.Y.Z.exe /VERYSILENT /NORESTART".
//    Esperado: nenhuma janela do Inno em momento algum; o app fecha
//    sozinho (Restart Manager, via CloseApplications=yes); o app reabre
//    sozinho (o [Run] com skipifnotsilent). Se ele NÃO fechar, o Inno
//    aborta ANTES de copiar - nada fica pela metade, e o motivo está no
//    log em %TEMP%\Setup Log*.txt (SetupLogging=yes no .iss).
//
// 3. Fluxo pelo app, de ponta a ponta. DEPENDE de uma release mais nova
//    que a instalada, com .exe + SHA256SUMS: quem roda o /VERYSILENT é o
//    app JÁ INSTALADO, não o instalador baixado, então atualizar PARA um
//    pacote novo usando um app velho não testa nada. Com este código
//    instalado e uma release maior publicada, aceitar a atualização.
//    Esperado: barra de download; depois "Instalando a atualização..."
//    FICA na tela até o app fechar - é exatamente o que mudou, antes ele
//    sumia no instante em que o instalador era disparado; reabre; a
//    versão nova aparece em Sobre. Ver o item PENDENTE do CLAUDE.md para
//    o estado da release v2.7.1.
//
// 4. Caminho triste, que é a razão de existir o cmd.Wait abaixo. Trocar a
//    linha do exec.Command temporariamente por
//    `exec.Command("cmd.exe", "/c", "exit", "3")` e confirmar que o
//    diálogo mostra o erro e devolve os botões, em vez de ficar parado em
//    "Instalando..." para sempre. Desfazer depois.
// =====================================================================

// instalarWindows baixa o AcessosSetup-X.Y.Z.exe, confere o sha256
// publicado junto da release e dispara a instalação silenciosa.
//
// /VERYSILENT, não /SILENT: com /SILENT o Inno mostra a PRÓPRIA janela de
// progresso, uma janela alheia por cima de tudo. O progresso que
// interessa — o download, que é a parte demorada — já está desenhado no
// diálogo do app.
//
// Quem fecha o app NÃO é esta função: é o instalador, pelo Restart
// Manager (CloseApplications=yes no .iss), no instante em que precisa
// sobrescrever o acessos.exe que está em execução. Esse é o jeito de
// chegar mais perto do Linux, onde o flatpak install monta um deploy novo
// sem derrubar ninguém e a janela do app fica no ar até o fim: aqui ela
// fica até o último momento possível. Sair assim que o instalador é
// disparado deixaria a tela vazia durante toda a cópia.
//
// Por isso o Wait: no caminho feliz ele NÃO volta, este processo morre
// esperando. Ele existe para o caminho triste — instalador que aborta
// (não conseguiu fechar o app, disco cheio, pacote corrompido) —, em que
// sem ninguém escutando o app ficaria parado em "Instalando…" para
// sempre. Reabrir depois é o [Run] do instalador.iss com skipifnotsilent.
func instalarWindows(r *Release, progresso func(float64), instalando func()) error {
	destino := filepath.Join(os.TempDir(), "AcessosSetup-"+r.Tag+".exe")
	if err := baixar(r.Bundle, destino, progresso); err != nil {
		return fmt.Errorf("falha ao baixar a atualização: %w", err)
	}

	if err := conferirSha256(destino, r.Sha256); err != nil {
		os.Remove(destino)
		return err
	}

	cmd := exec.Command(destino, "/VERYSILENT", "/NORESTART")
	if err := cmd.Start(); err != nil {
		return err
	}
	avisar(instalando)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("o instalador falhou: %w", err)
	}
	// Chegar aqui é raro: o instalador terminou sem precisar fechar este
	// processo (app rodando de outra pasta que não a instalada, por
	// exemplo). A instância nova já subiu pelo [Run], então esta sai
	// igual ao Linux.
	os.Remove(destino)
	return nil
}

func conferirSha256(caminho, esperado string) error {
	f, err := os.Open(caminho)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	obtido := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(obtido, esperado) {
		return fmt.Errorf("sha256 não confere: esperado %s, obtido %s", esperado, obtido)
	}
	return nil
}

func baixar(url, destino string, progresso func(float64)) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Acessos-Atualizador")
	cli := &http.Client{Timeout: tempoBaixar}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("servidor respondeu %s", resp.Status)
	}

	f, err := os.Create(destino)
	if err != nil {
		return err
	}
	defer f.Close()

	var lido int64
	buf := make([]byte, 1<<20)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			lido += int64(n)
			if progresso != nil && resp.ContentLength > 0 {
				progresso(float64(lido) / float64(resp.ContentLength))
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// noHost roda um comando FORA do sandbox: instalar um Flatpak de dentro
// de um Flatpak não funciona, quem instala é o host.
func noHost(args ...string) (string, error) {
	cmd := exec.Command("flatpak-spawn", append([]string{"--host"}, args...)...)
	saida, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(saida)), err
}

func cacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return d
	}
	lar, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(lar, ".cache")
}
