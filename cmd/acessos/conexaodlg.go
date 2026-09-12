package main

import (
	"fmt"
	"strconv"
	"strings"

	"gio.tools/icons"

	"acessos-go/internal/chaveiro"
	"acessos-go/internal/cofre"
	"acessos-go/internal/conexoes"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgConexao edita uma conexão do .ini. Ele grava campo a campo pelo
// editor de linhas (internal/conexoes/editar.go), então comentários e
// ordem do arquivo sobrevivem — o mesmo arquivo continua sendo lido e
// escrito pelo app Python.
type dlgConexao struct {
	w        *app.Window
	caminho  string
	original conexoes.Conexao

	nome, grupo, host                   widget.Editor
	vncPorta, vncUser, vncSenha         widget.Editor
	sshPorta, sshUser, sshSenha         widget.Editor
	rdpPorta, rdpUser, rdpSenha, rdpDom widget.Editor

	ligaVNC, ligaSSH, ligaRDP widget.Clickable
	vncOn, sshOn, rdpOn       bool

	// Acordeão: um bloco aberto por vez. O formulário inteiro aberto não
	// cabe na tela, e rolar para achar o campo do RDP é pior que clicar
	// no título. -1 = todos fechados.
	aberto int
	cabeca [3]widget.Clickable
	// modo da tela (VNC) e tela do RDP: os dois são segmentados, como no
	// app original, porque combo ignora o tema.
	btnModo [3]widget.Clickable
	modo    int
	btnTela [3]widget.Clickable
	telaRDP int
	// estado extra por protocolo
	vncRonly, vncAuto, sshAuto, rdpAuto      bool
	mrRonly, mrVncAuto, mrSshAuto, mrRdpAuto widget.Clickable
	rdpExtras                                widget.Editor
	// olho de cada campo de senha
	btnOlho  [3]widget.Clickable
	verSenha [3]bool

	btnChav   [3]widget.Clickable // usar credencial do chaveiro: vnc/ssh/rdp
	btnSalvar widget.Clickable
	btnCanc   widget.Clickable
	erro      string
	nova      bool // criação: grava uma seção nova em vez de editar
	recarrega func()
	lista     widget.List
}

// novaConexao abre o mesmo formulário em modo criação, já com o grupo
// preenchido quando veio do menu de um grupo — criar máquina "solta" é
// quase sempre engano de digitação do grupo.
func novaConexao(w *app.Window, caminho, grupo string, recarrega func()) {
	editarConexao(w, caminho, conexoes.Conexao{
		Grupo: dividirGrupo(grupo),
		VNC:   conexoes.AcessoVNC{Porta: 5900, Modo: "encaixar"},
		SSH:   conexoes.AcessoSSH{Porta: 22},
		RDP:   conexoes.AcessoRDP{Porta: 3389, Tela: "dinamico"},
	}, recarrega)
	if d, ok := dialogoAtual().(*dlgConexao); ok {
		d.nova = true
		d.vncOn = true
	}
}

func dividirGrupo(g string) []string {
	if g == "" {
		return nil
	}
	return strings.Split(g, ";")
}

func editarConexao(w *app.Window, caminho string, cx conexoes.Conexao, recarrega func()) {
	d := &dlgConexao{w: w, caminho: caminho, original: cx, recarrega: recarrega}
	d.lista.Axis = layout.Vertical
	por := func(e *widget.Editor, v string) {
		e.SingleLine = true
		e.SetText(v)
	}
	por(&d.nome, cx.Nome)
	por(&d.grupo, strings.Join(cx.Grupo, ";"))
	por(&d.host, cx.Host)
	por(&d.vncPorta, strconv.Itoa(cx.VNC.Porta))
	por(&d.vncUser, cx.VNC.Usuario)
	por(&d.sshPorta, strconv.Itoa(cx.SSH.Porta))
	por(&d.sshUser, cx.SSH.Usuario)
	por(&d.rdpPorta, strconv.Itoa(cx.RDP.Porta))
	por(&d.rdpUser, cx.RDP.Usuario)
	por(&d.rdpDom, cx.RDP.Dominio)
	// Senha NUNCA volta pra tela em claro: o campo nasce vazio e só é
	// gravado se alguém digitar algo. Vazio = mantém a que já está lá.
	//
	// ALIAS é exceção: ele não é segredo, é uma referência, e precisa
	// aparecer — senão o operador não vê que a conexão usa o chaveiro, e
	// pior, não tem como tirar.
	for i, e := range []*widget.Editor{&d.vncSenha, &d.sshSenha, &d.rdpSenha} {
		e.SingleLine = true
		e.Mask = '•'
		bruto := []string{cx.VNC.SenhaBruta, cx.SSH.SenhaBruta, cx.RDP.SenhaBruta}[i]
		if chaveiro.EhAlias(bruto) {
			e.SetText(bruto)
			e.Mask = 0
			d.verSenha[i] = true
		}
	}
	d.vncOn, d.sshOn, d.rdpOn = cx.Tem(conexoes.VNC), cx.Tem(conexoes.SSH), cx.Tem(conexoes.RDP)
	d.modo = indiceEm(cx.VNC.Modo, "encaixar", "1x1", "dinamico")
	d.telaRDP = indiceEm(cx.RDP.Tela, "dinamico", "janela", "cheia")
	d.vncRonly, d.vncAuto = cx.VNC.Ronly, cx.VNC.Auto
	d.sshAuto, d.rdpAuto = cx.SSH.Auto, cx.RDP.Auto
	d.rdpExtras.SingleLine = true
	d.aberto = 0
	if !d.vncOn && d.sshOn {
		d.aberto = 1
	}
	abrirDialogo(d)
}

func (d *dlgConexao) Titulo() string {
	if d.nova {
		return "Nova conexão"
	}
	return "Editar conexão"
}
func (d *dlgConexao) Largura() unit.Dp { return 520 }

func (d *dlgConexao) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if d.ligaVNC.Clicked(gtx) {
		d.vncOn = !d.vncOn
	}
	if d.ligaSSH.Clicked(gtx) {
		d.sshOn = !d.sshOn
	}
	if d.ligaRDP.Clicked(gtx) {
		d.rdpOn = !d.rdpOn
	}
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}
	// "usar do chaveiro" preenche usuário e senha com o MESMO alias — é
	// assim que o app original faz, e é o alias que fica gravado, não a
	// credencial resolvida.
	for i := range d.btnChav {
		if d.btnChav[i].Clicked(gtx) {
			d.escolherDoChaveiro(i)
		}
	}
	if d.btnSalvar.Clicked(gtx) {
		d.salvar()
	}

	linha := func(rot string, e *widget.Editor, dica string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						larg := gtx.Dp(96)
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
						return rotulo(th, fonteMono, spSecundario, rot, tema.Sec)(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return caixaEditor(gtx, th, e, dica, 0)
					}),
				)
			})
		})
	}
	// senha tem o MESMO rótulo à esquerda dos outros campos; o que muda é
	// só o olho no fim da linha. Sem isso ela ficava com outra largura e
	// desalinhava o formulário inteiro.
	linhaSenha := func(rot string, i int, ed *widget.Editor, dica string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						larg := gtx.Dp(96)
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
						return rotulo(th, fonteMono, spSecundario, rot, tema.Sec)(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return d.campoSenhaComOlho(gtx, th, i, ed, dica)
					}),
				)
			})
		})
	}

	for i := range d.cabeca {
		if d.cabeca[i].Clicked(gtx) {
			// acordeão exclusivo: clicar no bloco aberto fecha
			if d.aberto == i {
				d.aberto = -1
			} else {
				d.aberto = i
			}
		}
	}
	for i := range d.btnModo {
		if d.btnModo[i].Clicked(gtx) {
			d.modo = i
		}
		if d.btnTela[i].Clicked(gtx) {
			d.telaRDP = i
		}
	}
	if d.mrRonly.Clicked(gtx) {
		d.vncRonly = !d.vncRonly
	}
	if d.mrVncAuto.Clicked(gtx) {
		d.vncAuto = !d.vncAuto
	}
	if d.mrSshAuto.Clicked(gtx) {
		d.sshAuto = !d.sshAuto
	}
	if d.mrRdpAuto.Clicked(gtx) {
		d.rdpAuto = !d.rdpAuto
	}

	campos := []layout.FlexChild{
		linha("nome", &d.nome, "CAIXA5201"),
		linha("grupo", &d.grupo, "Loja 06;Caixas  (subgrupos com ;)"),
		linha("host", &d.host, "192.168.8.101"),
		espaco(4),
		d.bloco(gtx, th, 0, "TELA · VNC", &d.ligaVNC, d.vncOn, func() []layout.FlexChild {
			return []layout.FlexChild{
				linha("porta", &d.vncPorta, "5900"),
				linha("usuário", &d.vncUser, "só se o servidor pedir (VeNCrypt)"),
				linhaSenha("senha", 0, &d.vncSenha, "vazio pergunta na hora"),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return segmentado(gtx, th,
							[]*widget.Clickable{&d.btnModo[0], &d.btnModo[1], &d.btnModo[2]},
							[]string{"Encaixar", "1:1", "Dinâmico"}, d.modo)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return caixaMarcar(gtx, th, &d.mrRonly, d.vncRonly, "abrir bloqueado (só ver)")
						}),
						layout.Rigid(layout.Spacer{Width: 12}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return caixaMarcar(gtx, th, &d.mrVncAuto, d.vncAuto, "reconectar sozinho")
						}),
					)
				}),
			}
		}),
		d.bloco(gtx, th, 1, "SHELL · SSH", &d.ligaSSH, d.sshOn, func() []layout.FlexChild {
			return []layout.FlexChild{
				linha("porta", &d.sshPorta, "22"),
				linha("usuário", &d.sshUser, "obrigatório"),
				linhaSenha("senha", 1, &d.sshSenha, "vazio usa chave"),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return caixaMarcar(gtx, th, &d.mrSshAuto, d.sshAuto, "reconectar sozinho")
				}),
			}
		}),
		d.bloco(gtx, th, 2, "RDP", &d.ligaRDP, d.rdpOn, func() []layout.FlexChild {
			return []layout.FlexChild{
				linha("porta", &d.rdpPorta, "3389"),
				linha("usuário", &d.rdpUser, ""),
				linha("domínio", &d.rdpDom, "FQDN dispara Kerberos — prefira vazio"),
				linhaSenha("senha", 2, &d.rdpSenha, ""),
				linha("extras", &d.rdpExtras, "opções cruas do FreeRDP, separadas por espaço"),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return segmentado(gtx, th,
							[]*widget.Clickable{&d.btnTela[0], &d.btnTela[1], &d.btnTela[2]},
							[]string{"Dinâmico", "Janela", "Cheia"}, d.telaRDP)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return caixaMarcar(gtx, th, &d.mrRdpAuto, d.rdpAuto, "reconectar sozinho")
				}),
			}
		}),
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = gtx.Constraints.Max.Y * 6 / 10
			return material.List(th, &d.lista).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, campos...)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if d.erro == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 6}.Layout(gtx,
				rotulo(th, fonteMono, spSecundario, d.erro, tema.ErroFg))
		}),
		espaco(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					aviso := "senhas novas serão cifradas no cofre"
					if cofreAberto == nil {
						aviso = "cofre trancado: senha nova entraria em claro"
					}
					return rotulo(th, fonteMono, spCardMeta, aviso, tema.Fraco)(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnCanc, "Cancelar")
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoPrimario(gtx, th, &d.btnSalvar, "Salvar")
				}),
			)
		}),
	)
}

