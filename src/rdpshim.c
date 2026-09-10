/* rdpshim.c — ponte estavel entre Python (ctypes) e libfreerdp3.
 *
 * POR QUE ESTE ARQUIVO EXISTE
 * ---------------------------
 * Mesma razao do vncshim.c: falar com a libfreerdp direto do ctypes exigiria
 * replicar structs como rdpSettings/rdpContext/rdpGdi, cujo layout depende de
 * flags de compilacao e de versao. Este shim inclui os headers de verdade,
 * entao o compilador calcula os offsets certos para a lib desta maquina. O
 * Python so enxerga as funcoes simples daqui.
 *
 * BASE DO QUE FOI PORTADO
 * ------------------------
 * A logica de conexao, o pipeline grafico (gdi_init) e o tratamento de
 * certificado foram portados do gtk-frdp (frdp-session.c), que ja usavamos
 * embutido via GObject Introspection. Aqui e a MESMA logica, sem a casca
 * GObject/GTK: nos falamos com libfreerdp diretamente, do jeito que o
 * Remmina e o proprio gtk-frdp fazem por baixo.
 *
 * O laco de eventos usa freerdp_get_event_handles / WaitForMultipleObjects /
 * freerdp_check_event_handles, disparado por uma thread Python (a mesma
 * ideia do vs_esperar/vs_processar do VNC): esperar aqui, processar aqui,
 * avisar o Python so quando ha area suja para desenhar.
 *
 * CERTIFICADO
 * -----------
 * SEM IgnoreCertificate — de proposito. Essa flag faz a libfreerdp aceitar
 * qualquer certificado calada, sem chamar nenhum dos hooks abaixo; e um
 * bypass total, nao uma pergunta. O pedido aqui foi o oposto: uma
 * confirmacao estilo SSH ("a autenticidade do host nao pode ser
 * verificada..."), tanto para certificado NOVO quanto para MUDADO — a
 * decisao de aceitar e SEMPRE devolvida ao Python (hook_certificado_novo/
 * hook_certificado_mudou chamam ao_certificado_novo/ao_certificado_mudou e
 * esperam a resposta antes de prosseguir o handshake). Testado contra host
 * real removendo o .pem salvo em ~/.config/freerdp/server: o callback
 * dispara normalmente sem a flag.
 *
 * O armazenamento do certificado aceito fica a cargo da propria libfreerdp,
 * no mesmo diretorio (~/.config/freerdp/server), do jeito que o gtk-frdp
 * ja deixava.
 *
 * COMPILAR: veja build.sh / instalar.sh — precisa de freerdp3 (pkg-config
 * freerdp3 freerdp-client3 winpr3).
 */

#include <freerdp/freerdp.h>
#include <freerdp/gdi/gdi.h>
#include <freerdp/gdi/gfx.h>
#include <freerdp/channels/rdpgfx.h>
#include <freerdp/input.h>
#include <freerdp/scancode.h>
#include <freerdp/locale/keyboard.h>
#include <freerdp/client/cmdline.h>
#include <freerdp/addin.h>
#include <freerdp/client/channels.h>
#include <freerdp/client/cliprdr.h>
#include <freerdp/client/disp.h>
#include <freerdp/channels/channels.h>
#include <freerdp/channels/cliprdr.h>
#include <freerdp/channels/disp.h>
#include <winpr/wtypes.h>
#include <winpr/synch.h>
#include <winpr/user.h>       /* CF_TEXT, CF_UNICODETEXT */
#include <winpr/input.h>      /* GetVirtualKeyCodeFromKeycode e afins */

#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stdio.h>
#include <iconv.h>
#include <pthread.h>

/* ---- callbacks para o lado Python (escalares/ponteiros opacos, como no
 * vncshim) ---- */
typedef void (*cb_atualizou)(void *ctx, int x, int y, int w, int h);
typedef void (*cb_redimensionou)(void *ctx, int w, int h);
typedef void (*cb_desconectou)(void *ctx, const char *motivo);
/* devolve 1 (aceitar e guardar), 2 (aceitar so nesta sessao) ou 0 (recusar) —
 * mesma convencao do pVerifyCertificateEx/pVerifyChangedCertificateEx */
typedef int (*cb_certificado_novo)(void *ctx, const char *host, uint16_t porta,
                                   const char *nome_comum, const char *assunto,
                                   const char *emissor, const char *digital,
                                   uint32_t flags);
typedef int (*cb_certificado_mudou)(void *ctx, const char *host, uint16_t porta,
                                    const char *nome_comum, const char *assunto,
                                    const char *emissor, const char *digital_novo,
                                    const char *assunto_antigo,
                                    const char *emissor_antigo,
                                    const char *digital_antigo, uint32_t flags);
/* texto chegou do clipboard REMOTO (utf-8, sem terminador incluso no tam) */
typedef void (*cb_clip_texto)(void *ctx, const char *utf8, int tam);
/* canal Display Control pronto para receber pedidos de resize (depois de
 * DisplayControlCaps) — antes disso rs_pedir_resize so devolve 0 calado */
typedef void (*cb_disp_pronto)(void *ctx);

/* nosso rdpContext estendido — o padrao da lib e crescer freerdp_context com
 * campos proprios no final, e usar ContextSize/ContextNew para isso */
typedef struct {
    rdpContext ctx;
    void *sessao;               /* aponta de volta para a Sessao* dona */
} RdpCtx;

