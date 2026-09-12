package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"acessos-go/internal/massa/executor"
	"acessos-go/internal/massa/model"
)

// Decisao e a resposta do usuario ao dialogo de erro.
type Decisao int

const (
	DecContinuarHost Decisao = iota // segue com os proximos comandos deste PDV
	DecPularHost                    // abandona este PDV, segue com os outros
	DecContinuarTudo                // nao pergunta mais nada
	DecAbortar                      // para tudo agora
)

// ErroCtx e o contexto entregue ao dialogo de erro.
type ErroCtx struct {
	Host      model.Host
	IndiceCmd int
	Comando   string
	Saida     string
	ExitCode  int
	Erro      error
}

// ResumoCanario e o que a UI mostra entre a fase 1 e a fase 2.
type ResumoCanario struct {
	Resultados      []model.ResultadoHost
	TimeoutsExec    []time.Duration // por comando, ja calibrado
	NaoCalibrados   []int           // indices sem baseline confiavel
	TimeoutConexao  time.Duration
	ConexaoAjustada bool
	Falhou          bool // algum canario terminou em erro
}

// Config descreve uma execucao completa.
type Config struct {
	Hosts    []model.Host
	Comandos []*model.Comando
	Cred     model.Credencial
	// CredDeHost, quando definida, manda mais que Cred: é o gancho para
	// quem já tem a credencial de cada maquina guardada (o Acessos lê a
	// do proprio conexoes.ini). Devolver credencial vazia faz cair em
	// Cred, que continua sendo o padrao digitado na tela.
	CredDeHost     func(model.Host) model.Credencial
	UsarRoot       bool
	RootCred       model.Credencial
	Plataforma     model.Plataforma
	Workers        int
	TamCanario     int
	TimeoutConexao time.Duration
}

// Callbacks conectam o motor a UI. OnCanario e OnErro BLOQUEIAM a
// goroutine que os chama ate o usuario responder.
type Callbacks struct {
	OnStatus func(model.ResultadoHost)
	OnLog    func(string)

	// OnHostConcluido e chamado assim que um PDV termina, seja com
	// sucesso ou falha. E por aqui que o log.txt e gravado de forma
	// incremental, em vez de so no fim da execucao.
	OnHostConcluido func(model.ResultadoHost)

	OnCanario func(ResumoCanario) bool
	OnErro    func(ErroCtx) Decisao
	OnFim     func([]model.ResultadoHost)
}

// Runner orquestra tudo.
type Runner struct {
	Cfg Config
	Cb  Callbacks

	mu            sync.Mutex
	continuarTudo bool
	resultados    []model.ResultadoHost

	cancelar context.CancelFunc
}

func Novo(cfg Config, cb Callbacks) *Runner {
	return &Runner{Cfg: cfg, Cb: cb}
}

// Abortar cancela a execucao em andamento.
func (r *Runner) Abortar() {
	r.mu.Lock()
	c := r.cancelar
	r.mu.Unlock()
	if c != nil {
		c()
	}
}

