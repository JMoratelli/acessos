/* rdpshim.h — API exposta por rdpshim.c para o binding Go (cgo).
 * Espelha as assinaturas que o ctypes usava no app original. */

#ifndef RDPSHIM_H
#define RDPSHIM_H

#include <stdint.h>

typedef struct Sessao Sessao;

typedef void (*cb_atualizou)(void *ctx, int x, int y, int w, int h);
typedef void (*cb_redimensionou)(void *ctx, int w, int h);
typedef void (*cb_desconectou)(void *ctx, const char *motivo);
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
typedef void (*cb_clip_texto)(void *ctx, const char *utf8, int tam);
typedef void (*cb_disp_pronto)(void *ctx);
/* forma de cursor nova (ou "voltou ao padrão", com w=h=0 e mask=NULL).
 * mask: 1 byte por pixel (w*h bytes), o canal alfa decodificado do
 * XOR+AND mask do RDP — 0 = transparente, != 0 = opaco. */
typedef void (*cb_cursor)(void *ctx, int xhot, int yhot, int w, int h,
                          const uint8_t *mask);

Sessao *rs_criar(void *pyctx,
                  cb_atualizou ao_atualizar,
                  cb_redimensionou ao_redimensionar,
                  cb_desconectou ao_desconectar,
                  cb_certificado_novo ao_certificado_novo,
                  cb_certificado_mudou ao_certificado_mudou,
                  cb_clip_texto ao_clip_texto,
                  cb_disp_pronto ao_disp_pronto,
                  cb_cursor ao_cursor);

void rs_clipboard_definir_texto(Sessao *s, const char *utf8, int tam);
int rs_pedir_resize(Sessao *s, int largura, int altura);

void rs_definir_credenciais(Sessao *s, const char *usuario, const char *senha,
                            const char *dominio);

int rs_conectar(Sessao *s, const char *host, int porta);

int rs_esperar(Sessao *s, int ms);
int rs_processar(Sessao *s);

uint8_t *rs_framebuffer(Sessao *s);
int rs_largura(Sessao *s);
int rs_altura(Sessao *s);
int rs_stride(Sessao *s);

/* Trava o framebuffer e devolve ponteiro + geometria consistentes entre si
 * — usar em vez das quatro funções acima em sequência, que não são
 * atômicas frente a um resize concorrente. Devolve NULL se ainda não há
 * framebuffer (e aí a trava NÃO fica segurada).
 *
 * Quem recebe não-NULL TEM de chamar rs_destravar_quadro assim que
 * terminar de ler: enquanto a trava estiver na mão, um resize do lado do
 * servidor fica esperando. */
const uint8_t *rs_travar_quadro(Sessao *s, int *w_out, int *h_out, int *stride_out);
void rs_destravar_quadro(Sessao *s);
int rs_morto(Sessao *s);
int rs_erro_auth(Sessao *s);
const char *rs_erro_msg(Sessao *s);

void rs_ponteiro_mover(Sessao *s, int x, int y);
void rs_ponteiro_botao(Sessao *s, int x, int y, int botao, int pressionado);
void rs_ponteiro_roda(Sessao *s, int eixo, int passos);
void rs_tecla(Sessao *s, uint32_t keycode_x11, int pressionada);

void rs_destruir(Sessao *s);

#endif
