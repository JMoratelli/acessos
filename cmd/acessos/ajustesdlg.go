package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/explorer"
)

// dlgAjustes mostra ONDE o app está lendo cada coisa e deixa apontar para
// outro conexoes.ini. É a informação que mais some quando o arquivo está
// no Drive/Insync e existem duas cópias: sem ver o caminho, ninguém
// descobre que está editando o inventário errado.
type dlgAjustes struct {
	w           *app.Window
	ini         widget.Editor
	btnOk       widget.Clickable
	btnCanc     widget.Clickable
	btnProcurar widget.Clickable
	// aba é a faceta em exibição; btnAba são os três segmentos.
	aba         int
	btnAba      [3]widget.Clickable
	atalho      widget.Clickable
	atalhoOn    bool
	autostart   widget.Clickable
	autostartOn bool
	lista       widget.List
	erro        string
	aviso       string

	// procurar() roda em goroutine (ChooseFile bloqueia até o usuário
	// decidir) — o resultado só é aplicado no editor dentro do Corpo,
	// que é o laço de quadro; tocar no widget.Editor de outra goroutine
	// não é seguro.
	mu          sync.Mutex
	procurando  bool
	pendCaminho string
	pendErro    string
	pendPronto  bool
	// O autostart tem a mesma forma: o portal abre um diálogo e espera a
	// pessoa decidir, o que no laço de quadro seria a janela congelada
	// por minutos. autoIndo tranca o segundo clique; pendAuto é ponteiro
	// porque o resultado FALSO é resposta legítima ("o sistema disse
	// não") e precisa se distinguir de "ainda não respondeu".
	autoIndo bool
	pendAuto *bool
}

func abrirAjustes(w *app.Window, ini string) {
	d := &dlgAjustes{w: w}
	d.ini.SingleLine = true
	d.ini.SetText(ini)
	d.atalhoOn = atalhoGlobalLigado(ini)
	d.autostartOn = lerAutostart(ini) == autostartLigado
	abrirDialogo(d)
}

func (d *dlgAjustes) Titulo() string   { return "Ajustes" }
func (d *dlgAjustes) Largura() unit.Dp { return 620 }

// As três facetas dos Ajustes.
//
// São ABAS-FACETA e não abas-documento: cada uma é uma vista fixa do mesmo
// assunto, não algo que se abre e fecha. Por isso não há aba fixa nem
// botão de fechar — o padrão das abas de sessão, na tira de cima, é o
// outro e não se aplica aqui.
//
// Antes disto a tela era uma pilha plana: caminho de arquivo, quatro
// linhas de informação, três caixas de marcar e um log, tudo junto, sem
// nada dizendo o que pertencia a quê.
const (
	ajArquivos = iota
	ajAtalho
	ajDiagnostico
)

var rotulosAjustes = []string{"Arquivos", "Atalho", "Diagnóstico"}

