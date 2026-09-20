//go:build linux
/* A restrição não é decoração: fora do Linux só sobram arquivos Go sem
 * cgo neste pacote, e aí o Go recusa o pacote inteiro com "C source files
 * not allowed when not using cgo or SWIG". */

/* grab_wayland.c — dois recursos que o Gio nao oferece, resolvidos falando
 * Wayland direto com os ponteiros crus que app.WaylandViewEvent expoe:
 *
 *   1. INIBIR ATALHOS DO COMPOSITOR (keyboard-shortcuts-inhibit-unstable-v1)
 *      enquanto a janela remota tem foco. Sem isto, teclas que o compositor
 *      reserva pra si (visto na pratica: "w" disparando troca de janela)
 *      nunca chegavam ao app.
 *
 *   2. LER TECLADO DIRETO (wl_keyboard + xkbcommon), contornando o
 *      key.Event do Gio. Nesta pilha (Wayland/KDE) o Gio nao preenche
 *      Modifiers e nunca entrega o "soltou" de Ctrl/Alt/Shift/Super
 *      sozinhos — so o "apertou". Isso prendia esses modificadores
 *      pressionados pra sempre do lado remoto. Lendo aqui (do jeito que
 *      todo cliente Wayland de verdade faz, FreeRDP/UWAC incluso) temos
 *      apertou/soltou corretos pra toda tecla, com o keysym ja calculado
 *      pelo layout ativo — sem precisar do hack de maiuscula/minuscula.
 *
 * DISPATCH: nao chamamos wl_display_dispatch por conta propria (exceto o
 * roundtrip inicial, que so roda uma vez, na configuracao). Depois disso, os
 * eventos dos nossos objetos (registry/seat/keyboard) chegam pela MESMA fila
 * default que o Gio ja fica lendo continuamente para as janelas dele —
 * dispatch processa TODOS os objetos da fila, nao so os do dono original.
 * Ler o socket por conta propria correria com a leitura do Gio. */

#include <wayland-client.h>
#include <xkbcommon/xkbcommon.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
#include "keyboard-shortcuts-inhibit-unstable-v1-client-protocol.h"
#include "grab_wayland.h"

/* repeticao de tecla, do jeito que wl_keyboard obriga: o protocolo so
 * entrega apertou/soltou crus, repetir enquanto segura e trabalho do
 * cliente (todo terminal Wayland de verdade faz isso — foot, alacritty).
 * Sem taxa/atraso nenhum a chegar do compositor (repeat_info), a seta
 * segurada mandava UM byte e parava ai: exatamente o "segurar seta pra
 * baixo nao repete" relatado no terminal SSH. */
typedef struct {
    pthread_t thread;
    int thread_criada;
    pthread_mutex_t m;
    int ativo;             /* 1 enquanto uma tecla repetivel esta pressionada */
    int parar;             /* sinaliza a thread pra encerrar, em grab_parar */
    uint32_t keysym, keycode;
    int64_t geracao;       /* muda a cada apertou/soltou, invalida ciclo antigo */
    int32_t atraso_ms, intervalo_ms;
} EstadoRepeticao;

/* mimes que aceitamos AO LER o clipboard alheio, em ordem de preferencia —
 * mais amplo que o do Gio (que so tem a variante SEM hifen de utf8, e no
 * KDE isso nunca batia com nada). */
static const char *MIME_LEITURA[] = {
    "text/plain;charset=utf-8",
    "text/plain;charset=utf8",
    "UTF8_STRING",
    "text/plain",
    "STRING",
    "TEXT",
};
#define N_MIME_LEITURA (sizeof(MIME_LEITURA) / sizeof(MIME_LEITURA[0]))

/* mimes que OFERECEMOS ao anunciar nosso proprio clipboard */
static const char *MIME_ESCRITA[] = {
    "text/plain;charset=utf-8",
    "UTF8_STRING",
    "text/plain",
    "STRING",
    "TEXT",
};
#define N_MIME_ESCRITA (sizeof(MIME_ESCRITA) / sizeof(MIME_ESCRITA[0]))

#define MAX_MIMES_OFERTA 32

struct Grab {
    struct wl_display *display;
    struct wl_surface *superficie;
    struct wl_registry *registry;
    struct wl_seat *seat;
    struct wl_keyboard *keyboard;
    struct zwp_keyboard_shortcuts_inhibit_manager_v1 *manager;
    struct zwp_keyboard_shortcuts_inhibitor_v1 *inibidor;