typedef struct {
    freerdp *inst;
    void *pyctx;                 /* repassado de volta ao Python */
    cb_atualizou ao_atualizar;
    cb_redimensionou ao_redimensionar;
    cb_desconectou ao_desconectar;
    cb_certificado_novo ao_certificado_novo;
    cb_certificado_mudou ao_certificado_mudou;
    cb_clip_texto ao_clip_texto;
    cb_disp_pronto ao_disp_pronto;

    char *host;
    int porta;
    char *usuario;
    char *senha;
    char *dominio;

    int conectado;
    int erro_auth;               /* 1 se a falha foi de credenciais */
    char erro_msg[256];

    /* clipboard: canal CLIPRDR, so texto (CF_UNICODETEXT). NULL enquanto o
     * canal nao conectou (servidor pode nao anunciar RedirectClipboard). */
    CliprdrClientContext *cliprdr;
    pthread_mutex_t clip_lock;
    /* texto do HOST, pronto para responder um ServerFormatDataRequest —
     * guardado ja convertido para UTF-16LE, formato que o CF_UNICODETEXT
     * exige na fiacao do protocolo. */
    uint8_t *clip_local_utf16;
    size_t clip_local_utf16_bytes;

    /* Display Control: redimensionamento dinamico. NULL enquanto o canal
     * nao conectou (servidor pode nao suportar). SendMonitorLayout so pode
     * ser chamado depois que DisplayControlCaps informar os limites. */
    DispClientContext *disp;
    int disp_caps_ok;
    uint32_t disp_max_monitores, disp_fator_a, disp_fator_b;
} Sessao;

static Sessao *sessao_de(freerdp *inst) {
    RdpCtx *rc = (RdpCtx *)inst->context;
    return rc ? (Sessao *)rc->sessao : NULL;
}

/* ---- pipeline grafico: BeginPaint/EndPaint acumulam a area suja de um
 * lote e avisam o Python UMA vez por lote, igual ao hook_terminou do VNC */
static BOOL hook_begin_paint(rdpContext *context) {
    rdpGdi *gdi = context->gdi;
    gdi->primary->hdc->hwnd->invalid->null = TRUE;
    gdi->primary->hdc->hwnd->ninvalid = 0;
    return TRUE;
}

static BOOL hook_end_paint(rdpContext *context) {
    rdpGdi *gdi = context->gdi;
    Sessao *s = sessao_de(context->instance);
    if (!s) return TRUE;
    if (gdi->primary->hdc->hwnd->invalid->null) return TRUE;
    int x = gdi->primary->hdc->hwnd->invalid->x;
    int y = gdi->primary->hdc->hwnd->invalid->y;
    int w = gdi->primary->hdc->hwnd->invalid->w;
    int h = gdi->primary->hdc->hwnd->invalid->h;
    if (s->ao_atualizar) s->ao_atualizar(s->pyctx, x, y, w, h);
    return TRUE;
}

static BOOL hook_desktop_resize(rdpContext *context) {
    Sessao *s = sessao_de(context->instance);
    rdpGdi *gdi = context->gdi;
    UINT32 w = freerdp_settings_get_uint32(context->settings, FreeRDP_DesktopWidth);
    UINT32 h = freerdp_settings_get_uint32(context->settings, FreeRDP_DesktopHeight);
    if (!gdi_resize(gdi, w, h)) return FALSE;
    if (s && s->ao_redimensionar) s->ao_redimensionar(s->pyctx, (int)w, (int)h);
    return TRUE;
}

/* ---- certificado ---- */
static DWORD hook_certificado_novo(freerdp *inst, const char *host, UINT16 porta,
                                   const char *nome_comum, const char *assunto,
                                   const char *emissor, const char *digital,
                                   DWORD flags) {
    Sessao *s = sessao_de(inst);
    if (s && s->ao_certificado_novo)
        return (DWORD)s->ao_certificado_novo(s->pyctx, host, porta, nome_comum,
                                            assunto, emissor, digital,
                                            (uint32_t)flags);
    return 1;      /* sem gancho definido: aceita e guarda, como o gtk-frdp */
}

static DWORD hook_certificado_mudou(freerdp *inst, const char *host, UINT16 porta,
                                    const char *nome_comum, const char *assunto,
                                    const char *emissor, const char *digital_novo,
                                    const char *assunto_antigo,
                                    const char *emissor_antigo,
                                    const char *digital_antigo, DWORD flags) {
    Sessao *s = sessao_de(inst);
    if (s && s->ao_certificado_mudou)
        return (DWORD)s->ao_certificado_mudou(s->pyctx, host, porta, nome_comum,
                                             assunto, emissor, digital_novo,
                                             assunto_antigo, emissor_antigo,
                                             digital_antigo, (uint32_t)flags);
    return 0;      /* sem gancho definido: por seguranca, RECUSA a mudanca */
}

static BOOL hook_authenticate_ex(freerdp *inst, char **usuario, char **senha,
                                 char **dominio, rdp_auth_reason motivo) {
    Sessao *s = sessao_de(inst);
    (void)motivo;
    /* Credenciais ja foram passadas antes do connect (mesma licao do VNC e
     * do gtk-frdp: definir depois nao adianta). Chegar aqui significa que
     * ou faltou informar, ou o servidor rejeitou — nos dois casos so
     * repetimos o que ja tinhamos, sem inventar dialogo aqui: quem decide
     * se tenta de novo e o lado Python. */
    if (s) {
        free(*usuario); *usuario = strdup(s->usuario ? s->usuario : "");
        free(*senha);   *senha   = strdup(s->senha ? s->senha : "");
        free(*dominio); *dominio = strdup(s->dominio ? s->dominio : "");
        if (!s->usuario || !*s->usuario || !s->senha) s->erro_auth = 1;
    }
    return TRUE;
}

