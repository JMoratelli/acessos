//go:build linux

package telaproc

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// memoriaDisponivelMiB lê MemAvailable do /proc/meminfo.
//
// MemAvailable, e não MemFree: o kernel calcula nele quanto dá para
// entregar a um processo novo SEM entrar em troca, já descontando o cache
// que ele consegue recuperar. MemFree num Linux saudável é quase sempre
// baixo (o cache ocupa o resto), e usá-lo faria a rede de segurança
// disparar com a máquina inteira livre.
func memoriaDisponivelMiB() (int, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		chave, resto, ok := strings.Cut(sc.Text(), ":")
		if !ok || chave != "MemAvailable" {
			continue
		}
		campos := strings.Fields(resto) // "12345 kB"
		if len(campos) == 0 {
			return 0, false
		}
		kb, err := strconv.Atoi(campos[0])
		if err != nil {
			return 0, false
		}
		return kb / 1024, true
	}
	return 0, false
}

// residenteMiB é o RSS do PRÓPRIO processo, de /proc/self/statm (o segundo
// campo é o número de páginas residentes). statm e não smaps_rollup: isto
// é lido de poucos em poucos segundos pelo vigia de memória, e statm é uma
// linha só, ordens de grandeza mais barata de produzir para o kernel.
func residenteMiB() (int, bool) {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	campos := strings.Fields(string(b))
	if len(campos) < 2 {
		return 0, false
	}
	paginas, err := strconv.Atoi(campos[1])
	if err != nil {
		return 0, false
	}
	return paginas * os.Getpagesize() / (1 << 20), true
}
