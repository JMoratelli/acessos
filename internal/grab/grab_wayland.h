/* grab_wayland.h — API exposta ao Go para:
 *
 *   1. inibir os atalhos globais do compositor (keyboard-shortcuts-inhibit-
 *      unstable-v1) enquanto a janela remota estiver em primeiro plano;
 *   2. ler teclado direto do protocolo Wayland (wl_keyboard + xkbcommon),
 *      contornando o key.Event do Gio — nesta pilha (Wayland/KDE) ele nao
 *      preenche Modifiers e nunca entrega o "soltou" de Ctrl/Alt/Shift/Super
 *      sozinhos, so o "apertou". Lendo aqui temos apertou/soltou corretos
 *      para toda tecla, incluindo modificadores, e o keysym ja sai certo
 *      para o layout ativo (sem o hack de maiuscula/minuscula do lado Go);
 *   3. clipboard direto (wl_data_device), contornando o clipboard do Gio —
 *      ele so reconhece uma lista fixa e estreita de mime types ao LER
 *      ("text/plain;charset=utf8" sem hifen, UTF8_STRING, text/plain, TEXT,
 *      STRING) e no KDE/Wayland nao bateu com nada que foi copiado — leitura
 *      silenciosamente nunca voltava nada, sem erro nenhum. Aqui aceitamos
 *      um conjunto mais amplo (incluindo a forma com hifen, a usual). */

#ifndef GRAB_WAYLAND_H
#define GRAB_WAYLAND_H

#include <stdint.h>

typedef struct Grab Grab;

/* Chamado a cada tecla fisica apertada ou solta. keysym e o valor X11 ja
 * calculado pelo layout ativo (o que o VNC quer); keycode_x11 e o keycode
 * cru (evdev+8) que o RDP quer, para o FreeRDP traduzir pra scancode do
 * jeito dele. keysym pode vir 0 se a tecla for morta/composta — nesse caso
 * keycode_x11 ainda e valido. */
typedef void (*cb_tecla)(uint32_t keysym, uint32_t keycode_x11, int pressionada);

/* Chamado quando o clipboard LOCAL (do sistema) muda para algo que sabemos
 * ler (texto). fd e o lado de LEITURA de um pipe ja armado (a requisicao
 * wl_data_offer_receive ja foi enviada) — o chamador (Go) deve ler ate EOF
 * numa goroutine propria e fechar o fd depois. NAO chamado se a nova
 * selecao nao tiver nenhum mime type de texto reconhecido. */
typedef void (*cb_clip_oferta)(int fd_leitura);

/* display e surface sao os ponteiros crus que o Gio expoe via
 * app.WaylandViewEvent (*wl_display, *wl_surface). Devolve NULL se nem
 * wl_seat existir — mas mesmo com isso tenta armar o que for possivel
 * (grab de atalhos, teclado, clipboard sao independentes entre si: cada
 * um degrada sozinho se o compositor nao suportar aquele pedaco).
 *
 * ao_teclar e ao_clip podem ser NULL se so o grab de atalhos interessar. */
Grab *grab_iniciar(void *display, void *surface, cb_tecla ao_teclar,
                    cb_clip_oferta ao_clip);

/* Liga (1) ou desliga (0) a inibicao dos atalhos do compositor. Nasce
 * DESLIGADA: so deve ser ligada enquanto uma sessao remota estiver em
 * primeiro plano, senao o proprio usuario fica sem Alt+Tab. */
void grab_inibir(Grab *g, int ligar);

/* Anuncia utf8 como o clipboard ATUAL do lado Go (participa da selecao do
 * wl_data_device) — chamado quando o clipboard REMOTO muda e queremos
 * refletir isso no clipboard do sistema local. Sem efeito se ainda nao
 * houve nenhum evento de teclado real (precisa de um serial valido). */
void grab_clip_definir(Grab *g, const char *utf8, int tam);

/* Nao ha uma "grab_processar": os eventos (teclado, clipboard) chegam pelos
 * mesmos default queue + file descriptor que o proprio Gio ja fica lendo
 * continuamente para suas janelas — o wl_display_dispatch_pending que ELE
 * ja chama entrega tambem para os nossos listeners. Chamar dispatch por
 * conta propria aqui correria com a leitura do Gio no mesmo socket. */
void grab_parar(Grab *g);

#endif
