/* vncshim.c — ponte estavel entre Python (ctypes) e libvncclient.
 *
 * POR QUE ESTE ARQUIVO EXISTE
 * ---------------------------
 * A tentacao seria falar com a libvncclient direto do ctypes. Nao da, com
 * seguranca: a struct rfbClient tem blocos condicionais
 *
 *     #ifdef LIBVNCSERVER_HAVE_LIBZ      (z_stream, buffers do tight)
 *     #ifdef LIBVNCSERVER_HAVE_LIBJPEG   (estado do decodificador JPEG)
 *     #ifdef LIBVNCSERVER_HAVE_SASL      (sasl_conn_t etc)
 *     MUTEX(tlsRwMutex)                  (some quando compilada sem threads)
 *
 * cujo tamanho depende de COMO A DISTRIBUICAO COMPILOU a biblioteca. Os
 * campos que mais interessam (os hooks GotFrameBufferUpdate, GetPassword,
 * MallocFrameBuffer) ficam DEPOIS desses blocos. Replicar a struct em
 * ctypes seria chutar offsets — e offset errado nao da erro: da escrita em
 * memoria alheia e segfault muito depois, no lugar errado.
 *
 * Este shim inclui o header de verdade, entao o compilador calcula os
 * offsets corretos para a biblioteca instalada NESTA maquina. O Python so
 * enxerga as funcoes simples daqui, cuja assinatura nao muda.
 *
 * COMPILAR:  ./build.sh    (ou veja o comando la dentro)
 */

#include <rfb/rfbclient.h>
#include <stdlib.h>
/* ---- travas: CRITICAL_SECTION (Windows) ou pthread (resto), atras das
 * MESMAS quatro macros que o rdpshim usa — o resto do arquivo chama so
 * MUTEX_*, nunca a API nativa direto. */
#ifdef _WIN32
#include <windows.h>
typedef CRITICAL_SECTION vncshim_mutex_t;
#define MUTEX_INIT(m)    InitializeCriticalSection(m)
#define MUTEX_LOCK(m)    EnterCriticalSection(m)
#define MUTEX_UNLOCK(m)  LeaveCriticalSection(m)
#define MUTEX_DESTROY(m) DeleteCriticalSection(m)
#else
#include <pthread.h>
typedef pthread_mutex_t vncshim_mutex_t;
#define MUTEX_INIT(m)    pthread_mutex_init(m, NULL)
#define MUTEX_LOCK(m)    pthread_mutex_lock(m)
#define MUTEX_UNLOCK(m)  pthread_mutex_unlock(m)
#define MUTEX_DESTROY(m) pthread_mutex_destroy(m)
#endif
#include <string.h>
#include <stdint.h>
#include <stdarg.h>
#include <stdio.h>
#include <errno.h>

/* Callbacks para o lado Python. Deliberadamente simples: sem structs,
 * so escalares e ponteiros opacos. */
typedef void (*cb_atualizou)(void *ctx, int x, int y, int w, int h);
typedef void (*cb_redimensionou)(void *ctx, int w, int h);
typedef void (*cb_texto)(void *ctx, const char *texto, int tam);
typedef void (*cb_cursor)(void *ctx, int xhot, int yhot, int w, int h,
                          const uint8_t *mask);