/* ---- clipboard (canal CLIPRDR): so texto, formato CF_UNICODETEXT.
 *
 * Fluxo portado de frdp-channel-clipboard.c (gtk-frdp), so que sem a parte
 * de arquivos/imagens (fora de escopo aqui — se precisar, e o proximo passo
 * natural, usando o mesmo canal). Duas direcoes:
 *
 *   REMOTO -> HOST: o servidor manda ServerFormatList quando o clipboard de
 *   la muda. Respondemos ClientFormatListResponse e, se tem CF_UNICODETEXT
 *   na lista, pedimos os dados na hora (ClientFormatDataRequest) em vez de
 *   esperar o operador colar — mais simples que a troca "pull" que o
 *   gtk-frdp faz via GtkClipboard, ao custo de buscar texto que talvez
 *   nunca seja colado. Aceitavel: e so texto, poucos KB.
 *
 *   HOST -> REMOTO: rs_clipboard_definir_texto (chamada pelo Python quando o
 *   clipboard do host muda) guarda o texto e manda ClientFormatList
 *   anunciando CF_UNICODETEXT disponivel. Quando o servidor efetivamente
 *   pedir (ServerFormatDataRequest), respondemos com o texto guardado.
 */

/* Conversoes UTF-16LE <-> UTF-8. CF_UNICODETEXT trafega em UTF-16LE com
 * terminador nulo — o resto do mundo (GTK) fala UTF-8. */
static char *conv_utf16le_para_utf8(const uint8_t *dados, size_t bytes, size_t *tam_saida) {
    iconv_t cd = iconv_open("UTF-8", "UTF-16LE");
    if (cd == (iconv_t)-1) return NULL;
    size_t in_restam = bytes;
    size_t out_cap = bytes * 2 + 4;      /* UTF-8 nunca excede 2x o UTF-16 aqui */
    char *saida = (char *)malloc(out_cap);
    if (!saida) { iconv_close(cd); return NULL; }
    char *in_ptr = (char *)dados;
    char *out_ptr = saida;
    size_t out_restam = out_cap;
    size_t r = iconv(cd, &in_ptr, &in_restam, &out_ptr, &out_restam);
    iconv_close(cd);
    if (r == (size_t)-1) { free(saida); return NULL; }
    *tam_saida = out_cap - out_restam;
    return saida;
}

static uint8_t *conv_utf8_para_utf16le(const char *utf8, size_t tam, size_t *bytes_saida) {
    iconv_t cd = iconv_open("UTF-16LE", "UTF-8");
    if (cd == (iconv_t)-1) return NULL;
    size_t in_restam = tam;
    size_t out_cap = tam * 4 + 4;
    uint8_t *saida = (uint8_t *)malloc(out_cap);
    if (!saida) { iconv_close(cd); return NULL; }
    char *in_ptr = (char *)utf8;
    char *out_ptr = (char *)saida;
    size_t out_restam = out_cap;
    size_t r = iconv(cd, &in_ptr, &in_restam, &out_ptr, &out_restam);
    iconv_close(cd);
    if (r == (size_t)-1) { free(saida); return NULL; }
    size_t usado = out_cap - out_restam;
    /* terminador nulo UTF-16 (dois bytes) — CF_UNICODETEXT exige */
    saida = (uint8_t *)realloc(saida, usado + 2);
    saida[usado] = 0; saida[usado + 1] = 0;
    *bytes_saida = usado + 2;
    return saida;
}

static UINT hook_clip_monitor_ready(CliprdrClientContext *ctx,
                                    const CLIPRDR_MONITOR_READY *mr) {
    (void)mr;
    Sessao *s = (Sessao *)ctx->custom;

    CLIPRDR_GENERAL_CAPABILITY_SET gcs;
    memset(&gcs, 0, sizeof(gcs));
    gcs.capabilitySetType = CB_CAPSTYPE_GENERAL;
    gcs.capabilitySetLength = CB_CAPSTYPE_GENERAL_LEN;
    gcs.version = CB_CAPS_VERSION_2;
    gcs.generalFlags = CB_USE_LONG_FORMAT_NAMES;
    CLIPRDR_CAPABILITIES caps;
    memset(&caps, 0, sizeof(caps));
    caps.cCapabilitiesSets = 1;
    caps.capabilitySets = (CLIPRDR_CAPABILITY_SET *)&gcs;
    ctx->ClientCapabilities(ctx, &caps);

    CLIPRDR_FORMAT fmt;
    memset(&fmt, 0, sizeof(fmt));
    fmt.formatId = CF_UNICODETEXT;
    CLIPRDR_FORMAT_LIST fl;
    memset(&fl, 0, sizeof(fl));
    fl.common.msgType = CB_FORMAT_LIST;
    if (s) {
        pthread_mutex_lock(&s->clip_lock);
        int temos_texto = s->clip_local_utf16 != NULL;
        pthread_mutex_unlock(&s->clip_lock);
        if (temos_texto) {
            fl.numFormats = 1;
            fl.formats = &fmt;
        }
    }
    ctx->ClientFormatList(ctx, &fl);
    return CHANNEL_RC_OK;
}

static UINT hook_clip_server_format_list(CliprdrClientContext *ctx,
                                         const CLIPRDR_FORMAT_LIST *fl) {
    int tem_texto = 0;
    for (UINT32 i = 0; i < fl->numFormats; i++) {
        if (fl->formats[i].formatId == CF_UNICODETEXT
            || fl->formats[i].formatId == CF_TEXT) {
            tem_texto = 1;
            break;
        }
    }

    CLIPRDR_FORMAT_LIST_RESPONSE resp;
    memset(&resp, 0, sizeof(resp));
    resp.common.msgType = CB_FORMAT_LIST_RESPONSE;
    resp.common.msgFlags = CB_RESPONSE_OK;
    ctx->ClientFormatListResponse(ctx, &resp);

    if (tem_texto) {
        CLIPRDR_FORMAT_DATA_REQUEST req;
        memset(&req, 0, sizeof(req));
        req.common.msgType = CB_FORMAT_DATA_REQUEST;
        req.requestedFormatId = CF_UNICODETEXT;
        ctx->ClientFormatDataRequest(ctx, &req);
    }
    return CHANNEL_RC_OK;
}