func (d *dlgConexao) salvar() {
	nome := strings.TrimSpace(d.nome.Text())
	if err := d.validar(nome); err != nil {
		d.erro = err.Error()
		return
	}
	campos := map[string]string{
		"grupo": strings.TrimSpace(d.grupo.Text()),
		"host":  strings.TrimSpace(d.host.Text()),
		"vnc":   umZero(d.vncOn), "porta": d.vncPorta.Text(), "usuario": d.vncUser.Text(),
		"ssh": umZero(d.sshOn), "ssh_porta": d.sshPorta.Text(), "ssh_usuario": d.sshUser.Text(),
		"rdp": umZero(d.rdpOn), "rdp_porta": d.rdpPorta.Text(), "rdp_usuario": d.rdpUser.Text(),
		"rdp_dominio": d.rdpDom.Text(),
		"modo":        []string{"encaixar", "1x1", "dinamico"}[d.modo],
		"ronly":       umZero(d.vncRonly),
		"auto":        umZero(d.vncAuto),
		"ssh_auto":    umZero(d.sshAuto),
		"rdp_auto":    umZero(d.rdpAuto),
		"rdp_tela":    []string{"dinamico", "janela", "cheia"}[d.telaRDP],
		"rdp_extras":  strings.TrimSpace(d.rdpExtras.Text()),
	}
	// senha em branco = manter a que está no arquivo. Só entra no mapa
	// quando o operador digitou algo.
	for chave, ed := range map[string]*widget.Editor{
		"senha": &d.vncSenha, "ssh_senha": &d.sshSenha, "rdp_senha": &d.rdpSenha,
	} {
		nova := ed.Text()
		if nova == "" {
			continue
		}
		// Alias NUNCA é cifrado. Cifrar "!Zanthus" grava um segredo cujo
		// conteúdo é o texto do alias: na conexão seguinte ele é
		// decifrado e mandado COMO SENHA, e a autenticação falha sem
		// explicação. Era exatamente isso que quebrava o SSH.
		if chaveiro.EhAlias(nova) {
			campos[chave] = nova
			continue
		}
		if cofreAberto != nil {
			selada, err := cofreAberto.Cifrar(nova)
			if err != nil {
				d.erro = err.Error()
				return
			}
			nova = selada
		}
		campos[chave] = nova
	}
	var err error
	if d.nova {
		err = conexoes.Criar(d.caminho, nome, campos)
	} else {
		err = conexoes.Salvar(d.caminho, d.original.Nome, nome, campos)
	}
	if err != nil {
		d.erro = err.Error()
		return
	}
	if d.recarrega != nil {
		d.recarrega()
	}
	fmt.Printf("conexão %q salva\n", nome)
	fecharDialogo()
	d.w.Invalidate()
}

