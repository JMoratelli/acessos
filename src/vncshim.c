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
#include <string.h>
#include <stdint.h>

/* Callbacks para o lado Python. Deliberadamente simples: sem structs,
 * so escalares e ponteiros opacos. */
typedef void (*cb_atualizou)(void *ctx, int x, int y, int w, int h);
typedef void (*cb_redimensionou)(void *ctx, int w, int h);
typedef void (*cb_texto)(void *ctx, const char *texto, int tam);

typedef struct {
    rfbClient *cl;
    void *ctx;                 /* repassado de volta ao Python */
    cb_atualizou ao_atualizar;
    cb_redimensionou ao_redimensionar;
    cb_texto ao_receber_texto;
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
} Sessao;

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
    /* A lib faz free() no ponteiro devolvido, entao entregamos uma copia. */
    if (s && s->senha) return strdup(s->senha);
    return strdup("");
}

/* Autenticacao que exige USUARIO + SENHA (VeNCrypt Plain, UltraVNC
 * MSLogon). O hook GetPassword acima so cobre a autenticacao VNC classica,
 * que e somente senha. A lib libera tudo o que devolvemos aqui, entao
 * entregamos copias. */
static rfbCredential *hook_credencial(rfbClient *cl, int tipo) {
    Sessao *s = sessao_de(cl);
    if (tipo != rfbCredentialTypeUser) return NULL;   /* X509 nao tratado */

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
    if (s) {
        free(s->fb_velho);            /* este ja ninguem usa */
        s->fb_velho = cl->frameBuffer;
    }
    /* 4 bytes por pixel: casa com cairo FORMAT_RGB24, que tambem usa 32
     * bits por pixel (um byte ignorado). Assim o Cairo aponta direto para
     * esta memoria, sem conversao por quadro. */
    cl->frameBuffer = (uint8_t *)calloc((size_t)w * h, 4);
    if (!cl->frameBuffer) return FALSE;
    s->tem_sujo = 0;                  /* area suja do buffer antigo nao vale */

    if (s && s->ao_redimensionar) s->ao_redimensionar(s->ctx, w, h);
    return TRUE;
}

static void hook_cuttext(rfbClient *cl, const char *texto, int tam) {
    Sessao *s = sessao_de(cl);
    if (s && s->ao_receber_texto) s->ao_receber_texto(s->ctx, texto, tam);
}

/* ---- API exposta ao Python ---- */

Sessao *vs_criar(void *ctx,
                 cb_atualizou ao_atualizar,
                 cb_redimensionou ao_redimensionar,
                 cb_texto ao_receber_texto) {
    Sessao *s = (Sessao *)calloc(1, sizeof(Sessao));
    if (!s) return NULL;

    /* 8 bits por amostra, 3 amostras, 4 bytes por pixel = 32bpp */
    rfbClient *cl = rfbGetClient(8, 3, 4);
    if (!cl) { free(s); return NULL; }

    s->cl = cl;
    s->ctx = ctx;
    s->ao_atualizar = ao_atualizar;
    s->ao_redimensionar = ao_redimensionar;
    s->ao_receber_texto = ao_receber_texto;

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
    s->cl->serverHost = strdup(host);
    s->cl->serverPort = porta;
    /* rfbInitClient com argc=0: nao queremos que ele leia argv do processo */
    int argc = 0;
    if (!rfbInitClient(s->cl, &argc, NULL)) {
        /* Em falha, a propria lib ja liberou o rfbClient. */
        s->cl = NULL;
        s->morto = 1;
        return 0;
    }
    return 1;
}

/* Espera mensagem por ate `usecs`. >0 ha dados, 0 timeout, <0 erro. */
int vs_esperar(Sessao *s, int usecs) {
    if (!s || !s->cl || s->morto) return -1;
    return WaitForMessage(s->cl, usecs);
}

/* Processa uma mensagem. Devolve 1 se ok, 0 se a conexao caiu. */
int vs_processar(Sessao *s) {
    if (!s || !s->cl || s->morto) return 0;
    if (!HandleRFBServerMessage(s->cl)) { s->morto = 1; return 0; }
    return 1;
}

uint8_t *vs_framebuffer(Sessao *s) {
    return (s && s->cl) ? s->cl->frameBuffer : NULL;
}

int vs_largura(Sessao *s) { return (s && s->cl) ? s->cl->width : 0; }
int vs_altura(Sessao *s)  { return (s && s->cl) ? s->cl->height : 0; }
int vs_morto(Sessao *s)   { return (!s || s->morto || !s->cl) ? 1 : 0; }

void vs_ponteiro(Sessao *s, int x, int y, int botoes) {
    if (s && s->cl && !s->morto) SendPointerEvent(s->cl, x, y, botoes);
}

void vs_tecla(Sessao *s, uint32_t keysym, int pressionada) {
    if (s && s->cl && !s->morto)
        SendKeyEvent(s->cl, keysym, pressionada ? TRUE : FALSE);
}

void vs_enviar_texto(Sessao *s, const char *texto, int tam) {
    if (s && s->cl && !s->morto)
        SendClientCutText(s->cl, (char *)texto, tam);
}

void vs_destruir(Sessao *s) {
    if (!s) return;
    if (s->cl) {
        free(s->cl->frameBuffer);
        s->cl->frameBuffer = NULL;
        rfbClientCleanup(s->cl);
        s->cl = NULL;
    }
    free(s->fb_velho);
    free(s->senha);
    free(s->usuario);
    free(s);
}
