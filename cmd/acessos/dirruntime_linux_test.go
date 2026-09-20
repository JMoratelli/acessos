//go:build linux

package main

import (
	"path/filepath"
	"testing"
)

// O Flatpak isola XDG_CONFIG_HOME por app-id mas NÃO XDG_RUNTIME_DIR: lá
// dentro ele é /run/user/$UID para qualquer id. Se o socket não levar o id
// junto, um build de teste ao lado da produção fala no socket dela — e os
// dois viram uma instalação só. Ver dirRuntime.
func TestDirRuntimeSeparaInstalacaoParalela(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	t.Setenv("FLATPAK_ID", appIDProducao)
	producao := dirRuntime()
	if producao != filepath.Join("/run/user/1000", "acessos") {
		t.Fatalf("produção mudou de caminho: %s", producao)
	}

	t.Setenv("FLATPAK_ID", "org.jj.AcessosTeste")
	teste := dirRuntime()
	if teste == producao {
		t.Fatalf("id diferente tem de dar socket diferente, veio %s nos dois", teste)
	}
}

// Fora do Flatpak (build local, porte Windows rodando via wine, sessão
// solta) não há FLATPAK_ID, e o caminho tem de continuar o de sempre —
// senão um app instalado e um rodado do terminal deixariam de se
// encontrar.
func TestDirRuntimeSemFlatpakNaoMuda(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("FLATPAK_ID", "")
	if got := dirRuntime(); got != filepath.Join("/run/user/1000", "acessos") {
		t.Fatalf("sem FLATPAK_ID o caminho tem de ser o de sempre, veio %s", got)
	}
}