static UINT hook_clip_server_format_list_response(
    CliprdrClientContext *ctx, const CLIPRDR_FORMAT_LIST_RESPONSE *resp) {
    (void)ctx; (void)resp;
    return CHANNEL_RC_OK;
}

/* servidor esta pedindo O QUE O HOST TEM (operador colando no remoto) */
static UINT hook_clip_server_format_data_request(
    CliprdrClientContext *ctx, const CLIPRDR_FORMAT_DATA_REQUEST *req) {
    Sessao *s = (Sessao *)ctx->custom;
    CLIPRDR_FORMAT_DATA_RESPONSE resp;
    memset(&resp, 0, sizeof(resp));
    resp.common.msgType = CB_FORMAT_DATA_RESPONSE;

    if (!s || req->requestedFormatId != CF_UNICODETEXT) {
        resp.common.msgFlags = CB_RESPONSE_FAIL;
        return ctx->ClientFormatDataResponse(ctx, &resp);
    }

    pthread_mutex_lock(&s->clip_lock);
    uint8_t *copia = NULL;
    size_t tam = 0;
    if (s->clip_local_utf16) {
        tam = s->clip_local_utf16_bytes;
        copia = (uint8_t *)malloc(tam);
        if (copia) memcpy(copia, s->clip_local_utf16, tam);
    }
    pthread_mutex_unlock(&s->clip_lock);

    if (!copia) {
        resp.common.msgFlags = CB_RESPONSE_FAIL;
        UINT r = ctx->ClientFormatDataResponse(ctx, &resp);
        return r;
    }
    resp.common.msgFlags = CB_RESPONSE_OK;
    resp.common.dataLen = (UINT32)tam;
    resp.requestedFormatData = copia;
    UINT r = ctx->ClientFormatDataResponse(ctx, &resp);
    free(copia);
    return r;
}

/* resposta do servidor ao QUE O HOST PEDIU (operador colando no host) */
static UINT hook_clip_server_format_data_response(
    CliprdrClientContext *ctx, const CLIPRDR_FORMAT_DATA_RESPONSE *resp) {
    Sessao *s = (Sessao *)ctx->custom;
    if (!s || !s->ao_clip_texto) return CHANNEL_RC_OK;
    if (!(resp->common.msgFlags & CB_RESPONSE_OK)) return CHANNEL_RC_OK;

    size_t tam_utf8 = 0;
    char *utf8 = conv_utf16le_para_utf8(resp->requestedFormatData,
                                        resp->common.dataLen, &tam_utf8);
    if (!utf8) return CHANNEL_RC_OK;
    /* remove o \0 final que o CF_UNICODETEXT sempre carrega, se sobrou */
    while (tam_utf8 > 0 && utf8[tam_utf8 - 1] == '\0') tam_utf8--;
    s->ao_clip_texto(s->pyctx, utf8, (int)tam_utf8);
    free(utf8);
    return CHANNEL_RC_OK;
}

/* Servidor informa os limites de resolucao aceitos. SendMonitorLayout so
 * pode ser chamado depois deste callback — chamar antes e um "canal ainda
 * nao pronto" silencioso do lado do servidor. */
static UINT hook_disp_caps(DispClientContext *ctx, UINT32 max_monitores,
                           UINT32 fator_a, UINT32 fator_b) {
    Sessao *s = (Sessao *)ctx->custom;
    if (s) {
        s->disp_max_monitores = max_monitores;
        s->disp_fator_a = fator_a;
        s->disp_fator_b = fator_b;
        s->disp_caps_ok = 1;
        if (s->ao_disp_pronto) s->ao_disp_pronto(s->pyctx);
    }
    return CHANNEL_RC_OK;
}

static void hook_canal_conectou(void *context,
                                const ChannelConnectedEventArgs *e) {
    RdpCtx *rc = (RdpCtx *)context;
    Sessao *s = (Sessao *)rc->sessao;

    if (strcmp(e->name, DISP_DVC_CHANNEL_NAME) == 0) {
        DispClientContext *disp = (DispClientContext *)e->pInterface;
        disp->custom = s;
        disp->DisplayControlCaps = hook_disp_caps;
        s->disp = disp;
        s->disp_caps_ok = 0;
        return;
    }

    if (strcmp(e->name, CLIPRDR_SVC_CHANNEL_NAME) == 0) {
        CliprdrClientContext *cliprdr = (CliprdrClientContext *)e->pInterface;
        cliprdr->custom = s;
        cliprdr->MonitorReady = hook_clip_monitor_ready;
        cliprdr->ServerFormatList = hook_clip_server_format_list;
        cliprdr->ServerFormatListResponse = hook_clip_server_format_list_response;
        cliprdr->ServerFormatDataRequest = hook_clip_server_format_data_request;
        cliprdr->ServerFormatDataResponse = hook_clip_server_format_data_response;
        s->cliprdr = cliprdr;
        return;
    }

    if (strcmp(e->name, RDPGFX_DVC_CHANNEL_NAME) == 0) {
        /* SEM ISTO A TELA FICA BRANCA PARA SEMPRE apos o login.
         *
         * Com FreeRDP_SupportGraphicsPipeline=TRUE (ja ligado em
         * rs_conectar) o servidor manda os quadros pelo canal RDPGFX em vez
         * do caminho classico de bitmap update — e SEM gdi_graphics_
         * pipeline_init a gdi nunca aprende a decodificar esse canal: os
         * hooks BeginPaint/EndPaint (que avisam o Python via ao_atualizar)
         * so disparam para o caminho classico, entao nenhuma atualizacao
         * real chega. E exatamente o papel que
         * frdp_on_channel_connected_event_handler cumpre no gtk-frdp. */
        rdpContext *ctx = (rdpContext *)context;
        gdi_graphics_pipeline_init(ctx->gdi, (RdpgfxClientContext *)e->pInterface);
        return;
    }
}