func (d *dlgAjustes) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if d.btnCanc.Clicked(gtx) {
		fecharDialogo()
	}
	if d.btnOk.Clicked(gtx) {
		d.aplicar()
	}
	if d.atalho.Clicked(gtx) {
		d.trocarAtalho()
	}
	if d.autostart.Clicked(gtx) {
		d.trocarAutostart()
	}
	for i := range d.btnAba {
		if d.btnAba[i].Clicked(gtx) {
			d.aba = i
		}
	}
	if d.btnProcurar.Clicked(gtx) {
		d.mu.Lock()
		ja := d.procurando
		d.procurando = true
		d.mu.Unlock()
		if !ja {
			go d.procurar()
		}
	}
	d.mu.Lock()
	if d.pendPronto {
		d.ini.SetText(d.pendCaminho)
		d.pendPronto = false
	}
	if d.pendErro != "" {
		d.erro = d.pendErro
		d.pendErro = ""
	}
	if d.pendAuto != nil {
		d.autostartOn = *d.pendAuto
		d.pendAuto = nil
		d.erro = ""
	}
	autoIndo := d.autoIndo
	d.mu.Unlock()
	d.lista.Axis = layout.Vertical

	filhos := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return segmentado(gtx, th,
				[]*widget.Clickable{&d.btnAba[0], &d.btnAba[1], &d.btnAba[2]},
				rotulosAjustes, d.aba)
		}),
		espaco(14),
	}
	switch d.aba {
	case ajAtalho:
		filhos = append(filhos, d.corpoAtalho(th, autoIndo)...)
	case ajDiagnostico:
		filhos = append(filhos, d.corpoDiagnostico(th)...)
	default:
		filhos = append(filhos, d.corpoArquivos(th)...)
	}

	// Erro e aviso moram FORA da aba, logo acima dos botões. Quem trocou
	// o atalho e viu a gravação falhar pode estar em outra aba no quadro
	// seguinte, e a mensagem não pode sumir junto com a aba que a gerou.
	if d.erro != "" {
		filhos = append(filhos, espaco(8),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, d.erro, tema.ErroFg)))
	}
	if d.aviso != "" {
		filhos = append(filhos, espaco(8),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, d.aviso, tema.OkFg)))
	}

	filhos = append(filhos, espaco(14), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		botoes := []layout.FlexChild{
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoNeutro(gtx, th, &d.btnCanc, "Fechar")
			}),
		}
		// "Usar este arquivo" age sobre o campo da aba de Arquivos. Nas
		// outras seria oferecer ação sobre algo que não está na tela.
		// Fechar fica em todas: sempre há caminho de saída.
		if d.aba == ajArquivos {
			botoes = append(botoes,
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoPrimario(gtx, th, &d.btnOk, "Usar este arquivo")
				}))
		}
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, botoes...)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

// corpoArquivos: onde o app lê cada coisa. É a informação que mais some
// quando o conexoes.ini está no Drive/Insync e existem duas cópias — sem
// ver o caminho, ninguém descobre que está editando o inventário errado.
func (d *dlgAjustes) corpoArquivos(th *material.Theme) []layout.FlexChild {
	dir := filepath.Dir(d.ini.Text())
	linhaInfo := func(rot, valor string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						larg := gtx.Dp(110)
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = larg, larg
						return rotulo(th, fonteMono, spSecundario, rot, tema.Sec)(gtx)
					}),
					layout.Flexed(1, rotulo(th, fonteMono, spSecundario, valor, tema.Texto)),
				)
			})
		})
	}
	return []layout.FlexChild{
		layout.Rigid(rotulo(th, fonteMono, spSecundario, "conexões (arquivo em uso)", tema.Sec)),
		espaco(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return caixaEditor(gtx, th, &d.ini, "caminho do conexoes.ini", 0)
				}),
				layout.Rigid(layout.Spacer{Width: 6}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoSutil(gtx, th, &d.btnProcurar, "procurar…")
				}),
			)
		}),
		espaco(12),
		linhaInfo("snippets", caminhoSnippets(d.ini.Text())),
		linhaInfo("chaveiro", filepath.Join(dir, "chaveiro.ini")),
		linhaInfo("cofre", ondeEstaOCofre()),
		linhaInfo("tema", nomeDoTema()),
	}
}