typedef struct {
    rfbClient *cl;
    void *ctx;                 /* repassado de volta ao Python */
    cb_atualizou ao_atualizar;
    cb_redimensionou ao_redimensionar;
    cb_texto ao_receber_texto;
    cb_cursor ao_cursor;
    char *senha;               /* copia nossa; a lib libera a que devolvemos */
    char *usuario;             /* para VeNCrypt Plain / UltraVNC MSLogon */
    int morto;
    /* retangulo envolvente acumulado entre um lote e outro (ver
     * hook_update / hook_terminou) */
    int sx0, sy0, sx1, sy1;
    int tem_sujo;
    /* Buffers aposentados. Ver hook_malloc_fb: nao podemos liberar o
     * framebuffer antigo na hora, porque a superficie Cairo do lado Python
     * ainda aponta para ele. */
    uint8_t *fb_velho;

    /* ---- o framebuffer, do jeito que NOS o conhecemos ----
     *
     * fb/fb_w/fb_h sao gravados juntos, sob fb_lock, por hook_malloc_fb. A
     * leitura tem de ser pelos TRES, e nao por cl->width/cl->height/
     * cl->frameBuffer: a libvncclient grava o tamanho novo em cl ANTES de
     * chamar MallocFrameBuffer (ResizeClientBuffer), entao existe uma
     * janela em que o tamanho ja e o novo e o ponteiro ainda e o buffer
     * velho. Quem lesse os campos da lib em chamadas separadas copiava
     * (largura NOVA x altura NOVA x 4) de dentro do buffer ANTIGO — num
     * 1024x768 que vira 1920x1080 sao ~5 MB lidos alem do fim da
     * alocacao, que e alocacao grande (mmap) e vira SIGSEGV. E o mesmo
     * defeito que o rdpshim fechou com o fb_lock dele; aqui o buffer e
     * nosso, entao guardamos o par ponteiro+tamanho e acabou a duvida.
     *
     * fb aponta para o MESMO bloco que cl->frameBuffer; quem libera
     * continua sendo o rodizio do fb_velho. */
    uint8_t *fb;
    int fb_w, fb_h;
    vncshim_mutex_t fb_lock;

    /* Serializa as ESCRITAS no socket RFB. A goroutine de comandos
     * (ponteiro, tecla, clipboard) e a de rede (vs_processar, que responde
     * pedidos e manda FramebufferUpdateRequest) escreviam no mesmo socket
     * sem trava nenhuma. Ponteiro e tecla sao um write() unico de 6 a 8
     * bytes e passavam ilesos, mas SendClientCutText escreve cabecalho e
     * corpo em DUAS chamadas: colar um texto grande deixava a mensagem
     * aberta enquanto a outra thread encaixava um pedido no meio dela, o
     * servidor lia o pedido como se fosse texto e a sessao caia como
     * "conexao perdida".
     *
     * vs_esperar NAO toma esta trava, de proposito: ele fica ate 200ms
     * parado no select, e segurar a escrita por esse tempo engasgaria
     * teclado e ponteiro. */
    vncshim_mutex_t escrita_lock;

    /* ---- diagnostico da falha (espelha o que o rdpshim ja fazia) ----
     *
     * Sem isto, TODA falha virava a mesma frase no Python ("nao foi
     * possivel conectar"): host desligado, senha errada e "o servidor
     * exige usuario e voce nao informou" eram indistinguiveis. Sao
     * problemas com solucoes opostas — um pede conferir a rede, outro
     * pede digitar de novo, o terceiro pede um campo que a tela nem
     * mostrava. */
    int pediu_credencial;      /* o servidor chegou a pedir autenticacao */
    int exige_usuario;         /* o esquema negociado quer usuario+senha
                                * (VeNCrypt Plain, UltraVNC MS-Logon II) */
    int falta_usuario;         /* ...e nao tinhamos usuario para dar */
    int erro_auth;             /* a falha foi de credencial, nao de rede */
    int recusado;              /* o servidor cortou antes de perguntar
                                * (lista negra do UltraVNC, p.ex.) */
    char erro_msg[256];        /* ultima mensagem da propria libvncclient */
} Sessao;

/* ULTIMA MENSAGEM DA LIB, POR THREAD.
 *
 * A libvncclient so conta o que deu errado por printf global; o retorno de
 * rfbInitClient e um sim/nao seco. Capturamos o texto para poder mostrar o
 * motivo de verdade.
 *
 * DOIS CANAIS, e isso importa: descobrir qual usar nao foi obvio.
 * "ConnectClientToTcpAddr6: connect" (host inalcancavel) sai por
 * rfbClientErr, mas "VNC connection failed: password check failed!" —
 * justamente a mensagem mais util — sai por rfbClientLog, o canal
 * NORMAL. Capturando so o de erro, a falha de senha chegava ao Python
 * como mensagem vazia.
 *
 * __thread: cada aba tem sua propria thread de rede, e sem isso duas
 * conexoes falhando ao mesmo tempo sobrescreveriam a mensagem uma da
 * outra — a aba erraria ao explicar o proprio erro. */
static __thread char erro_thread[256];
static __thread char log_thread[256];

static void guardar(char *destino, size_t tam, const char *formato,
                    va_list ap) {
    vsnprintf(destino, tam, formato, ap);
    /* a lib manda a linha com \n; guardamos sem, para caber num rotulo */
    size_t n = strlen(destino);
    while (n && (destino[n - 1] == '\n' || destino[n - 1] == '\r'))
        destino[--n] = '\0';
}

static void hook_log_erro(const char *formato, ...) {
    va_list ap;
    va_start(ap, formato);
    guardar(erro_thread, sizeof(erro_thread), formato, ap);
    va_end(ap);
    /* erro continua indo para o stderr do app, como sempre foi */
    fprintf(stderr, "[vnc] %s\n", erro_thread);
}

