package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// splash é o cartão central mostrado no lugar da tela remota (RDP/VNC),
// do terminal (SSH) ou do painel remoto (SFTP) enquanto não há sessão
// viva — a mesma peça para as quatro abas de protocolo, para "atender
// todas as telas com os passos de conexão" em vez de cada uma inventar o
// próprio jeito de dizer "conectando".
//
// Inspirado no Termius: uma lista de passos que vai se marcando conforme
// a conexão avança, e um botão de ação que aparece assim que a espera já
// foi longa demais ou a tentativa terminou em erro — em vez de deixar
// girando pra sempre sem dar ao operador nenhum jeito de agir.
type splash struct {
	w *app.Window

	mu        sync.Mutex
	visivel   bool // false só enquanto a sessão está viva (ver concluir)
	passos    []string
	atual     int       // índice do passo em andamento (ou último alcançado)
	desde     time.Time // quando o passo ATUAL começou
	erro      string    // por que a última tentativa parou; "" = nenhuma
	proximaEm time.Time // reconexão automática agendada; zero = nenhuma

	tickando atomic.Bool
}

// Todos os métodos abaixo (e desenharSplash) toleram s == nil como "sem
// splash nenhum, não desenha nada": testes que montam uma aba com um
// literal de struct em vez do construtor (ver sftpDeTeste em
// sftpsel_test.go) nunca chamam novoSplash.

func novoSplash(w *app.Window) *splash { return &splash{w: w} }

// iniciar reseta os PASSOS para o começo de uma tentativa nova e garante
// que o cartão esteja visível. É seguro chamar de novo a cada tentativa,
// inclusive a primeira.
func (s *splash) iniciar(passos []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.visivel = true
	s.passos = passos
	s.atual = 0
	s.desde = time.Now()
	s.erro = ""
	s.proximaEm = time.Time{}
	s.mu.Unlock()
	s.w.Invalidate()
	s.animar()
}

// avancar marca o passo i como o atual. i pode pular o do meio: em RDP e
// VNC o caminho de sucesso não observa nada entre "pedi a conexão" e "o
// servidor confirmou".
func (s *splash) avancar(i int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if i > s.atual {
		s.atual = i
		s.desde = time.Now()
	}
	s.mu.Unlock()
	s.w.Invalidate()
}

// concluir esconde o cartão: a sessão está viva. Barato de chamar de
// novo (RDP/VNC chamam isto a cada quadro recebido, não só no primeiro).
func (s *splash) concluir() {
	if s == nil {
		return
	}
	s.mu.Lock()
	jaEscondido := !s.visivel
	s.visivel = false
	s.atual = len(s.passos)
	s.mu.Unlock()
	if !jaEscondido {
		s.w.Invalidate()
	}
}

// setErro registra por que a última tentativa parou — inclusive uma
// sessão que ESTAVA viva e caiu — e torna o cartão visível de novo.
//
// O "de novo" importa: concluir() já pode ter escondido o cartão (a
// sessão chegou a conectar). Sem reafirmar visivel aqui, uma sessão que
// caiu depois de conectada ficava com o cartão escondido bem no momento
// em que mais precisava aparecer — durante a espera de reconexão, que é
// justamente quando falhou()/aguardar() são chamados.
func (s *splash) setErro(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.visivel = true
	s.erro = msg
	s.mu.Unlock()
	s.w.Invalidate()
	s.animar()
}

// aguardar anota quando a reconexão automática vai acontecer, para o
// cartão mostrar a contagem regressiva — só faz sentido chamar isto no
// caminho de backoff automático; a espera por um clique manual não tem
// prazo, então não chama aguardar nenhum.
func (s *splash) aguardar(proximaEm time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.visivel = true
	s.proximaEm = proximaEm
	s.mu.Unlock()
	s.w.Invalidate()
	s.animar()
}

type splashFoto struct {
	passos    []string
	atual     int
	desde     time.Time
	erro      string
	proximaEm time.Time
	visivel   bool
}