    struct xkb_context *xkb_ctx;
    struct xkb_keymap *xkb_keymap;
    struct xkb_state *xkb_state;

    cb_tecla ao_teclar;
    uint32_t ultimo_serial; /* de um key press real — exigido por set_selection */
    EstadoRepeticao rep;

    /* Quais teclas estao EM BAIXO agora, por keycode evdev (0..255).
     *
     * Perder o foco com uma tecla pressionada nunca gerava o "soltou": o
     * compositor manda o `leave` e o release seguinte vai para quem ganhou
     * o foco, nunca para nos. Do lado remoto o modificador fica preso — no
     * RDP tudo o que se digita depois vira atalho, e no SSH e pior, porque
     * a aba guarda o proprio estado e passa a mandar caracteres de
     * controle. Sai do buraco sozinho so quando a pessoa aperta e solta a
     * mesma tecla de novo, o que ninguem adivinha.
     *
     * So a thread de dispatch mexe nisto (teclado_tecla e teclado_leave
     * rodam nela), entao nao precisa de trava. */
    uint8_t baixas[32];

    /* ---- clipboard (wl_data_device) ---- */
    struct wl_data_device_manager *data_mgr;
    struct wl_data_device *data_dev;
    cb_clip_oferta ao_clip;

    /* oferta em construcao: entre o data_offer (novo objeto) e o selection
     * (qual objeto e a selecao atual) o compositor manda um evento "offer"
     * por mime type suportado — vamos guardando aqui. */
    struct wl_data_offer *oferta_pendente;
    char mimes_pendentes[MAX_MIMES_OFERTA][64];
    int n_mimes_pendentes;

    /* nossa propria oferta (o que OFERECEMOS para o resto do sistema) */
    /* POR QUE EXISTE clip_m — e de onde vem a corrida DE VERDADE.
     *
     * Os callbacks do data source (fonte_enviar, fonte_cancelada) rodam de
     * dentro do dispatch do Wayland. Neste backend do Gio o dispatch
     * acontece na propria goroutine do laco de eventos: app.Window.Event()
     * cai em driver.Event() (third_party/gio/app/os_wayland.go:1582), que
     * chama dispatch() quando nao ha evento pendente.
     *
     * Logo, em cmd/acessos NAO ha corrida: publicarClipboard so enfileira, e
     * quem chama grab_clip_definir e o laco de quadro (clipboard.go) — a
     * mesma goroutine que despacha, tudo serializado. Ja existiu aqui um
     * comentario afirmando que "sao threads DIFERENTES"; era falso.
     *
     * A corrida real esta nos OUTROS binarios: cmd/vncview e cmd/rdpview
     * chamam gh.SetClipboardText() direto de sess.OnCutText, ou seja, da
     * goroutine da sessao, sem passar por laco nenhum (ver
     * cmd/vncview/clipboard.go:35). Ali sim um free(clip_local) podia cair
     * em cima do compositor pedindo o texto: uso apos liberacao, derrubando
     * o processo dentro do cgo sem rastro em Go. E por causa DELES que a
     * trava existe — nao apague achando que o app nao precisa.
     *
     * REGRA: nao toque em `fonte` nem em `clip_local` sem clip_m. E nao
     * segure clip_m durante o write() do fonte_enviar — ver la o porque. */
    pthread_mutex_t clip_m;
    struct wl_data_source *fonte;
    char *clip_local;      /* copia utf-8 do texto atual */
    int clip_local_len;
};

/* ---- xkbcommon: keymap chega como fd; convertemos em xkb_state ---- */

static void teclado_keymap(void *dados, struct wl_keyboard *kbd,
                            uint32_t formato, int fd, uint32_t tam) {
    Grab *g = (Grab *)dados;
    if (formato != WL_KEYBOARD_KEYMAP_FORMAT_XKB_V1) { close(fd); return; }

    char *mapa = mmap(NULL, tam, PROT_READ, MAP_PRIVATE, fd, 0);
    close(fd);
    if (mapa == MAP_FAILED) return;

    struct xkb_keymap *keymap = xkb_keymap_new_from_string(
        g->xkb_ctx, mapa, XKB_KEYMAP_FORMAT_TEXT_V1, XKB_KEYMAP_COMPILE_NO_FLAGS);
    munmap(mapa, tam);
    if (!keymap) return;

    struct xkb_state *state = xkb_state_new(keymap);
    if (!state) { xkb_keymap_unref(keymap); return; }

    xkb_state_unref(g->xkb_state);
    xkb_keymap_unref(g->xkb_keymap);
    g->xkb_keymap = keymap;
    g->xkb_state = state;
}