static void hook_canal_desconectou(void *context,
                                   const ChannelDisconnectedEventArgs *e) {
    RdpCtx *rc = (RdpCtx *)context;
    Sessao *s = (Sessao *)rc->sessao;

    if (strcmp(e->name, DISP_DVC_CHANNEL_NAME) == 0) {
        s->disp = NULL;
        s->disp_caps_ok = 0;
        return;
    }
    if (strcmp(e->name, CLIPRDR_SVC_CHANNEL_NAME) == 0) {
        s->cliprdr = NULL;
        return;
    }
    if (strcmp(e->name, RDPGFX_DVC_CHANNEL_NAME) == 0) {
        rdpContext *ctx = (rdpContext *)context;
        gdi_graphics_pipeline_uninit(ctx->gdi, (RdpgfxClientContext *)e->pInterface);
        return;
    }
}

/* Sem isto o canal CLIPRDR (e qualquer outro) nunca e carregado: no
 * FreeRDP3 o carregamento dos plugins de canal acontece via este callback,
 * nao mais dentro do PreConnect (foi o que o gtk-frdp tambem descobriu —
 * ver o #ifdef HAVE_FREERDP3 em torno de LoadChannels no frdp-session.c). */
static BOOL hook_load_channels(freerdp *inst) {
    return freerdp_client_load_addins(inst->context->channels,
                                      inst->context->settings);
}

/* ---- pre/post connect: prepara o pipeline grafico (gdi_init), do jeito
 * que o gtk-frdp faz em frdp_post_connect ---- */
static BOOL hook_pre_connect(freerdp *inst) {
    rdpSettings *settings = inst->context->settings;
    BYTE *ordens = freerdp_settings_get_pointer_writable(settings, FreeRDP_OrderSupport);
    if (ordens) {
        memset(ordens, 0, 32);
        ordens[NEG_DSTBLT_INDEX] = TRUE;
        ordens[NEG_PATBLT_INDEX] = TRUE;
        ordens[NEG_SCRBLT_INDEX] = TRUE;
        ordens[NEG_OPAQUE_RECT_INDEX] = TRUE;
        ordens[NEG_MULTIOPAQUERECT_INDEX] = TRUE;
        ordens[NEG_LINETO_INDEX] = TRUE;
        ordens[NEG_POLYLINE_INDEX] = TRUE;
        ordens[NEG_MEMBLT_INDEX] = TRUE;
        ordens[NEG_MEMBLT_V2_INDEX] = TRUE;
        ordens[NEG_GLYPH_INDEX_INDEX] = TRUE;
        ordens[NEG_FAST_INDEX_INDEX] = TRUE;
    }
    PubSub_SubscribeChannelConnected(inst->context->pubSub, hook_canal_conectou);
    PubSub_SubscribeChannelDisconnected(inst->context->pubSub, hook_canal_desconectou);
    return TRUE;
}

static BOOL hook_post_connect(freerdp *inst) {
    rdpContext *context = inst->context;
    if (!gdi_init(inst, PIXEL_FORMAT_BGRX32)) return FALSE;
    context->update->BeginPaint = hook_begin_paint;
    context->update->EndPaint = hook_end_paint;
    context->update->DesktopResize = hook_desktop_resize;
    /* primeiro quadro: framebuffer ja existe, avisa o tamanho de uma vez */
    Sessao *s = sessao_de(inst);
    if (s && s->ao_redimensionar)
        s->ao_redimensionar(s->pyctx, context->gdi->width, context->gdi->height);
    return TRUE;
}

static void hook_post_disconnect(freerdp *inst) {
    if (inst && inst->context) gdi_free(inst);
}

static BOOL hook_context_new(freerdp *inst, rdpContext *context) {
    (void)inst; (void)context;
    return TRUE;
}

static void hook_context_free(freerdp *inst, rdpContext *context) {
    (void)inst; (void)context;
}

/* ---- API exposta ao Python ---- */

Sessao *rs_criar(void *pyctx,
                 cb_atualizou ao_atualizar,
                 cb_redimensionou ao_redimensionar,
                 cb_desconectou ao_desconectar,
                 cb_certificado_novo ao_certificado_novo,
                 cb_certificado_mudou ao_certificado_mudou,
                 cb_clip_texto ao_clip_texto,
                 cb_disp_pronto ao_disp_pronto) {
    Sessao *s = (Sessao *)calloc(1, sizeof(Sessao));
    if (!s) return NULL;
    pthread_mutex_init(&s->clip_lock, NULL);

    /* Registra o provedor de addins ESTATICOS (compilados dentro da propria
     * freerdp-client3), para o hook_load_channels achar cliprdr/rdpdr/disp
     * etc sem precisar de .so de plugin instalados a parte no sistema.
     * Sem isto o freerdp_client_load_addins tenta abrir plugins dinamicos
     * que nao existem aqui, e a conexao falha com
     *     ERRCONNECT_PRE_CONNECT_FAILED
     * mesmo com o nosso PreConnect tendo retornado TRUE — o gtk-frdp faz a
     * mesma chamada em frdp_session_init_freerdp. */
    freerdp_register_addin_provider(freerdp_channels_load_static_addin_entry, 0);

    freerdp *inst = freerdp_new();
    if (!inst) { pthread_mutex_destroy(&s->clip_lock); free(s); return NULL; }

    inst->ContextSize = sizeof(RdpCtx);
    inst->ContextNew = hook_context_new;
    inst->ContextFree = hook_context_free;
    inst->PreConnect = hook_pre_connect;
    inst->PostConnect = hook_post_connect;
    inst->PostDisconnect = hook_post_disconnect;
    inst->LoadChannels = hook_load_channels;
    inst->AuthenticateEx = hook_authenticate_ex;
    inst->VerifyCertificateEx = hook_certificado_novo;
    inst->VerifyChangedCertificateEx = hook_certificado_mudou;

    if (!freerdp_context_new(inst)) {
        freerdp_free(inst);
        pthread_mutex_destroy(&s->clip_lock);
        free(s);
        return NULL;
    }
    ((RdpCtx *)inst->context)->sessao = s;

    s->inst = inst;
    s->pyctx = pyctx;
    s->ao_atualizar = ao_atualizar;
    s->ao_redimensionar = ao_redimensionar;
    s->ao_desconectar = ao_desconectar;
    s->ao_certificado_novo = ao_certificado_novo;
    s->ao_certificado_mudou = ao_certificado_mudou;
    s->ao_clip_texto = ao_clip_texto;
    s->ao_disp_pronto = ao_disp_pronto;
    return s;
}