func umZero(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

var _ = cofre.Cifrado

// duplicarConexao abre o editor já preenchido com a PRÓXIMA da sequência:
// nome e IP incrementados, pulando os que já existem no arquivo. É o
// caminho de cadastro em série (uma loja com 40 caixas), e é por isso que
// ele abre o editor em vez de gravar direto — o operador confere antes.
func duplicarConexao(w *app.Window, caminho string, arq *conexoes.Arquivo, cx conexoes.Conexao, recarrega func()) {
	usados := map[string]bool{}
	hosts := map[string]bool{}
	for _, c := range arq.Conexoes {
		usados[strings.ToLower(c.Nome)] = true
		hosts[c.Host] = true
	}

	nome := proximoNome(cx.Nome)
	for i := 0; i < 200 && usados[strings.ToLower(nome)]; i++ {
		nome = proximoNome(nome)
	}
	host := proximoHost(cx.Host)
	for i := 0; i < 200 && hosts[host] && host != cx.Host; i++ {
		seguinte := proximoHost(host)
		if seguinte == host { // bateu no teto do octeto
			break
		}
		host = seguinte
	}

	copia := cx
	copia.Nome = nome
	copia.Host = host
	editarConexao(w, caminho, copia, recarrega)
	if d, ok := dialogoAtual().(*dlgConexao); ok {
		d.nova = true
	}
}

// escolherDoChaveiro abre o menu de credenciais e preenche o par
// usuário/senha do protocolo com o alias escolhido.
func (d *dlgConexao) escolherDoChaveiro(protocolo int) {
	if chaveiroAtual == nil {
		return
	}
	campos := [][2]*widget.Editor{
		{&d.vncUser, &d.vncSenha},
		{&d.sshUser, &d.sshSenha},
		{&d.rdpUser, &d.rdpSenha},
	}[protocolo]

	var itens []*itemMenu
	for _, nome := range chaveiroAtual.Ordem {
		nome := nome
		c := chaveiroAtual.Credenciais[nome]
		itens = append(itens, &itemMenu{
			rotulo: "!" + nome + "  (" + c.Usuario + ")",
			acao: func() {
				// o alias vai nos DOIS campos: é o que o app original
				// grava, e é o que permite trocar a senha em um lugar só.
				campos[0].SetText("!" + nome)
				campos[1].SetText("!" + nome)
			},
		})
	}
	if len(itens) == 0 {
		return
	}
	// na posição do ponteiro, não num ponto fixo: antes o menu nascia no
	// canto da janela, longe do botão que o abriu.
	abrirMenu(ultimaPosPonteiro(), itens)
}

func indiceEm(v string, opcoes ...string) int {
	for i, o := range opcoes {
		if v == o {
			return i
		}
	}
	return 0
}

// campoSenhaComOlho: senha com o botão de mostrar. O olho é essencial em
// campo que nasce vazio significando "mantém a atual" — sem ver o que se
// digitou, errar a senha nova e só descobrir na próxima conexão é fácil.
func (d *dlgConexao) campoSenhaComOlho(gtx layout.Context, th *material.Theme, i int, ed *widget.Editor, dica string) layout.Dimensions {
	if d.btnOlho[i].Clicked(gtx) {
		d.verSenha[i] = !d.verSenha[i]
	}
	if d.verSenha[i] {
		ed.Mask = 0
	} else {
		ed.Mask = '•'
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return caixaEditor(gtx, th, ed, dica, 0)
		}),
		layout.Rigid(layout.Spacer{Width: 4}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			ic := icons.ActionVisibility
			if d.verSenha[i] {
				ic = icons.ActionVisibilityOff
			}
			return toggleSessao(gtx, th, &d.btnOlho[i], ic, d.verSenha[i], tema.Azul)
		}),
	)
}

