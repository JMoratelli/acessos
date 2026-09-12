package conexoes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A edição do .ini é feita LINHA A LINHA, não regravando a partir da
// estrutura lida. O arquivo é escrito e lido também pelo app original em
// Python, e carrega comentários, ordem e espaçamento que o operador
// reconhece; regravar tudo a partir do nosso modelo apagaria isso e
// transformaria qualquer edição num diff gigante.
//
// Toda escrita é atômica: grava num arquivo temporário ao lado e renomeia
// por cima. Um INI truncado no meio de uma gravação levaria junto as 274
// conexões.

func gravarAtomico(caminho string, linhas []string) error {
	tmp := caminho + ".tmp"
	conteudo := strings.Join(linhas, "\n")
	if !strings.HasSuffix(conteudo, "\n") {
		conteudo += "\n"
	}
	if err := os.WriteFile(tmp, []byte(conteudo), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}

func lerLinhas(caminho string) ([]string, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n"), nil
}

// nomeSecao devolve o nome se a linha for um cabeçalho [assim].
func nomeSecao(linha string) (string, bool) {
	t := strings.TrimSpace(linha)
	if len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']' {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// faixaSecao acha o intervalo [ini,fim) das linhas de uma seção, incluindo
// o cabeçalho.
func faixaSecao(linhas []string, nome string) (int, int, bool) {
	ini := -1
	for i, l := range linhas {
		n, ok := nomeSecao(l)
		if !ok {
			continue
		}
		if ini >= 0 {
			return ini, i, true
		}
		if n == nome {
			ini = i
		}
	}
	if ini >= 0 {
		return ini, len(linhas), true
	}
	return 0, 0, false
}

// chaveDaLinha separa "chave = valor" preservando o que veio antes do "=".
func chaveDaLinha(linha string) (chave, valor string, ok bool) {
	i := strings.IndexByte(linha, '=')
	if i < 0 {
		return "", "", false
	}
	c := strings.TrimSpace(linha[:i])
	if c == "" || strings.HasPrefix(c, "#") || strings.HasPrefix(c, ";") {
		return "", "", false
	}
	return c, strings.TrimSpace(linha[i+1:]), true
}

// Remover apaga a seção inteira de uma conexão.
func Remover(caminho, nome string) error {
	guardarCopia(caminho, fmt.Sprintf("removeu %q", nome))
	linhas, err := lerLinhas(caminho)
	if err != nil {
		return err
	}
	ini, fim, ok := faixaSecao(linhas, nome)
	if !ok {
		return fmt.Errorf("conexão %q não está em %s", nome, caminho)
	}
	return gravarAtomico(caminho, append(append([]string{}, linhas[:ini]...), linhas[fim:]...))
}

// Duplicar copia a seção inteira com outro nome, logo abaixo da original.
// Copiar as linhas (em vez de reescrever a partir do modelo) mantém até os
// segredos cifrados como estão — não precisa do cofre aberto para duplicar.
func Duplicar(caminho, nome, novoNome string) error {
	guardarCopia(caminho, fmt.Sprintf("duplicou %q", nome))
	linhas, err := lerLinhas(caminho)
	if err != nil {
		return err
	}
	if _, _, existe := faixaSecao(linhas, novoNome); existe {
		return fmt.Errorf("já existe uma conexão chamada %q", novoNome)
	}
	ini, fim, ok := faixaSecao(linhas, nome)
	if !ok {
		return fmt.Errorf("conexão %q não está em %s", nome, caminho)
	}
	copia := append([]string{"[" + novoNome + "]"}, linhas[ini+1:fim]...)
	// garante uma linha em branco separando as seções
	if n := len(copia); n > 0 && strings.TrimSpace(copia[n-1]) != "" {
		copia = append(copia, "")
	}
	out := append([]string{}, linhas[:fim]...)
	out = append(out, copia...)
	out = append(out, linhas[fim:]...)
	return gravarAtomico(caminho, out)
}

// Salvar aplica campos a uma seção: chave existente é substituída no lugar
// (preservando a posição), chave nova entra no fim da seção, e valor vazio
// remove a chave. Renomear a seção também é suportado (novoNome != "").
func Salvar(caminho, nome, novoNome string, campos map[string]string) error {
	guardarCopia(caminho, fmt.Sprintf("editou %q", nome))
	linhas, err := lerLinhas(caminho)
	if err != nil {
		return err
	}
	ini, fim, ok := faixaSecao(linhas, nome)
	if !ok {
		return fmt.Errorf("conexão %q não está em %s", nome, caminho)
	}
	if novoNome != "" && novoNome != nome {
		if _, _, existe := faixaSecao(linhas, novoNome); existe {
			return fmt.Errorf("já existe uma conexão chamada %q", novoNome)
		}
		linhas[ini] = "[" + novoNome + "]"
	}

	restante := map[string]string{}
	for k, v := range campos {
		restante[k] = v
	}
	var corpo []string
	for _, l := range linhas[ini+1 : fim] {
		c, _, ok := chaveDaLinha(l)
		if !ok {
			corpo = append(corpo, l)
			continue
		}
		novo, tem := restante[c]
		if !tem {
			corpo = append(corpo, l)
			continue
		}
		delete(restante, c)
		if novo == "" {
			continue // valor vazio remove a chave
		}
		corpo = append(corpo, c+" = "+novo)
	}
	// chaves novas entram antes da linha em branco final, se houver
	var novas []string
	for _, k := range ordemChaves {
		if v, tem := restante[k]; tem && v != "" {
			novas = append(novas, k+" = "+v)
			delete(restante, k)
		}
	}
	for k, v := range restante {
		if v != "" {
			novas = append(novas, k+" = "+v)
		}
	}
	corte := len(corpo)
	for corte > 0 && strings.TrimSpace(corpo[corte-1]) == "" {
		corte--
	}
	corpo = append(append(append([]string{}, corpo[:corte]...), novas...), corpo[corte:]...)

	out := append([]string{}, linhas[:ini+1]...)
	out = append(out, corpo...)
	out = append(out, linhas[fim:]...)
	return gravarAtomico(caminho, out)
}

// ordemChaves mantém as chaves novas na mesma ordem que o app original
// grava, pra o arquivo continuar familiar.
// Os campos do VNC não têm prefixo no arquivo — são os mais antigos, de
// quando o app só fazia tela. Mudar isso quebraria todo .ini existente e o
// app Python, que lê o MESMO arquivo.
var ordemChaves = []string{
	"grupo", "host",
	"vnc", "porta", "usuario", "senha", "modo", "auto", "ronly",
	"ssh", "ssh_porta", "ssh_usuario", "ssh_senha", "ssh_auto",
	"rdp", "rdp_porta", "rdp_usuario", "rdp_senha", "rdp_dominio", "rdp_tela", "rdp_auto",
}

// TrocarSenhaMestra recifra TODO segredo do arquivo e regrava a seção
// [cofre]. Decifrar com a chave velha e cifrar com a nova é feito linha a
// linha, então uma conexão que o nosso modelo ainda não entenda continua
// sendo migrada junto.
func TrocarSenhaMestra(caminho string, decifrar func(string) (string, error),
	cifrar func(string) (string, error), novoCofre map[string]string) error {

	guardarCopia(caminho, "trocou a senha mestra (recifrou tudo)")

	linhas, err := lerLinhas(caminho)
	if err != nil {
		return err
	}
	emCofre := false
	for i, l := range linhas {
		if n, ok := nomeSecao(l); ok {
			emCofre = n == "cofre"
			continue
		}
		c, v, ok := chaveDaLinha(l)
		if !ok {
			continue
		}
		if emCofre {
			if novo, tem := novoCofre[c]; tem {
				linhas[i] = c + " = " + novo
			}
			continue
		}
		if !strings.HasPrefix(v, "enc:v1:") {
			continue
		}
		claro, err := decifrar(v)
		if err != nil {
			return fmt.Errorf("linha %d (%s): %w", i+1, c, err)
		}
		selado, err := cifrar(claro)
		if err != nil {
			return fmt.Errorf("linha %d (%s): %w", i+1, c, err)
		}
		linhas[i] = c + " = " + selado
	}
	return gravarAtomico(caminho, linhas)
}

// Criar acrescenta uma seção nova no fim do arquivo. O nome é a chave da
// seção (é assim que o .ini identifica a conexão), então duplicata é erro
// e não silêncio: duas seções com o mesmo nome fariam o leitor ficar com
// uma e descartar a outra sem avisar.
func Criar(caminho, nome string, campos map[string]string) error {
	guardarCopia(caminho, fmt.Sprintf("criou %q", nome))
	linhas, err := lerLinhas(caminho)
	if err != nil {
		return err
	}
	if _, _, existe := faixaSecao(linhas, nome); existe {
		return fmt.Errorf("já existe uma conexão chamada %q", nome)
	}
	for len(linhas) > 0 && strings.TrimSpace(linhas[len(linhas)-1]) == "" {
		linhas = linhas[:len(linhas)-1]
	}
	linhas = append(linhas, "", "["+nome+"]")
	for _, k := range ordemChaves {
		if v := campos[k]; v != "" {
			linhas = append(linhas, k+" = "+v)
		}
	}
	return gravarAtomico(caminho, linhas)
}

// ------------------------------------------------------------ histórico

// Antes de QUALQUER gravação, uma cópia do arquivo vai para
// historico/conexoes.AAAAMMDD-HHMMSS.ini, e um índice registra o que
// mudou. São 274 conexões num arquivo só: um erro de edição sem cópia
// significa recadastrar tudo à mão.
//
// A rotação é por evento de gravação (as 20 últimas), não por tempo: o
// que interessa é poder voltar N edições, e não N dias.
const maxHistorico = 20

func dirHistorico(caminho string) string {
	return filepath.Join(filepath.Dir(caminho), "historico")
}

// guardarCopia copia o arquivo atual para o histórico e anota o motivo.
// Falhar aqui NÃO impede a gravação: perder a cópia é ruim, não gravar a
// edição que o operador acabou de fazer é pior.
func guardarCopia(caminho, motivo string) {
	dir := dirHistorico(caminho)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	b, err := os.ReadFile(caminho)
	if err != nil {
		return
	}
	carimbo := time.Now().Format("20060102-150405")
	destino := filepath.Join(dir, "conexoes."+carimbo+".ini")
	if err := os.WriteFile(destino, b, 0o600); err != nil {
		return
	}
	if f, err := os.OpenFile(filepath.Join(dir, "index.txt"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintf(f, "%s  %s\n", carimbo, motivo)
		f.Close()
	}
	rotacionar(dir)
}

// rotacionar mantém só as cópias mais recentes.
func rotacionar(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var copias []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "conexoes.") && strings.HasSuffix(e.Name(), ".ini") {
			copias = append(copias, e.Name())
		}
	}
	if len(copias) <= maxHistorico {
		return
	}
	sort.Strings(copias) // o carimbo ordena por tempo
	for _, velha := range copias[:len(copias)-maxHistorico] {
		os.Remove(filepath.Join(dir, velha))
	}
}
