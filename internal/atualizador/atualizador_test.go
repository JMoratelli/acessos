package atualizador

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func TestMaisNova(t *testing.T) {
	casos := []struct {
		remota, local string
		querIr        bool
	}{
		{"v2.0.0", "1.2.2", true},
		{"2.0.1", "2.0.0", true},
		{"2.0.0", "2.0.0", false},
		{"1.9.9", "2.0.0", false},
		{"v2.1", "2.0.9", true},
		{"2.0.0-rc1", "2.0.0", false}, // só os dígitos contam
		{"2.0.2", "v2.0.10", false},   // compara número, não texto
	}
	for _, c := range casos {
		if got := MaisNova(c.remota, c.local); got != c.querIr {
			t.Errorf("MaisNova(%q, %q) = %v, queria %v", c.remota, c.local, got, c.querIr)
		}
	}
}

// rel monta uma release com os anexos pelo nome.
func rel(tag string, anexos ...string) releaseGitHub {
	r := releaseGitHub{Tag: tag}
	for _, n := range anexos {
		r.Assets = append(r.Assets, assetGitHub{Nome: n, URL: "https://exemplo/" + tag + "/" + n})
	}
	return r
}

const somaQualquer = "abc123"

func somaOK(string, string) (string, error) { return somaQualquer, nil }

// O caso real de 2026-09-20: a v2.7.1 saiu só com o instalador do
// Windows, e o Linux, na 2.5.2, precisa cair na v2.7.0 em vez de concluir
// que não há nada.
func TestEscolherCaiNaReleaseAnteriorQuandoALatestNaoTemOPacoteDaqui(t *testing.T) {
	lista := []releaseGitHub{
		rel("v2.7.1", "AcessosSetup-2.7.1.exe", "SHA256SUMS.txt"),
		rel("v2.7.0", "AcessosSetup-2.7.0.exe", "org.jj.Acessos-2.7.0.flatpak", "SHA256SUMS.txt"),
		rel("v2.6.3", "AcessosSetup-2.6.3.exe", "org.jj.Acessos-2.6.3.flatpak", "SHA256SUMS.txt"),
	}

	got, err := escolher(lista, "2.5.2", "linux", somaOK)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if got == nil || got.Tag != "v2.7.0" {
		t.Fatalf("no Linux queria v2.7.0, veio %v", got)
	}
	if got.Bundle != "https://exemplo/v2.7.0/org.jj.Acessos-2.7.0.flatpak" {
		t.Errorf("bundle errado: %s", got.Bundle)
	}

	// A mesma lista, no Windows, continua indo para a mais nova de todas.
	got, err = escolher(lista, "2.5.2", "windows", somaOK)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if got == nil || got.Tag != "v2.7.1" {
		t.Fatalf("no Windows queria v2.7.1, veio %v", got)
	}
	if got.Sha256 != somaQualquer {
		t.Errorf("sha256 não foi preenchido: %q", got.Sha256)
	}
}

func TestEscolherIgnoraRascunhoPreviaEOQueNaoEMaisNovo(t *testing.T) {
	naoPublicada := rel("v3.0.0", "org.jj.Acessos-3.0.0.flatpak")
	naoPublicada.Rascunho = true
	previa := rel("v2.9.0", "org.jj.Acessos-2.9.0.flatpak")
	previa.Previa = true

	lista := []releaseGitHub{
		naoPublicada,
		previa,
		rel("v2.7.0", "org.jj.Acessos-2.7.0.flatpak"),
	}
	got, err := escolher(lista, "2.5.2", "linux", somaOK)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if got == nil || got.Tag != "v2.7.0" {
		t.Fatalf("queria v2.7.0, veio %v", got)
	}

	// Já na mais nova de todas: nada a oferecer.
	got, err = escolher(lista, "2.7.0", "linux", somaOK)
	if err != nil || got != nil {
		t.Fatalf("queria nil sem erro, veio %v / %v", got, err)
	}
}

// A API devolve por data de criação, não por versão: uma correção de
// linha antiga publicada depois não pode esconder o que veio antes dela.
func TestEscolherOrdenaPorVersaoNaoPelaOrdemDaAPI(t *testing.T) {
	lista := []releaseGitHub{
		rel("v2.4.2", "org.jj.Acessos-2.4.2.flatpak"), // publicada por último
		rel("v2.7.0", "org.jj.Acessos-2.7.0.flatpak"),
	}
	got, err := escolher(lista, "2.4.0", "linux", somaOK)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if got == nil || got.Tag != "v2.7.0" {
		t.Fatalf("queria v2.7.0, veio %v", got)
	}
}

// Sem nenhum pacote para esta plataforma em release alguma: nil, e sem
// erro — é "não há o que oferecer", não uma falha.
func TestEscolherSemPacoteDaPlataformaEmNenhumaRelease(t *testing.T) {
	lista := []releaseGitHub{
		rel("v2.7.1", "AcessosSetup-2.7.1.exe", "SHA256SUMS.txt"),
		rel("v2.7.0", "AcessosSetup-2.7.0.exe", "SHA256SUMS.txt"),
	}
	got, err := escolher(lista, "2.5.2", "linux", somaOK)
	if err != nil || got != nil {
		t.Fatalf("queria nil sem erro, veio %v / %v", got, err)
	}
}