static void teclado_modifiers(void *dados, struct wl_keyboard *kbd,
                               uint32_t serial, uint32_t depressed,
                               uint32_t latched, uint32_t locked,
                               uint32_t grupo) {
    Grab *g = (Grab *)dados;
    if (!g->xkb_state) return;
    xkb_state_update_mask(g->xkb_state, depressed, latched, locked, 0, 0, grupo);
}

/* ainda_valida: a geracao pedida ainda e a que esta em curso, a thread
 * nao foi mandada parar e a tecla continua pressionada. Qualquer "nao"
 * aqui derruba o ciclo de repeticao em curso. */
static int rep_ainda_valida(Grab *g, int64_t geracao) {
    pthread_mutex_lock(&g->rep.m);
    int ok = !g->rep.parar && g->rep.ativo && g->rep.geracao == geracao;
    pthread_mutex_unlock(&g->rep.m);
    return ok;
}

/* rep_esperar: dorme ms em fatias curtas, saindo mais cedo se o estado
 * mudou (tecla solta, trocou de tecla, ou grab_parar chamado). Fatia de
 * 15ms e curta o bastante pra nao atrasar perceptivelmente nem o inicio
 * nem cada repeticao seguinte. */
static int rep_esperar(Grab *g, int64_t geracao, int32_t ms) {
    int32_t passado = 0;
    const int32_t fatia = 15;
    while (passado < ms) {
        usleep(fatia * 1000);
        passado += fatia;
        if (!rep_ainda_valida(g, geracao)) return 0;
    }
    return 1;
}

/* rep_loop roda pela vida inteira do Grab: fica parada enquanto nenhuma
 * tecla repetivel esta pressionada, e quando uma fica, espera o atraso
 * inicial e entao chama ao_teclar(..., pressionada=1) — um "apertou"
 * sintetico — a cada intervalo, ate soltar ou trocar de tecla. */
static void *rep_loop(void *arg) {
    Grab *g = (Grab *)arg;
    for (;;) {
        pthread_mutex_lock(&g->rep.m);
        if (g->rep.parar) { pthread_mutex_unlock(&g->rep.m); return NULL; }
        int ativo = g->rep.ativo;
        int64_t geracao = g->rep.geracao;
        uint32_t ks = g->rep.keysym, kc = g->rep.keycode;
        int32_t atraso = g->rep.atraso_ms, intervalo = g->rep.intervalo_ms;
        pthread_mutex_unlock(&g->rep.m);

        if (!ativo || intervalo <= 0) { usleep(15000); continue; }
        if (!rep_esperar(g, geracao, atraso)) continue;

        while (rep_ainda_valida(g, geracao)) {
            if (g->ao_teclar) g->ao_teclar(ks, kc, 1);
            if (!rep_esperar(g, geracao, intervalo)) break;
        }
    }
}

/* grab_modificadores: estado AUTORITATIVO dos modificadores, vindo do
 * evento wl_keyboard.modifiers que o compositor manda (teclado_modifiers,
 * acima), e nao de contar press/release no app.
 *
 * A diferenca importa: quando o compositor captura uma combinacao como
 * atalho GLOBAL dele, ele consome o evento, e o release dos modificadores
 * nunca chega aqui. Quem conta press/release fica com o modificador preso
 * em "apertado" para sempre — e ai o atalho de tecla limpa do app (F12)
 * nunca mais dispara. */
int grab_modificadores(Grab *g) {
    if (!g || !g->xkb_state) return 0;
    int m = 0;
    if (xkb_state_mod_name_is_active(g->xkb_state, XKB_MOD_NAME_CTRL,
                                     XKB_STATE_MODS_EFFECTIVE) > 0) m |= 1;
    if (xkb_state_mod_name_is_active(g->xkb_state, XKB_MOD_NAME_SHIFT,
                                     XKB_STATE_MODS_EFFECTIVE) > 0) m |= 2;
    if (xkb_state_mod_name_is_active(g->xkb_state, XKB_MOD_NAME_ALT,
                                     XKB_STATE_MODS_EFFECTIVE) > 0) m |= 4;
    if (xkb_state_mod_name_is_active(g->xkb_state, XKB_MOD_NAME_LOGO,
                                     XKB_STATE_MODS_EFFECTIVE) > 0) m |= 8;
    return m;
}

