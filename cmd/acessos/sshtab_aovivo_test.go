//go:build (linux || windows) && !race

package main

// Teste contra um servidor SSH DE VERDADE — mesma convenção do
// telaworker_aovivo_test.go: desligado por padrão, credencial nunca mora
// aqui. Para rodar:
//
//	ACESSOS_SSH_AOVIVO=192.168.0.10 ACESSOS_SSH_USUARIO=fulano \
//	ACESSOS_SSH_SENHA=... go test ./cmd/acessos/ -run AoVivoSSH -v
//
// Bypassa o grab de teclado (Wayland) e o diálogo de host key de propósito:
// aqui o alvo é a tubulação HandleKey -> bytesDaTecla -> stdin -> PTY
// remoto -> vt10x, não a captura de hardware (que não dá pra simular sem
// um compositor e um teclado físico de verdade).
//
// Fora do -race pelo mesmo motivo do telaworker_aovivo_test.go: nada aqui
// usa handle de ponteiro cru, mas mantém a convenção do pacote.

import (
	"bufio"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"acessos-go/internal/massa/executor"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"github.com/hinshun/vt10x"
	"golang.org/x/crypto/ssh"
)

// sshAoVivo abre uma sessão de verdade e devolve um sshTab pronto para
// receber HandleKey/HandlePointer, sem janela nenhuma — mesmo espírito do
// TestSelecaoPorArrastoNoTerminal em sshsel_test.go.
func sshAoVivo(t *testing.T) *sshTab {
	t.Helper()
	host := os.Getenv("ACESSOS_SSH_AOVIVO")
	if host == "" {
		t.Skip("ACESSOS_SSH_AOVIVO não definido — teste ao vivo desligado")
	}
	porta := 22
	if v := os.Getenv("ACESSOS_SSH_PORTA"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			porta = n
		}
	}

	cfg := &ssh.ClientConfig{
		User:   os.Getenv("ACESSOS_SSH_USUARIO"),
		Auth:   []ssh.AuthMethod{ssh.Password(os.Getenv("ACESSOS_SSH_SENHA"))},
		Config: executor.AlgoritmosLegado(),
		// Teste: aceita a chave do servidor sem consultar known_hosts nem
		// abrir diálogo — internal/hostkey é coisa da UI, fora de escopo
		// aqui.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	cli, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, porta), cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { cli.Close() })

	sess, err := cli.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	modos := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", 24, 80, modos); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	entrada, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	saida, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}

	// Sem material.NewTheme()/shaperDoApp() de propósito: esses testes só
	// exercitam teclado (HandleKey) e sessão, nunca Layout — e o shaper
	// escaneia a fontconfig do sistema inteira (Noto CJK incluso), o que
	// sozinho já passa de um minuto nesta máquina.
	tab := &sshTab{cols: 80, rows: 24, corpo: 13, entrada: entrada, sess: sess, cli: cli}
	tab.term = vt10x.New(vt10x.WithSize(80, 24), vt10x.WithWriter(escritorEntrada{tab}), vt10x.WithScrollback(2000))

	if err := sess.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	go func() {
		br := bufio.NewReader(saida)
		for {
			if err := tab.term.Parse(br); err != nil {
				return
			}
		}
	}()

	// espera o prompt inicial: sem isto, o primeiro comando enviado corre
	// o risco de chegar antes do shell remoto estar pronto pra ler stdin.
	tela(t, tab, 15*time.Second, func(s string) bool { return strings.TrimSpace(s) != "" })
	return tab
}

