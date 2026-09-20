package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"acessos-go/internal/conexoes"
	"acessos-go/internal/massa/executor"
	"acessos-go/internal/massa/model"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Detectar plataforma lê o BANNER do SSH (RFC 4253 §4.2): o servidor se
// identifica antes de qualquer negociação, então dá para saber se a
// máquina é Windows ou Linux e qual a versão do OpenSSH sem mandar uma
// única credencial. É seguro rodar no parque inteiro.
//
// O resultado vira `windows = 0/1` no .ini — e quando não dá para afirmar,
// NÃO grava palpite: o massa prefere não saber a saber errado, porque
// mandar comando de shell para um PowerShell (ou o contrário) estraga.
type dlgPlataforma struct {
	w       *app.Window
	alvos   []conexoes.Conexao
	lista   widget.List
	btnFech widget.Clickable
	btnIr   widget.Clickable

	mu        sync.Mutex
	linhas    []linhaSonda
	rodando   bool
	feitos    int
	recarrega func()
}

type linhaSonda struct {
	nome, host, banner string
	plataforma         model.Plataforma
	erro               string
}

func detectarPlataforma(w *app.Window, alvos []conexoes.Conexao, recarrega func()) {
	d := &dlgPlataforma{w: w, alvos: alvos, recarrega: recarrega}
	d.lista.Axis = layout.Vertical
	abrirDialogo(d)
}

func (d *dlgPlataforma) Titulo() string   { return "Detectar plataforma" }
func (d *dlgPlataforma) Largura() unit.Dp { return 640 }

func (d *dlgPlataforma) rodar() {
	d.mu.Lock()
	if d.rodando {
		d.mu.Unlock()
		return
	}
	d.rodando, d.feitos, d.linhas = true, 0, nil
	d.mu.Unlock()

	go func() {
		// UMA cópia de histórico para a detecção inteira (ver
		// conexoes.Lote), e não uma por máquina: numa loja de 54 máquinas
		// eram 54 cópias de uma vez, e como a rotação guarda só as 20
		// últimas, uma detecção APAGAVA todo o histórico de edições de
		// verdade — que é justamente o que salva quem errou uma edição em
		// 274 conexões. A gravação continua sendo máquina a máquina, de
		// propósito: juntar tudo para escrever no fim faria fechar a
		// janela no meio perder o que já tinha sido detectado.
		var lote *conexoes.Lote
		if caminhoINI != "" {
			lote = conexoes.NovoLote(caminhoINI,
				fmt.Sprintf("detectou a plataforma de %d máquina(s)", len(d.alvos)))
		}

		// As sondas vão em paralelo, com teto. Cada uma espera até 4s pelo
		// banner, e em série uma loja com metade das máquinas desligada
		// levava MINUTOS — tempo em que a janela fica aberta sem nada
		// acontecer. O teto é baixo de propósito: são conexões TCP para o
		// parque inteiro, e abrir todas de uma vez é o tipo de coisa que
		// assusta firewall.
		//
		// Efeito colateral aceito: as linhas aparecem na ordem em que as
		// respostas CHEGAM, não na ordem da lista. Cada uma traz o nome e
		// o host, e as máquinas vivas responderem primeiro é melhor
		// retorno do que esperar a fila.
		const emParalelo = 8
		vaga := make(chan struct{}, emParalelo)
		var wg sync.WaitGroup
		for _, cx := range d.alvos {
			vaga <- struct{}{}
			wg.Add(1)
			go func(cx conexoes.Conexao) {
				defer wg.Done()
				defer func() { <-vaga }()

				s := executor.Sondar(cx.Host, 4*time.Second)
				l := linhaSonda{nome: cx.Nome, host: cx.Host, banner: s.Banner, plataforma: s.Plataforma}
				if s.Erro != nil {
					l.erro = s.FaseFalha()
				}
				// Só grava o que dá para afirmar.
				switch s.Plataforma {
				case model.Windows:
					gravarPlataforma(lote, cx.Nome, "1")
				case model.Linux:
					gravarPlataforma(lote, cx.Nome, "0")
				}
				d.mu.Lock()
				d.linhas = append(d.linhas, l)
				d.feitos++
				d.mu.Unlock()
				d.w.Invalidate()
			}(cx)
		}
		wg.Wait()

		d.mu.Lock()
		d.rodando = false
		d.mu.Unlock()
		if d.recarrega != nil {
			d.recarrega()
		}
		d.w.Invalidate()
	}()
}

// gravarPlataforma escreve o windows = 0/1 pelo lote, que é quem guarda a
// cópia do histórico uma vez só. Lote nil é o app ainda sem inventário
// escolhido: detectar continua valendo, só não grava.
func gravarPlataforma(lote *conexoes.Lote, nome, valor string) {
	if lote == nil {
		return
	}
	if err := lote.Salvar(nome, "", map[string]string{"windows": valor}); err != nil {
		fmt.Fprintf(os.Stderr, "gravar windows de %s: %v\n", nome, err)
	}
}

func (d *dlgPlataforma) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if d.btnFech.Clicked(gtx) {
		fecharDialogo()
	}
	if d.btnIr.Clicked(gtx) {
		d.rodar()
	}
	d.mu.Lock()
	linhas := append([]linhaSonda{}, d.linhas...)
	rodando, feitos := d.rodando, d.feitos
	d.mu.Unlock()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(rotulo(th, fonteMono, spSecundario,
			"Lê o banner do SSH. Nenhuma credencial é enviada.", tema.Sec)),
		espaco(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = gtx.Constraints.Max.Y / 2
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					superficie(gtx, gtx.Constraints.Min, tema.Vidro1, tema.Borda, 8)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				},
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(6).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.List(th, &d.lista).Layout(gtx, len(linhas), func(gtx layout.Context, i int) layout.Dimensions {
							l := linhas[i]
							return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return selinhoPlataforma(gtx, th, l.plataforma)
									}),
									layout.Rigid(layout.Spacer{Width: 6}.Layout),
									layout.Rigid(negrito(txt(th, fonteMono, spCorpo, l.nome, tema.Texto)).Layout),
									layout.Rigid(layout.Spacer{Width: 8}.Layout),
									layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
										texto, cor := l.banner, tema.Fraco
										if l.erro != "" {
											texto, cor = l.erro, tema.ErroFg
										}
										return rotuloLinha(th, fonteMono, spCardMeta, texto, cor)(gtx)
									}),
								)
							})
						})
					})
				},
			)
		}),
		espaco(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(rotulo(th, fonteMono, spSecundario,
					fmt.Sprintf("%d de %d", feitos, len(d.alvos)), tema.Sec)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botaoNeutro(gtx, th, &d.btnFech, "Fechar")
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return botao(gtx, th, &d.btnIr, "Detectar", pesoPrimario, rodando)
				}),
			)
		}),
	)
}