static void teclado_tecla(void *dados, struct wl_keyboard *kbd,
                          uint32_t serial, uint32_t tempo, uint32_t key,
                          uint32_t estado) {
    Grab *g = (Grab *)dados;
    g->ultimo_serial = serial; /* clipboard write (set_selection) precisa disto */
    if (!g->xkb_state || !g->ao_teclar) return;

    /* evdev -> X11: off-by-8 historico do protocolo Core. O RDP quer este
     * keycode cru (o proprio FreeRDP traduz pra scancode); o VNC quer o
     * keysym, que so o xkbcommon sabe calcular (depende do layout ativo). */
    xkb_keycode_t codigo = key + 8;
    const xkb_keysym_t *syms;
    int n = xkb_state_key_get_syms(g->xkb_state, codigo, &syms);
    uint32_t keysym = (n == 1) ? (uint32_t)syms[0] : 0;
    int pressionada = estado == WL_KEYBOARD_KEY_STATE_PRESSED;

    /* xkb_keymap_key_repeats: e o proprio layout quem diz se ESTA tecla
     * repete — modificador puro (Ctrl/Shift/Alt/Super/CapsLock) normalmente
     * nao repete em teclado nenhum, e perguntar aqui poupa uma lista feita
     * a mao (que teria que casar com o layout de cada usuario). */
    if (g->xkb_keymap && xkb_keymap_key_repeats(g->xkb_keymap, codigo)) {
        pthread_mutex_lock(&g->rep.m);
        g->rep.geracao++;
        if (pressionada) {
            g->rep.ativo = 1;
            g->rep.keysym = keysym;
            g->rep.keycode = (uint32_t)codigo;
        } else if (g->rep.keycode == (uint32_t)codigo) {
            /* so cancela se for A MESMA tecla que estava repetindo — soltar
             * uma tecla diferente da que repete nao pode interromper ela */
            g->rep.ativo = 0;
        }
        pthread_mutex_unlock(&g->rep.m);
    }

    /* registra a tecla como em baixo ANTES de avisar: se o consumidor
     * demorar, o leave que chegar depois ja sabe que ela existe. */
    if (key < 256) {
        if (pressionada) g->baixas[key >> 3] |= (uint8_t)(1u << (key & 7));
        else g->baixas[key >> 3] &= (uint8_t)~(1u << (key & 7));
    }

    g->ao_teclar(keysym, (uint32_t)codigo, pressionada);
}

static void teclado_enter(void *dados, struct wl_keyboard *kbd, uint32_t serial,
                          struct wl_surface *surface, struct wl_array *teclas) {}
static void teclado_leave(void *dados, struct wl_keyboard *kbd, uint32_t serial,
                          struct wl_surface *surface) {
    /* perdeu o foco (troca de aba/janela): nao pode ficar uma tecla
     * "presa" repetindo pra sempre do lado de dentro */
    Grab *g = (Grab *)dados;
    pthread_mutex_lock(&g->rep.m);
    g->rep.ativo = 0;
    g->rep.geracao++;
    pthread_mutex_unlock(&g->rep.m);

    /* SOLTA o que ficou em baixo. Sem isto o modificador fica preso do
     * lado remoto — ver o campo `baixas` na struct. O release e mandado
     * com keysym 0 de proposito: o keysym depende do estado dos
     * modificadores, que e justamente o que estamos desfazendo, e quem usa
     * keysym (VNC) trata 0 como "sem simbolo"; quem usa keycode (RDP)
     * recebe o codigo certo, que e o que importa para o "soltou". */
    for (int byte = 0; byte < (int)sizeof(g->baixas); byte++) {
        if (!g->baixas[byte]) continue;
        for (int bit = 0; bit < 8; bit++) {
            if (!(g->baixas[byte] & (1u << bit))) continue;
            uint32_t key = (uint32_t)(byte * 8 + bit);
            if (g->ao_teclar) g->ao_teclar(0, key + 8, 0);
        }
        g->baixas[byte] = 0;
    }

    /* e zera os modificadores do nosso lado tambem, senao o proximo
     * keysym calculado sairia como se o Ctrl ainda estivesse em baixo. */
    if (g->xkb_state)
        xkb_state_update_mask(g->xkb_state, 0, 0, 0, 0, 0, 0);
}
static void teclado_repeat_info(void *dados, struct wl_keyboard *kbd,
                                int32_t taxa, int32_t atraso) {
    /* vem do proprio compositor: respeita o que a pessoa configurou em
     * "velocidade do teclado" em vez de inventar um numero fixo aqui.
     * taxa 0 quer dizer "sem repeticao nenhuma" (protocolo permite). */
    Grab *g = (Grab *)dados;
    pthread_mutex_lock(&g->rep.m);
    g->rep.atraso_ms = atraso;
    g->rep.intervalo_ms = (taxa > 0) ? (1000 / taxa) : 0;
    pthread_mutex_unlock(&g->rep.m);
}

