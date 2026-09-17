package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

// bufFechavel é um io.WriteCloser em memória, só para dar a t.entrada algo
// pra escrever sem precisar de sessão SSH nenhuma.
type bufFechavel struct{ bytes.Buffer }

func (bufFechavel) Close() error { return nil }

// TestModeBracketPasteAcompanhaCSI2004 cobre o patch local em
// third_party/vt10x (ver PATCH.md): o vt10x de cima do rio não sabia
// distinguir CSI ?2004h/l de qualquer outro modo desconhecido.
func TestModeBracketPasteAcompanhaCSI2004(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(80, 24))

	term.Write([]byte("\x1b[?2004h"))
	term.Lock()
	ligado := term.Mode()&vt10x.ModeBracketPaste != 0
	term.Unlock()
	if !ligado {
		t.Fatal("CSI ?2004h devia ligar ModeBracketPaste")
	}

	term.Write([]byte("\x1b[?2004l"))
	term.Lock()
	ligado = term.Mode()&vt10x.ModeBracketPaste != 0
	term.Unlock()
	if ligado {
		t.Fatal("CSI ?2004l devia desligar ModeBracketPaste")
	}
}

// TestColarSoEnvolveQuandoAppPediu é o comportamento que a marcação
// incondicional de antes não tinha: sem CSI ?2004h nenhum, colar manda o
// texto cru — com, manda envolvido em \x1b[200~/\x1b[201~.
func TestColarSoEnvolveQuandoAppPediu(t *testing.T) {
	registrarClipboardSistema("echo alo")
	defer registrarClipboardSistema("")

	nova := func() (*sshTab, *bufFechavel) {
		buf := &bufFechavel{}
		tab := &sshTab{entrada: buf}
		tab.term = vt10x.New(vt10x.WithSize(80, 24))
		return tab, buf
	}

	t.Run("sem_pedido", func(t *testing.T) {
		tab, buf := nova()
		tab.colarDoSistema()
		if got := buf.String(); got != "echo alo" {
			t.Fatalf("colou %q, esperado o texto cru sem marcação", got)
		}
	})

	t.Run("com_pedido", func(t *testing.T) {
		tab, buf := nova()
		tab.term.Write([]byte("\x1b[?2004h"))
		tab.colarDoSistema()
		got := buf.String()
		if !strings.HasPrefix(got, marcaColadoIni) || !strings.HasSuffix(got, marcaColadoFim) {
			t.Fatalf("colou %q, esperado envolvido em %q/%q", got, marcaColadoIni, marcaColadoFim)
		}
	})
}