func (s *splash) foto() splashFoto {
	if s == nil {
		return splashFoto{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return splashFoto{
		passos: s.passos, atual: s.atual, desde: s.desde,
		erro: s.erro, proximaEm: s.proximaEm,
		visivel: s.visivel,
	}
}

// animar mantém o quadro vivo enquanto o cartão precisar de algo que só
// o tempo muda: o "[*]" piscando do passo atual, o botão que aparece
// depois de travadoApos, e a contagem regressiva de reconexão. Sem isto
// o cartão parecia parado entre um evento de rede e o próximo, que podem
// ficar vários segundos em silêncio. No máximo uma goroutine de cada vez
// (mesma ideia do invalidador em invalidar.go).
func (s *splash) animar() {
	if !s.tickando.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.tickando.Store(false)
		tk := time.NewTicker(200 * time.Millisecond)
		defer tk.Stop()
		for range tk.C {
			if !s.foto().visivel {
				return
			}
			s.w.Invalidate()
		}
	}()
}

// travadoApos é quanto tempo um passo fica "em andamento" antes do
// cartão oferecer o botão de agir — mesmo sem erro nenhum ainda. Rede de
// PDV lenta existe; oito segundos dá tempo sem deixar a impressão de
// travado.
const travadoApos = 8 * time.Second

// desenharSplash desenha o cartão: emblema do protocolo com um halo
// pulsando, a trilha de progresso (bolinhas, não uma lista de texto) e
// só a legenda do passo ATUAL — o resto do histórico já está dito pela
// trilha, escrever tudo de novo em texto seria repetir a mesma
// informação duas vezes. Quando a espera já é longa ou uma tentativa
// termina, aparece o botão de agir agora.
func desenharSplash(gtx layout.Context, th *material.Theme, s *splash,
	titulo string, ic *widget.Icon, corAccent color.NRGBA,
	btn *widget.Clickable, agir func(),
) layout.Dimensions {
	f := s.foto()
	if !f.visivel {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	if btn.Clicked(gtx) && agir != nil {
		agir()
	}

	travado := f.erro != "" || time.Since(f.desde) > travadoApos
	agora := float64(time.Now().UnixNano()) / float64(time.Second)

	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		larg := gtx.Dp(240)
		if maxLarg := gtx.Constraints.Max.X - gtx.Dp(24); larg > maxLarg {
			larg = maxLarg
		}
		gtx.Constraints.Min.X = larg
		gtx.Constraints.Max.X = larg
		gtx.Constraints.Min.Y = 0

		macro := op.Record(gtx.Ops)
		dims := layout.UniformInset(20).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			var filhos []layout.FlexChild
			filhos = append(filhos,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return emblemaComHalo(gtx, ic, corAccent, agora)
					})
				}),
				espaco(14),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, negrito(txt(th, fonteMono, spCardHost, titulo, tema.Texto)).Layout)
				}),
			)

			if len(f.passos) > 1 {
				filhos = append(filhos, espaco(16),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return trilhaPassos(gtx, len(f.passos), f.atual, corAccent)
					}))
			}

			texto, cor := statusDoSplash(f)
			if texto != "" {
				filhos = append(filhos, espaco(12),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, rotulo(th, fonteMono, spCardMeta, texto, cor))
					}))
			}
			if !f.proximaEm.IsZero() {
				restante := time.Until(f.proximaEm).Round(time.Second)
				if restante < 0 {
					restante = 0
				}
				filhos = append(filhos, espaco(4),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, rotulo(th, fonteMono, spCardMeta,
							fmt.Sprintf("nova tentativa em %s", restante), tema.AtencaoFg))
					}))
			}
			if travado {
				rot := "Conectar agora"
				filhos = append(filhos, espaco(16),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return botaoSessao(gtx, th, btn, rot)
						})
					}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
		})
		conteudo := macro.Stop()
		sombra(gtx, dims.Size, 14)
		superficie(gtx, dims.Size, tema.Cartao, tema.Borda, 14)
		conteudo.Add(gtx.Ops)
		return dims
	})
}

// statusDoSplash resume o estado atual numa frase só: o erro, quando
// houver, vence — é a informação mais importante no instante em que
// existe. Sem erro, é só o nome do passo em andamento.
func statusDoSplash(f splashFoto) (string, color.NRGBA) {
	if f.erro != "" {
		return f.erro, tema.ErroFg
	}
	if f.atual >= 0 && f.atual < len(f.passos) {
		return f.passos[f.atual] + "…", tema.Sec
	}
	return "", tema.Sec
}