static const struct wl_keyboard_listener ouvinte_teclado = {
    .keymap = teclado_keymap,
    .enter = teclado_enter,
    .leave = teclado_leave,
    .key = teclado_tecla,
    .modifiers = teclado_modifiers,
    .repeat_info = teclado_repeat_info,
};

/* ---- clipboard: wl_data_device (leitura de oferta alheia) ---- */

static void oferta_mime(void *dados, struct wl_data_offer *offer,
                        const char *mime) {
    Grab *g = (Grab *)dados;
    if (offer != g->oferta_pendente) return; /* oferta antiga, ja trocada */
    if (g->n_mimes_pendentes >= MAX_MIMES_OFERTA) return;
    snprintf(g->mimes_pendentes[g->n_mimes_pendentes], 64, "%s", mime);
    g->n_mimes_pendentes++;
}

/* eventos de source_actions/action nao interessam (so texto simples) */
static void oferta_source_actions(void *d, struct wl_data_offer *o, uint32_t a) {}
static void oferta_action(void *d, struct wl_data_offer *o, uint32_t a) {}

static const struct wl_data_offer_listener ouvinte_oferta = {
    .offer = oferta_mime,
    .source_actions = oferta_source_actions,
    .action = oferta_action,
};

static void dispositivo_nova_oferta(void *dados, struct wl_data_device *dev,
                                    struct wl_data_offer *offer) {
    Grab *g = (Grab *)dados;
    g->oferta_pendente = offer;
    g->n_mimes_pendentes = 0;
    wl_data_offer_add_listener(offer, &ouvinte_oferta, g);
}

/* a oferta acima de fato virou A selecao (clipboard) atual: escolhe o
 * melhor mime que sabemos ler e ja pede os dados — o Go le o fd ate EOF. */
static void dispositivo_selecionou(void *dados, struct wl_data_device *dev,
                                   struct wl_data_offer *offer) {
    Grab *g = (Grab *)dados;
    if (!offer || !g->ao_clip) {
        if (offer) wl_data_offer_destroy(offer);
        return;
    }

    const char *escolhido = NULL;
    for (size_t i = 0; i < N_MIME_LEITURA && !escolhido; i++) {
        for (int j = 0; j < g->n_mimes_pendentes; j++) {
            if (strcmp(MIME_LEITURA[i], g->mimes_pendentes[j]) == 0) {
                escolhido = MIME_LEITURA[i];
                break;
            }
        }
    }
    if (!escolhido) {
        wl_data_offer_destroy(offer); /* nada de texto reconhecido */
        return;
    }

    int fds[2];
    if (pipe(fds) != 0) { wl_data_offer_destroy(offer); return; }
    wl_data_offer_receive(offer, escolhido, fds[1]);
    close(fds[1]); /* nosso lado de escrita: o compositor/fonte tem o dele */
    wl_display_flush(g->display); /* manda a requisicao sem esperar resposta */
    wl_data_offer_destroy(offer);

    g->ao_clip(fds[0]); /* Go le fds[0] ate EOF numa goroutine e fecha */
}

static const struct wl_data_device_listener ouvinte_dispositivo = {
    .data_offer = dispositivo_nova_oferta,
    .selection = dispositivo_selecionou,
};

