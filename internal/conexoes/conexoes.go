// Package conexoes lê o conexoes.ini do app original (mesmo arquivo, sem
// conversão) e devolve as conexões já agrupadas.
//
// PARSER PRÓPRIO, DE PROPÓSITO: a maioria das libs de INI trata ";" como
// início de comentário, e aqui ";" é o SEPARADOR DE SUBGRUPO
// ("grupo = Loja 06;Caixas"). Uma lib com comentário inline engoliria o
// subgrupo inteiro em silêncio — o app original também desabilita isso no
// configparser pelo mesmo motivo.
package conexoes

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Protocolo é um dos acessos que uma conexão oferece.
type Protocolo string

const (
	VNC  Protocolo = "vnc"
	SSH  Protocolo = "ssh"
	RDP  Protocolo = "rdp"
	SFTP Protocolo = "sftp" // deriva do SSH (mesmas credenciais/porta)
)

// SenhaBruta é o valor COMO ESTÁ no arquivo (pode ser "!alias" ou um
// segredo cifrado). O editor precisa dele para não regravar por cima de um
// alias — resolver e depois salvar o resultado apagaria a referência.
type AcessoVNC struct {
	SenhaBruta string
	Ligado     bool
	Porta      int
	Usuario    string
	Senha      string
	Modo       string // encaixar | 1x1 | dinamico
	Ronly      bool
	Auto       bool
}

type AcessoSSH struct {
	SenhaBruta string
	Ligado     bool
	Porta      int
	Usuario    string
	Senha      string
	Auto       bool
}

type AcessoRDP struct {
	SenhaBruta string
	Ligado     bool
	Porta      int
	Usuario    string
	Senha      string
	Dominio    string
	Tela       string // dinamico | cheia | janela
	Auto       bool
}

// Conexao é uma seção do .ini (o nome da seção é o nome exibido).
type Conexao struct {
	// Windows vem da sonda de plataforma (banner SSH). Ausente = ainda
	// não se sabe, e o massa trata isso como "assume Linux, mas avisa".
	Windows bool
	Nome    string
	Grupo   []string // ["Loja 06", "Caixas"]
	Host    string

	VNC AcessoVNC
	SSH AcessoSSH
	RDP AcessoRDP

	bruto map[string]string
}

// Arquivo é o conexoes.ini inteiro.
type Arquivo struct {
	Geral    map[string]string
	Cofre    map[string]string
	Conexoes []Conexao
}

// Carregar lê o .ini. As senhas ficam como estão no arquivo (possivelmente
// "enc:v1:..."); quem precisa do texto claro decifra com internal/cofre.
func Carregar(caminho string) (*Arquivo, error) {
	f, err := os.Open(caminho)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	arq := &Arquivo{Geral: map[string]string{}, Cofre: map[string]string{}}
	secao := ""
	campos := map[string]string{}

	fechar := func() {
		switch secao {
		case "":
			// antes da primeira seção: nada
		case "geral":
			arq.Geral = campos
		case "cofre":
			arq.Cofre = campos
		default:
			arq.Conexoes = append(arq.Conexoes, montar(secao, campos))
		}
		campos = map[string]string{}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		linha := strings.TrimSpace(sc.Text())
		// "#" é comentário; ";" NÃO é (ver comentário do pacote).
		if linha == "" || strings.HasPrefix(linha, "#") {
			continue
		}
		if strings.HasPrefix(linha, "[") && strings.HasSuffix(linha, "]") {
			fechar()
			secao = strings.TrimSpace(linha[1 : len(linha)-1])
			continue
		}
		chave, valor, ok := strings.Cut(linha, "=")
		if !ok {
			continue
		}
		campos[strings.ToLower(strings.TrimSpace(chave))] = strings.TrimSpace(valor)
	}
	fechar()
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lendo %s: %w", caminho, err)
	}
	return arq, nil
}

