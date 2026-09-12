// Teste manual: lê o conexoes.ini real e mostra o resumo da árvore.
package main

import (
	"flag"
	"fmt"
	"os"

	"acessos-go/internal/conexoes"
)

func main() {
	caminho := flag.String("ini", "", "caminho do conexoes.ini")
	flag.Parse()

	arq, err := conexoes.Carregar(*caminho)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%d conexões, geral=%v, cofre kdf=%q\n", len(arq.Conexoes), arq.Geral, arq.Cofre["kdf"])

	for _, g := range arq.Arvore() {
		fmt.Printf("%s (%d)\n", g.Nome, g.Total())
		for _, f := range g.Filhos {
			fmt.Printf("   %s (%d)\n", f.Nome, f.Total())
			for i, cx := range f.Conexoes {
				if i >= 2 {
					fmt.Printf("      ...\n")
					break
				}
				fmt.Printf("      %-16s %-16s vnc=%v ssh=%v rdp=%v\n", cx.Nome, cx.Host, cx.Tem(conexoes.VNC), cx.Tem(conexoes.SSH), cx.Tem(conexoes.RDP))
			}
		}
		for i, cx := range g.Conexoes {
			if i >= 2 {
				fmt.Printf("   ...\n")
				break
			}
			fmt.Printf("   %-16s %-16s vnc=%v ssh=%v rdp=%v\n", cx.Nome, cx.Host, cx.Tem(conexoes.VNC), cx.Tem(conexoes.SSH), cx.Tem(conexoes.RDP))
		}
	}
}
