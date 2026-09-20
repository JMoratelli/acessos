//go:build windows

// telacheia é verificação DE CAMPO da tela cheia do Windows, medida numa
// janela DE VERDADE do Gio — a mesma combinação de opções que a janela de
// sessão destacada usa (app.Decorated(false) + app.Fullscreen).
//
// Existe porque isto não aparece em `go test`: não há HWND, não há
// monitor e não há barra de tarefas. É o irmão Windows do cmd/fantasma
// (que mede decoração no Wayland) e do cmd/grabtest — mesma ideia,
// problema diferente, por isso ferramenta separada em vez de mais um
// flag num arquivo irmão.
//
// O QUE ELA PEGA. A tela cheia do Gio no Windows é "maximizada SEM
// WS_OVERLAPPEDWINDOW". A janela até nasce do tamanho do monitor inteiro,
// mas o WM_NCCALCSIZE do Gio grampeava o CLIENTE na ÁREA ÚTIL do monitor
// — que exclui a barra de tarefas. Resultado medido na v2.7.0, num
// monitor de 1920x1036 com barra de 40px: janela 1920x1036, cliente
// 1920x996, e a faixa da barra de tarefas sobrando na tela. Ver o patch
// em third_party/gio/PATCH.md.
//
// As três fases cobrem o conserto E o que ele não podia quebrar:
//
//	cheia       cliente tem de ser o monitor INTEIRO
//	maximizada  cliente tem de parar na ÁREA ÚTIL (senão a janela
//	            principal passaria a comer a barra de tarefas)
//	janela      volta a caber na área útil, e sem decoração do sistema
//
// Uso:
//
//	go run ./cmd/telacheia          mede e sai (código 1 se falhou)
//	go run ./cmd/telacheia -ver 3s  segura cada fase na tela para olhar
package main

import (
	"flag"
	"fmt"
	"image/color"
	"os"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")

	pGetClientRect     = user32.NewProc("GetClientRect")
	pClientToScreen    = user32.NewProc("ClientToScreen")
	pGetWindowRect     = user32.NewProc("GetWindowRect")
	pGetWindowLongW    = user32.NewProc("GetWindowLongW")
	pMonitorFromWindow = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW   = user32.NewProc("GetMonitorInfoW")
	pFindWindowW       = user32.NewProc("FindWindowW")
)

// gwlExStyle é variável, e não constante, porque constante negativa não
// converte para uintptr.
var gwlExStyle = int32(-20)

const (
	wsExTopmost             = 0x00000008
	monitorDefaultToNearest = 2
)

type retangulo struct{ Esq, Topo, Dir, Base int32 }

func (r retangulo) String() string {
	return fmt.Sprintf("(%d,%d)-(%d,%d) %dx%d",
		r.Esq, r.Topo, r.Dir, r.Base, r.Dir-r.Esq, r.Base-r.Topo)
}

func (r retangulo) cabeEm(o retangulo) bool {
	return r.Esq >= o.Esq && r.Topo >= o.Topo && r.Dir <= o.Dir && r.Base <= o.Base
}

type ponto struct{ X, Y int32 }

type infoMonitor struct {
	tamanho  uint32
	Monitor  retangulo
	AreaUtil retangulo
	Flags    uint32
}

// cliente devolve o retângulo do CLIENTE em coordenadas de tela — é o que
// o app de fato pinta, e portanto o que interessa medir.
func cliente(hwnd syscall.Handle) retangulo {
	var r retangulo
	pGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))
	tl := ponto{r.Esq, r.Topo}
	pClientToScreen.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&tl)))
	return retangulo{tl.X, tl.Y, tl.X + (r.Dir - r.Esq), tl.Y + (r.Base - r.Topo)}
}

func janelaRect(hwnd syscall.Handle) retangulo {
	var r retangulo
	pGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))
	return r
}