// montar aplica os MESMOS defaults do Conexao.__init__ do acessos.py —
// inclusive os dois que não são óbvios: ssh/rdp nascem ligados quando há
// usuário configurado, e vnc nasce LIGADO quando a chave não existe.
func montar(nome string, c map[string]string) Conexao {
	cx := Conexao{
		Nome:  nome,
		Grupo: caminhoGrupo(c["grupo"]),
		Host:  c["host"],
		bruto: c,
	}

	cx.Windows = verdade(c, "windows", false)
	cx.VNC = AcessoVNC{
		Ligado:     verdade(c, "vnc", true),
		Porta:      inteiro(c, "porta", 5900),
		Usuario:    c["usuario"],
		Senha:      c["senha"],
		SenhaBruta: c["senha"],
		Modo:       escolha(c["modo"], "encaixar", "encaixar", "1x1", "dinamico"),
		Ronly:      verdade(c, "ronly", false),
		Auto:       verdade(c, "auto", false),
	}
	cx.SSH = AcessoSSH{
		Ligado:     verdade(c, "ssh", c["ssh_usuario"] != ""),
		Porta:      inteiro(c, "ssh_porta", 22),
		Usuario:    c["ssh_usuario"],
		Senha:      c["ssh_senha"],
		SenhaBruta: c["ssh_senha"],
		Auto:       verdade(c, "ssh_auto", false),
	}
	cx.RDP = AcessoRDP{
		Ligado:     verdade(c, "rdp", c["rdp_usuario"] != ""),
		Porta:      inteiro(c, "rdp_porta", 3389),
		Usuario:    c["rdp_usuario"],
		Senha:      c["rdp_senha"],
		SenhaBruta: c["rdp_senha"],
		Dominio:    c["rdp_dominio"],
		Tela:       escolha(c["rdp_tela"], "dinamico", "dinamico", "cheia", "janela"),
		Auto:       verdade(c, "rdp_auto", false),
	}
	return cx
}

// Tem diz se a conexão oferece aquele protocolo (SFTP vem junto do SSH).
func (c Conexao) Tem(p Protocolo) bool {
	switch p {
	case VNC:
		return c.VNC.Ligado
	case SSH, SFTP:
		return c.SSH.Ligado
	case RDP:
		return c.RDP.Ligado
	}
	return false
}

// GrupoStr é o caminho do grupo como aparece no arquivo ("Loja 06;Caixas").
func (c Conexao) GrupoStr() string { return strings.Join(c.Grupo, ";") }

// Grupo é um nó da árvore de grupos (Loja 06 > Caixas).
type Grupo struct {
	Nome     string
	Caminho  []string
	Filhos   []*Grupo
	Conexoes []Conexao
}

// Total conta as conexões deste grupo e de todos os descendentes.
func (g *Grupo) Total() int {
	n := len(g.Conexoes)
	for _, f := range g.Filhos {
		n += f.Total()
	}
	return n
}

// Arvore monta a hierarquia de grupos a partir da lista plana, na ordem em
// que aparecem no arquivo (o app original ordena por nome; aqui manter a
// ordem do arquivo já dá um resultado estável e previsível).
func (a *Arquivo) Arvore() []*Grupo {
	raiz := &Grupo{}
	indice := map[string]*Grupo{}

	for _, cx := range a.Conexoes {
		pai := raiz
		caminho := []string{}
		for _, nivel := range cx.Grupo {
			caminho = append(caminho, nivel)
			chave := strings.Join(caminho, ";")
			g, ok := indice[chave]
			if !ok {
				g = &Grupo{Nome: nivel, Caminho: append([]string{}, caminho...)}
				indice[chave] = g
				pai.Filhos = append(pai.Filhos, g)
			}
			pai = g
		}
		pai.Conexoes = append(pai.Conexoes, cx)
	}
	ordenar(raiz)
	return raiz.Filhos
}

func ordenar(g *Grupo) {
	sort.SliceStable(g.Filhos, func(i, j int) bool {
		return strings.ToLower(g.Filhos[i].Nome) < strings.ToLower(g.Filhos[j].Nome)
	})
	sort.SliceStable(g.Conexoes, func(i, j int) bool {
		return strings.ToLower(g.Conexoes[i].Nome) < strings.ToLower(g.Conexoes[j].Nome)
	})
	for _, f := range g.Filhos {
		ordenar(f)
	}
}

func caminhoGrupo(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"Sem grupo"}
	}
	return out
}

// verdade segue o verdade() do acessos.py: 1|sim|s|true|yes|y|on.
func verdade(c map[string]string, chave string, def bool) bool {
	v, ok := c[chave]
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "sim", "s", "true", "yes", "y", "on":
		return true
	}
	return false
}

func inteiro(c map[string]string, chave string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(c[chave]))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func escolha(v, def string, validos ...string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, ok := range validos {
		if v == ok {
			return v
		}
	}
	return def
}
