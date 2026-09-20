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

// janelaPrincipal é a janela do app. Preenchida no arranque.
//
// Existe para o que NÃO pode ser desenhado numa janela de sessão: o modal
// é global e só a janela principal o desenha, então um diálogo pedido por
// uma sessão destacada tem de nascer apontando para cá — senão ele ficaria
// invisível e a sessão travaria esperando uma resposta que ninguém vê.
var janelaPrincipal *app.Window

// estadoJanela junta o que pertence a UMA janela.
type estadoJanela struct {
	w *app.Window

	// principal distingue a janela do app das janelas de sessão.
	//
	// Os atalhos do PRÓPRIO app que chegam pela captura (F12 recolhe a
	// lateral, Ctrl+W fecha aba, Ctrl+G régua) só fazem sentido nela: numa
	// janela de sessão eles mexeriam no estado da outra janela, e — pior —
	// engoliriam a tecla antes de ela chegar na máquina remota. F12 é
	// tecla de trabalho dentro de uma sessão Windows.
	principal bool

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

func novoEstadoJanela(w *app.Window) *estadoJanela {
	return &estadoJanela{w: w, principal: true}
}

// novoEstadoJanelaSessao é o estado de uma janela destacada: mesma coisa,
// menos os atalhos do app. Ver o campo principal.
func novoEstadoJanelaSessao(w *app.Window) *estadoJanela {
	return &estadoJanela{w: w}
}

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
	// Sem captura ainda não há o que pedir, e ANOTAR aqui seria pior que
	// não fazer nada: o quer==inibicaoLigada abaixo passaria a devolver
	// cedo para sempre, e a captura de verdade — criada no primeiro
	// WaylandViewEvent, que pode chegar DEPOIS do primeiro quadro — nunca
	// receberia o pedido. Na janela destacada isso é a sessão em tela
	// cheia sem inibição nenhuma, calada.
	g := e.grab.Load()
	if g == nil {
		return
	}
	if quer == e.inibicaoLigada {
		return
	}
	e.inibicaoLigada = quer
	g.Inibir(quer)
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

// ---------------------------------------------------------- destacadas

// abasDestacadas são as sessões que saíram da tira e vivem em janela
// própria.
//
// Existe por causa da SAÍDA do app: o encerramento percorre bar.tabs
// chamando Close(), e uma aba destacada não está mais lá. Sem este
// registro ela nunca receberia Close, o stop dela nunca fecharia, e o
// processo-filho que hospeda a sessão ficaria órfão — no Linux ele acaba
// morrendo com o socket, mas no Windows não há essa garantia.
var abasDestacadas struct {
	mu  sync.Mutex
	set map[Tab]bool
}

func marcarDestacada(t Tab) {
	abasDestacadas.mu.Lock()
	defer abasDestacadas.mu.Unlock()
	if abasDestacadas.set == nil {
		abasDestacadas.set = map[Tab]bool{}
	}
	abasDestacadas.set[t] = true
}

func desmarcarDestacada(t Tab) {
	abasDestacadas.mu.Lock()
	defer abasDestacadas.mu.Unlock()
	delete(abasDestacadas.set, t)
}

// abasForaDaTira devolve as destacadas, para o encerramento fechá-las
// junto com as que estão na tira.
func abasForaDaTira() []Tab {
	abasDestacadas.mu.Lock()
	defer abasDestacadas.mu.Unlock()
	fora := make([]Tab, 0, len(abasDestacadas.set))
	for t := range abasDestacadas.set {
		fora = append(fora, t)
	}
	return fora
}