func monitorDe(hwnd syscall.Handle) infoMonitor {
	var mi infoMonitor
	mi.tamanho = uint32(unsafe.Sizeof(mi))
	h, _, _ := pMonitorFromWindow.Call(uintptr(hwnd), monitorDefaultToNearest)
	pGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
	return mi
}

// barraNoTopo diz se a barra de tarefas continua TOPMOST. O shell tira
// esse bit quando reconhece uma janela de tela cheia em primeiro plano.
// É informativo, não critério de falha: na medição da v2.7.0 o shell já
// cedia a tela — quem não a ocupava era o app.
func barraNoTopo() (bool, bool) {
	nome, err := syscall.UTF16PtrFromString("Shell_TrayWnd")
	if err != nil {
		return false, false
	}
	h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(nome)), 0)
	if h == 0 {
		return false, false
	}
	ex, _, _ := pGetWindowLongW.Call(h, uintptr(gwlExStyle))
	return uint32(ex)&wsExTopmost != 0, true
}

// fase é um estado da janela e o que se espera dele.
type fase struct {
	nome     string
	pedir    func(*app.Window)
	modo     app.WindowMode
	conferir func(mi infoMonitor, cli retangulo) string
}

var fases = []fase{
	{
		nome:  "cheia",
		pedir: nil, // a janela já nasce em tela cheia
		modo:  app.Fullscreen,
		conferir: func(mi infoMonitor, cli retangulo) string {
			if cli != mi.Monitor {
				return fmt.Sprintf("cliente %v, esperado o monitor inteiro %v", cli, mi.Monitor)
			}
			return ""
		},
	},
	{
		nome: "maximizada",
		pedir: func(w *app.Window) {
			w.Option(app.Maximized.Option(), app.Decorated(false))
		},
		modo: app.Maximized,
		conferir: func(mi infoMonitor, cli retangulo) string {
			if cli != mi.AreaUtil {
				return fmt.Sprintf("cliente %v, esperado a área útil %v "+
					"(maximizar não pode cobrir a barra de tarefas)", cli, mi.AreaUtil)
			}
			return ""
		},
	},
	{
		nome: "janela",
		pedir: func(w *app.Window) {
			w.Option(app.Windowed.Option(), app.Decorated(false))
		},
		modo: app.Windowed,
		conferir: func(mi infoMonitor, cli retangulo) string {
			if !cli.cabeEm(mi.AreaUtil) {
				return fmt.Sprintf("cliente %v não cabe na área útil %v", cli, mi.AreaUtil)
			}
			return ""
		},
	},
	{
		// O caminho do BOTÃO: quem saiu da tela cheia e voltou. É pelo
		// pedirModo (janelasessao.go) que ele passa, com a decoração
		// reafirmada junto — e é outro caminho que o da janela que já
		// nasce cheia, na primeira fase.
		nome: "cheia de novo",
		pedir: func(w *app.Window) {
			w.Option(app.Fullscreen.Option(), app.Decorated(false))
		},
		modo: app.Fullscreen,
		conferir: func(mi infoMonitor, cli retangulo) string {
			if cli != mi.Monitor {
				return fmt.Sprintf("cliente %v, esperado o monitor inteiro %v", cli, mi.Monitor)
			}
			return ""
		},
	},
}

