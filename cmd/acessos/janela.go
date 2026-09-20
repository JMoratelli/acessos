package main

// O que era global enquanto o app tinha UMA janela só.
//
// A sessão remota vai sair para janela própria, e aí cada uma dessas
// coisas passa a ter uma resposta por janela em vez de uma resposta para o
// processo. Este arquivo é onde elas moram agora.

import (
	"sync"
	"sync/atomic"

	"acessos-go/internal/grab"

	"gioui.org/app"
)

// estadoJanela junta o que pertence a UMA janela.
type estadoJanela struct {
	w *app.Window

	// grab é a captura Wayland desta janela: teclado, clipboard e
	// inibição dos atalhos do compositor. Cada janela tem a sua porque
	// cada uma tem a própria superfície Wayland — foi por causa disto que
	// internal/grab deixou de ser singleton.
	grab atomic.Pointer[grab.Handle]

	// inibicaoLigada memoriza o que já foi pedido à captura, para não
	// refazer o pedido a cada quadro. Só o laço de quadro desta janela
	// mexe nele, daí não precisar ser atômico.
	inibicaoLigada bool
}

func novoEstadoJanela(w *app.Window) *estadoJanela { return &estadoJanela{w: w} }

// atualizarInibicao liga a captura dos atalhos do compositor só enquanto
// uma TELA remota está em foco NESTA janela. O SSH recebe o teclado cru
// (precisa de Ctrl+C, setas, Tab) mas NÃO inibe: num terminal ninguém
// espera perder o Alt+Tab do próprio desktop.
func (e *estadoJanela) atualizarInibicao(t Tab) {
	quer := false
	if _, ok := t.(telaRemota); ok {
		quer = true
	}
	if temDialogo() || focoEmCampo.Load() {
		// Diálogo aberto ou campo de texto em foco devolve os atalhos ao
		// compositor: o foco está aqui, não na máquina remota.
		//
		// RESSALVA CONHECIDA: focoEmCampo ainda é do processo, não desta
		// janela — os três helpers que o marcam (dashtab, modal, massatab)
		// desenham sem saber em que janela estão. Enquanto a janela
		// destacada não tiver campo de texto nenhum, isso não muda
		// resultado. Quando tiver, o erro cai para o lado SEGURO: sobra
		// inibição desligada, ou seja, o compositor fica com os atalhos
		// dele e algumas teclas não chegam na máquina remota — chato e
		// visível. O contrário (inibir enquanto alguém digita num campo) é
		// que seria o defeito de verdade, e esse não acontece.
		quer = false
	}
	if quer == e.inibicaoLigada {
		return
	}
	e.inibicaoLigada = quer
	e.grab.Load().Inibir(quer)
}

// ---------------------------------------------------------------- abas

// abasAtivas guarda a aba à vista DE CADA JANELA.
//
// Era um ponteiro só, e cabia: com uma janela, "a aba ativa" é uma. Com a
// sessão remota saindo para janela própria passam a existir várias abas à
// vista ao mesmo tempo — uma por janela —, e a pergunta que o código faz
// ("o operador está olhando para esta aba?") só tem resposta certa se cada
// janela responder pela sua.
//
// Serve para duas coisas:
//
//   - sessão em segundo plano não disputar o clipboard do SISTEMA. VNC e
//     RDP mandam o clipboard do servidor assim que o canal abre, e sem
//     este filtro a última aba a conectar roubava o que o operador tinha
//     acabado de copiar;
//   - não redesenhar uma janela por causa de aba que ninguém olha (ver
//     invalidar.go).
//
// A ARBITRAGEM DO CLIPBOARD FICA EM ABERTO para quando a janela destacada
// existir: com duas janelas à vista, duas abas são "ativas" ao mesmo tempo
// e as duas publicariam. O critério certo passa a ser qual janela tem o
// foco do sistema, não qual aba está à frente dentro dela.
var abasAtivas struct {
	mu  sync.Mutex
	por map[*app.Window]Tab
}

// marcarAbaAtiva é chamado no fim de cada quadro, com a aba que acabou de
// ser DESENHADA — que é a definição certa de "aba à vista".
func marcarAbaAtiva(w *app.Window, t Tab) {
	abasAtivas.mu.Lock()
	defer abasAtivas.mu.Unlock()
	if abasAtivas.por == nil {
		abasAtivas.por = map[*app.Window]Tab{}
	}
	abasAtivas.por[w] = t
}

// esquecerJanela apaga a marca ao fechar a janela. Sem isto o mapa
// guardaria janelas mortas, e uma aba que morreu junto continuaria
// respondendo "sim, estou à vista" para sempre.
func esquecerJanela(w *app.Window) {
	abasAtivas.mu.Lock()
	defer abasAtivas.mu.Unlock()
	delete(abasAtivas.por, w)
}

// ehAbaAtiva diz se t é a aba à vista de ALGUMA janela.
func ehAbaAtiva(t Tab) bool {
	abasAtivas.mu.Lock()
	defer abasAtivas.mu.Unlock()
	for _, a := range abasAtivas.por {
		if a == t {
			return true
		}
	}
	return false
}

// abaAtivaDe devolve a aba à vista DESTA janela, ou nil antes do primeiro
// quadro dela. Usado de goroutine solta (o callback de clipboard do grab),
// onde ler a barra de abas seria corrida de dados limpa.
func abaAtivaDe(w *app.Window) Tab {
	abasAtivas.mu.Lock()
	defer abasAtivas.mu.Unlock()
	return abasAtivas.por[w]
}

// nenhumaAbaMarcada é o arranque: antes do primeiro quadro ninguém foi
// marcado, e aí todo pedido de redesenho tem de passar, sob pena de a
// primeira imagem nunca aparecer.
func nenhumaAbaMarcada() bool {
	abasAtivas.mu.Lock()
	defer abasAtivas.mu.Unlock()
	return len(abasAtivas.por) == 0
}