static void hook_log_normal(const char *formato, ...) {
    va_list ap;
    va_start(ap, formato);
    guardar(log_thread, sizeof(log_thread), formato, ap);
    va_end(ap);
    /* o log normal e verboso (uma linha por etapa do handshake) e antes
     * ia inteiro para o stderr do app; agora so sai com VS_LOG=1 */
    if (getenv("VS_LOG")) fprintf(stderr, "[vnc] %s\n", log_thread);
}

/* A lib chama "falha de autenticacao" de varios jeitos; o Python so
 * precisa saber que foi credencial. */
static int parece_falha_de_auth(const char *m) {
    if (!m || !*m) return 0;
    return strstr(m, "password") || strstr(m, "Password")
        || strstr(m, "authentication") || strstr(m, "Authentication")
        || strstr(m, "auth failed") || strstr(m, "credential");
}

/* O SERVIDOR RECUSOU A CONEXAO ANTES DE PEDIR CREDENCIAL.
 *
 * Caso concreto, visto num PDV com UltraVNC: apos algumas senhas erradas
 * ele poe o IP de origem numa lista negra e passa a responder o
 * handshake de versao com ZERO esquemas de seguranca + "Your connection
 * has been rejected". Nao e senha errada (nem chegou a perguntar) e nao e
 * rede (o TCP abriu).
 *
 * A distincao importa por um motivo pratico: o tempo de bloqueio do
 * UltraVNC DOBRA a cada reincidencia. Reconectar sozinho nesse estado —
 * que e o que o app fazia, por achar que era queda comum — mantem a
 * maquina inacessivel por cada vez mais tempo. */
static int parece_recusa_do_servidor(const char *m) {
    if (!m || !*m) return 0;
    /* "rejected" sozinho NAO serve: a mensagem de senha errada do
     * UltraVNC e "authentication rejected", que e o oposto disto — ali o
     * servidor perguntou e nao gostou da resposta. Casar por "rejected"
     * fazia senha errada ser classificada como bloqueio, e o app parava
     * de oferecer nova tentativa. O que caracteriza a recusa previa e a
     * conexao ser barrada, nao a credencial. */
    return strstr(m, "connection has been rejected")
        || strstr(m, "too many")
        || strstr(m, "blacklist") || strstr(m, "Blacklist")
        || strstr(m, "security types is ZERO");
}

/* A libvncclient nos devolve o rfbClient nos callbacks; guardamos o Sessao*
 * no clientData dela para achar o caminho de volta. */
static const char *CHAVE = "vncshim";

static Sessao *sessao_de(rfbClient *cl) {
    return (Sessao *)rfbClientGetClientData(cl, (void *)CHAVE);
}

/* ---- hooks chamados pela libvncclient (na thread de rede) ---- */

/* ACUMULA E NAO AVISA. Chamado uma vez por RETANGULO — um unico lote do
 * servidor pode trazer centenas.
 *
 * Avisar o Python aqui era o erro: cada chamada de volta precisa adquirir o
 * GIL, e com varias sessoes abertas as threads de rede passavam o tempo
 * disputando o GIL entre si. A thread principal ficava sem janela para
 * processar eventos e a interface inteira congelava — sem travar processo
 * nenhum, apenas sem responder a clique. Agrupar do lado Python nao
 * adiantava: quando o Python roda, o GIL ja foi tomado.
 *
 * Aqui so unimos os retangulos; quem avisa e o hook_terminou, uma vez por
 * lote. */
static void hook_update(rfbClient *cl, int x, int y, int w, int h) {
    Sessao *s = sessao_de(cl);
    if (!s) return;
    if (!s->tem_sujo) {
        s->sx0 = x; s->sy0 = y; s->sx1 = x + w; s->sy1 = y + h;
        s->tem_sujo = 1;
        return;
    }
    if (x < s->sx0) s->sx0 = x;
    if (y < s->sy0) s->sy0 = y;
    if (x + w > s->sx1) s->sx1 = x + w;
    if (y + h > s->sy1) s->sy1 = y + h;
}

/* Fim de um lote de atualizacao: agora sim, UMA volta ao Python. */
static void hook_terminou(rfbClient *cl) {
    Sessao *s = sessao_de(cl);
    if (!s || !s->tem_sujo) return;
    s->tem_sujo = 0;
    if (s->ao_atualizar)
        s->ao_atualizar(s->ctx, s->sx0, s->sy0,
                        s->sx1 - s->sx0, s->sy1 - s->sy0);
}