/* ---- clipboard: wl_data_source (anunciar o NOSSO clipboard) ---- */

static void fonte_enviar(void *dados, struct wl_data_source *fonte,
                         const char *mime, int fd) {
    Grab *g = (Grab *)dados;
    (void)fonte;

    /* Copia SOB TRAVA, escreve FORA dela. O write abaixo e num pipe que o
     * outro lado pode estar lendo devagar — pode bloquear por tempo
     * indeterminado. Segurar clip_m durante ele penduraria o laco de quadro
     * na proxima publicacao de clipboard, trocando um bug de corrida por um
     * de travamento.
     *
     * RISCO QUE SOBRA, ANOTADO DE PROPOSITO: este callback roda de dentro
     * do dispatch do Wayland, que neste backend e a PROPRIA goroutine do
     * laco de eventos. Se quem pediu o nosso clipboard parar de ler no meio,
     * o pipe enche, o write fica preso e a interface inteira congela junto —
     * sem clique, sem hover, sem redesenho. Quanto maior o texto, mais
     * facil: colar um clipboard grande de uma sessao RDP num programa lento
     * e o caso plausivel.
     *
     * Fica como esta DE PROPOSITO: em producao isso nunca foi observado, e
     * a correcao (entregar o fd para uma thread propria, que escreve e
     * fecha por conta) poe ciclo de vida de thread no caminho do clipboard
     * — mais superficie de risco do que o defeito que evita. Se um dia
     * alguem relatar "a interface travou ao copiar", o suspeito e este, e o
     * caminho da correcao esta aqui. */
    char *copia = NULL;
    int tam = 0;
    pthread_mutex_lock(&g->clip_m);
    if (g->clip_local && g->clip_local_len > 0) {
        copia = (char *)malloc((size_t)g->clip_local_len);
        if (copia) {
            memcpy(copia, g->clip_local, (size_t)g->clip_local_len);
            tam = g->clip_local_len;
        }
    }
    pthread_mutex_unlock(&g->clip_m);

    if (copia) {
        ssize_t escrito = 0;
        while (escrito < tam) {
            ssize_t n = write(fd, copia + escrito, (size_t)(tam - escrito));
            if (n <= 0) break;
            escrito += n;
        }
        free(copia);
    }
    close(fd);
}

/* O compositor cancela a nossa fonte quando OUTRO programa assume o
 * clipboard. Destruir e so isso era um ponteiro solto: g->fonte continuava
 * apontando para o proxy ja liberado, e a publicacao seguinte fazia
 * wl_data_source_destroy no mesmo endereco de novo — SIGSEGV dentro do
 * cgo, derrubando o aplicativo inteiro. Apareceu de duas formas: abrindo
 * muitas telas de uma vez (cada sessao publica ao conectar) e com Ctrl+X
 * numa sessao RDP (o recorte remoto publica logo depois de o compositor
 * cancelar). Dai zerar o campo ANTES de destruir. */
static void fonte_cancelada(void *dados, struct wl_data_source *fonte) {
    Grab *g = (Grab *)dados;
    if (!g) { wl_data_source_destroy(fonte); return; }

    /* O destroy fica DENTRO da trava, e SÓ se a fonte ainda for a nossa
     * atual. Deixar o destroy fora (como ja esteve) reabria o double-free
     * por outro caminho: esta funcao chega com `fonte` na mao e para na
     * trava; enquanto isso grab_clip_definir, que ja a segura, destroi
     * essa mesma fonte e cria outra. Ao passar, a comparacao falha, nao
     * zeravamos nada — mas destruiamos `fonte` de novo, ja liberada.
     *
     * Se g->fonte != fonte, quem trocou ja destruiu: aqui nao ha o que
     * fazer alem de sair. */
    pthread_mutex_lock(&g->clip_m);
    if (g->fonte == fonte) {
        g->fonte = NULL;
        wl_data_source_destroy(fonte);
    }
    pthread_mutex_unlock(&g->clip_m);
}

static void fonte_alvo(void *d, struct wl_data_source *f, const char *m) {}

static const struct wl_data_source_listener ouvinte_fonte = {
    .target = fonte_alvo,
    .send = fonte_enviar,
    .cancelled = fonte_cancelada,
};

/* ---- registry: acha wl_seat, manager de atalhos e clipboard ---- */

