//go:build windows && !race

package main

// memoriaDe, no Windows. Irmão de memoriaproc_posix_test.go.
//
// Sem esta metade o PACOTE INTEIRO deixava de compilar no Windows: o
// telaworker_filho_test.go é (linux || windows) e chama memoriaDe, que
// só existia no arquivo de teste ao vivo, marcado linux. Resultado —
// `go test ./cmd/acessos/` falhava no Windows com "undefined: memoriaDe"
// e NENHUM teste do app rodava lá. É a armadilha de arquivo irmão fora
// de sincronia que o CLAUDE.md descreve, e agora os dois lados existem.
//
// Sobre o nome sem sufixo _windows: ver memoriaproc_posix_test.go.

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// K32GetProcessMemoryInfo não está exposta pela versão de
// golang.org/x/sys/windows usada aqui — mesma situação (e mesma saída)
// do internal/telaproc/memoria_windows.go, que a chama para o próprio
// processo.
var procGetProcessMemoryInfoTeste = syscall.NewLazyDLL("kernel32.dll").
	NewProc("K32GetProcessMemoryInfo")

// processMemoryCountersEx espelha PROCESS_MEMORY_COUNTERS_EX do Win32:
// é a versão com PrivateUsage, que é o que corresponde à memória privada
// do smaps do Linux.
type processMemoryCountersEx struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

// memoriaDe devolve o mesmo mapa (em KiB) que a versão Linux, com as
// chaves que os testes leem:
//
//	Rss            working set — o que está em memória física agora
//	Private_Dirty  PrivateUsage — o que só este processo usa
//
// Pss NÃO existe no Windows: não há como saber a fatia proporcional do
// que é compartilhado com os outros filhos. Fica ausente (lê como 0) em
// vez de virar um número inventado — o custo marginal de uma sessão se
// mede pelo privado, que está aqui.
func memoriaDe(pid int) (map[string]int, error) {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("abrindo o processo %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	var c processMemoryCountersEx
	c.CB = uint32(unsafe.Sizeof(c))
	r, _, chamouErr := procGetProcessMemoryInfoTeste.Call(
		uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.CB))
	if r == 0 {
		return nil, fmt.Errorf("medindo o processo %d: %w", pid, chamouErr)
	}
	return map[string]int{
		"Rss":           int(uint64(c.WorkingSetSize) / 1024),
		"Private_Dirty": int(uint64(c.PrivateUsage) / 1024),
	}, nil
}
