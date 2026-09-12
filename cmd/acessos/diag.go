package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Diagnóstico: as últimas linhas do que o app registrou, para olhar
// quando algo não funciona sem precisar rodar pelo terminal. Fica dentro
// dos Ajustes — o app original tem um painel por aba ligado por tecla,
// mas atalho global foi dispensado aqui.
//
// É um anel de tamanho fixo: log de sessão remota cresce sem parar, e
// segurar tudo em memória por horas não ajuda ninguém.
const maxLinhasDiag = 400

var (
	diagMu     sync.Mutex
	diagLinhas []string
)

// reg registra uma linha no diagnóstico E no stderr. O stderr continua
// porque é onde se olha quando o app nem abre.
func reg(formato string, args ...any) {
	linha := fmt.Sprintf(time.Now().Format("15:04:05")+"  "+formato, args...)
	diagMu.Lock()
	diagLinhas = append(diagLinhas, linha)
	if len(diagLinhas) > maxLinhasDiag {
		diagLinhas = diagLinhas[len(diagLinhas)-maxLinhasDiag:]
	}
	diagMu.Unlock()
	fmt.Fprintln(os.Stderr, linha)
}

func diagnostico() []string {
	diagMu.Lock()
	defer diagMu.Unlock()
	return append([]string{}, diagLinhas...)
}