static char *hook_senha(rfbClient *cl) {
    Sessao *s = sessao_de(cl);
    if (s) {
        s->pediu_credencial = 1;
        if (!s->senha || !*s->senha) s->erro_auth = 1;
    }
    /* A lib faz free() no ponteiro devolvido, entao entregamos uma copia. */
    if (s && s->senha) return strdup(s->senha);
    return strdup("");
}

/* Autenticacao que exige USUARIO + SENHA (VeNCrypt Plain, UltraVNC
 * MS-Logon II). O hook GetPassword acima so cobre a autenticacao VNC
 * classica, que e somente senha. A lib libera tudo o que devolvemos aqui,
 * entao entregamos copias.
 *
 * MARCAR "EXIGE USUARIO" E O PONTO DESTA FUNCAO, tanto quanto devolver a
 * credencial. Antes, sem usuario definido, mandavamos string VAZIA em
 * silencio: o servidor recusava e o app dizia "nao foi possivel conectar",
 * como se fosse rede. So que a tela nem tinha campo de usuario para
 * preencher — o operador nao tinha como consertar o que nao sabia que
 * faltava. Com a marca, o Python sabe que precisa perguntar, e pergunta
 * SO quando o servidor de fato exigiu. */
static rfbCredential *hook_credencial(rfbClient *cl, int tipo) {
    Sessao *s = sessao_de(cl);

    if (tipo == rfbCredentialTypeX509) {
        /* Continuamos sem tratar: exige CA/certificado do cliente, que o
         * app nao coleta. Antes isto era um "return NULL" mudo e a
         * conexao morria sem explicacao nenhuma; agora ao menos diz o
         * que aconteceu. */
        if (s) {
            s->pediu_credencial = 1;
            s->erro_auth = 1;
            snprintf(s->erro_msg, sizeof(s->erro_msg),
                     "o servidor exige certificado X509, que este cliente "
                     "não trata");
        }
        return NULL;
    }
    if (tipo != rfbCredentialTypeUser) {
        if (s) {
            s->pediu_credencial = 1;
            s->erro_auth = 1;
            snprintf(s->erro_msg, sizeof(s->erro_msg),
                     "o servidor pediu um tipo de credencial que este "
                     "cliente não trata (%d)", tipo);
        }
        return NULL;
    }

    if (s) {
        s->pediu_credencial = 1;
        s->exige_usuario = 1;
        if (!s->usuario || !*s->usuario) {
            s->falta_usuario = 1;
            s->erro_auth = 1;
        }
        if (!s->senha || !*s->senha) s->erro_auth = 1;
    }

    rfbCredential *c = (rfbCredential *)calloc(1, sizeof(rfbCredential));
    if (!c) return NULL;
    c->userCredential.username = strdup((s && s->usuario) ? s->usuario : "");
    c->userCredential.password = strdup((s && s->senha) ? s->senha : "");
    return c;
}

static rfbBool hook_malloc_fb(rfbClient *cl) {
    Sessao *s = sessao_de(cl);
    int w = cl->width, h = cl->height;

    /* NAO LIBERAR O BUFFER ANTIGO AQUI.
     *
     * Este hook roda na THREAD DE REDE, mas a superficie Cairo do lado
     * Python ainda aponta para o buffer atual e a THREAD PRINCIPAL pode
     * estar pintando a partir dele neste exato instante. Um free() aqui e
     * uso-apos-liberacao classico: corrompe o heap silenciosamente e o
     * sintoma aparece longe da causa — no caso observado, a janela parava
     * de repintar com a aplicacao aparentemente saudavel (loop de eventos
     * rodando, Python ocioso, nenhuma thread bloqueada).
     *
     * Guardamos o antigo e so o liberamos na PROXIMA realocacao — quando o
     * Python ja trocou de superficie ha muito tempo — ou na destruicao.
     * Um buffer extra de memoria e barato perto de um heap corrompido.
     */
    /* 4 bytes por pixel: casa com cairo FORMAT_RGB24, que tambem usa 32
     * bits por pixel (um byte ignorado). Assim o Cairo aponta direto para
     * esta memoria, sem conversao por quadro.
     *
     * Alocado ANTES de tomar a trava: calloc de alguns MB nao precisa
     * segurar quem esta copiando o quadro. */
    uint8_t *novo = (uint8_t *)calloc((size_t)w * h, 4);
    if (!novo) return FALSE;

    /* "if (s)" aqui tambem: o resto da funcao ja tratava s como podendo
     * ser NULL, menos as atribuicoes — bastava o clientData nao estar no
     * lugar para virar escrita em ponteiro nulo dentro da thread de rede. */
    if (s) {
        MUTEX_LOCK(&s->fb_lock);
        free(s->fb_velho);            /* este ja ninguem usa */
        s->fb_velho = cl->frameBuffer;
        cl->frameBuffer = novo;
        /* o trio anda JUNTO: ponteiro e tamanho trocam sob a mesma trava */
        s->fb = novo;
        s->fb_w = w;
        s->fb_h = h;
        s->tem_sujo = 0;              /* area suja do buffer antigo nao vale */
        MUTEX_UNLOCK(&s->fb_lock);
        /* o aviso sai FORA da trava: ele atravessa para o Go, e o Go
         * responde capturando quadro — com a trava na mao, isso seria
         * abraco mortal. */
        if (s->ao_redimensionar) s->ao_redimensionar(s->ctx, w, h);
    } else {
        cl->frameBuffer = novo;
    }
    return TRUE;
}

