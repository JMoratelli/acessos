//go:build windows

package telaproc

import (
	"syscall"
	"unsafe"
)

// As duas funções abaixo saem direto da kernel32 por LazyDLL: a versão de
// golang.org/x/sys/windows que este projeto vendoriza não expõe nenhuma
// das duas, e acrescentar dependência por duas chamadas não se paga.

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
	procProcessMemoryInfo  = kernel32.NewProc("K32GetProcessMemoryInfo")
)

// memoryStatusEx espelha MEMORYSTATUSEX do Win32.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// memoriaDisponivelMiB usa ullAvailPhys, o equivalente prático do
// MemAvailable do Linux: memória física que o sistema pode entregar agora.
func memoriaDisponivelMiB() (int, bool) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, _ := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, false
	}
	return int(m.AvailPhys / (1 << 20)), true
}

// processMemoryCounters espelha PROCESS_MEMORY_COUNTERS do Win32.
type processMemoryCounters struct {
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
}

// residenteMiB é o working set do PRÓPRIO processo — o análogo do RSS.
func residenteMiB() (int, bool) {
	var c processMemoryCounters
	c.CB = uint32(unsafe.Sizeof(c))
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	r, _, _ := procProcessMemoryInfo.Call(
		uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.CB))
	if r == 0 {
		return 0, false
	}
	return int(uint64(c.WorkingSetSize) / (1 << 20)), true
}
