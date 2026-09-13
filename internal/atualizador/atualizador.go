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
//  3. No Flatpak o download vai para o cache do app, um caminho REAL do
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
	"strconv"
	"strings"
	"time"
)

const (
	AppID       = "org.jj.Acessos"
	urlRelease  = "https://api.github.com/repos/JMoratelli/acessos/releases/latest"
	tempoRede   = 8 * time.Second
	tempoBaixar = 5 * time.Minute
)

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

// Checar consulta a release mais recente. Devolve nil quando não há nada
// a oferecer: versão igual ou mais velha, ou release sem bundle anexado.
func Checar(versaoAtual string) (*Release, error) {
	req, err := http.NewRequest("GET", urlRelease, nil)
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

	var dado struct {
		Tag    string `json:"tag_name"`
		Corpo  string `json:"body"`
		Assets []struct {
			Nome string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dado); err != nil {
		return nil, err
	}
	if dado.Tag == "" || !MaisNova(dado.Tag, versaoAtual) {
		return nil, nil
	}

	if runtime.GOOS == "windows" {
		var exeNome, exeURL, somasURL string
		for _, a := range dado.Assets {
			if strings.HasPrefix(a.Nome, "AcessosSetup-") && strings.HasSuffix(a.Nome, ".exe") {
				exeNome, exeURL = a.Nome, a.URL
			}
			if a.Nome == "SHA256SUMS.txt" {
				somasURL = a.URL
			}
		}
		if exeURL == "" || somasURL == "" {
			return nil, nil
		}
		soma, err := acharSoma(somasURL, exeNome)
		if err != nil {
			return nil, err
		}
		if soma == "" {
			// Sem hash publicado para este arquivo não há como conferir o
			// instalador antes de rodar — melhor não oferecer do que
			// baixar um .exe às cegas.
			return nil, nil
		}
		return &Release{Tag: dado.Tag, Bundle: exeURL, Sha256: soma, Notas: dado.Corpo}, nil
	}

	for _, a := range dado.Assets {
		if strings.HasSuffix(a.Nome, ".flatpak") {
			return &Release{Tag: dado.Tag, Bundle: a.URL, Notas: dado.Corpo}, nil
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

// Instalar baixa o pacote da release e reinstala por cima, deixando uma
// instância nova em pé. Quem chama deve encerrar a janela atual logo
// depois.
func Instalar(r *Release, progresso func(float64)) error {
	if runtime.GOOS == "windows" {
		return instalarWindows(r, progresso)
	}
	return instalarFlatpak(r, progresso)
}

func instalarFlatpak(r *Release, progresso func(float64)) error {
	destino := filepath.Join(cacheDir(), "atualizacao.flatpak")
	if err := os.MkdirAll(filepath.Dir(destino), 0o700); err != nil {
		return err
	}
	if err := baixar(r.Bundle, destino, progresso); err != nil {
		return fmt.Errorf("falha ao baixar a atualização: %w", err)
	}
	defer os.Remove(destino)

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

// instalarWindows baixa o AcessosSetup-X.Y.Z.exe, confere o sha256
// publicado junto da release e dispara a instalação silenciosa.
//
// Não espera o instalador terminar (Start, não Run): o instalador vai
// substituir ESTE .exe, que está em execução agora — quem chama fecha a
// janela atual logo em seguida (CloseApplications no .iss é só a rede de
// segurança caso o processo ainda esteja de pé nesse instante). Reabrir
// depois de instalar é o [Run] do instalador.iss com a flag
// skipifnotsilent, não o Restart Manager: como o processo já saiu
// sozinho antes da hora de sobrescrever o arquivo, nunca haveria nada
// para o Restart Manager reabrir.
func instalarWindows(r *Release, progresso func(float64)) error {
	destino := filepath.Join(os.TempDir(), "AcessosSetup-"+r.Tag+".exe")
	if err := baixar(r.Bundle, destino, progresso); err != nil {
		return fmt.Errorf("falha ao baixar a atualização: %w", err)
	}

	if err := conferirSha256(destino, r.Sha256); err != nil {
		os.Remove(destino)
		return err
	}

	// /SILENT (não /VERYSILENT) deixa a barra do próprio instalador
	// visível — o app já vai fechar em seguida, então não custa mostrar
	// o que está rodando.
	return exec.Command(destino, "/SILENT", "/NORESTART").Start()
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