static void hook_cuttext(rfbClient *cl, const char *texto, int tam) {
    Sessao *s = sessao_de(cl);
    if (s && s->ao_receber_texto) s->ao_receber_texto(s->ctx, texto, tam);
}

/* O SERVIDOR MANDOU UM NOVO FORMATO DE CURSOR.
 *
 * useRemoteCursor (ver vs_criar) ja faz o servidor mandar isto em vez de
 * carimbar o ponteiro no framebuffer; so nao estava sendo usado pra nada.
 * cl->rcMask tem width*height bytes, um por pixel — e so o que o lado Go
 * precisa pra adivinhar a FORMA (seta / texto / ocupado). cl->rcSource
 * (as cores) nao interessa aqui: nao desenhamos o bitmap, so aproximamos
 * um pointer.Cursor local do Gio.
 *
 * CUIDADO COM O VALOR DE CADA BYTE: e 0 ou 1, NAO 0/255 (este comentario
 * afirmava 0/255 e estava errado). Quem preenche e HandleCursorShape, em
 * libvncclient/cursor.c, expandindo o bitmask de transparencia bit a bit
 * com `>> b & 1` — entao o maximo e 1, em qualquer servidor VNC.
 *
 * O lado Go espera ALFA de verdade, que e o que o RDP entrega, e corta a
 * silhueta em alfaOpaco=128 (cmd/acessos/cursorforma.go). Repassando o
 * rcMask cru, TODO pixel ficava abaixo do corte: a silhueta saia vazia,
 * medirForma devolvia falso e classificar caia em CursorDefault. Ou
 * seja, o cursor remoto do VNC NUNCA aparecia — sempre a seta padrao, e
 * sem erro nenhum no caminho pra denunciar. Achado em 2026-09-20
 * capturando mascaras de um servidor de verdade (cmd/cursorcap).
 *
 * Por isso a expansao para 0/255 abaixo: quem consome os dois protocolos
 * recebe a mesma coisa, que e o que classificar ja supunha. */
static void hook_cursor(rfbClient *cl, int xhot, int yhot, int w, int h,
                        int bytesPerPixel) {
    (void)bytesPerPixel;
    Sessao *s = sessao_de(cl);
    if (getenv("VS_LOG"))
        fprintf(stderr, "[vnc] cursor: %dx%d hot=(%d,%d) rcMask=%p\n",
                w, h, xhot, yhot, (void *)cl->rcMask);
    if (!s || !s->ao_cursor) return;
    if (cl->rcMask && w > 0 && h > 0) {
        /* Buffer proprio porque o rcMask e da libvncclient e sera
         * reescrito no proximo cursor. Liberar logo depois da chamada e
         * seguro: o lado Go COPIA antes de entregar (ver goAoCursor, em
         * vnc.go) — mesma garantia de que o irmao RDP depende. */
        size_t n = (size_t)w * (size_t)h;
        uint8_t *alfa = (uint8_t *)malloc(n);
        if (!alfa) {
            /* Sem memoria pra copia, o cursor padrao e melhor do que
             * mandar o formato errado adiante. */
            s->ao_cursor(s->ctx, 0, 0, 0, 0, NULL);
            return;
        }
        for (size_t i = 0; i < n; i++) alfa[i] = cl->rcMask[i] ? 255 : 0;
        s->ao_cursor(s->ctx, xhot, yhot, w, h, alfa);
        free(alfa);
    } else {
        /* Cursor sem máscara (o servidor escondeu o ponteiro, ou mandou
         * tamanho zero): avisar assim mesmo. Calar aqui deixava o cursor
         * anterior valendo para sempre — mesmo defeito do lado RDP. */
        s->ao_cursor(s->ctx, 0, 0, 0, 0, NULL);
    }
}

