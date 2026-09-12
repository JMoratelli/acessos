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
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
#include "keyboard-shortcuts-inhibit-unstable-v1-client-protocol.h"
#include "grab_wayland.h"

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

    g->ao_teclar(keysym, (uint32_t)codigo, estado == WL_KEYBOARD_KEY_STATE_PRESSED);
}

static void teclado_enter(void *dados, struct wl_keyboard *kbd, uint32_t serial,
                          struct wl_surface *surface, struct wl_array *teclas) {}
static void teclado_leave(void *dados, struct wl_keyboard *kbd, uint32_t serial,
                          struct wl_surface *surface) {}
static void teclado_repeat_info(void *dados, struct wl_keyboard *kbd,
                                int32_t taxa, int32_t atraso) {}

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
    if (g->clip_local && g->clip_local_len > 0) {
        ssize_t escrito = 0;
        while (escrito < g->clip_local_len) {
            ssize_t n = write(fd, g->clip_local + escrito, g->clip_local_len - escrito);
            if (n <= 0) break;
            escrito += n;
        }
    }
    close(fd);
}

static void fonte_cancelada(void *dados, struct wl_data_source *fonte) {
    wl_data_source_destroy(fonte);
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
    if (g->inibidor) zwp_keyboard_shortcuts_inhibitor_v1_destroy(g->inibidor);
    if (g->manager) zwp_keyboard_shortcuts_inhibit_manager_v1_destroy(g->manager);
    if (g->fonte) wl_data_source_destroy(g->fonte);
    if (g->data_dev) wl_proxy_destroy((struct wl_proxy *)g->data_dev);
    if (g->data_mgr) wl_proxy_destroy((struct wl_proxy *)g->data_mgr);
    if (g->keyboard) wl_keyboard_release(g->keyboard);
    if (g->seat) wl_proxy_destroy((struct wl_proxy *)g->seat);
    if (g->registry) wl_proxy_destroy((struct wl_proxy *)g->registry);
    xkb_state_unref(g->xkb_state);
    xkb_keymap_unref(g->xkb_keymap);
    xkb_context_unref(g->xkb_ctx);
    free(g->clip_local);
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
    if (!g->fonte) return;
    wl_data_source_add_listener(g->fonte, &ouvinte_fonte, g);
    for (size_t i = 0; i < N_MIME_ESCRITA; i++)
        wl_data_source_offer(g->fonte, MIME_ESCRITA[i]);

    wl_data_device_set_selection(g->data_dev, g->fonte, g->ultimo_serial);
    wl_display_flush(g->display);
}
