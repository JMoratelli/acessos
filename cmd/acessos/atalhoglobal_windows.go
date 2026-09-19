//go:build windows

package main

// Atalho global no Windows: RegisterHotKey. Mais simples que no Linux —
// aqui é o app quem escolhe a tecla e o sistema garante que ela chega
// mesmo com outra janela em foco, sem portal, sem diálogo de confirmação
// e sem precisar do processo -servico (ver servico_outros.go e o item 3d
// do BACKLOG): não há sessão de portal para morrer junto com o processo,
// então o atalho é registrado direto dentro do app — ver a chamada em
// main.go, guardada por "cli == nil" (sem serviço à parte).
//
// RegisterHotKey só entrega WM_HOTKEY na fila de mensagens da MESMA
// thread do SO que chamou o registro — não em qualquer goroutine. Por
// isso a goroutine abaixo prende a própria thread com LockOSThread e
// nunca a devolve: é a casa inteira do atalho, do registro ao laço que
// espera a tecla.

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000 // segurar a tecla não repete o disparo

	wmHotkey = 0x0312

	idAtalho = 1 // único hotkey que este processo registra
)

const wmQuit = 0x0012

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
)

// msg espelha o bastante da MSG do Win32 (winuser.h) para GetMessageW
// escrever nela sem estourar memória — só Message e WParam são lidos.
type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
}

type AtalhoGlobal struct {
	Gatilho string
	// Caiu fecha quando o laço de mensagens sai — na prática, só por
	// Fechar: RegisterHotKey não tem sessão externa para morrer junto com
	// o processo, ao contrário do portal do Linux.
	Caiu chan struct{}

	// tid é a thread do SO que registrou o atalho e está parada no
	// GetMessageW. É por ela que Fechar acorda o laço: WM_HOTKEY e
	// WM_QUIT chegam na fila da thread, não do processo.
	tid      uint32
	fecharUm sync.Once
}

// Fechar solta o atalho, liberando a tecla para o resto do sistema.
// Idempotente. Posta WM_QUIT na thread do laço: GetMessageW devolve 0, o
// laço sai e o UnregisterHotKey adiado roda na MESMA thread que
// registrou — exigência do Win32, e a razão de não dar para simplesmente
// chamar UnregisterHotKey daqui.
func (a *AtalhoGlobal) Fechar() {
	if a == nil {
		return
	}
	a.fecharUm.Do(func() {
		if a.tid == 0 {
			return
		}
		procPostThreadMessageW.Call(uintptr(a.tid), wmQuit, 0, 0)
	})
}

func registrarAtalhoGlobal(id, descricao, gatilho string, ao func(token string)) (*AtalhoGlobal, error) {
	mods, vk, err := analisarGatilho(gatilho)
	if err != nil {
		return nil, fmt.Errorf("atalho %q: %w", gatilho, err)
	}

	pronto := make(chan error, 1)
	a := &AtalhoGlobal{Gatilho: gatilho, Caiu: make(chan struct{})}

	go func() {
		runtime.LockOSThread()
		// Antes de registrar: quem chamou só recebe o ponteiro depois do
		// <-pronto, então gravar aqui não corre com o Fechar.
		a.tid = windows.GetCurrentThreadId()

		ok, _, chamouErr := procRegisterHotKey.Call(0, idAtalho, uintptr(mods|modNoRepeat), uintptr(vk))
		if ok == 0 {
			pronto <- fmt.Errorf("RegisterHotKey: %w", chamouErr)
			return
		}
		defer procUnregisterHotKey.Call(0, idAtalho)
		pronto <- nil

		var m msg
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			// -1 é erro; 0 é WM_QUIT. Nenhum dos dois deveria acontecer
			// nesta thread (sem janela própria, sem motivo para sair) —
			// travar em vez de girar em erro é a resposta certa.
			if int(ret) <= 0 {
				close(a.Caiu)
				return
			}
			if m.Message == wmHotkey && m.WParam == idAtalho && ao != nil {
				ao("")
			}
		}
	}()

	if err := <-pronto; err != nil {
		return nil, err
	}
	return a, nil
}

// analisarGatilho lê "CTRL+SHIFT+F12" — o mesmo formato usado do lado
// Linux, onde vira preferred_trigger do portal — e devolve os
// fsModifiers e o virtual-key code que RegisterHotKey pede.
func analisarGatilho(gatilho string) (mods uint32, vk uint32, err error) {
	partes := strings.Split(gatilho, "+")
	if len(partes) < 2 {
		return 0, 0, fmt.Errorf("sem modificador: %q", gatilho)
	}
	for _, p := range partes[:len(partes)-1] {
		switch strings.ToUpper(strings.TrimSpace(p)) {
		case "CTRL", "CONTROL":
			mods |= modControl
		case "SHIFT":
			mods |= modShift
		case "ALT":
			mods |= modAlt
		case "WIN", "SUPER", "META":
			mods |= modWin
		default:
			return 0, 0, fmt.Errorf("modificador desconhecido: %q", p)
		}
	}
	tecla := strings.ToUpper(strings.TrimSpace(partes[len(partes)-1]))
	switch {
	case len(tecla) == 1 && ((tecla[0] >= 'A' && tecla[0] <= 'Z') || (tecla[0] >= '0' && tecla[0] <= '9')):
		vk = uint32(tecla[0])
	case strings.HasPrefix(tecla, "F"):
		n, convErr := strconv.Atoi(tecla[1:])
		if convErr != nil || n < 1 || n > 24 {
			return 0, 0, fmt.Errorf("tecla desconhecida: %q", tecla)
		}
		vk = uint32(windows.VK_F1) + uint32(n-1)
	default:
		return 0, 0, fmt.Errorf("tecla desconhecida: %q", tecla)
	}
	return mods, vk, nil
}