// corpoAtalho junta o que decide se o Ctrl+Shift+F12 existe e até quando.
func (d *dlgAjustes) corpoAtalho(th *material.Theme, autoIndo bool) []layout.FlexChild {
	filhos := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return caixaMarcar(gtx, th, &d.atalho, d.atalhoOn,
				"atalho global (Ctrl+Shift+F12) para a busca de máquinas")
		}),
	}

	// O atalho existe mas o sistema não amarrou tecla nenhuma — o caso de
	// quem fechou o diálogo do KDE sem querer. Antes isto só saía em
	// stderr, e o Ctrl+Shift+F12 ficava morto sem nada na tela explicando.
	// Ver atalhogatilho.go.
	//
	// Cor de ATENÇÃO, não a de erro: o vermelho aqui é de ação destrutiva
	// e de falha, e gastá-lo num aviso que pede uma providência faria ele
	// parar de significar perigo.
	//
	// Só aparece com a caixa MARCADA: desmarcada, "sem tecla" é o estado
	// esperado, e avisar seria alarme sobre o que a pessoa acabou de pedir.
	if d.atalhoOn && atalhoSemTecla() {
		filhos = append(filhos, espaco(4), layout.Rigid(recuado(
			rotulo(th, fonteMono, spSecundario,
				"registrado SEM TECLA — amarre em Preferências do Sistema → Atalhos → Acessos",
				tema.AtencaoFg))))
	}

	// Autostart: só onde a ideia existe (Linux, portal Background). Fora
	// dali não há serviço à parte para subir no login, e a caixa não faria
	// nada — ver autostartpref.go.
	if definirAutostartNoSistema != nil {
		filhos = append(filhos, espaco(10),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return caixaMarcar(gtx, th, &d.autostart, d.autostartOn,
					"manter o atalho valendo depois do login, sem abrir o app")
			}))
		if autoIndo {
			filhos = append(filhos, espaco(4), layout.Rigid(recuado(
				rotulo(th, fonteMono, spSecundario,
					"esperando a resposta do sistema…", tema.Sec))))
		}
	}
	return filhos
}

// corpoDiagnostico era uma caixa de marcar que abria um painel embaixo de
// tudo. Como aba, a própria escolha da aba é o "mostrar" — uma caixa para
// revelar conteúdo dentro de uma aba dedicada a ele seria um clique a
// mais sem dizer nada.
func (d *dlgAjustes) corpoDiagnostico(th *material.Theme) []layout.FlexChild {
	linhas := diagnostico()
	return []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = gtx.Constraints.Max.Y / 2
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					superficie(gtx, gtx.Constraints.Min, tema.TermBg, tema.Borda2, 8)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				},
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.List(th, &d.lista).Layout(gtx, len(linhas), func(gtx layout.Context, i int) layout.Dimensions {
							// mais recentes primeiro: é o que se quer ver
							return rotuloLinha(th, fonteMono, spCardMeta, linhas[len(linhas)-1-i], tema.TermFg)(gtx)
						})
					})
				},
			)
		}),
	}
}

// trocarAtalho liga/desliga o atalho global. Grava a chave ANTES de
// aplicar: no Linux quem registra é outro processo, e ele relê o arquivo
// ao ser avisado — avisar primeiro seria avisar sobre um estado que ainda
// não está no disco.
//
// Falha de gravação não mexe na caixa: marcar uma preferência que não
// sobreviveu ao próximo start é pior que não marcar nada.
func (d *dlgAjustes) trocarAtalho() {
	novo := !d.atalhoOn
	if err := salvarAtalhoGlobalLigado(d.ini.Text(), novo); err != nil {
		d.erro = err.Error()
		return
	}
	d.atalhoOn = novo
	d.erro = ""
	if aplicarAtalhoGlobal != nil {
		aplicarAtalhoGlobal(novo)
	}
}