/* Chamada pelo Python quando o clipboard do HOST muda (texto). Guarda em
 * UTF-16LE, ja pronto para responder um ServerFormatDataRequest, e anuncia
 * ao servidor que ha algo novo — mesmo passo que send_client_format_list
 * faz no gtk-frdp. Sem canal conectado ainda, so guarda: o anuncio sai
 * pelo hook_clip_monitor_ready assim que o canal abrir. */
void rs_clipboard_definir_texto(Sessao *s, const char *utf8, int tam) {
    if (!s) return;
    size_t bytes = 0;
    uint8_t *utf16 = (utf8 && tam > 0)
        ? conv_utf8_para_utf16le(utf8, (size_t)tam, &bytes) : NULL;

    pthread_mutex_lock(&s->clip_lock);
    free(s->clip_local_utf16);
    s->clip_local_utf16 = utf16;
    s->clip_local_utf16_bytes = bytes;
    pthread_mutex_unlock(&s->clip_lock);

    if (s->cliprdr && utf16) {
        CLIPRDR_FORMAT fmt;
        memset(&fmt, 0, sizeof(fmt));
        fmt.formatId = CF_UNICODETEXT;
        CLIPRDR_FORMAT_LIST fl;
        memset(&fl, 0, sizeof(fl));
        fl.common.msgType = CB_FORMAT_LIST;
        fl.numFormats = 1;
        fl.formats = &fmt;
        s->cliprdr->ClientFormatList(s->cliprdr, &fl);
    }
}

/* Pede ao servidor que redimensione a area remota — canal Display Control,
 * portado de frdp_channel_display_control_resize_display (gtk-frdp).
 * Devolve 1 se o pedido foi enviado, 0 se ainda nao da (canal nao
 * conectado, caps nao chegaram, ou area maior que o permitido). */
int rs_pedir_resize(Sessao *s, int largura, int altura) {
    if (!s || !s->disp || !s->disp_caps_ok) return 0;

    uint32_t lw = (uint32_t)largura, lh = (uint32_t)altura;
    if (lw < DISPLAY_CONTROL_MIN_MONITOR_WIDTH) lw = DISPLAY_CONTROL_MIN_MONITOR_WIDTH;
    if (lw > DISPLAY_CONTROL_MAX_MONITOR_WIDTH) lw = DISPLAY_CONTROL_MAX_MONITOR_WIDTH;
    if (lh < DISPLAY_CONTROL_MIN_MONITOR_HEIGHT) lh = DISPLAY_CONTROL_MIN_MONITOR_HEIGHT;
    if (lh > DISPLAY_CONTROL_MAX_MONITOR_HEIGHT) lh = DISPLAY_CONTROL_MAX_MONITOR_HEIGHT;
    if (lw % 2) lw--;      /* largura impar confunde alguns servidores */

    /* limite de area que o servidor aceita, informado no DisplayControlCaps */
    if ((uint64_t)lw * lh >
        (uint64_t)s->disp_max_monitores * s->disp_fator_a * s->disp_fator_b) {
        return 0;
    }

    DISPLAY_CONTROL_MONITOR_LAYOUT layout;
    memset(&layout, 0, sizeof(layout));
    layout.Flags = DISPLAY_CONTROL_MONITOR_PRIMARY;
    layout.Width = lw;
    layout.Height = lh;
    layout.Orientation = 0;              /* ORIENTATION_LANDSCAPE (DMDO_DEFAULT) */
    layout.DesktopScaleFactor = 100;
    layout.DeviceScaleFactor = 100;

    return s->disp->SendMonitorLayout(s->disp, 1, &layout) == CHANNEL_RC_OK;
}

void rs_definir_credenciais(Sessao *s, const char *usuario, const char *senha,
                            const char *dominio) {
    if (!s) return;
    free(s->usuario); s->usuario = usuario ? strdup(usuario) : NULL;
    free(s->senha);   s->senha   = senha ? strdup(senha) : NULL;
    free(s->dominio); s->dominio = dominio ? strdup(dominio) : NULL;
}

/* Conecta. Devolve 1 em sucesso. BLOQUEIA — chame de uma thread, igual ao
 * vs_conectar do VNC. */