// emblemaComHalo é o "cartão de visita" da conexão: o ícone do protocolo
// (o mesmo do Selo() da aba) dentro de um círculo, com um halo atrás que
// respira de leve — o único traço "vivo" do cartão que não depende de
// olhar para a trilha de passos.
func emblemaComHalo(gtx layout.Context, ic *widget.Icon, cor color.NRGBA, agora float64) layout.Dimensions {
	const badgeDp, haloDp = 52, 88
	tam := gtx.Dp(haloDp)
	lado := gtx.Dp(badgeDp)

	return layout.Stack{Alignment: layout.Center}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			pulso := 0.08 + 0.07*(0.5+0.5*math.Sin(agora*2*math.Pi/2.2))
			halo := cor
			halo.A = uint8(255 * pulso)
			r := image.Rect(0, 0, tam, tam)
			paint.FillShape(gtx.Ops, halo, clip.Ellipse(r).Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(tam, tam)}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			fundo := cor
			fundo.A = 46
			r := image.Rect(0, 0, lado, lado)
			paint.FillShape(gtx.Ops, fundo, clip.Ellipse(r).Op(gtx.Ops))
			paint.FillShape(gtx.Ops, cor, clip.Stroke{Path: clip.Ellipse(r).Path(gtx.Ops), Width: 1.2}.Op())
			gtx.Constraints.Min = image.Pt(lado, lado)
			gtx.Constraints.Max = gtx.Constraints.Min
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return icone(gtx, ic, cor, 24)
			})
		}),
	)
}

// trilhaPassos é a barra de progresso com uma bolinha por passo — feito
// (preenchida), atual (preenchida, maior) e pendente (só o contorno) —
// no lugar da lista de nomes em texto: o nome de cada passo já concluído
// não importa mais depois que ele passou, só QUANTO falta.
func trilhaPassos(gtx layout.Context, n, atual int, corAtiva color.NRGBA) layout.Dimensions {
	larg := gtx.Constraints.Max.X
	altura := gtx.Dp(16)
	raioCheia := gtx.Dp(5)
	raioVazia := gtx.Dp(4)
	espessura := gtx.Dp(2)
	y := altura / 2

	bolinha := func(cx, raio int, cor color.NRGBA, contornoSo bool) {
		r := image.Rect(cx-raio, y-raio, cx+raio, y+raio)
		if contornoSo {
			paint.FillShape(gtx.Ops, tema.Cartao, clip.Ellipse(r).Op(gtx.Ops))
			paint.FillShape(gtx.Ops, cor, clip.Stroke{Path: clip.Ellipse(r).Path(gtx.Ops), Width: float32(gtx.Dp(1.3))}.Op())
			return
		}
		paint.FillShape(gtx.Ops, cor, clip.Ellipse(r).Op(gtx.Ops))
	}

	if n <= 1 {
		bolinha(larg/2, raioCheia, corAtiva, false)
		return layout.Dimensions{Size: image.Pt(larg, altura)}
	}

	passo := float64(larg-2*raioCheia) / float64(n-1)
	xDe := func(i int) int { return raioCheia + int(passo*float64(i)+0.5) }

	linha := func(x0, x1 int, cor color.NRGBA) {
		if x1 <= x0 {
			return
		}
		paint.FillShape(gtx.Ops, cor,
			clip.Rect{Min: image.Pt(x0, y-espessura/2), Max: image.Pt(x1, y+espessura/2)}.Op())
	}
	fimFeito := xDe(atual)
	if atual >= n-1 {
		fimFeito = larg - raioCheia
	}
	linha(raioCheia, larg-raioCheia, tema.Borda)
	linha(raioCheia, fimFeito, corAtiva)

	for i := 0; i < n; i++ {
		x := xDe(i)
		switch {
		case i < atual:
			bolinha(x, raioVazia, corAtiva, false)
		case i == atual:
			bolinha(x, raioCheia, corAtiva, false)
		default:
			bolinha(x, raioVazia, tema.Borda, true)
		}
	}
	return layout.Dimensions{Size: image.Pt(larg, altura)}
}
