package executor

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

var reTeste = regexp.MustCompile(`FIM`)

func TestLeitorCasaNoBuffer(t *testing.T) {
	l := novoLeitor()
	l.buf = []byte("antes FIM depois")

	antes, casado, err := l.lerAte(reTeste, time.Second, 0)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if antes != "antes " || casado != "FIM" {
		t.Fatalf("antes=%q casado=%q", antes, casado)
	}
	if string(l.buf) != " depois" {
		t.Fatalf("resto nao guardado: %q", l.buf)
	}
}

func TestLeitorCasaViaBombear(t *testing.T) {
	l := novoLeitor()
	go l.bombear(strings.NewReader("saida do comando FIM sobrou"))

	antes, casado, err := l.lerAte(reTeste, 2*time.Second, 0)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if antes != "saida do comando " || casado != "FIM" {
		t.Fatalf("antes=%q casado=%q", antes, casado)
	}
}

// TestLeitorSessaoCaiSemMarcador reproduz o bug encontrado no
// WindowsExec: o pipe do processo remoto morre (EOF) antes do marcador
// aparecer. lerAte tem que devolver na hora, sem esperar o timeout
// absoluto — era exatamente essa deteccao que faltava no bombear() do
// Windows antes da extracao deste tipo.
func TestLeitorSessaoCaiSemMarcador(t *testing.T) {
	l := novoLeitor()
	go l.bombear(strings.NewReader("saida parcial sem marcador"))

	inicio := time.Now()
	_, _, err := l.lerAte(reTeste, 30*time.Minute, 0)
	decorrido := time.Since(inicio)

	if err == nil {
		t.Fatal("esperava erro de sessao encerrada, veio nil")
	}
	if !errors.Is(err, ErrConexao) {
		t.Fatalf("erro nao encapsula ErrConexao: %v", err)
	}
	if decorrido > 5*time.Second {
		t.Fatalf("lerAte demorou %s para detectar EOF — deveria ser quase instantaneo, "+
			"nao esperar o timeout de %s", decorrido, 30*time.Minute)
	}
}

// TestLeitorDoisBombearNaoPanica cobre o caso do Windows: stdout e
// stderr sao dois pipes, dois bombear() escrevendo no mesmo leitor. Um
// morrer antes do outro nao pode derrubar o irmao com send-on-closed-
// channel (motivo de bombear() nunca fechar l.dados).
func TestLeitorDoisBombearNaoPanica(t *testing.T) {
	l := novoLeitor()
	rStdout, wStdout := io.Pipe()
	rStderr, wStderr := io.Pipe()
	go l.bombear(rStdout)
	go l.bombear(rStderr)

	// stderr morre primeiro.
	wStderr.Close()
	time.Sleep(50 * time.Millisecond)

	// stdout ainda manda dado depois disso — nao pode panicar.
	if _, err := wStdout.Write([]byte("ainda vivo FIM")); err != nil {
		t.Fatalf("escrita em stdout falhou: %v", err)
	}
	wStdout.Close()

	_, casado, err := l.lerAte(reTeste, 2*time.Second, 0)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if casado != "FIM" {
		t.Fatalf("casado=%q", casado)
	}
}

func TestLeitorTimeoutAbsoluto(t *testing.T) {
	l := novoLeitor()
	_, _, err := l.lerAte(reTeste, 20*time.Millisecond, 0)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("esperava ErrTimeout, veio %v", err)
	}
}

func TestLeitorTimeoutIdle(t *testing.T) {
	l := novoLeitor()
	_, _, err := l.lerAte(reTeste, time.Second, 20*time.Millisecond)
	if !errors.Is(err, ErrIdle) {
		t.Fatalf("esperava ErrIdle, veio %v", err)
	}
}