// validar repete as regras do app original: são elas que impedem um .ini
// que o outro app não consegue ler.
func (d *dlgConexao) validar(nome string) error {
	if nome == "" {
		return fmt.Errorf("o nome não pode ficar vazio")
	}
	if strings.ContainsAny(nome, "[]") {
		return fmt.Errorf("o nome não pode ter colchetes (é o delimitador da seção no .ini)")
	}
	if strings.EqualFold(nome, "geral") || strings.EqualFold(nome, "cofre") {
		return fmt.Errorf("%q é uma seção reservada do arquivo", nome)
	}
	if strings.TrimSpace(d.host.Text()) == "" {
		return fmt.Errorf("o host não pode ficar vazio")
	}
	if !d.vncOn && !d.sshOn && !d.rdpOn {
		return fmt.Errorf("ligue ao menos um serviço (tela, shell ou RDP)")
	}
	if d.sshOn && strings.TrimSpace(d.sshUser.Text()) == "" {
		return fmt.Errorf("SSH exige usuário")
	}
	for rot, ed := range map[string]*widget.Editor{
		"porta da tela": &d.vncPorta, "porta do SSH": &d.sshPorta, "porta do RDP": &d.rdpPorta,
	} {
		v := strings.TrimSpace(ed.Text())
		if v == "" {
			continue
		}
		if _, err := strconv.Atoi(v); err != nil {
			return fmt.Errorf("%s precisa ser número", rot)
		}
	}
	return nil
}

