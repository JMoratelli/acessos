//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// No Windows o executável é compilado com -H=windowsgui, senão um console
// preto abre atrás da janela. O preço é que NÃO existe stdout nem stderr:
// tudo o que o app (e a libfreerdp) escreveria some. Sem isso, um erro de
// conexão vira "fica tentando e não conecta", sem nenhuma pista — que foi
// exatamente o que aconteceu com o RDP no primeiro teste em Windows.
//
// Então o log vai para arquivo, ao lado do conexoes.ini (sempre gravável,
// e é onde quem usa já sabe procurar), guardando a execução anterior em
// log.anterior.txt — o problema costuma ser notado depois de reabrir o app.
// É o mesmo esquema do porte Windows da versão Python.
func iniciarLog(dir string) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	caminho := filepath.Join(dir, "log.txt")
	_ = os.Remove(filepath.Join(dir, "log.anterior.txt"))
	_ = os.Rename(caminho, filepath.Join(dir, "log.anterior.txt"))

	f, err := os.Create(caminho)
	if err != nil {
		return
	}
	// Reatribuir os.Stdout/os.Stderr resolve o lado Go. O lado C (a
	// libfreerdp escreve pelo runtime C, não pelo Go) só acompanha se o
	// HANDLE padrão do processo também mudar — daí o SetStdHandle.
	os.Stdout, os.Stderr = f, f
	h := windows.Handle(f.Fd())
	_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, h)
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, h)

	// A libfreerdp tem log próprio (WLog), configurado por variáveis de
	// ambiente lidas na primeira vez que ela registra algo — por isso vem
	// ANTES de qualquer conexão. Em arquivo separado para não embaralhar
	// o rastro do app com o do protocolo.
	os.Setenv("WLOG_APPENDER", "FILE")
	os.Setenv("WLOG_FILEAPPENDER_OUTPUT_FILE_PATH", dir)
	os.Setenv("WLOG_FILEAPPENDER_OUTPUT_FILE_NAME", "freerdp.log")
	// INFO dá o essencial (negociação de segurança, canais, motivo da
	// queda) sem encher o disco. Quem já definiu WLOG_LEVEL por fora manda
	// — é assim que se pede um DEBUG para diagnosticar sem recompilar:
	//     set WLOG_LEVEL=DEBUG && acessos.exe
	if os.Getenv("WLOG_LEVEL") == "" {
		os.Setenv("WLOG_LEVEL", "INFO")
	}
	fmt.Fprintf(os.Stderr, "Acessos %s — log desta execução\n", versaoInstalada())
}
