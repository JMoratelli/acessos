package executor

import (
	"fmt"
	"io"
	"regexp"
	"time"
)

// leitor acumula a saida de um processo remoto (um ou mais pipes) num
// canal de bytes e espera por marcadores nela. Compartilhado entre
// LinuxSSH e WindowsExec: as duas sessoes faziam a MESMA leitura por
// marcador, cada uma com sua copia — so o numero de pipes bombeados
// muda (um so no Linux, que tem PTY e stderr misturado; stdout e
// stderr separados no Windows).
type leitor struct {
	dados chan []byte // bytes acumulados dos pipes
	buf   []byte      // resto nao consumido da ultima leitura
	fim   chan error  // primeiro erro (ou nil) de um pipe que fechou
}

func novoLeitor() *leitor {
	return &leitor{dados: make(chan []byte, 64), fim: make(chan error, 1)}
}

// bombear le de r e empilha em dados ate errar (o normal e EOF, quando o
// processo remoto sai). Sinaliza fim sem bloquear e NUNCA fecha dados:
// no Windows dois bombear (stdout e stderr) escrevem no mesmo leitor, e
// fechar o canal a partir de um deles derrubaria o outro com "send on
// closed channel" caso ainda tivesse bytes para entregar.
func (l *leitor) bombear(r io.Reader) {
	b := make([]byte, 32*1024)
	for {
		n, err := r.Read(b)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, b[:n])
			l.dados <- cp
		}
		if err != nil {
			select {
			case l.fim <- err:
			default:
			}
			return
		}
	}
}

// lerAte acumula saida ate o regex casar. Devolve o texto anterior ao
// casamento, o proprio trecho casado, e guarda o resto para a proxima
// leitura.
func (l *leitor) lerAte(re *regexp.Regexp, absoluto, idle time.Duration) (string, string, error) {
	var acc []byte
	acc = append(acc, l.buf...)
	l.buf = nil

	inicio := time.Now()
	ultimoByte := time.Time{}

	if loc := re.FindIndex(acc); loc != nil {
		l.buf = append([]byte{}, acc[loc[1]:]...)
		return string(acc[:loc[0]]), string(acc[loc[0]:loc[1]]), nil
	}

	var chAbs <-chan time.Time
	if absoluto > 0 {
		t := time.NewTimer(absoluto)
		defer t.Stop()
		chAbs = t.C
	}
	var tIdle *time.Timer
	var chIdle <-chan time.Time
	if idle > 0 {
		tIdle = time.NewTimer(idle)
		defer tIdle.Stop()
		chIdle = tIdle.C
	}

	for {
		select {
		case b := <-l.dados:
			acc = append(acc, b...)
			ultimoByte = time.Now()
			if tIdle != nil {
				if !tIdle.Stop() {
					select {
					case <-tIdle.C:
					default:
					}
				}
				tIdle.Reset(idle)
			}
			if loc := re.FindIndex(acc); loc != nil {
				l.buf = append([]byte{}, acc[loc[1]:]...)
				return string(acc[:loc[0]]), string(acc[loc[0]:loc[1]]), nil
			}
		case err := <-l.fim:
			// O adeus as vezes carrega o marcador (ou a mensagem de erro
			// do processo) no ultimo pedaco: esgota o que ja estava
			// bufferizado antes de desistir, em vez de arriscar uma
			// corrida entre este case e o "case b := <-l.dados" acima.
			acc = append(acc, drenar(l.dados)...)
			if loc := re.FindIndex(acc); loc != nil {
				l.buf = append([]byte{}, acc[loc[1]:]...)
				return string(acc[:loc[0]]), string(acc[loc[0]:loc[1]]), nil
			}
			msg := "sessao encerrada pelo host"
			if err != nil && err != io.EOF {
				msg += ": " + err.Error()
			}
			return string(acc), "", fmt.Errorf("%w: %s", ErrConexao, msg)
		case <-chAbs:
			return string(acc), "", fmt.Errorf("%w apos %s: %s",
				ErrTimeout, time.Since(inicio).Round(time.Second),
				diagnostico(len(acc), inicio, ultimoByte))
		case <-chIdle:
			return string(acc), "", fmt.Errorf("%w apos %s: %s",
				ErrIdle, time.Since(inicio).Round(time.Second),
				diagnostico(len(acc), inicio, ultimoByte))
		}
	}
}

// drenar esvazia o que ja estiver disponivel em ch sem bloquear.
func drenar(ch <-chan []byte) []byte {
	var acc []byte
	for {
		select {
		case b := <-ch:
			acc = append(acc, b...)
		default:
			return acc
		}
	}
}