// conferir roda as três fases e devolve as falhas. segurar > 0 deixa cada
// fase na tela por esse tempo, para conferir com o olho.
func conferir(segurar time.Duration) []string {
	var falhas []string
	reprovar := func(f string, a ...any) {
		m := fmt.Sprintf(f, a...)
		fmt.Println("  FALHOU:", m)
		falhas = append(falhas, m)
	}

	w := new(app.Window)
	w.Option(
		app.Title("telacheia"),
		// As mesmas opções da janela de sessão destacada
		// (cmd/acessos/janelasessao.go).
		app.Decorated(false),
		app.Size(unit.Dp(1024), unit.Dp(768)),
	)
	w.Option(app.Fullscreen.Option())

	var (
		ops      op.Ops
		hwnd     syscall.Handle
		atual    int
		desde    time.Time
		modoVis  app.WindowMode
		decorada bool
	)
	espera := 700*time.Millisecond + segurar
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			if e.Err != nil {
				reprovar("janela morreu: %v", e.Err)
			}
			return falhas

		case app.Win32ViewEvent:
			hwnd = syscall.Handle(e.HWND)

		case app.ConfigEvent:
			// O app lê o modo DAQUI para saber em que estado está (ver o
			// ConfigEvent em janelasessao.go): se ele mentir, o botão de
			// tela cheia passa a mostrar o ícone errado.
			modoVis = e.Config.Mode
			decorada = e.Config.Decorated

		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			// Pintar TUDO: pixel não pintado numa janela sem decoração
			// mostra a moldura do DWM (ver o cabeçalho de
			// cmd/acessos/buscapop.go).
			paint.Fill(gtx.Ops, color.NRGBA{R: 0x14, G: 0x16, B: 0x1a, A: 0xff})
			e.Frame(gtx.Ops)

			if desde.IsZero() {
				desde = time.Now()
			}
			// atual == len(fases) é o intervalo entre pedir o fechamento e
			// o DestroyEvent chegar: ainda vêm quadros, e sem esta guarda
			// o laço indexaria uma fase que não existe.
			if atual == len(fases) || hwnd == 0 || time.Since(desde) < espera {
				w.Invalidate()
				continue
			}

			f := fases[atual]
			mi := monitorDe(hwnd)
			cli := cliente(hwnd)
			topo, achou := barraNoTopo()
			fmt.Printf("\n[%s]\n", f.nome)
			fmt.Printf("  monitor   %v\n", mi.Monitor)
			fmt.Printf("  área útil %v\n", mi.AreaUtil)
			fmt.Printf("  janela    %v\n", janelaRect(hwnd))
			fmt.Printf("  cliente   %v\n", cli)
			fmt.Printf("  Config    Mode=%v Decorated=%v\n", modoVis, decorada)
			if achou {
				fmt.Printf("  barra de tarefas TOPMOST: %v\n", topo)
			}
			if m := f.conferir(mi, cli); m != "" {
				reprovar("%s: %s", f.nome, m)
			}
			if modoVis != f.modo {
				reprovar("%s: ConfigEvent diz Mode=%v", f.nome, modoVis)
			}
			if decorada {
				reprovar("%s: ConfigEvent diz Decorated=true — a barrinha do app "+
					"ficaria embaixo da moldura do sistema", f.nome)
			}

			atual++
			if atual == len(fases) {
				go func() { w.Perform(system.ActionClose) }()
				continue
			}
			desde = time.Now()
			if p := fases[atual].pedir; p != nil {
				// Fora do quadro, como o app faz (ver acaoPendente em
				// janelasessao.go): w.Option() de dentro do layout entra
				// em Configure → ShowWindow e a janela trava.
				go p(w)
			}
			w.Invalidate()
		}
	}
}

func main() {
	segurar := flag.Duration("ver", 0, "segura cada fase na tela por este tempo")
	flag.Parse()

	// Cão-de-guarda: uma janela sem moldura em tela cheia que trave deixa
	// a tela do operador presa. Aconteceu ao escrever esta ferramenta.
	time.AfterFunc(30*time.Second+3*(*segurar), func() {
		fmt.Println("\nTRAVOU: saindo pelo cão-de-guarda")
		os.Exit(2)
	})

	var falhas atomic.Int64
	go func() {
		fs := conferir(*segurar)
		falhas.Store(int64(len(fs)))
		if len(fs) == 0 {
			fmt.Println("\nRESULTADO: OK")
			os.Exit(0)
		}
		fmt.Printf("\nRESULTADO: %d FALHA(S)\n", len(fs))
		os.Exit(1)
	}()
	app.Main()
}
