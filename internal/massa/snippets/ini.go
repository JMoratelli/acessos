package snippets

import (
	"bufio"
	"os"
	"strings"
)

// SecaoINI e uma secao [nome] com seus pares chave=valor,
// preservando a ordem original das chaves.
type SecaoINI struct {
	Nome   string
	Ordem  []string
	Campos map[string]string
}

func (s *SecaoINI) set(k, v string) {
	if _, ok := s.Campos[k]; !ok {
		s.Ordem = append(s.Ordem, k)
	}
	s.Campos[k] = v
}

// Get devolve o valor da chave ou o padrao informado.
func (s *SecaoINI) Get(k, padrao string) string {
	if v, ok := s.Campos[k]; ok {
		return v
	}
	return padrao
}

// GetBool aceita sim/nao, true/false, 1/0, yes/no.
func (s *SecaoINI) GetBool(k string, padrao bool) bool {
	v := strings.ToLower(strings.TrimSpace(s.Get(k, "")))
	switch v {
	case "sim", "true", "1", "yes", "s", "y":
		return true
	case "nao", "não", "false", "0", "no", "n":
		return false
	}
	return padrao
}

// LerINI le um arquivo INI. Linhas que comecam com espaco ou tab sao
// tratadas como continuacao do valor anterior (separadas por \n), que e
// como comandos multilinha ficam guardados no comandos.ini.
//
// Retorna lista vazia sem erro se o arquivo nao existir.
func LerINI(caminho string) ([]*SecaoINI, error) {
	f, err := os.Open(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var secoes []*SecaoINI
	var atual *SecaoINI
	var ultimaChave string

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for sc.Scan() {
		linha := strings.TrimRight(sc.Text(), "\r")
		trim := strings.TrimSpace(linha)

		// continuacao: linha indentada, dentro de uma chave conhecida,
		// e que nao seja comentario nem inicio de secao.
		indentada := linha != "" && (linha[0] == ' ' || linha[0] == '\t')
		if indentada && atual != nil && ultimaChave != "" &&
			!strings.HasPrefix(trim, "[") && !ehComentario(trim) {
			atual.Campos[ultimaChave] += "\n" + trim
			continue
		}

		if trim == "" || ehComentario(trim) {
			continue
		}

		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			atual = &SecaoINI{
				Nome:   strings.TrimSpace(trim[1 : len(trim)-1]),
				Campos: map[string]string{},
			}
			secoes = append(secoes, atual)
			ultimaChave = ""
			continue
		}

		i := strings.Index(trim, "=")
		if i < 0 {
			continue
		}
		chave := strings.ToLower(strings.TrimSpace(trim[:i]))
		valor := strings.TrimSpace(trim[i+1:])

		if atual == nil {
			// pares soltos antes de qualquer secao vao para uma secao vazia
			atual = &SecaoINI{Nome: "", Campos: map[string]string{}}
			secoes = append(secoes, atual)
		}
		atual.set(chave, valor)
		ultimaChave = chave
	}

	return secoes, sc.Err()
}

func ehComentario(s string) bool {
	return strings.HasPrefix(s, "#") || strings.HasPrefix(s, ";")
}

// EscreverArquivo grava conteudo com permissao restrita (0600).
func EscreverArquivo(caminho, conteudo string) error {
	return os.WriteFile(caminho, []byte(conteudo), 0o600)
}

// IndentarMultilinha prepara um valor multilinha para gravacao em INI.
func IndentarMultilinha(v string) string {
	linhas := strings.Split(v, "\n")
	for i := 1; i < len(linhas); i++ {
		linhas[i] = "    " + linhas[i]
	}
	return strings.Join(linhas, "\n")
}
