//go:build windows

package vida

import (
	"encoding/binary"
	"net"
	"syscall"
	"time"
	"unsafe"
)

// No Windows não existe o socket ICMP sem privilégio do Linux: a
// x/net/icmp tenta o caminho POSIX e morre de nil pointer. O equivalente
// nativo é o IcmpSendEcho do iphlpapi.dll, que faz ping sem exigir
// administrador — é o que o próprio ping.exe usa. Chamamos por
// syscall.LazyDLL para não arrastar cgo só por causa disto.
var (
	iphlpapi            = syscall.NewLazyDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
)

func pingICMP(host string, prazo time.Duration) bool {
	alvo, err := net.ResolveIPAddr("ip4", host)
	if err != nil || alvo.IP.To4() == nil {
		return false
	}
	h, _, _ := procIcmpCreateFile.Call()
	if h == 0 || h == uintptr(syscall.InvalidHandle) {
		return false
	}
	defer procIcmpCloseHandle.Call(h)

	// o endereço vai como IPv4 cru na ordem da rede, que num little-endian
	// é exatamente o uint32 lido em little-endian dos quatro octetos
	destino := uintptr(binary.LittleEndian.Uint32(alvo.IP.To4()))
	dados := []byte("acessos")
	// ICMP_ECHO_REPLY + os dados de volta; a margem cobre a variação de
	// alinhamento da struct entre versões do Windows.
	resposta := make([]byte, 256)

	n, _, _ := procIcmpSendEcho.Call(h, destino,
		uintptr(unsafe.Pointer(&dados[0])), uintptr(len(dados)),
		0, // sem opções de IP
		uintptr(unsafe.Pointer(&resposta[0])), uintptr(len(resposta)),
		uintptr(prazo/time.Millisecond))
	// devolve o número de respostas; 0 é "não respondeu no prazo"
	return n > 0
}
