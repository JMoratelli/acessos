package model

import "time"

// Plataforma identifica o tipo de alvo.
type Plataforma string

const (
	Linux   Plataforma = "linux"
	Windows Plataforma = "windows"
	Ambos   Plataforma = "ambos"
)

// Host e um alvo de execucao lido do pdvs.csv.
type Host struct {
	IP string
	// Plataforma vem marcada no pdvs.csv. Vazia = ainda nao sabemos;
	// a sonda descobre e grava de volta no arquivo.
	Plataforma Plataforma
	Faixa      string // primeiros 3 octetos, ex: 192.168.8
	Filial     string // nome resolvido via filiais.conf
	Linha      int    // linha de origem no CSV, para mensagens
}

// Credencial e um par usuario/senha.
type Credencial struct {
	Usuario string
	Senha   string
	Dominio string // usado apenas na plataforma Windows (futuro)
}

// Perfil e uma credencial nomeada e persistida em credenciais.conf.
// A senha de root NUNCA e persistida aqui.
type Perfil struct {
	Nome    string
	Usuario string
	Senha   string
	Padrao  bool
}

// Comando e uma linha de execucao. Pode ser multilinha.
type Comando struct {
	Texto       string
	IgnorarExit bool          // definido pelo usuario ou pela heuristica
	AutoIgnorar bool          // true quando a heuristica decidiu ignorar
	TimeoutExec time.Duration // 0 = sem limite (antes da calibracao)
	TimeoutIdle time.Duration // 0 = desligado
	Calibrado   bool          // true depois que o canario mediu
	Plataforma  Plataforma
}

// Snippet e um comando salvo em comandos.ini.
type Snippet struct {
	ID          string
	Descricao   string
	Comando     string
	Root        bool
	IgnorarExit bool
	Plataforma  Plataforma
}

// StatusExec descreve o estado de um host na execucao.
type StatusExec int

const (
	StatusPendente StatusExec = iota
	StatusConectando
	StatusExecutando
	StatusOK
	StatusErro
	StatusFalhaConexao
	StatusPulado
	StatusTimeout
)

func (s StatusExec) String() string {
	switch s {
	case StatusPendente:
		return "Pendente"
	case StatusConectando:
		return "Conectando"
	case StatusExecutando:
		return "Executando"
	case StatusOK:
		return "OK"
	case StatusErro:
		return "Erro"
	case StatusFalhaConexao:
		return "Sem conexao"
	case StatusPulado:
		return "Pulado"
	case StatusTimeout:
		return "Timeout"
	}
	return "?"
}

// ResultadoComando e a saida de um unico comando em um unico host.
type ResultadoComando struct {
	IndiceCmd int
	Saida     string
	ExitCode  int
	Duracao   time.Duration
	Erro      error // erro de transporte/timeout, nao exit code
}

// ResultadoHost agrega tudo que aconteceu em um host.
type ResultadoHost struct {
	Host     Host
	Status   StatusExec
	Motivo   string
	Comandos []ResultadoComando
	Duracao  time.Duration
	// Tempos de abertura, para separar "sessao demora a abrir" de
	// "comando demora a rodar" sem ter de cronometrar na mao.
	TempoConexao  time.Duration
	TempoElevacao time.Duration
	Iniciado      time.Time
	Terminado     time.Time
}