static void registro_global(void *dados, struct wl_registry *registry,
                             uint32_t nome, const char *interface,
                             uint32_t versao) {
    Grab *g = (Grab *)dados;
    if (strcmp(interface, wl_seat_interface.name) == 0) {
        g->seat = wl_registry_bind(registry, nome, &wl_seat_interface, 1);
    } else if (strcmp(interface,
                       zwp_keyboard_shortcuts_inhibit_manager_v1_interface.name) == 0) {
        g->manager = wl_registry_bind(
            registry, nome, &zwp_keyboard_shortcuts_inhibit_manager_v1_interface, 1);
    } else if (strcmp(interface, wl_data_device_manager_interface.name) == 0) {
        g->data_mgr = wl_registry_bind(registry, nome, &wl_data_device_manager_interface, 1);
    }
}

static void registro_removido(void *dados, struct wl_registry *registry,
                               uint32_t nome) {
    /* nao acompanhamos remocao: o grab e coisa de sessao curta */
}

static const struct wl_registry_listener ouvinte_registro = {
    .global = registro_global,
    .global_remove = registro_removido,
};

void grab_parar(Grab *g) {
    if (!g) return;
    if (g->rep.thread_criada) {
        pthread_mutex_lock(&g->rep.m);
        g->rep.parar = 1;
        pthread_mutex_unlock(&g->rep.m);
        pthread_join(g->rep.thread, NULL);
        pthread_mutex_destroy(&g->rep.m);
    }
    if (g->inibidor) zwp_keyboard_shortcuts_inhibitor_v1_destroy(g->inibidor);
    if (g->manager) zwp_keyboard_shortcuts_inhibit_manager_v1_destroy(g->manager);
    /* sob a trava, pelo mesmo motivo de fonte_cancelada: a goroutine de uma
     * sessao pode estar publicando clipboard neste instante (e em
     * cmd/vncview e cmd/rdpview ela publica direto, sem passar pelo laco). */
    pthread_mutex_lock(&g->clip_m);
    if (g->fonte) { wl_data_source_destroy(g->fonte); g->fonte = NULL; }
    pthread_mutex_unlock(&g->clip_m);
    if (g->data_dev) wl_proxy_destroy((struct wl_proxy *)g->data_dev);
    if (g->data_mgr) wl_proxy_destroy((struct wl_proxy *)g->data_mgr);
    if (g->keyboard) wl_keyboard_release(g->keyboard);
    if (g->seat) wl_proxy_destroy((struct wl_proxy *)g->seat);
    if (g->registry) wl_proxy_destroy((struct wl_proxy *)g->registry);
    xkb_state_unref(g->xkb_state);
    xkb_keymap_unref(g->xkb_keymap);
    xkb_context_unref(g->xkb_ctx);
    free(g->clip_local);
    pthread_mutex_destroy(&g->clip_m);
    free(g);
}