/* ---- API exposta ao Python ---- */

Sessao *vs_criar(void *ctx,
                 cb_atualizou ao_atualizar,
                 cb_redimensionou ao_redimensionar,
                 cb_texto ao_receber_texto,
                 cb_cursor ao_cursor) {
    Sessao *s = (Sessao *)calloc(1, sizeof(Sessao));
    if (!s) return NULL;
    MUTEX_INIT(&s->fb_lock);
    MUTEX_INIT(&s->escrita_lock);

    /* Desvia os dois canais da lib para os nossos buffers por thread. Sao
     * variaveis GLOBAIS da libvncclient, entao bastaria uma vez — mas
     * custa nada reafirmar, e assim nao depende da ordem de criacao das
     * abas. */
    rfbClientErr = hook_log_erro;
    rfbClientLog = hook_log_normal;

    /* 8 bits por amostra, 3 amostras, 4 bytes por pixel = 32bpp */
    rfbClient *cl = rfbGetClient(8, 3, 4);
    if (!cl) { free(s); return NULL; }

    s->cl = cl;
    s->ctx = ctx;
    s->ao_atualizar = ao_atualizar;
    s->ao_redimensionar = ao_redimensionar;
    s->ao_receber_texto = ao_receber_texto;
    s->ao_cursor = ao_cursor;

    /* Formato de pixel casado com cairo FORMAT_RGB24 em little-endian:
     * na memoria os bytes saem B,G,R,X — que e o que o Cairo espera. */
    cl->format.bitsPerPixel = 32;
    cl->format.depth = 24;
    cl->format.bigEndian = FALSE;
    cl->format.trueColour = TRUE;
    cl->format.redMax = 255;
    cl->format.greenMax = 255;
    cl->format.blueMax = 255;
    /* ORDEM DOS CANAIS.
     *
     * O cairo FORMAT_RGB24 em little-endian quer os bytes B,G,R,X na
     * memoria, o que corresponde ao valor de pixel R<<16 | G<<8 | B —
     * portanto redShift=16, greenShift=8, blueShift=0. E tambem o formato
     * nativo da maioria dos servidores (o PDV reporta exatamente
     * "shift red 16 green 8 blue 0").
     *
     * CUIDADO AO "CORRIGIR" ISTO: uma versao anterior usava redShift=0
     * porque foi calibrada contra um servidor de teste cujo formato NATIVO
     * era shift red 0 — em alguns caminhos o decodificador tight escreve na
     * ordem nativa do servidor e ignora o pedido, entao o teste apontou o
     * inverso do certo. Se as cores sairem trocadas (vermelho <-> azul),
     * rode com VS_BGR=1 para inverter em tempo de execucao e comparar,
     * em vez de recompilar no escuro.
     */
    cl->format.redShift = 16;
    cl->format.greenShift = 8;
    cl->format.blueShift = 0;
    if (getenv("VS_BGR")) {
        cl->format.redShift = 0;
        cl->format.blueShift = 16;
    }

    cl->GotFrameBufferUpdate = hook_update;
    cl->GetPassword = hook_senha;
    cl->MallocFrameBuffer = hook_malloc_fb;
    cl->GotXCutText = hook_cuttext;
    cl->FinishedFrameBufferUpdate = hook_terminou;
    cl->GetCredential = hook_credencial;
    cl->GotCursorShape = hook_cursor;

    /* CURSOR: pedir os pseudo-encodings de cursor faz o servidor mandar o
     * ponteiro SEPARADO, em vez de pinta-lo dentro do framebuffer. Sem
     * isto o cursor remoto vem "carimbado" na imagem e, somado ao cursor
     * local do sistema, produz o efeito de dois ponteiros (um seguindo o
     * outro com o atraso da rede).
     *
     * Como nao desenhamos a forma remota, o resultado pratico e: some o
     * ponteiro carimbado e fica so o cursor local, que se move sem
     * depender da rede. */
    cl->appData.useRemoteCursor = TRUE;

    cl->canHandleNewFBSize = TRUE;
    cl->connectTimeout = 3;      /* nao ficar 2 min pendurado em host morto */

    rfbClientSetClientData(cl, (void *)CHAVE, s);
    return s;
}

void vs_definir_senha(Sessao *s, const char *senha) {
    if (!s) return;
    free(s->senha);
    s->senha = senha ? strdup(senha) : NULL;
}

