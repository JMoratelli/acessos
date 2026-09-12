// Comando de teste manual: conecta a um servidor VNC, espera o primeiro
// framebuffer chegar e reporta o resultado. Não é a UI final — só valida
// o binding cgo contra libvncclient.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"acessos-go/internal/vnc"
)

func main() {
	host := flag.String("host", "", "endereço do servidor VNC")
	port := flag.Int("port", 5900, "porta")
	user := flag.String("user", "", "usuário (se o servidor exigir)")
	pass := flag.String("pass", "", "senha")
	flag.Parse()

	if *host == "" {
		fmt.Fprintln(os.Stderr, "uso: vnctest -host <ip> [-port 5900] [-user u] -pass <senha>")
		os.Exit(2)
	}

	sess := vnc.New()
	defer sess.Close()

	sess.SetCredentials(*user, *pass)

	sess.OnResize = func(w, h int) {
		fmt.Printf("framebuffer: %dx%d\n", w, h)
	}
	updates := 0
	sess.OnUpdate = func(x, y, w, h int) {
		updates++
	}

	fmt.Printf("conectando a %s:%d...\n", *host, *port)
	if err := sess.Connect(*host, *port); err != nil {
		ce := err.(*vnc.ConnectError)
		fmt.Fprintf(os.Stderr, "falha: %s (auth=%v precisa_usuario=%v recusado=%v)\n",
			ce.Message, ce.AuthFailed, ce.NeedsUsername, ce.Rejected)
		os.Exit(1)
	}
	fmt.Println("conectado.")

	stop := make(chan struct{})
	go func() {
		if err := sess.Run(stop); err != nil {
			fmt.Fprintf(os.Stderr, "sessão encerrada: %v\n", err)
		}
	}()

	dur := 20 * time.Second
	if v := os.Getenv("VNCTEST_DUR"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			dur = d
		}
	}
	time.Sleep(dur)
	close(stop)

	buf, w, h := sess.Framebuffer()
	fmt.Printf("recebido: framebuffer %dx%d (%d bytes), %d lotes de atualização\n",
		w, h, len(buf), updates)
}