Grab *grab_iniciar(void *display, void *surface, cb_tecla ao_teclar,
                   cb_clip_oferta ao_clip) {
    Grab *g = (Grab *)calloc(1, sizeof(Grab));
    if (!g) return NULL;

    g->display = (struct wl_display *)display;
    g->superficie = (struct wl_surface *)surface;
    g->ao_teclar = ao_teclar;
    g->ao_clip = ao_clip;
    g->xkb_ctx = xkb_context_new(XKB_CONTEXT_NO_FLAGS);
    if (!g->xkb_ctx) { free(g); return NULL; }

    /* valores por padrao ate o wl_keyboard.repeat_info do compositor chegar
     * (400ms / ~30 por segundo, o padrao usual de xterm/GNOME) — sem isto a
     * primeira tecla segurada antes do evento chegar nao repetiria. */
    pthread_mutex_init(&g->rep.m, NULL);
    /* clip_m protege fonte/clip_local contra a thread de despacho do Gio —
     * ver o comentario na struct Grab. Inicializar AQUI, antes de qualquer
     * registro de listener: a partir do roundtrip abaixo o compositor ja
     * pode chamar fonte_cancelada. */
    pthread_mutex_init(&g->clip_m, NULL);
    g->rep.atraso_ms = 400;
    g->rep.intervalo_ms = 33;
    if (pthread_create(&g->rep.thread, NULL, rep_loop, g) == 0) {
        g->rep.thread_criada = 1;
    }

    g->registry = wl_display_get_registry(g->display);
    if (!g->registry) { grab_parar(g); return NULL; }
    wl_registry_add_listener(g->registry, &ouvinte_registro, g);

    /* roundtrip UNICO, so na configuracao: forca o servidor a anunciar os
     * globals (seat/manager/data_mgr) antes de seguirmos. Depois disto o
     * Gio quem drena o socket — ver nota no topo do arquivo. */
    wl_display_roundtrip(g->display);

    if (!g->seat) {
        /* sem wl_seat nao ha teclado nem clipboard nenhum pra ler; desiste */
        grab_parar(g);
        return NULL;
    }

    if (ao_teclar) {
        g->keyboard = wl_seat_get_keyboard(g->seat);
        if (g->keyboard) {
            wl_keyboard_add_listener(g->keyboard, &ouvinte_teclado, g);
            /* mais um roundtrip: precisa do keymap (que chega como evento)
             * antes que a primeira tecla real apareca. */
            wl_display_roundtrip(g->display);
        }
    }

    /* o inibidor NAO nasce ligado: ele so faz sentido quando a aba ativa
     * e uma sessao remota. Ligado o tempo todo, o Alt+Tab do proprio
     * usuario morre enquanto ele olha o painel. */

    if (g->data_mgr) {
        g->data_dev = wl_data_device_manager_get_data_device(g->data_mgr, g->seat);
        if (g->data_dev) {
            wl_data_device_add_listener(g->data_dev, &ouvinte_dispositivo, g);
        }
    }

    return g; /* valido mesmo se so parte disto emplacou */
}

/* Liga/desliga a inibicao dos atalhos do compositor. O objeto inibidor e
 * criado e destruido aqui, sem tocar no resto do Grab (teclado, clipboard
 * e o proprio wl_seat continuam de pe) — recriar o Grab inteiro ja
 * derrubou o processo antes, ver a nota em cmd/acessos/main.go. */
void grab_inibir(Grab *g, int ligar) {
    if (!g || !g->manager || !g->seat || !g->superficie) return;
    if (ligar && !g->inibidor) {
        g->inibidor = zwp_keyboard_shortcuts_inhibit_manager_v1_inhibit_shortcuts(
            g->manager, g->superficie, g->seat);
        wl_display_flush(g->display);
    } else if (!ligar && g->inibidor) {
        zwp_keyboard_shortcuts_inhibitor_v1_destroy(g->inibidor);
        g->inibidor = NULL;
        wl_display_flush(g->display);
    }
}

/* Anuncia utf8 como a selecao atual. Recria a wl_data_source a cada
 * chamada (o protocolo exige uma nova fonte por selecao — reusar uma ja
 * usada e erro). */
void grab_clip_definir(Grab *g, const char *utf8, int tam) {
    if (!g || !g->data_mgr || !g->data_dev) return;

    /* Tudo o que mexe em clip_local/fonte fica sob clip_m — ver o comentario
     * na struct. Nenhuma das chamadas libwayland daqui dispara callback
     * sincrono (destroy/create/offer/set_selection so ENFILEIRAM requisicao,
     * e flush nao despacha), entao segurar a trava aqui nao arrisca
     * reentrar em fonte_enviar e travar contra nos mesmos. */
    pthread_mutex_lock(&g->clip_m);

    free(g->clip_local);
    g->clip_local = NULL;
    g->clip_local_len = 0;
    if (utf8 && tam > 0) {
        g->clip_local = (char *)malloc((size_t)tam);
        if (g->clip_local) {
            memcpy(g->clip_local, utf8, (size_t)tam);
            g->clip_local_len = tam;
        }
    }

    if (g->fonte) wl_data_source_destroy(g->fonte);
    g->fonte = wl_data_device_manager_create_data_source(g->data_mgr);
    if (!g->fonte) {
        pthread_mutex_unlock(&g->clip_m);
        return;
    }
    wl_data_source_add_listener(g->fonte, &ouvinte_fonte, g);
    for (size_t i = 0; i < N_MIME_ESCRITA; i++)
        wl_data_source_offer(g->fonte, MIME_ESCRITA[i]);

    wl_data_device_set_selection(g->data_dev, g->fonte, g->ultimo_serial);
    wl_display_flush(g->display);

    pthread_mutex_unlock(&g->clip_m);
}