// Executar roda as duas fases. Deve ser chamada de uma goroutine.
func (r *Runner) Executar() {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancelar = cancel
	r.resultados = nil
	r.continuarTudo = false
	r.mu.Unlock()
	defer cancel()

	hosts := r.Cfg.Hosts
	if len(hosts) == 0 {
		r.log("Nenhum PDV selecionado.")
		r.fim()
		return
	}

	// ------- Fase 1: canario, sempre, sequencial -------
	n := r.Cfg.TamCanario
	if n < 1 {
		n = 1
	}
	if n > len(hosts) {
		n = len(hosts)
	}

	r.log(fmt.Sprintf("=== FASE 1 - canario em %d PDV(s), sequencial ===", n))

	var canarios []model.ResultadoHost
	piorCmd := make([]time.Duration, len(r.Cfg.Comandos))
	var piorConn time.Duration

	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			break
		}
		res, dConn, dCmds := r.rodarHost(ctx, hosts[i], true)
		canarios = append(canarios, res)
		r.registrar(res)
		if dConn > piorConn {
			piorConn = dConn
		}
		for j, d := range dCmds {
			if j < len(piorCmd) && d > piorCmd[j] {
				piorCmd[j] = d
			}
		}
	}

	if ctx.Err() != nil {
		r.log("Execucao abortada durante o canario.")
		r.fim()
		return
	}

	// ------- Calibracao -------
	resumo := ResumoCanario{Resultados: canarios}
	for j := range r.Cfg.Comandos {
		t := CalcularTimeoutExec(piorCmd[j])
		resumo.TimeoutsExec = append(resumo.TimeoutsExec, t)
		if piorCmd[j] <= 0 {
			resumo.NaoCalibrados = append(resumo.NaoCalibrados, j)
		}
	}
	resumo.TimeoutConexao, resumo.ConexaoAjustada =
		AjustarTimeoutConexao(r.Cfg.TimeoutConexao, piorConn)
	for _, c := range canarios {
		if c.Status != model.StatusOK {
			resumo.Falhou = true
		}
	}

	if len(hosts) <= n {
		r.log("Todos os PDVs selecionados ja rodaram no canario.")
		r.fim()
		return
	}

	seguir := true
	if r.Cb.OnCanario != nil {
		seguir = r.Cb.OnCanario(resumo)
	}
	if !seguir {
		r.log("Execucao interrompida pelo usuario apos o canario.")
		r.fim()
		return
	}

	// aplica os timeouts calibrados
	for j, c := range r.Cfg.Comandos {
		if j < len(resumo.TimeoutsExec) {
			c.TimeoutExec = resumo.TimeoutsExec[j]
			c.Calibrado = piorCmd[j] > 0
		}
	}
	r.Cfg.TimeoutConexao = resumo.TimeoutConexao

	// ------- Fase 2: pool paralelo -------
	restantes := hosts[n:]
	w := r.Cfg.Workers
	if w < 1 {
		w = 1
	}
	if w > len(restantes) {
		w = len(restantes)
	}
	r.log(fmt.Sprintf("=== FASE 2 - %d PDV(s) restantes, %d em paralelo ===", len(restantes), w))

	fila := make(chan model.Host)
	var wg sync.WaitGroup
	for i := 0; i < w; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for h := range fila {
				if ctx.Err() != nil {
					return
				}
				res, _, _ := r.rodarHost(ctx, h, false)
				r.registrar(res)
			}
		}()
	}

despacho:
	for _, h := range restantes {
		select {
		case <-ctx.Done():
			break despacho
		case fila <- h:
		}
	}
	close(fila)
	wg.Wait()

	if ctx.Err() != nil {
		r.log("Execucao abortada.")
	}
	r.fim()
}

