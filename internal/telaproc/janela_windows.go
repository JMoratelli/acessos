//go:build windows

package telaproc

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// semJanela impede que o filho apareça. O binário já é compilado com
// -H=windowsgui, então ele não abriria console; CREATE_NO_WINDOW garante
// isso também quando alguém constrói sem essa flag (build de depuração),
// que é justamente quando um console fantasma por aba assustaria.
func semJanela(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
