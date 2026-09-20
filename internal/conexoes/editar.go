package conexoes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"acessos-go/internal/iniutil"
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

// SalvarGeral grava chaves na seção [geral] (tema, fonte, caminho…),
// criando a seção se ela ainda não existir. Sem cópia no histórico de
// propósito: são preferências de interface, mexidas a cada clique, e
// encher o histórico com elas afogaria as edições de conexão, que são o
// que realmente importa poder desfazer.
func SalvarGeral(caminho string, campos map[string]string) error {
	linhas, err := iniutil.LerLinhas(caminho)
	if err != nil {
		return err
	}
	if _, _, ok := iniutil.Faixa(linhas, "geral"); !ok {
		// [geral] vai no TOPO: é o cabeçalho do arquivo, e uma seção nova
		// no fim ficaria depois das conexões, onde ninguém procura.
		linhas = append([]string{"[geral]", ""}, linhas...)
		if err := iniutil.GravarAtomico(caminho, linhas); err != nil {
			return err
		}
	}
	return salvarSecao(caminho, "geral", "", campos)
}

// Remover apaga a seção inteira de uma conexão.
func Remover(caminho, nome string) error {
	guardarCopia(caminho, fmt.Sprintf("removeu %q", nome))
	linhas, err := iniutil.LerLinhas(caminho)
	if err != nil {
		return err
	}
	ini, fim, ok := iniutil.Faixa(linhas, nome)
	if !ok {
		return fmt.Errorf("conexão %q não está em %s", nome, caminho)
	}
	return iniutil.GravarAtomico(caminho, append(append([]string{}, linhas[:ini]...), linhas[fim:]...))
}

// Duplicar copia a seção inteira com outro nome, logo abaixo da original.
// Copiar as linhas (em vez de reescrever a partir do modelo) mantém até os
// segredos cifrados como estão — não precisa do cofre aberto para duplicar.
func Duplicar(caminho, nome, novoNome string) error {
	guardarCopia(caminho, fmt.Sprintf("duplicou %q", nome))
	linhas, err := iniutil.LerLinhas(caminho)
	if err != nil {
		return err
	}
	if _, _, existe := iniutil.Faixa(linhas, novoNome); existe {
		return fmt.Errorf("já existe uma conexão chamada %q", novoNome)
	}
	ini, fim, ok := iniutil.Faixa(linhas, nome)
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
	return iniutil.GravarAtomico(caminho, out)
}

// Salvar aplica campos a uma seção: chave existente é substituída no lugar
// (preservando a posição), chave nova entra no fim da seção, e valor vazio
// remove a chave. Renomear a seção também é suportado (novoNome != "").
func Salvar(caminho, nome, novoNome string, campos map[string]string) error {
	guardarCopia(caminho, fmt.Sprintf("editou %q", nome))
	return salvarSecao(caminho, nome, novoNome, campos)
}

// Lote agrupa várias gravações numa OPERAÇÃO só, do ponto de vista do
// histórico: a cópia do arquivo é guardada uma vez, na primeira gravação,
// e as seguintes apenas escrevem.
//
// Existe por um defeito concreto. Detectar a plataforma de uma loja
// gravava máquina por máquina, cada gravação guardando a sua cópia — numa
// loja de 54 máquinas eram 54 cópias de uma vez, e como a rotação mantém
// só as 20 últimas (maxHistorico), uma detecção APAGAVA todas as cópias de
// edição de verdade. O histórico existe justamente para desfazer um erro
// de edição em 274 conexões; enchê-lo de cópias idênticas o destrói.
//
// A gravação continua sendo INCREMENTAL, e isso é de propósito: juntar
// tudo para escrever no fim faria fechar o app no meio perder o que já
// tinha sido detectado. O que muda é só o histórico.
//
// Pode ser usado de várias goroutines: a trava também serializa a escrita
// do arquivo, que é regravado inteiro a cada seção (sem ela, duas sondas
// terminando juntas se sobrescreveriam).
type Lote struct {
	mu      sync.Mutex
	caminho string
	motivo  string
	copiado bool
}

// NovoLote abre a operação. O motivo é o que vai para o índice do
// histórico — uma linha para a operação inteira, não uma por máquina.
func NovoLote(caminho, motivo string) *Lote {
	return &Lote{caminho: caminho, motivo: motivo}
}

// Salvar grava uma seção, guardando a cópia do histórico só na primeira.
func (l *Lote) Salvar(nome, novoNome string, campos map[string]string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.copiado {
		guardarCopia(l.caminho, l.motivo)
		l.copiado = true
	}
	return salvarSecao(l.caminho, nome, novoNome, campos)
}

// salvarSecao é o miolo do Salvar, sem o histórico — separado para o
// SalvarGeral e o Lote reusarem a mesma edição linha a linha.
func salvarSecao(caminho, nome, novoNome string, campos map[string]string) error {
	linhas, err := iniutil.LerLinhas(caminho)
	if err != nil {
		return err
	}
	ini, fim, ok := iniutil.Faixa(linhas, nome)
	if !ok {
		return fmt.Errorf("conexão %q não está em %s", nome, caminho)
	}
	if novoNome != "" && novoNome != nome {
		if _, _, existe := iniutil.Faixa(linhas, novoNome); existe {
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
	return iniutil.GravarAtomico(caminho, out)
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

	linhas, err := iniutil.LerLinhas(caminho)
	if err != nil {
		return err
	}
	emCofre := false
	for i, l := range linhas {
		if n, ok := iniutil.NomeSecao(l); ok {
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
	return iniutil.GravarAtomico(caminho, linhas)
}

// Criar acrescenta uma seção nova no fim do arquivo. O nome é a chave da
// seção (é assim que o .ini identifica a conexão), então duplicata é erro
// e não silêncio: duas seções com o mesmo nome fariam o leitor ficar com
// uma e descartar a outra sem avisar.
func Criar(caminho, nome string, campos map[string]string) error {
	guardarCopia(caminho, fmt.Sprintf("criou %q", nome))
	linhas, err := iniutil.LerLinhas(caminho)
	if err != nil {
		return err
	}
	if _, _, existe := iniutil.Faixa(linhas, nome); existe {
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
	return iniutil.GravarAtomico(caminho, linhas)
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
	// O carimbo tem resolução de SEGUNDO, e duas gravações dentro do mesmo
	// segundo geravam o mesmo nome: a segunda sobrescrevia a primeira em
	// silêncio, o índice anotava as duas linhas e só existia um arquivo —
	// ou seja, o histórico mentia justamente no caso de edição rápida
	// seguida de outra. O desempate mantém as duas.
	//
	// Uma cópia desempatada ordena antes da irmã sem sufixo do MESMO
	// segundo (o '0' vem antes do 'i' de ".ini"), o que só teria efeito se
	// a rotação caísse exatamente entre as duas — e aí apagaria uma de
	// duas cópias do mesmo instante, que é indiferente.
	for i := 2; i < 100; i++ {
		if _, err := os.Stat(destino); err != nil {
			break
		}
		destino = filepath.Join(dir, fmt.Sprintf("conexoes.%s.%02d.ini", carimbo, i))
	}
	if err := os.WriteFile(destino, b, 0o600); err != nil {
		return
	}
	// O índice anota o NOME do arquivo, e não só o carimbo: com o
	// desempate acima, o carimbo sozinho deixaria de identificar a cópia.
	if f, err := os.OpenFile(filepath.Join(dir, "index.txt"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintf(f, "%s  %s\n", filepath.Base(destino), motivo)
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