// bloco é uma seção do acordeão: cabeçalho com o interruptor do serviço e
// o corpo, que só aparece quando o bloco está aberto.
//
// O interruptor fica no cabeçalho mas NÃO é o alvo do clique de abrir: o
// clique de abrir/fechar é do resto da linha. Interruptor dentro de
// cabeçalho que também abre/fecha nunca recebe o próprio clique — é uma
// armadilha documentada no app original.
func (d *dlgConexao) bloco(gtx layout.Context, th *material.Theme, i int, titulo string,
	liga *widget.Clickable, ligado bool, corpo func() []layout.FlexChild) layout.FlexChild {

	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		filhos := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 4, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return caixaMarcar(gtx, th, liga, ligado, "")
						}),
						layout.Rigid(layout.Spacer{Width: 6}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return d.cabeca[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								cor := tema.Sec
								if d.aberto == i {
									cor = tema.Texto
								}
								seta := "▸"
								if d.aberto == i {
									seta = "▾"
								}
								return negrito(txt(th, fonteMono, spSecundario, seta+"  "+titulo, cor)).Layout(gtx)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if chaveiroAtual == nil || len(chaveiroAtual.Ordem) == 0 || d.aberto != i {
								return layout.Dimensions{}
							}
							return botaoSutil(gtx, th, &d.btnChav[i], "usar do chaveiro")
						}),
					)
				})
			}),
		}
		if d.aberto == i {
			filhos = append(filhos, corpo()...)
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
	})
}