// No Windows, release sem o hash publicado não é oferecida — mas a
// varredura segue para a anterior em vez de desistir.
func TestEscolherPulaWindowsSemHashPublicado(t *testing.T) {
	lista := []releaseGitHub{
		rel("v2.7.1", "AcessosSetup-2.7.1.exe", "SHA256SUMS.txt"),
		rel("v2.7.0", "AcessosSetup-2.7.0.exe", "SHA256SUMS.txt"),
	}
	somaSoDa270 := func(_, nome string) (string, error) {
		if nome == "AcessosSetup-2.7.0.exe" {
			return somaQualquer, nil
		}
		return "", nil
	}
	got, err := escolher(lista, "2.5.2", "windows", somaSoDa270)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if got == nil || got.Tag != "v2.7.0" {
		t.Fatalf("queria v2.7.0, veio %v", got)
	}
}

// Falha de rede ao ler o SHA256SUMS não pode virar "está tudo
// atualizado": se nada mais servir, o erro sobe.
func TestEscolherDevolveErroQuandoNadaServiuEHouveFalha(t *testing.T) {
	lista := []releaseGitHub{rel("v2.7.1", "AcessosSetup-2.7.1.exe", "SHA256SUMS.txt")}
	somaQuebrada := func(string, string) (string, error) {
		return "", errors.New("GitHub respondeu 503 Service Unavailable")
	}
	got, err := escolher(lista, "2.5.2", "windows", somaQuebrada)
	if got != nil {
		t.Fatalf("não deveria oferecer nada, veio %v", got)
	}
	if err == nil {
		t.Fatal("queria o erro da leitura do SHA256SUMS, veio nil")
	}

	// Mas se uma release mais velha servir, ela vale e o erro não atrapalha.
	lista = append(lista, rel("v2.7.0", "AcessosSetup-2.7.0.exe", "SHA256SUMS.txt"))
	soAPrimeiraQuebra := func(_, nome string) (string, error) {
		if nome == "AcessosSetup-2.7.1.exe" {
			return "", errors.New("GitHub respondeu 503 Service Unavailable")
		}
		return somaQualquer, nil
	}
	got, err = escolher(lista, "2.5.2", "windows", soAPrimeiraQuebra)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if got == nil || got.Tag != "v2.7.0" {
		t.Fatalf("queria v2.7.0, veio %v", got)
	}
}

// Checar de ponta a ponta contra um servidor local, com o JSON no formato
// que a API devolve de verdade. escolher() é testado direto acima; o que
// falta cobrir aqui é a DECODIFICAÇÃO da lista — os nomes dos campos
// ("tag_name", "draft", "prerelease", "browser_download_url") são
// contrato com o GitHub e um erro de digitação em qualquer um deles
// passaria calado: o campo viria zerado e a release seria descartada
// como se não servisse.
func TestChecarDecodificaAListaDaAPI(t *testing.T) {
	const corpo = `[
	  {"tag_name":"v2.8.0","draft":true,"prerelease":false,"body":"rascunho",
	   "assets":[{"name":"org.jj.Acessos-2.8.0.flatpak","browser_download_url":"https://exemplo/2.8.0.flatpak"}]},
	  {"tag_name":"v2.7.1","draft":false,"prerelease":false,"body":"só Windows",
	   "assets":[{"name":"AcessosSetup-2.7.1.exe","browser_download_url":"https://exemplo/2.7.1.exe"},
	             {"name":"SHA256SUMS.txt","browser_download_url":"https://exemplo/somas.txt"}]},
	  {"tag_name":"v2.7.0","draft":false,"prerelease":false,"body":"sessão RDP em janela própria",
	   "assets":[{"name":"AcessosSetup-2.7.0.exe","browser_download_url":"https://exemplo/2.7.0.exe"},
	             {"name":"org.jj.Acessos-2.7.0.flatpak","browser_download_url":"https://exemplo/2.7.0.flatpak"},
	             {"name":"SHA256SUMS.txt","browser_download_url":"https://exemplo/somas.txt"}]}
	]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, corpo)
	}))
	defer srv.Close()

	antes := urlReleases
	urlReleases = srv.URL
	defer func() { urlReleases = antes }()

	if runtime.GOOS == "windows" {
		t.Skip("o caso do Windows depende de baixar o SHA256SUMS; escolher() já cobre")
	}

	got, err := Checar("2.5.2")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if got == nil {
		t.Fatal("não ofereceu nada; queria a v2.7.0")
	}
	if got.Tag != "v2.7.0" {
		t.Errorf("Tag = %q, queria v2.7.0 (a v2.7.1 não tem .flatpak e a v2.8.0 é rascunho)", got.Tag)
	}
	if got.Bundle != "https://exemplo/2.7.0.flatpak" {
		t.Errorf("Bundle = %q", got.Bundle)
	}
	if got.Notas != "sessão RDP em janela própria" {
		t.Errorf("Notas = %q", got.Notas)
	}
}

// Já na versão mais nova com pacote: nada a oferecer, e sem erro.
func TestChecarNaoOferecePacoteQueJaEstaInstalado(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"tag_name":"v2.7.0","assets":[
		  {"name":"org.jj.Acessos-2.7.0.flatpak","browser_download_url":"https://exemplo/2.7.0.flatpak"}]}]`)
	}))
	defer srv.Close()

	antes := urlReleases
	urlReleases = srv.URL
	defer func() { urlReleases = antes }()

	got, err := Checar("2.7.0")
	if err != nil || got != nil {
		t.Fatalf("queria nil sem erro, veio %v / %v", got, err)
	}
}

// Servidor fora do ar não pode virar "está tudo atualizado".
func TestChecarPropagaFalhaDaAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	antes := urlReleases
	urlReleases = srv.URL
	defer func() { urlReleases = antes }()

	if _, err := Checar("2.5.2"); err == nil {
		t.Fatal("queria erro, veio nil")
	}
}