int rs_conectar(Sessao *s, const char *host, int porta) {
    if (!s || !s->inst) return 0;
    rdpSettings *settings = s->inst->context->settings;

    free(s->host); s->host = strdup(host);
    s->porta = porta;

    freerdp_settings_set_string(settings, FreeRDP_ServerHostname, host);
    freerdp_settings_set_uint32(settings, FreeRDP_ServerPort, (UINT32)porta);
    if (s->usuario) freerdp_settings_set_string(settings, FreeRDP_Username, s->usuario);
    if (s->senha)   freerdp_settings_set_string(settings, FreeRDP_Password, s->senha);
    if (s->dominio) freerdp_settings_set_string(settings, FreeRDP_Domain, s->dominio);

    freerdp_settings_set_bool(settings, FreeRDP_RdpSecurity, TRUE);
    freerdp_settings_set_bool(settings, FreeRDP_TlsSecurity, TRUE);
    freerdp_settings_set_bool(settings, FreeRDP_NlaSecurity, TRUE);
    freerdp_settings_set_uint32(settings, FreeRDP_EncryptionMethods,
                                ENCRYPTION_METHOD_40BIT | ENCRYPTION_METHOD_128BIT
                                | ENCRYPTION_METHOD_FIPS);
    freerdp_settings_set_uint32(settings, FreeRDP_EncryptionLevel,
                                ENCRYPTION_LEVEL_CLIENT_COMPATIBLE);
    freerdp_settings_set_bool(settings, FreeRDP_NegotiateSecurityLayer, TRUE);

    freerdp_settings_set_bool(settings, FreeRDP_DesktopResize, TRUE);
    freerdp_settings_set_bool(settings, FreeRDP_DynamicResolutionUpdate, TRUE);
    freerdp_settings_set_bool(settings, FreeRDP_SupportDisplayControl, TRUE);
    freerdp_settings_set_bool(settings, FreeRDP_RemoteFxCodec, TRUE);
    freerdp_settings_set_bool(settings, FreeRDP_SupportGraphicsPipeline, TRUE);
    /* canal CLIPRDR — sem isto o LoadChannels nem tenta carregar o plugin
     * de clipboard e hook_canal_conectou nunca dispara para "cliprdr". */
    freerdp_settings_set_bool(settings, FreeRDP_RedirectClipboard, TRUE);
    freerdp_settings_set_uint32(settings, FreeRDP_ColorDepth, 32);
    freerdp_settings_set_bool(settings, FreeRDP_AllowFontSmoothing, TRUE);
    freerdp_settings_set_bool(settings, FreeRDP_AllowUnanouncedOrdersFromServer, TRUE);

    /* IgnoreCertificate NAO e usado de proposito: essa flag faz a
     * libfreerdp pular a verificacao inteira e NUNCA chamar
     * VerifyCertificateEx/VerifyChangedCertificateEx — bypass total, sem
     * pedir nada a ninguem. O pedido explicito aqui foi o oposto: uma
     * caixa de confirmacao estilo SSH, tanto para certificado novo quanto
     * mudado. Os dois callbacks (hook_certificado_novo/hook_certificado_
     * mudou, ja registrados como VerifyCertificateEx/VerifyChangedCertifi
     * cateEx acima) disparam normalmente sem esta flag — testado contra
     * host real removendo o .pem salvo. */

    /* layout de teclado: pt-BR ABNT2 por padrao, com escape por env var —
     * mesma solucao que ja tinhamos para o gtk-frdp dentro do Flatpak */
    {
        const char *env = getenv("ACESSOS_KBD_LAYOUT");
        uint32_t layout = 0x00000416;
        if (env && *env) layout = (uint32_t)strtoul(env, NULL, 0);
        freerdp_settings_set_uint32(settings, FreeRDP_KeyboardLayout, layout);
        freerdp_settings_set_uint32(settings, FreeRDP_KeyboardType, 7);
        freerdp_settings_set_uint32(settings, FreeRDP_KeyboardSubType, 2);
    }

    if (!freerdp_connect(s->inst)) {
        UINT32 codigo = freerdp_get_last_error(s->inst->context);
        const char *msg = freerdp_get_last_error_string(codigo);
        snprintf(s->erro_msg, sizeof(s->erro_msg), "%s", msg ? msg : "falha ao conectar");
        switch (codigo) {
            case FREERDP_ERROR_AUTHENTICATION_FAILED:
            case FREERDP_ERROR_CONNECT_NO_OR_MISSING_CREDENTIALS:
            case FREERDP_ERROR_CONNECT_LOGON_FAILURE:
            case FREERDP_ERROR_CONNECT_ACCOUNT_EXPIRED:
                s->erro_auth = 1;
                break;
            default:
                break;
        }
        return 0;
    }
    s->conectado = 1;
    return 1;
}

/* Espera ate `ms` por atividade nos handles do FreeRDP. >0 ha o que
 * processar, 0 timeout, <0 erro/desconectou. */
int rs_esperar(Sessao *s, int ms) {
    if (!s || !s->inst || !s->conectado) return -1;
    HANDLE handles[64];
    DWORD n = freerdp_get_event_handles(s->inst->context, handles, 64);
    if (n == 0) return -1;
    DWORD status = WaitForMultipleObjects(n, handles, FALSE, (DWORD)ms);
    if (status == WAIT_TIMEOUT) return 0;
    if (status == WAIT_FAILED) return -1;
    return 1;
}

/* Processa eventos pendentes. Devolve 1 se ok, 0 se a conexao caiu. */
int rs_processar(Sessao *s) {
    if (!s || !s->inst || !s->conectado) return 0;
    if (freerdp_shall_disconnect_context(s->inst->context)) {
        s->conectado = 0;
        return 0;
    }
    if (!freerdp_check_event_handles(s->inst->context)) {
        if (freerdp_get_last_error(s->inst->context) != FREERDP_ERROR_SUCCESS) {
            s->conectado = 0;
            return 0;
        }
    }
    return 1;
}