// tela poll a tela do vt10x até `pronto` bater ou o prazo estourar — é o
// ÚNICO ponto que sabe esperar por conteúdo remoto; todo teste ao vivo
// deste arquivo usa esta função em vez de reinventar o próprio poll.
func tela(t *testing.T, tab *sshTab, prazo time.Duration, pronto func(string) bool) string {
	t.Helper()
	limite := time.Now().Add(prazo)
	var ultima string
	for time.Now().Before(limite) {
		// SEM Lock/Unlock: String() do vt10x tranca sozinho (ver o
		// comentário no fix de copiarSelecao em sshtab.go) — chamar Lock
		// aqui também deadlocava esta função inteira, sempre na primeira
		// volta.
		ultima = tab.term.String()
		if pronto(ultima) {
			return ultima
		}
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatalf("tela não chegou ao esperado em %s; última tela:\n%s", prazo, ultima)
	return ""
}

func contendo(sub string) func(string) bool {
	return func(s string) bool { return strings.Contains(s, sub) }
}

// linha simula digitar um comando de shell e apertar Enter, e só volta
// quando `marca` aparece na tela — reutilizado pelos dois testes abaixo em
// vez de cada um duplicar "manda bytes, espera eco".
func linha(t *testing.T, tab *sshTab, comando, marca string) string {
	t.Helper()
	tab.enviar([]byte(comando + "\r"))
	return tela(t, tab, 10*time.Second, contendo(marca))
}

// tecla despacha um keysym pelo MESMO caminho que o grab do Wayland usaria
// (HandleKey), então o que este teste prova sobre setas cobre exatamente a
// tradução + sessão — só não cobre a captura de hardware em si, que não dá
// pra simular daqui.
func tecla(tab *sshTab, keysym uint32) {
	tab.HandleKey(keysym, 0, true)
	tab.HandleKey(keysym, 0, false)
}

const (
	ksUp   = 0xff52
	ksDown = 0xff54
	ksEnd  = 0xff57
)

// TestAoVivoSSHHistoricoComSeta cobre o terceiro sintoma do item 3 do
// BACKLOG: "seta pra cima não traz o último comando". Se a seta virar
// bytes certos, quem recupera o histórico é o bash do outro lado — a
// sessão só precisa entregar \x1b[A.
func TestAoVivoSSHHistoricoComSeta(t *testing.T) {
	tab := sshAoVivo(t)

	marca := fmt.Sprintf("marca-historico-%d", time.Now().UnixNano())
	linha(t, tab, "echo "+marca, marca)

	// limpa a linha de comando atual (deveria estar vazia, mas não custa
	// garantir) e manda Up: se a seta virar \x1b[A, o readline do bash
	// repõe o "echo marca-..." na linha, ainda SEM apertar Enter.
	tecla(tab, ksUp)
	tela(t, tab, 5*time.Second, contendo("echo "+marca))
	t.Logf("seta pra cima trouxe o comando do histórico")

	// Enter confirma e o eco do echo aparece DE NOVO na tela — prova que
	// o \r também chegou certo depois da seta.
	tecla(tab, 0xff0d)
	tela(t, tab, 5*time.Second, func(s string) bool {
		return strings.Count(s, marca) >= 2
	})
}

// TestAoVivoSSHNanoSetas é o teste pedido: abre um arquivo de verdade no
// nano, navega com seta, digita, salva (Ctrl+O, Enter) e sai (Ctrl+X) — o
// caminho inteiro que "entrar em arquivo / navegar com seta / salvar"
// description. Usa um arquivo em /tmp criado e apagado por este teste, sem
// tocar em nada que já exista na máquina.
func TestAoVivoSSHNanoSetas(t *testing.T) {
	tab := sshAoVivo(t)

	caminho := fmt.Sprintf("/tmp/acessos_teste_nano_%d.txt", time.Now().UnixNano())
	t.Cleanup(func() {
		// best-effort: se o teste falhou no meio, ainda tenta limpar.
		tab.enviar([]byte(fmt.Sprintf("rm -f %s\r", caminho)))
	})

	linha(t, tab, fmt.Sprintf("printf 'linha1\\nlinha2\\nlinha3\\n' > %s", caminho), "$")
	linha(t, tab, fmt.Sprintf("nano %s", caminho), "linha1")
	// o cabeçalho do nano com o nome do arquivo prova que abriu de
	// verdade, não que ficou preso no prompt do shell.
	tela(t, tab, 5*time.Second, contendo(caminho[strings.LastIndex(caminho, "/")+1:]))

	// Down leva pra linha2, End leva pro fim dela — se qualquer uma das
	// duas não virar bytes, o "X" cai no lugar errado (linha1) e o teste
	// pega isso no cat final.
	tecla(tab, ksDown)
	tecla(tab, ksEnd)
	tab.enviar([]byte("X"))
	tela(t, tab, 5*time.Second, contendo("linha2X"))
	t.Logf("seta baixo + End posicionou o cursor certo dentro do nano")

	// Ctrl+O grava — espera o RODAPÉ pedir o nome antes de confirmar com
	// Enter, senão o Enter corre o risco de chegar antes do prompt do
	// nano existir e não confirmar nada (visto na prática: sem esperar
	// aqui, o teste seguinte passava por acidente lendo o BUFFER do nano
	// ainda aberto, não o arquivo salvo de verdade).
	tab.mods.ctrl = true
	tecla(tab, 'O')
	tab.mods.ctrl = false
	tela(t, tab, 5*time.Second, contendo("File Name to Write"))
	tecla(tab, 0xff0d) // Enter confirma o nome do arquivo
	tela(t, tab, 5*time.Second, contendo("Wrote"))
	t.Logf("nano confirmou a gravação (\"Wrote ...\" no rodapé)")

	// Ctrl+X sai — bytesDaTecla(Ctrl+letra) é o caminho exercitado aqui.
	// Espera o CROMO do nano sumir da tela antes de mandar o próximo
	// comando: sem isto o "cat ..." corre o risco de ser digitado DENTRO
	// do nano em vez de chegar ao shell.
	tab.mods.ctrl = true
	tecla(tab, 'X')
	tab.mods.ctrl = false
	tela(t, tab, 5*time.Second, func(s string) bool {
		return !strings.Contains(s, "GNU nano") && strings.Contains(s, "$")
	})
	t.Logf("nano saiu, de volta ao shell")

	linha(t, tab, fmt.Sprintf("cat %s", caminho), "linha2X")
	final := tela(t, tab, 5*time.Second, contendo("linha2X"))
	if strings.Contains(final, "GNU nano") {
		t.Fatalf("ainda dentro do nano ao rodar cat — não prova nada sobre o arquivo salvo:\n%s", final)
	}
	if !strings.Contains(final, "linha1") || !strings.Contains(final, "linha3") {
		t.Fatalf("arquivo salvo não bate com o esperado:\n%s", final)
	}
	t.Logf("nano abriu, navegou, editou e salvou certo — cat leu do disco:\n%s", final)
}

// TestAoVivoSSHHtopMouseRelata confere item 2 do backlog contra um htop DE
// VERDADE: sem geometria de janela nenhuma (não chama Layout — não precisa
// de shaper de fonte pra isto), só planta os campos que celulaEm lê e
// manda um scroll pelo MESMO HandlePointer que o mouse de verdade usa.
// Prova duas coisas que só um servidor real confirma: que o htop desta
// distro liga os modos de mouse que a implementação assume, e que os
// bytes que mandamos fazem a tela dele mudar de verdade.
func TestAoVivoSSHHtopMouseRelata(t *testing.T) {
	tab := sshAoVivo(t)

	// geometria sintética: HandlePointer só lê estes campos por dentro de
	// celulaEm, não desenha nada.
	tab.mu.Lock()
	tab.margem, tab.avanco, tab.alturaCel = 0, 8, 16
	tab.mu.Unlock()

	linha(t, tab, "htop", "CPU")
	tab.term.Lock()
	modo := tab.term.Mode()
	tab.term.Unlock()
	if modo&vt10x.ModeMouseMask == 0 {
		t.Fatalf("htop 3.0.5 não ligou modo de mouse nenhum — Mode()=%v", modo)
	}
	t.Logf("htop ligou modo de mouse Mode()=%v (SGR=%v)", modo, modo&vt10x.ModeMouseSgr != 0)

	antes := tab.term.String()

	// roda pra baixo no meio da tela — htop deveria mover a seleção ou
	// rolar a lista de processos.
	pos := f32Pt(200, 200)
	for i := 0; i < 6; i++ {
		tab.HandlePointer(pointer.Event{Kind: pointer.Scroll, Position: pos,
			Scroll: f32Pt(0, 1), Source: pointer.Mouse}, image.Pt(900, 500))
	}

	mudou := tela(t, tab, 5*time.Second, func(s string) bool { return s != antes })
	t.Logf("depois da roda, a tela do htop mudou (prova que ele recebeu e reagiu ao relato de mouse):\n%s", mudou)

	// sanity check na direção oposta: SEM modo de mouse (shell puro), a
	// mesma roda NÃO deveria sair pelo fio — cai no scrollback local.
	tab.mods.ctrl = true
	tecla(tab, 'C') // Ctrl+C sai do htop
	tab.mods.ctrl = false
	tela(t, tab, 5*time.Second, func(s string) bool {
		return !strings.Contains(s, "CPU") && strings.Contains(s, "$")
	})
	tab.term.Lock()
	modoPosHtop := tab.term.Mode()
	tab.term.Unlock()
	if modoPosHtop&vt10x.ModeMouseMask != 0 {
		t.Fatalf("htop devia ter desligado o modo de mouse ao sair, ainda ligado: %v", modoPosHtop)
	}
	t.Logf("de volta ao shell puro, modo de mouse desligado — roda volta a ser scrollback local")
}

func f32Pt(x, y float32) f32.Point { return f32.Point{X: x, Y: y} }