void vs_definir_usuario(Sessao *s, const char *usuario) {
    if (!s) return;
    free(s->usuario);
    s->usuario = usuario ? strdup(usuario) : NULL;
}

/* Conecta. Devolve 1 em sucesso. BLOQUEIA — chame de uma thread. */
int vs_conectar(Sessao *s, const char *host, int porta) {
    if (!s || !s->cl) return 0;

    /* free ANTES de substituir: o rfbGetClient ja deixa um serverHost
     * alocado aqui, e sobrescrever direto vazava aquela alocacao a cada
     * tentativa de conexao (reconexao automatica repete isto sem fim). */
    free(s->cl->serverHost);
    s->cl->serverHost = strdup(host);
    s->cl->serverPort = porta;

    /* estado de diagnostico zerado por TENTATIVA, nao por sessao: numa
     * reconexao o motivo da falha anterior nao pode contaminar esta */
    s->pediu_credencial = 0;
    s->exige_usuario = 0;
    s->falta_usuario = 0;
    s->erro_auth = 0;
    s->recusado = 0;
    s->erro_msg[0] = '\0';
    erro_thread[0] = '\0';
    log_thread[0] = '\0';

    /* rfbInitClient com argc=0: nao queremos que ele leia argv do processo */
    int argc = 0;
    if (!rfbInitClient(s->cl, &argc, NULL)) {
        /* Em falha, a propria lib ja liberou o rfbClient. */
        s->cl = NULL;
        s->morto = 1;

        /* MOTIVO, em ordem de qualidade: o que os hooks de credencial
         * escreveram (mais especifico), depois a mensagem do canal
         * normal quando ela fala de autenticacao ("password check
         * failed!"), e por fim o erro cru de rede. */
        if (!s->erro_msg[0]) {
            if (parece_falha_de_auth(log_thread))
                snprintf(s->erro_msg, sizeof(s->erro_msg), "%s", log_thread);
            else if (erro_thread[0])
                snprintf(s->erro_msg, sizeof(s->erro_msg), "%s", erro_thread);
            else if (log_thread[0])
                snprintf(s->erro_msg, sizeof(s->erro_msg), "%s", log_thread);
        }

        /* ORDEM: "pediu credencial?" decide primeiro.
         *
         * O SINAL MAIS CONFIAVEL de "foi credencial" nao e a mensagem, e o
         * fato de o servidor ter PEDIDO uma: se ele chegou a chamar nosso
         * hook e a conexao morreu depois disso, o handshake de rede tinha
         * dado certo — o que falhou foi o login. So quando o servidor NAO
         * perguntou nada e ainda assim barrou e que se trata de recusa
         * previa (lista negra).
         *
         * Inverter estes dois ja custou um falso positivo: "authentication
         * rejected" (senha errada) caiu como bloqueio, e a aba parava de
         * oferecer nova tentativa por causa de uma palavra em comum. */
        if (s->pediu_credencial || parece_falha_de_auth(log_thread)) {
            s->erro_auth = 1;
        } else if (parece_recusa_do_servidor(log_thread)
                   || parece_recusa_do_servidor(erro_thread)) {
            s->recusado = 1;
        }
        return 0;
    }
    return 1;
}

/* ---- diagnostico da ultima tentativa (ver os campos em Sessao) ---- */

int vs_erro_auth(Sessao *s)       { return s ? s->erro_auth : 0; }
int vs_falta_usuario(Sessao *s)   { return s ? s->falta_usuario : 0; }
int vs_exige_usuario(Sessao *s)   { return s ? s->exige_usuario : 0; }
int vs_pediu_credencial(Sessao *s) { return s ? s->pediu_credencial : 0; }
int vs_recusado(Sessao *s)        { return s ? s->recusado : 0; }

const char *vs_erro_msg(Sessao *s) {
    return (s && s->erro_msg[0]) ? s->erro_msg : "";
}

/* Espera mensagem por ate `usecs`. >0 ha dados, 0 timeout, <0 erro.
 *
 * RETRY EM EINTR: o runtime do Go usa sinais (SIGURG) para preemptar
 * goroutines, e isso interrompe o select() de dentro do WaitForMessage com
 * EINTR — a libvncclient nao repete a chamada sozinha, entao sem este loop
 * cada preempcao virava um "erro aguardando dados do servidor" falso,
 * derrubando conexoes saudaveis (visto na pratica: sessao caindo a cada
 * poucos segundos mesmo sem nenhuma instabilidade real de rede). */
int vs_esperar(Sessao *s, int usecs) {
    if (!s || !s->cl || s->morto) return -1;
    for (;;) {
        int n = WaitForMessage(s->cl, usecs);
        if (n < 0 && errno == EINTR) continue;
        return n;
    }
}

