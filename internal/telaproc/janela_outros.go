//go:build !windows

package telaproc

import "os/exec"

// Fora do Windows não existe janela de console para esconder.
func semJanela(cmd *exec.Cmd) {}
