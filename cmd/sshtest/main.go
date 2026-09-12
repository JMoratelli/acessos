// Comando de teste manual: conecta via SSH (x/crypto/ssh, Go puro — sem
// cgo), roda um comando remoto simples e mostra a saída. Valida o motor
// antes de qualquer UI de terminal.
package main

import (
	"flag"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
)

func main() {
	host := flag.String("host", "", "endereço do servidor SSH")
	port := flag.Int("port", 22, "porta")
	user := flag.String("user", "", "usuário")
	pass := flag.String("pass", "", "senha")
	flag.Parse()

	if *host == "" || *user == "" {
		fmt.Fprintln(os.Stderr, "uso: sshtest -host <ip> [-port 22] -user <u> -pass <senha>")
		os.Exit(2)
	}

	cfg := &ssh.ClientConfig{
		User:            *user,
		Auth:            []ssh.AuthMethod{ssh.Password(*pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // esqueleto de teste; ver TODO de verificação de host key
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("conectando a %s...\n", addr)
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("conectado.")

	sess, err := conn.NewSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir sessão: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	out, err := sess.CombinedOutput("echo teste-ssh-ok && uname -a && whoami")
	if err != nil {
		fmt.Fprintf(os.Stderr, "comando falhou: %v\noutput: %s\n", err, out)
		os.Exit(1)
	}
	fmt.Printf("saída remota:\n%s", out)
}
