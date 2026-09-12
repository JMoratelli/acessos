// Comando de teste manual: conecta via SFTP sobre SSH (x/crypto/ssh +
// pkg/sftp, Go puro), lista o diretório home remoto e faz um upload/
// download/remoção de arquivo de teste, validando o motor de ponta a ponta.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func main() {
	host := flag.String("host", "", "endereço do servidor SSH/SFTP")
	port := flag.Int("port", 22, "porta")
	user := flag.String("user", "", "usuário")
	pass := flag.String("pass", "", "senha")
	flag.Parse()

	if *host == "" || *user == "" {
		fmt.Fprintln(os.Stderr, "uso: sftptest -host <ip> [-port 22] -user <u> -pass <senha>")
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
		fmt.Fprintf(os.Stderr, "falha ssh: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client, err := sftp.NewClient(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha sftp: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	fmt.Println("conectado (sftp).")

	wd, err := client.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd falhou: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("diretório remoto: %s\n", wd)

	entries, err := client.ReadDir(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "readdir falhou: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%d itens:\n", len(entries))
	for _, e := range entries {
		tipo := "arquivo"
		if e.IsDir() {
			tipo = "dir"
		}
		fmt.Printf("  %-6s %8d  %s\n", tipo, e.Size(), e.Name())
	}

	// round-trip: cria, escreve, lê de volta e remove um arquivo de teste.
	testPath := wd + "/.acessos-go-sftptest"
	conteudo := []byte("round-trip ok\n")

	f, err := client.Create(testPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create falhou: %v\n", err)
		os.Exit(1)
	}
	if _, err := f.Write(conteudo); err != nil {
		fmt.Fprintf(os.Stderr, "write falhou: %v\n", err)
		os.Exit(1)
	}
	f.Close()

	f2, err := client.Open(testPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open (leitura) falhou: %v\n", err)
		os.Exit(1)
	}
	var lido bytes.Buffer
	if _, err := lido.ReadFrom(f2); err != nil {
		fmt.Fprintf(os.Stderr, "read falhou: %v\n", err)
		os.Exit(1)
	}
	f2.Close()

	if err := client.Remove(testPath); err != nil {
		fmt.Fprintf(os.Stderr, "remove falhou: %v\n", err)
		os.Exit(1)
	}

	if lido.String() != string(conteudo) {
		fmt.Fprintf(os.Stderr, "round-trip divergiu: escrito %q lido %q\n", conteudo, lido.String())
		os.Exit(1)
	}
	fmt.Println("round-trip upload/download/remove: ok")
}
