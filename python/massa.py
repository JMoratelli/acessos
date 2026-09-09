#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Execucao de comandos em lote via SSH — motor, sem interface.

Porte do protocolo do `pdvmanager` (Go). O que esta aqui foi descoberto
quebrando em producao; ler os comentarios antes de "simplificar" qualquer
coisa.

Sem dependencia de GTK de proposito: da para testar tudo isto contra um
sshd de descarte, sem abrir janela nenhuma.

Desenvolvido por @JJMoratelli.
"""

import base64
import re
import socket
import time

try:
    import paramiko
    TEM_PARAMIKO, ERRO_PARAMIKO = True, ""
except ImportError as e:            # pragma: no cover
    paramiko = None
    TEM_PARAMIKO, ERRO_PARAMIKO = False, str(e)


# --------------------------------------------------------------- marcadores
#
# Escritos SEMPRE partidos ('__ZBE' 'GIN__'). O shell concatena, mas a linha
# DIGITADA nunca casa com o regex — sem isso o eco do proprio comando
# dispara o marcador e a leitura sai errada.
MARCA_VIVO = "__ZVIVO__"
MARCA_INI = "__ZBEGIN__"
MARCA_FIM = "__ZEND__"
MARCA_SYNC = "__ZSYNC__"

RE_FIM = re.compile(r"__ZEND__:(-?\d+):")
RE_SENHA = re.compile(r"(?i)(password|senha)\s*:")

# comandos que retornam != 0 em situacao normal: nao devem abrir dialogo
TOLERANTES = {
    "grep", "egrep", "fgrep", "zgrep", "diff", "cmp", "rsync",
    "pgrep", "pkill", "test", "[", "find", "mount", "umount",
}

# comandos que encerram ou substituem o shell — como todos os comandos do
# PDV rodam na MESMA sessao, derrubam os seguintes daquele PDV
MATAM_SESSAO = {"exit", "logout", "exec"}

ESPERA_POS_SU = 0.30      # ver elevar(): sem isto as linhas se perdem
FATIA_BASE64 = 900        # limite de linha do terminal em modo canonico


class ErroConexao(Exception):
    """TCP/auth falhou. NUNCA abre dialogo: vai para o relatorio."""


class ErroShell(Exception):
    """Conectou mas o shell nao ficou utilizavel. Carrega os bytes lidos."""


class ErroTimeout(Exception):
    """Estourou tempo de execucao ou de silencio. Pergunta ao operador."""


# --------------------------------------------------------------- utilidades

def _fora_de_aspas(texto):
    """Separa em segmentos por && || | ; ignorando o que esta entre aspas."""
    segmentos, atual, aspa, i = [], [], None, 0
    while i < len(texto):
        c = texto[i]
        if aspa:
            atual.append(c)
            if c == aspa:
                aspa = None
        elif c in "'\"":
            aspa = c
            atual.append(c)
        elif c in "|&;":
            prox = texto[i + 1] if i + 1 < len(texto) else ""
            if c in "|&" and prox == c:
                i += 1
            segmentos.append("".join(atual))
            atual = []
        else:
            atual.append(c)
        i += 1
    segmentos.append("".join(atual))
    return [s.strip() for s in segmentos if s.strip()]


def _ultimo_efetivo(comando):
    """Ultimo comando de fato executado numa linha.

    Numa pipeline o exit code e o do ULTIMO elemento — por isso
    'grep x y | wc -l' NAO e tolerante: quem decide o codigo e o wc."""
    segs = _fora_de_aspas(comando)
    if not segs:
        return ""
    palavras = segs[-1].split()
    pular = {"sudo", "time", "nohup", "then", "else", "elif", "do"}
    for p in palavras:
        if "=" in p and not p.startswith("="):
            continue                     # LC_ALL=C grep ...
        if p in pular:
            continue
        return p.rsplit("/", 1)[-1]      # /usr/bin/grep -> grep
    return ""


def tolera_exit(comando):
    return _ultimo_efetivo(comando) in TOLERANTES


def mata_sessao(comando):
    """Avisa (nao bloqueia). '(exit 3)' em subshell e seguro."""
    for seg in _fora_de_aspas(comando):
        limpo = seg.split("#", 1)[0].strip()
        if not limpo or limpo.startswith("(") or limpo.startswith("{"):
            continue
        if limpo.split()[0] if limpo.split() else "" in MATAM_SESSAO:
            return True
        primeiro = limpo.split()[0] if limpo.split() else ""
        if primeiro in MATAM_SESSAO:
            return True
    return False


def precisa_base64(comando):
    """Quando o modo literal seria arriscado."""
    if "\n" in comando or "<<" in comando:
        return True
    if MARCA_INI in comando or MARCA_FIM in comando:
        return True
    # '#' no nivel de cima comentaria o resto da linha
    fora, aspa = True, None
    for c in comando:
        if aspa:
            if c == aspa:
                aspa = None
        elif c in "'\"":
            aspa = c
        elif c == "#" and fora:
            return True
    if aspa is not None:                 # aspas desequilibradas
        return True
    return False


def calcular_timeout(pior_segundos):
    """Margem sobre o PIOR tempo, nao a media.

    Tres amostras e pouco, e se os canarios forem maquinas boas a media
    subestima o parque velho."""
    if pior_segundos is None:
        # SEM MEDIDA = generoso. Aplicar o piso de 30s a um comando nao
        # medido derruba o parque inteiro com "timeout" em maquina sadia.
        return 30 * 60
    if pior_segundos < 5:
        return max(30, int(pior_segundos * 10))
    if pior_segundos <= 60:
        return int(pior_segundos * 5)
    return int(pior_segundos * 3)


# --------------------------------------------------------------- sonda

def sondar(host, porta=22, timeout=4.0):
    """Le a linha de identificacao do servidor SSH.

    O servidor manda o banner ANTES de qualquer negociacao ou autenticacao
    (RFC 4253 4.2). Abrir TCP, ler, fechar — nenhuma credencial trafega,
    entao e seguro rodar no parque inteiro.

    Devolve (ok, banner, plataforma, ms). plataforma: 'windows', 'linux'
    ou '' quando nao da para afirmar."""
    inicio = time.monotonic()
    try:
        with socket.create_connection((host, int(porta)), timeout) as s:
            s.settimeout(timeout)
            dados = b""
            while b"\n" not in dados and len(dados) < 512:
                pedaco = s.recv(256)
                if not pedaco:
                    break
                dados += pedaco
    except Exception as e:
        return False, str(e), "", int((time.monotonic() - inicio) * 1000)

    ms = int((time.monotonic() - inicio) * 1000)
    banner = dados.decode("utf-8", "replace").strip()
    b = banner.lower()

    if "for_windows" in b:
        return True, banner, "windows", ms
    for marca in ("ubuntu", "debian", "raspbian", "freebsd", "dropbear"):
        if marca in b:
            return True, banner, "linux", ms
    if "openssh" in b:
        # Windows SEMPRE carrega "for_Windows" no banner
        return True, banner, "linux", ms
    # Melhor admitir que nao sabe do que gravar palpite errado no INI
    return True, banner, "", ms


# --------------------------------------------------------------- sessao

class SessaoLinux:
    """Uma sessao de shell por host, comandos em sequencia.

    Sessao unica e o que faz 'cd /tmp' no comando 1 valer no comando 2.
    Variaveis de shell tambem persistem."""

    def __init__(self, host, porta, usuario, senha, timeout_conexao=10):
        self.host, self.porta = host, int(porta or 22)
        self.usuario, self.senha = usuario, senha
        self.timeout_conexao = timeout_conexao
        self.cli = None
        self.chan = None
        self.buffer = ""
        self.elevado = False

    # ---- conexao
    def abrir(self):
        if not TEM_PARAMIKO:
            raise ErroConexao("paramiko ausente: %s" % ERRO_PARAMIKO)
        self.cli = paramiko.SSHClient()
        self.cli.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        try:
            self.cli.connect(
                self.host, port=self.porta, username=self.usuario,
                password=self.senha, timeout=self.timeout_conexao,
                banner_timeout=self.timeout_conexao,
                auth_timeout=self.timeout_conexao,
                allow_agent=False, look_for_keys=False)
        except Exception as e:
            raise ErroConexao("%s" % e)

        # PTY obrigatorio: a elevacao usa `su`, e `su` so le senha de um
        # terminal — nao aceita pipe, ao contrario de `sudo -S`.
        # Consequencia: stdout e stderr chegam MISTURADOS, sem separacao.
        self.chan = self.cli.invoke_shell(term="dumb", width=500, height=5000)
        self.chan.settimeout(0.2)
        self._preparar_shell()

    def _preparar_shell(self):
        """Abertura em duas fases, enviadas de uma vez.

        Separar as fases na LEITURA e nao na escrita economiza uma ida e
        volta por PDV."""
        self._enviar('echo "%s""%s"' % (MARCA_VIVO[:6], MARCA_VIVO[6:]))
        # linha separada: e a parte mais nova e a mais provavel de nao ser
        # aceita por um shell diferente; isolada, um engasgo aqui nao
        # derruba o resto do preparo
        self._enviar("set +o history 2>/dev/null || true; "
                     "unset HISTFILE 2>/dev/null || true")
        self._enviar(
            "stty -echo 2>/dev/null; PS1=''; PS2=''; PROMPT_COMMAND=''; "
            "unalias -a 2>/dev/null; "
            "__ZB='%s''%s'; __ZE='%s''%s'; __ZS='%s''%s'; true"
            % (MARCA_INI[:6], MARCA_INI[6:], MARCA_FIM[:5], MARCA_FIM[5:],
               MARCA_SYNC[:6], MARCA_SYNC[6:]))
        self._enviar("echo $__ZS")

        # Fase 1: prova que o shell esta vivo e lendo, ANTES de qualquer
        # alteracao. Falha aqui = MOTD esperando entrada, shell travado,
        # conta sem shell interativo.
        try:
            self._ler_ate(re.compile(re.escape(MARCA_VIVO)), 15)
        except ErroTimeout as e:
            raise ErroShell("nao respondeu a um echo simples. Recebido: %s"
                            % (self.buffer[-800:] or "<nada>"))
        # Fase 2: prova que o preparo foi digerido. Falha so aqui = o shell
        # engasgou em alguma opcao do preparo. Mensagens diferentes de
        # proposito: sem isso os dois casos viravam um "timeout" generico.
        try:
            self._ler_ate(re.compile(re.escape(MARCA_SYNC)), 15)
        except ErroTimeout:
            raise ErroShell("respondeu ao echo mas travou no preparo. "
                            "Recebido: %s" % (self.buffer[-800:] or "<nada>"))

    def _enviar(self, linha):
        self.chan.send(linha + "\n")

    def _ler_ate(self, regex, limite, idle=0):
        """Acumulador com resto.

        Guarda o que sobrou DEPOIS do casamento para a leitura seguinte —
        sem isso perdem-se bytes entre comandos.

        Tres relogios simultaneos: absoluto, idle (reiniciado a cada byte)
        e o proprio timeout do socket."""
        fim = time.monotonic() + limite
        ultimo_byte = time.monotonic()
        while True:
            m = regex.search(self.buffer)
            if m:
                antes = self.buffer[:m.start()]
                self.buffer = self.buffer[m.end():]
                return antes, m
            agora = time.monotonic()
            if agora > fim:
                raise ErroTimeout("timeout de %ds" % limite)
            if idle and (agora - ultimo_byte) > idle:
                raise ErroTimeout("sem saida por %ds (idle)" % idle)
            try:
                dados = self.chan.recv(65536)
                if not dados:
                    raise ErroShell("conexao encerrada pelo host")
                # errors=replace: saida de PDV pode ter byte invalido e
                # derrubaria um decode estrito
                self.buffer += dados.decode("utf-8", "replace")
                ultimo_byte = time.monotonic()
            except socket.timeout:
                continue

    # ---- elevacao
    def elevar(self, senha_root, usuario_root="root"):
        """`su - root` na sessao ja aberta.

        Nao `sudo`: assim o acesso SSH continua sendo do usuario comum,
        sem precisar liberar root no sshd."""
        self._enviar("su - %s" % usuario_root)
        try:
            self._ler_ate(RE_SENHA, 15)
        except ErroTimeout:
            raise ErroShell("`su` nao pediu senha. Recebido: %s"
                            % (self.buffer[-400:] or "<nada>"))
        self._enviar(senha_root)

        # NAO REMOVER: enviar o preparo antes de o shell novo assumir a
        # entrada faz as linhas se perderem na troca. Ja foi removido para
        # "economizar tempo" e a elevacao quebrou.
        time.sleep(ESPERA_POS_SU)

        # o shell novo tem eco, prompt e HISTFILE proprios
        self.buffer = ""
        self._preparar_shell()

        # Confirmar DE VERDADE: com senha errada o `su` falha em silencio,
        # e sem esta checagem voce acharia que esta rodando como root.
        saida, _ = self.executar("id -u", 15)[0:2]
        if saida.strip() != "0":
            raise ErroShell("elevacao falhou: id -u devolveu %r" % saida.strip())
        self.elevado = True

    # ---- execucao
    def executar(self, comando, limite, idle=0):
        """Devolve (saida, exit_code, ms)."""
        inicio = time.monotonic()
        if precisa_base64(comando):
            b64 = base64.b64encode(comando.encode("utf-8")).decode("ascii")
            self._enviar("ZCMD=''")
            # o alfabeto base64 nao contem aspa simples, entao o quoting e
            # seguro; o fatiamento existe por causa do limite de linha do
            # terminal em modo canonico
            for i in range(0, len(b64), FATIA_BASE64):
                self._enviar("ZCMD=$ZCMD'%s'" % b64[i:i + FATIA_BASE64])
            # eval roda no shell ATUAL — e o que preserva cd e variaveis
            self._enviar('echo $__ZB; eval "$(printf \'%s\' "$ZCMD" '
                         '| base64 -d)"; echo "$__ZE:$?:"')
        else:
            # modo literal: quem observar a sessao do lado do PDV le o
            # comando de verdade, nao um blob
            self._enviar('echo $__ZB; %s; echo "$__ZE:$?:"' % comando)

        # Descartar TUDO ate o marcador de inicio elimina o eco do comando,
        # a montagem do ZCMD e qualquer lixo — independentemente de o
        # terminal respeitar `stty -echo`. Foi a correcao que acabou com o
        # ruido no log.
        self._ler_ate(re.compile(re.escape(MARCA_INI)), limite, idle)
        saida, m = self._ler_ate(RE_FIM, limite, idle)
        codigo = int(m.group(1))

        saida = saida.replace("\r\n", "\n").replace("\r", "\n").strip("\n")
        return saida, codigo, int((time.monotonic() - inicio) * 1000)

    def fechar(self):
        for obj in (self.chan, self.cli):
            try:
                if obj is not None:
                    obj.close()
            except Exception:
                pass


class SessaoWindows:
    """OpenSSH Server nativo do Windows.

    EXPERIMENTAL: no projeto original o lado cliente foi testado apenas
    contra um stub que imita o subconjunto de PowerShell do protocolo. A
    semantica real (`-Command -` lendo de stdin, Invoke-Expression,
    comportamento de $?) nunca foi validada contra um Windows de verdade.

    Diferencas deliberadas em relacao ao Linux:
      - SEM PTY: o PTY existe la so por causa do `su`. Aqui a sessao e
        limpa e stdout/stderr vem separados.
      - SEM elevacao: ou a credencial ja e administrativa, ou nao sobe —
        o UAC nao se aplica a logon de rede."""

    PREPARO = (
        "$ErrorActionPreference = 'Continue'\n"
        "$ProgressPreference = 'SilentlyContinue'\n"
        "try { [Console]::OutputEncoding = "
        "[System.Text.Encoding]::UTF8 } catch {}\n"
        "try { $OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}\n"
        "$FormatEnumerationLimit = -1\n"
    )

    def __init__(self, host, porta, usuario, senha, dominio="",
                 timeout_conexao=10):
        self.host, self.porta = host, int(porta or 22)
        self.senha = senha
        self.timeout_conexao = timeout_conexao
        self.usuario = self._qualificar(usuario, dominio)
        self.cli = None
        self.chan = None
        self.buffer = ""
        self.elevado = False

    @staticmethod
    def _qualificar(usuario, dominio):
        """Respeita quem ja vier qualificado (DOM\\user ou user@dominio)."""
        if "\\" in usuario or "@" in usuario:
            return usuario
        return "%s\\%s" % (dominio, usuario) if dominio else usuario

    def abrir(self):
        if not TEM_PARAMIKO:
            raise ErroConexao("paramiko ausente: %s" % ERRO_PARAMIKO)
        self.cli = paramiko.SSHClient()
        self.cli.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        try:
            self.cli.connect(
                self.host, port=self.porta, username=self.usuario,
                password=self.senha, timeout=self.timeout_conexao,
                banner_timeout=self.timeout_conexao,
                auth_timeout=self.timeout_conexao,
                allow_agent=False, look_for_keys=False)
        except Exception as e:
            raise ErroConexao("%s" % e)

        # -Command - le da entrada padrao: da sessao persistente, como no
        # Linux, em vez de um processo por comando
        self.chan = self.cli.get_transport().open_session()
        self.chan.settimeout(0.2)
        self.chan.exec_command(
            "powershell -NoLogo -NoProfile -NonInteractive -Command -")
        self._enviar(self.PREPARO)

    def elevar(self, senha_root, usuario_root="root"):
        """Erro EXPLICITO, nunca no-op silencioso: quem chamar precisa
        saber que a elevacao nao aconteceu."""
        raise ErroShell(
            "Windows nao suporta elevacao por esta via: o UAC nao se aplica "
            "a logon de rede. Use uma credencial ja administrativa.")

    def _enviar(self, texto):
        self.chan.send(texto + "\n")

    def _ler_ate(self, regex, limite, idle=0):
        fim = time.monotonic() + limite
        ultimo_byte = time.monotonic()
        while True:
            m = regex.search(self.buffer)
            if m:
                antes = self.buffer[:m.start()]
                self.buffer = self.buffer[m.end():]
                return antes, m
            agora = time.monotonic()
            if agora > fim:
                raise ErroTimeout("timeout de %ds" % limite)
            if idle and (agora - ultimo_byte) > idle:
                raise ErroTimeout("sem saida por %ds (idle)" % idle)
            leu = False
            # sem PTY os fluxos vem separados: juntar do lado do cliente
            for pronto, ler in ((self.chan.recv_ready(), self.chan.recv),
                                (self.chan.recv_stderr_ready(),
                                 self.chan.recv_stderr)):
                if pronto:
                    dados = ler(65536)
                    if dados:
                        self.buffer += dados.decode("utf-8", "replace")
                        leu = True
            if leu:
                ultimo_byte = time.monotonic()
            else:
                if self.chan.exit_status_ready() and not self.chan.recv_ready():
                    raise ErroShell("powershell encerrou antes do esperado")
                time.sleep(0.05)

    def executar(self, comando, limite, idle=0):
        inicio = time.monotonic()
        # Base64 do PowerShell e UTF-16LE por padrao: sem decodificar
        # explicitamente como UTF-8, acento e aspas quebram.
        b64 = base64.b64encode(comando.encode("utf-8")).decode("ascii")
        self._enviar("$ZB = ''")
        for i in range(0, len(b64), FATIA_BASE64):
            self._enviar("$ZB += '%s'" % b64[i:i + FATIA_BASE64])
        self._enviar(
            "$ZCMD = [System.Text.Encoding]::UTF8.GetString("
            "[System.Convert]::FromBase64String($ZB))")
        # $LASTEXITCODE persiste entre comandos: zerar antes
        self._enviar("$global:LASTEXITCODE = 0")
        self._enviar('Write-Output ("%s" + "%s")'
                     % (MARCA_INI[:5], MARCA_INI[5:]))
        self._enviar(
            "try { Invoke-Expression $ZCMD 2>&1 | Out-String -Stream "
            "| Write-Output; $ZOK = $? } "
            "catch { Write-Output $_.Exception.Message; $ZOK = $false }")
        # $LASTEXITCODE so e preenchido por executavel nativo; cmdlet mexe
        # no $?. Combinar os dois, senao Get-Service que falha volta 0.
        self._enviar("$ZC = 0")
        self._enviar("if ($LASTEXITCODE -ne $null -and $LASTEXITCODE -ne 0) "
                     "{ $ZC = $LASTEXITCODE } elseif (-not $ZOK) { $ZC = 1 }")
        self._enviar('Write-Output ("%s" + "%s:" + $ZC + ":")'
                     % (MARCA_FIM[:4], MARCA_FIM[4:]))

        self._ler_ate(re.compile(re.escape(MARCA_INI)), limite, idle)
        saida, m = self._ler_ate(RE_FIM, limite, idle)
        codigo = int(m.group(1))
        saida = saida.replace("\r\n", "\n").replace("\r", "\n").strip("\n")
        return saida, codigo, int((time.monotonic() - inicio) * 1000)

    def fechar(self):
        for obj in (self.chan, self.cli):
            try:
                if obj is not None:
                    obj.close()
            except Exception:
                pass


def abrir_sessao(conexao, senha_root=None, timeout_conexao=10):
    """Escolhe a sessao pela plataforma da maquina."""
    if getattr(conexao, "windows", False):
        s = SessaoWindows(conexao.host, conexao.ssh_porta,
                          conexao.ssh_usuario, conexao.ssh_senha,
                          getattr(conexao, "rdp_dominio", ""),
                          timeout_conexao)
    else:
        s = SessaoLinux(conexao.host, conexao.ssh_porta,
                        conexao.ssh_usuario, conexao.ssh_senha,
                        timeout_conexao)
    s.abrir()
    return s