// trocarAutostart liga/desliga a subida do serviço no login.
//
// A ordem é a INVERSA da de trocarAtalho, de propósito. Lá a chave manda,
// e quem registra relê o arquivo. Aqui quem manda é o portal: a chave só
// guarda a resposta para não repetir a pergunta a cada login. Gravar sem
// falar com o sistema deixaria a caixa desmarcada e o serviço subindo
// assim mesmo — mentira silenciosa, do tipo que só aparece no próximo
// login. Então pergunta primeiro, grava o que o sistema responder.
//
// E responde em goroutine porque o portal abre um diálogo e espera a
// pessoa ler (prazo de minutos, ver definirAutostart): no laço de quadro
// isso é a janela inteira congelada. Mesmo desenho do procurar().
func (d *dlgAjustes) trocarAutostart() {
	d.mu.Lock()
	ja := d.autoIndo
	d.autoIndo = true
	d.mu.Unlock()
	if ja {
		return
	}
	quer := !d.autostartOn
	// O caminho é lido AQUI, no laço de quadro: tocar no widget.Editor de
	// outra goroutine não é seguro (ver o comentário da struct).
	caminho := d.ini.Text()
	go func() {
		ok, err := definirAutostartNoSistema(quer)
		if err == nil {
			// Grava o que o sistema DECIDIU, não o que foi pedido: o
			// diálogo é do desktop, e a pessoa pode dizer não nele.
			err = salvarAutostart(caminho, ok)
		}
		d.mu.Lock()
		d.autoIndo = false
		if err != nil {
			d.pendErro = err.Error()
		} else {
			d.pendAuto = &ok
		}
		d.mu.Unlock()
		d.w.Invalidate()
	}()
}

// recuado alinha uma linha solta com o RÓTULO da caixa de marcar acima
// dela, e não com a caixinha: 15dp do quadrado mais 6dp do espaçador,
// ambos de caixaMarcar. Sem isso o aviso fica pendurado na margem e não
// se liga à caixa a que se refere.
func recuado(dentro layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(21)}.Layout(gtx, dentro)
	}
}

// aplicar troca o arquivo em uso. Não regrava nada: só passa a ler de
// outro lugar, e o cofre volta a ficar trancado — a senha mestra de um
// arquivo não vale para outro.
func (d *dlgAjustes) aplicar() {
	novo := d.ini.Text()
	if _, err := os.Stat(novo); err != nil {
		d.erro = err.Error()
		return
	}
	if trocarArquivoINI == nil {
		d.erro = "troca de arquivo indisponível nesta sessão"
		return
	}
	if err := trocarArquivoINI(novo); err != nil {
		d.erro = err.Error()
		return
	}
	d.erro = ""
	d.aviso = fmt.Sprintf("lendo de %s", novo)
	d.w.Invalidate()
}

// procurar abre o diálogo nativo do sistema (portal do xdg-desktop no
// Linux, comdlg32 no Windows) para escolher o conexoes.ini navegando em
// vez de copiar e colar o caminho. É bloqueante — daí rodar em goroutine
// própria, sinalizada por procurando para não abrir dois de uma vez.
func (d *dlgAjustes) procurar() {
	defer func() {
		d.mu.Lock()
		d.procurando = false
		d.mu.Unlock()
		d.w.Invalidate()
	}()

	f, err := explorerAcessos.ChooseFile(".ini")
	if err != nil {
		if !errors.Is(err, explorer.ErrUserDecline) {
			d.mu.Lock()
			d.pendErro = err.Error()
			d.mu.Unlock()
		}
		return
	}
	defer f.Close()

	osf, ok := f.(*os.File)
	if !ok {
		return
	}
	d.mu.Lock()
	d.pendCaminho, d.pendPronto = osf.Name(), true
	d.mu.Unlock()
}

// trocarArquivoINI é preenchido no main: recarrega o painel a partir de
// outro arquivo.
var trocarArquivoINI func(string) error

// aplicarAtalhoGlobal é preenchido no main: faz o novo estado valer
// AGORA, sem esperar o próximo start. Quem registra o atalho muda com a
// plataforma — no Linux é o processo -servico (e o main só avisa pelo
// socket), no Windows é o próprio app —, e é por isso que isto é um
// gancho em vez de uma chamada direta daqui.
var aplicarAtalhoGlobal func(bool)

func ondeEstaOCofre() string {
	if chaveiroAtual != nil && len(chaveiroAtual.Cofre) > 0 {
		return "chaveiro.ini"
	}
	return "conexoes.ini (formato antigo)"
}

func nomeDoTema() string {
	if tema.Fundo == temaEscuro.Fundo {
		return "escuro"
	}
	return "claro"
}
