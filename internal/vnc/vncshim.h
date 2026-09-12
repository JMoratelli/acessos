/* vncshim.h — API exposta por vncshim.c para o binding Go (cgo).
 * Espelha exatamente as assinaturas usadas antes pelo ctypes em Python. */

#ifndef VNCSHIM_H
#define VNCSHIM_H

#include <stdint.h>

typedef struct Sessao Sessao;

typedef void (*cb_atualizou)(void *ctx, int x, int y, int w, int h);
typedef void (*cb_redimensionou)(void *ctx, int w, int h);
typedef void (*cb_texto)(void *ctx, const char *texto, int tam);

Sessao *vs_criar(void *ctx,
                  cb_atualizou ao_atualizar,
                  cb_redimensionou ao_redimensionar,
                  cb_texto ao_receber_texto);

void vs_definir_senha(Sessao *s, const char *senha);
void vs_definir_usuario(Sessao *s, const char *usuario);

int vs_conectar(Sessao *s, const char *host, int porta);

int vs_erro_auth(Sessao *s);
int vs_falta_usuario(Sessao *s);
int vs_exige_usuario(Sessao *s);
int vs_pediu_credencial(Sessao *s);
int vs_recusado(Sessao *s);
const char *vs_erro_msg(Sessao *s);

int vs_esperar(Sessao *s, int usecs);
int vs_processar(Sessao *s);

uint8_t *vs_framebuffer(Sessao *s);
int vs_largura(Sessao *s);
int vs_altura(Sessao *s);
int vs_morto(Sessao *s);

void vs_ponteiro(Sessao *s, int x, int y, int botoes);
void vs_tecla(Sessao *s, uint32_t keysym, int pressionada);
void vs_enviar_texto(Sessao *s, const char *texto, int tam);

void vs_destruir(Sessao *s);

#endif
