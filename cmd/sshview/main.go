// sshview é um cliente SSH interativo de terminal: coloca o terminal local
// em modo raw, pede um PTY remoto do tamanho certo, e faz a ponte de bytes
// nos dois sentidos. Go puro (x/crypto/ssh) — sem cgo, sem shim C.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

func main() {
	host := flag.String("host", "", "endereço do servidor SSH")
	port := flag.Int("port", 22, "porta")
	user := flag.String("user", "", "usuário")
	pass := flag.String("pass", "", "senha")
	flag.Parse()

	if *host == "" || *user == "" {
		fmt.Fprintln(os.Stderr, "uso: sshview -host <ip> [-port 22] -user <u> -pass <senha>")
		os.Exit(2)
	}

	cfg := &ssh.ClientConfig{
		User:            *user,
		Auth:            []ssh.AuthMethod{ssh.Password(*pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // esqueleto: falta guardar/checar host key (ver TODO)
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("conectando a %s...\n", addr)
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	sess, err := conn.NewSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir sessão: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao colocar terminal em modo raw: %v\n", err)
		os.Exit(1)
	}
	defer term.Restore(fd, oldState)

	w, h, err := term.GetSize(fd)
	if err != nil {
		w, h = 80, 24
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty("xterm-256color", h, w, modes); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao pedir pty: %v\n", err)
		os.Exit(1)
	}

	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr

	// SIGWINCH: repassa o redimensionamento do terminal local pro remoto.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			if w, h, err := term.GetSize(fd); err == nil {
				sess.WindowChange(h, w)
			}
		}
	}()

	if err := sess.Shell(); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir shell: %v\n", err)
		os.Exit(1)
	}

	err = sess.Wait()
	term.Restore(fd, oldState)
	if err != nil && err != io.EOF {
		if _, ok := err.(*ssh.ExitError); !ok {
			fmt.Fprintf(os.Stderr, "sessão encerrada: %v\n", err)
		}
	}
}
