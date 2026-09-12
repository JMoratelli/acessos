package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Onde o app procura os arquivos quando ninguém passa -ini.
//
// Sem um padrão o programa abre VAZIO — e aberto pelo menu (ou pelo
// Flatpak, que não passa argumento nenhum) era exatamente isso que
// acontecia. O padrão é o mesmo diretório do app Python, de propósito: os
// dois leem e escrevem os mesmos arquivos.
//
//	Linux:   $XDG_CONFIG_HOME/acessos  ou  ~/.config/acessos
//	Windows: %APPDATA%\acessos
//
// A chave [geral] caminho aponta um DIRETÓRIO e faz do arquivo padrão um
// PONTEIRO: dá para manter o inventário numa pasta sincronizada e deixar
// só o ponteiro no lugar padrão. Um único salto é seguido — dois arquivos
// apontando um para o outro entrariam em laço.
func dirPadrao() string {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("APPDATA"); base != "" {
			return filepath.Join(base, "acessos")
		}
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "acessos")
	}
	lar, err := os.UserHomeDir()
	if err != nil {
		return "acessos"
	}
	return filepath.Join(lar, ".config", "acessos")
}

// caminhoINIPadrao devolve o conexoes.ini a usar. arg vence sempre.
func caminhoINIPadrao(arg string) string {
	if arg != "" {
		if abs, err := filepath.Abs(expandirTil(arg)); err == nil {
			return abs
		}
		return arg
	}
	padrao := filepath.Join(dirPadrao(), "conexoes.ini")
	if destino := lerCaminhoGeral(padrao); destino != "" {
		alvo := filepath.Join(destino, "conexoes.ini")
		if alvo != padrao {
			if _, err := os.Stat(alvo); err == nil {
				return alvo
			}
			fmt.Fprintf(os.Stderr,
				"[geral] caminho aponta para %s, mas não há conexoes.ini lá; usando %s\n",
				destino, padrao)
		}
	}
	return padrao
}

// lerCaminhoGeral lê apenas a chave [geral] caminho, sem carregar o resto:
// é o passo que resolve o ovo e a galinha de "preciso abrir um INI para
// saber qual INI abrir".
func lerCaminhoGeral(arquivo string) string {
	b, err := os.ReadFile(arquivo)
	if err != nil {
		return ""
	}
	emGeral := false
	for _, linha := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(linha)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			emGeral = strings.EqualFold(t, "[geral]")
			continue
		}
		if !emGeral {
			continue
		}
		chave, valor, ok := strings.Cut(t, "=")
		if !ok || strings.TrimSpace(chave) != "caminho" {
			continue
		}
		return expandirTil(strings.TrimSpace(valor))
	}
	return ""
}

func expandirTil(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	lar, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(lar, strings.TrimPrefix(p, "~"))
}

// garantirINI cria o diretório e um conexoes.ini de exemplo quando ainda
// não existe. Um app que abre vazio e não explica como cadastrar a
// primeira máquina não é utilizável na primeira execução — e o arquivo
// comentado é a documentação que fica junto do dado.
func garantirINI(caminho string) error {
	if _, err := os.Stat(caminho); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(caminho), 0o700); err != nil {
		return err
	}
	return os.WriteFile(caminho, []byte(iniExemplo), 0o600)
}

const iniExemplo = `# conexoes.ini — inventário do Acessos.
#
# Uma seção por máquina. O nome da seção é o nome que aparece no painel.
# Subgrupos são separados por ponto e vírgula: "Loja 06;Caixas".
#
# As senhas podem ficar em claro (como abaixo) ou cifradas pelo cofre —
# destranque o cofre pelo cadeado na barra de cima e o app passa a cifrar
# o que for gravado pela interface.

[geral]
tema = claro
# caminho = /outro/diretorio   ; move inventário, snippets e histórico

[EXEMPLO]
grupo = Exemplos
host = 192.168.0.10
# tela (VNC)
vnc = 1
porta = 5900
senha =
modo = encaixar
# shell (SSH) — usuário é obrigatório
ssh = 1
ssh_porta = 22
ssh_usuario = suporte
ssh_senha =
# RDP
rdp = 0
rdp_porta = 3389
rdp_usuario =
rdp_senha =
`