// rodarHost executa a fila de comandos em um unico PDV.
// Devolve o resultado, a duracao da conexao e a duracao de cada comando.
func (r *Runner) rodarHost(ctx context.Context, h model.Host, canario bool) (model.ResultadoHost, time.Duration, []time.Duration) {
	res := model.ResultadoHost{Host: h, Iniciado: time.Now(), Status: model.StatusConectando}
	duracoes := make([]time.Duration, len(r.Cfg.Comandos))
	r.status(res)

	// A plataforma do HOST manda; a do Cfg é só o padrao de quem ainda
	// nao foi sondado (ver detectar plataforma no Acessos).
	plat := r.Cfg.Plataforma
	if h.Plataforma != "" {
		plat = h.Plataforma
	}
	ex, err := executor.Novo(plat)
	if err != nil {
		res.Status = model.StatusErro
		res.Motivo = err.Error()
		return r.encerrar(res), 0, duracoes
	}
	defer ex.Fechar()

	tConn := r.Cfg.TimeoutConexao
	if tConn <= 0 {
		tConn = TimeoutConexaoPadrao
	}

	cred := r.Cfg.Cred
	if r.Cfg.CredDeHost != nil {
		if c := r.Cfg.CredDeHost(h); c.Usuario != "" {
			cred = c
		}
	}

	t0 := time.Now()
	if err := ex.Conectar(h.IP, cred, tConn); err != nil {
		dConn := time.Since(t0)
		// Falha de conexao NUNCA abre dialogo: PDV desligado nao e bug seu.
		if errors.Is(err, executor.ErrConexao) {
			res.Status = model.StatusFalhaConexao
		} else {
			res.Status = model.StatusErro
		}
		res.Motivo = err.Error()
		return r.encerrar(res), dConn, duracoes
	}
	dConn := time.Since(t0)
	res.TempoConexao = dConn

	if r.Cfg.UsarRoot {
		tElev := time.Now()
		if err := ex.Elevar(r.Cfg.RootCred); err != nil {
			res.Status = model.StatusErro
			res.Motivo = err.Error()
			res.TempoElevacao = time.Since(tElev)
			return r.encerrar(res), dConn, duracoes
		}
		res.TempoElevacao = time.Since(tElev)
	}

	res.Status = model.StatusExecutando
	r.status(res)

	for i, cmd := range r.Cfg.Comandos {
		if ctx.Err() != nil {
			res.Status = model.StatusPulado
			res.Motivo = "abortado"
			return r.encerrar(res), dConn, duracoes
		}

		tExec := cmd.TimeoutExec
		if canario || tExec <= 0 || !cmd.Calibrado {
			// Sem medida confiavel, teto generoso. Apertar aqui foi o que
			// fazia PDV bom aparecer como "timeout de execucao".
			tExec = SemBaseline
		}

		ti := time.Now()
		saida, code, err := ex.Rodar(cmd.Texto, tExec, cmd.TimeoutIdle)
		d := time.Since(ti)
		duracoes[i] = d

		rc := model.ResultadoComando{
			IndiceCmd: i, Saida: saida, ExitCode: code, Duracao: d, Erro: err,
		}
		res.Comandos = append(res.Comandos, rc)

		falhou := false
		switch {
		case err != nil:
			falhou = true
		case code != 0 && !cmd.IgnorarExit:
			falhou = true
		}

		if !falhou {
			continue
		}

		// Erro de transporte com a sessao morta: nao adianta continuar.
		if errors.Is(err, executor.ErrConexao) {
			res.Status = model.StatusFalhaConexao
			res.Motivo = err.Error()
			return r.encerrar(res), dConn, duracoes
		}

		if errors.Is(err, executor.ErrTimeout) || errors.Is(err, executor.ErrIdle) {
			res.Status = model.StatusTimeout
		} else {
			res.Status = model.StatusErro
		}
		res.Motivo = fmt.Sprintf("comando %d (exit %d)", i+1, code)

		// A fila deste PDV para aqui, sempre. O que muda e o resto.
		dec := r.perguntar(ErroCtx{
			Host: h, IndiceCmd: i, Comando: cmd.Texto,
			Saida: saida, ExitCode: code, Erro: err,
		})

		switch dec {
		case DecContinuarHost:
			res.Status = model.StatusExecutando
			continue
		case DecPularHost:
			res.Status = model.StatusPulado
			return r.encerrar(res), dConn, duracoes
		case DecContinuarTudo:
			r.mu.Lock()
			r.continuarTudo = true
			r.mu.Unlock()
			res.Status = model.StatusExecutando
			continue
		case DecAbortar:
			r.Abortar()
			return r.encerrar(res), dConn, duracoes
		}
	}

	if res.Status == model.StatusExecutando {
		res.Status = model.StatusOK
	}
	return r.encerrar(res), dConn, duracoes
}

// perguntar consulta a UI, respeitando o "nao perguntar de novo".
func (r *Runner) perguntar(ctx ErroCtx) Decisao {
	r.mu.Lock()
	tudo := r.continuarTudo
	r.mu.Unlock()
	if tudo {
		return DecContinuarHost
	}
	if r.Cb.OnErro == nil {
		return DecAbortar
	}
	return r.Cb.OnErro(ctx)
}

func (r *Runner) encerrar(res model.ResultadoHost) model.ResultadoHost {
	res.Terminado = time.Now()
	res.Duracao = res.Terminado.Sub(res.Iniciado)
	r.status(res)
	return res
}

func (r *Runner) registrar(res model.ResultadoHost) {
	r.mu.Lock()
	r.resultados = append(r.resultados, res)
	r.mu.Unlock()
	if r.Cb.OnHostConcluido != nil {
		r.Cb.OnHostConcluido(res)
	}
}

func (r *Runner) status(res model.ResultadoHost) {
	if r.Cb.OnStatus != nil {
		r.Cb.OnStatus(res)
	}
}

func (r *Runner) log(s string) {
	if r.Cb.OnLog != nil {
		r.Cb.OnLog(s)
	}
}

func (r *Runner) fim() {
	r.mu.Lock()
	out := make([]model.ResultadoHost, len(r.resultados))
	copy(out, r.resultados)
	r.mu.Unlock()
	if r.Cb.OnFim != nil {
		r.Cb.OnFim(out)
	}
}
