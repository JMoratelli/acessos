//go:build !linux && !windows

package telaproc

// Sem jeito portátil de perguntar isso aqui. Devolver false faz as duas
// proteções FALHAREM ABERTAS (ver reservar e VigiarMemoria): é de
// propósito — barrar ou matar sessão numa plataforma onde nem sabemos
// medir seria transformar uma proteção em impedimento.
func memoriaDisponivelMiB() (int, bool) { return 0, false }
func residenteMiB() (int, bool)         { return 0, false }