/* Processa uma mensagem. Devolve 1 se ok, 0 se a conexao caiu.
 *
 * Toma a escrita_lock porque tratar uma mensagem ESCREVE no socket (a lib
 * responde pedidos e manda o FramebufferUpdateRequest do proximo lote), e
 * isso corria com as escritas da goroutine de comandos. */
int vs_processar(Sessao *s) {
    if (!s || !s->cl || s->morto) return 0;
    MUTEX_LOCK(&s->escrita_lock);
    rfbBool ok = s->morto ? FALSE : HandleRFBServerMessage(s->cl);
    MUTEX_UNLOCK(&s->escrita_lock);
    if (!ok) { s->morto = 1; return 0; }
    return 1;
}

/* Trava o framebuffer e devolve o ponteiro para leitura DIRETA, com a
 * geometria junto, ou NULL se ainda nao ha framebuffer (e ai a trava nao
 * fica segurada). Quem recebe nao-NULL TEM de chamar vs_destravar_quadro.
 *
 * Nao devolve copia: o unico consumidor ja copia para um buffer proprio, e
 * a copia intermediaria era um segundo buffer de tela cheia por quadro.
 *
 * SUBSTITUI ler vs_largura/vs_altura/vs_framebuffer em sequencia: as tres
 * chamadas nao sao atomicas entre si, e a libvncclient grava o tamanho novo
 * ANTES de trocar o buffer, entao existe uma janela em que o tamanho ja e o
 * novo e o ponteiro ainda e o velho — copiar com o par errado le alem do
 * fim da alocacao. Aqui o trio sai sob a mesma trava, e vem do que NOS
 * alocamos (ver os campos fb/fb_w/fb_h). Mesmo desenho do
 * rs_capturar_quadro, no rdpshim. */
const uint8_t *vs_travar_quadro(Sessao *s, int *w_out, int *h_out) {
    if (!s) return NULL;

    MUTEX_LOCK(&s->fb_lock);
    uint8_t *fb = s->fb;
    int w = s->fb_w, h = s->fb_h;
    if (!fb || w <= 0 || h <= 0) {
        MUTEX_UNLOCK(&s->fb_lock);
        return NULL;
    }
    if (w_out) *w_out = w;
    if (h_out) *h_out = h;
    return fb;                    /* a trava SEGUE na mao do chamador */
}

void vs_destravar_quadro(Sessao *s) {
    if (s) MUTEX_UNLOCK(&s->fb_lock);
}

int vs_morto(Sessao *s)   { return (!s || s->morto || !s->cl) ? 1 : 0; }

/* As tres escritas abaixo sao serializadas entre si E contra o
 * vs_processar: ver escrita_lock na Sessao. */
void vs_ponteiro(Sessao *s, int x, int y, int botoes) {
    if (!s || !s->cl || s->morto) return;
    MUTEX_LOCK(&s->escrita_lock);
    if (!s->morto) SendPointerEvent(s->cl, x, y, botoes);
    MUTEX_UNLOCK(&s->escrita_lock);
}

void vs_tecla(Sessao *s, uint32_t keysym, int pressionada) {
    if (!s || !s->cl || s->morto) return;
    MUTEX_LOCK(&s->escrita_lock);
    if (!s->morto) SendKeyEvent(s->cl, keysym, pressionada ? TRUE : FALSE);
    MUTEX_UNLOCK(&s->escrita_lock);
}

void vs_enviar_texto(Sessao *s, const char *texto, int tam) {
    if (!s || !s->cl || s->morto) return;
    MUTEX_LOCK(&s->escrita_lock);
    if (!s->morto) SendClientCutText(s->cl, (char *)texto, tam);
    MUTEX_UNLOCK(&s->escrita_lock);
}

void vs_destruir(Sessao *s) {
    if (!s) return;
    MUTEX_LOCK(&s->fb_lock);
    s->fb = NULL;                 /* ninguem captura mais a partir daqui */
    s->fb_w = s->fb_h = 0;
    if (s->cl) {
        free(s->cl->frameBuffer);
        s->cl->frameBuffer = NULL;
        rfbClientCleanup(s->cl);
        s->cl = NULL;
    }
    MUTEX_UNLOCK(&s->fb_lock);
    MUTEX_DESTROY(&s->fb_lock);
    MUTEX_DESTROY(&s->escrita_lock);
    free(s->fb_velho);
    free(s->senha);
    free(s->usuario);
    free(s);
}