uint8_t *rs_framebuffer(Sessao *s) {
    if (!s || !s->inst || !s->inst->context || !s->inst->context->gdi) return NULL;
    return s->inst->context->gdi->primary_buffer;
}

int rs_largura(Sessao *s) {
    return (s && s->inst && s->inst->context && s->inst->context->gdi)
        ? s->inst->context->gdi->width : 0;
}

int rs_altura(Sessao *s) {
    return (s && s->inst && s->inst->context && s->inst->context->gdi)
        ? s->inst->context->gdi->height : 0;
}

int rs_stride(Sessao *s) {
    return (s && s->inst && s->inst->context && s->inst->context->gdi)
        ? s->inst->context->gdi->stride : 0;
}

int rs_morto(Sessao *s) { return (!s || !s->conectado) ? 1 : 0; }
int rs_erro_auth(Sessao *s) { return s ? s->erro_auth : 0; }
const char *rs_erro_msg(Sessao *s) { return s ? s->erro_msg : ""; }

/* ---- entrada: ponteiro e teclado ---- */

void rs_ponteiro_mover(Sessao *s, int x, int y) {
    if (!s || !s->inst || !s->conectado) return;
    if (x < 0) x = 0;
    if (y < 0) y = 0;
    freerdp_input_send_mouse_event(s->inst->context->input, PTR_FLAGS_MOVE,
                                   (UINT16)x, (UINT16)y);
}

/* botao: 1=esquerdo 2=meio 3=direito; pressionado 1/0 */
void rs_ponteiro_botao(Sessao *s, int x, int y, int botao, int pressionado) {
    if (!s || !s->inst || !s->conectado) return;
    if (x < 0) x = 0;
    if (y < 0) y = 0;
    UINT16 flags = pressionado ? PTR_FLAGS_DOWN : 0;
    switch (botao) {
        case 1: flags |= PTR_FLAGS_BUTTON1; break;
        case 2: flags |= PTR_FLAGS_BUTTON3; break;   /* RDP: 2=direito, 3=meio */
        case 3: flags |= PTR_FLAGS_BUTTON2; break;
        default: return;
    }
    freerdp_input_send_mouse_event(s->inst->context->input, flags,
                                   (UINT16)x, (UINT16)y);
}

/* roda: eixo 0=vertical 1=horizontal; passos positivo/negativo */
void rs_ponteiro_roda(Sessao *s, int eixo, int passos) {
    if (!s || !s->inst || !s->conectado || passos == 0) return;
    UINT16 flags = eixo ? PTR_FLAGS_HWHEEL : PTR_FLAGS_WHEEL;
    UINT16 valor = (UINT16)(abs(passos) * 0x78 > 255 ? 255 : abs(passos) * 0x78);
    if (passos < 0) flags |= PTR_FLAGS_WHEEL_NEGATIVE | ((~valor + 1) & 0x01FF);
    else flags |= valor & 0x01FF;
    freerdp_input_send_mouse_event(s->inst->context->input, flags, 0, 0);
}

/* keycode: codigo X11 (hardware_keycode do GDK, ja no padrao X11 mesmo sob
 * Wayland).
 *
 * freerdp_keyboard_get_rdp_scancode_from_x11_keycode (o caminho "legado" que
 * o gtk-frdp usa em versoes < 3.11) esta MARCADA deprecated desde a 3.11.0
 * com a nota "implement yourself in client" — na pratica, nesta build
 * (3.31.1) ela nao mapeia mais nada de verdade e nenhuma tecla chegava do
 * outro lado (confirmado testando contra host real: mouse funcionava,
 * teclado nao).
 *
 * O caminho novo, que o proprio gtk-frdp ja teria migrado para builds
 * recentes (ver o ramo HAVE_FREERDP_3_11_0 em frdp_session_send_key):
 *   keycode X11 -> GetVirtualKeyCodeFromKeycode(..., WINPR_KEYCODE_TYPE_XKB)
 *              -> GetVirtualScanCodeFromVirtualKeyCode(..., IBM_ENHANCED)
 *              -> freerdp_input_send_keyboard_event_ex
 * SEM o deslocamento de 8 que o caminho legado precisava: aquele offset
 * compensava o FreeRDP caindo em codigos estilo evdev quando nao achava
 * DISPLAY (caso do Flatpak sandboxed); aqui os dois lados (GDK e WinPR/XKB)
 * ja falam o mesmo dialeto de keycode X11, sem tradução extra. */
void rs_tecla(Sessao *s, uint32_t keycode_x11, int pressionada) {
    if (!s || !s->inst || !s->conectado) return;

    DWORD vk = GetVirtualKeyCodeFromKeycode(keycode_x11, WINPR_KEYCODE_TYPE_XKB);
    if (vk == 0) return;
    DWORD scancode = GetVirtualScanCodeFromVirtualKeyCode(vk, WINPR_KBD_TYPE_IBM_ENHANCED);
    if (scancode == RDP_SCANCODE_UNKNOWN) return;

    freerdp_input_send_keyboard_event_ex(s->inst->context->input,
                                         pressionada ? TRUE : FALSE, FALSE,
                                         scancode);
}

void rs_destruir(Sessao *s) {
    if (!s) return;
    if (s->inst) {
        if (s->conectado) freerdp_disconnect(s->inst);
        /* freerdp_free ja libera o contexto por dentro (mesmo padrao do
         * gtk-frdp em idle_close: so freerdp_free, sem context_free
         * separado). Chamar os dois seria liberar duas vezes o mesmo
         * bloco. */
        freerdp_free(s->inst);
    }
    free(s->host);
    free(s->usuario);
    free(s->senha);
    free(s->dominio);
    free(s->clip_local_utf16);
    pthread_mutex_destroy(&s->clip_lock);
    free(s);
}
