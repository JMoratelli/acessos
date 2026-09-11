#!/usr/bin/env python3
"""Bateria do vncshim — exercita a lib em C direto, sem abrir janela.

    python3 testes/teste_vncshim.py                    # so os casos locais
    python3 testes/teste_vncshim.py 10.0.0.5,,senha    # + um alvo real
    python3 testes/teste_vncshim.py 10.0.0.5,joao,x    # host que pede usuario

Alvo = "host[,usuario[,senha]]". Nenhum host fica gravado aqui: o que
serve para uma rede nao serve para outra, e IP de cliente nao entra em
repositorio.

O QUE ISTO VERIFICA, e por que importa:
o shim precisa dizer POR QUE falhou, nao so que falhou. "Host desligado",
"senha errada" e "o servidor exige usuario e voce nao informou" pedem
reacoes opostas do app — a ultima, inclusive, precisa abrir um campo que
a tela normalmente nem mostra. Antes os tres chegavam ao Python como a
mesma frase.
"""
import ctypes
import os
import sys
import time

AQUI = os.path.dirname(os.path.abspath(__file__))
CANDIDATOS = [
    os.path.join(AQUI, os.pardir, "python", "libvncshim.so"),
    os.path.expanduser("~/.local/lib/acessos/libvncshim.so"),
    "libvncshim.so",
]

falhas = []


def _carregar():
    for c in CANDIDATOS:
        try:
            return ctypes.CDLL(c), c
        except OSError:
            continue
    print("libvncshim.so não encontrada. Compile com:\n"
          "  gcc -shared -fPIC -O2 -Wall -o python/libvncshim.so "
          "src/vncshim.c $(pkg-config --cflags --libs libvncclient)")
    sys.exit(2)


_l, _caminho = _carregar()

CB_ATU = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_int, ctypes.c_int,
                          ctypes.c_int, ctypes.c_int)
CB_RES = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_int, ctypes.c_int)
CB_TXT = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_char_p, ctypes.c_int)

_l.vs_criar.restype = ctypes.c_void_p
_l.vs_criar.argtypes = [ctypes.c_void_p, CB_ATU, CB_RES, CB_TXT]
_l.vs_conectar.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_int]
_l.vs_definir_senha.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
_l.vs_definir_usuario.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
_l.vs_erro_msg.restype = ctypes.c_char_p
for f in ("vs_erro_auth", "vs_falta_usuario", "vs_exige_usuario",
          "vs_pediu_credencial", "vs_recusado", "vs_largura", "vs_altura",
          "vs_morto", "vs_erro_msg", "vs_destruir"):
    getattr(_l, f).argtypes = [ctypes.c_void_p]

_vivos = []       # callbacks precisam sobreviver enquanto a sessao existir


def sondar(host, porta=5900, usuario=None, senha=None):
    """Uma tentativa de conexao. Devolve o diagnostico como dict."""
    cbs = (CB_ATU(lambda *a: None), CB_RES(lambda *a: None),
           CB_TXT(lambda *a: None))
    _vivos.extend(cbs)
    s = _l.vs_criar(None, *cbs)
    if not s:
        raise RuntimeError("vs_criar devolveu NULL")
    if senha is not None:
        _l.vs_definir_senha(s, senha.encode())
    if usuario is not None:
        _l.vs_definir_usuario(s, usuario.encode())

    t0 = time.time()
    ok = bool(_l.vs_conectar(s, host.encode(), porta))
    r = {
        "ok": ok,
        "segundos": time.time() - t0,
        "pediu_credencial": bool(_l.vs_pediu_credencial(s)),
        "exige_usuario": bool(_l.vs_exige_usuario(s)),
        "falta_usuario": bool(_l.vs_falta_usuario(s)),
        "erro_auth": bool(_l.vs_erro_auth(s)),
        "recusado": bool(_l.vs_recusado(s)),
        "msg": (_l.vs_erro_msg(s) or b"").decode("utf-8", "replace"),
        "tela": (_l.vs_largura(s), _l.vs_altura(s)) if ok else None,
    }
    _l.vs_destruir(s)
    return r


def checar(titulo, r, **esperado):
    ruins = [(k, esperado[k], r[k]) for k in esperado if r[k] != esperado[k]]
    print("  %-46s %5.1fs  %s" % (titulo, r["segundos"],
                                  "conectou" if r["ok"] else "falhou"))
    if r["msg"]:
        print("       motivo: %s" % r["msg"])
    if r["tela"]:
        print("       tela: %dx%d" % r["tela"])
    for campo, esp, obtido in ruins:
        print("       FALHOU: %s esperado %s, veio %s" % (campo, esp, obtido))
        falhas.append("%s: %s" % (titulo, campo))
    return r


def _com_servidor_falso():
    """Os casos que dependem do SERVIDOR, contra um de mentira local.

    Sem isto, "o servidor exige usuário" só dava para exercitar num PDV
    com UltraVNC de verdade — e cada tentativa falha lá aproxima o IP de
    uma lista negra cujo tempo de bloqueio dobra a cada reincidência.
    Aqui roda offline, em milissegundos, sem consequência para ninguém."""
    sys.path.insert(0, AQUI)
    try:
        from servidor_falso_vnc import em_thread
    except ImportError:
        print("  (servidor_falso_vnc.py não encontrado — pulando)")
        return

    portas = {"plain": 5911, "senha": 5912, "recusa": 5913}
    for esquema, porta in portas.items():
        em_thread(porta, esquema)
    time.sleep(0.4)      # deixa os sockets subirem antes de bater neles

    print("=== servidor que EXIGE usuário (VeNCrypt Plain) ===")
    checar("sem usuário: tem de marcar que faltou",
           sondar("127.0.0.1", portas["plain"], senha="so-a-senha"),
           ok=False, exige_usuario=True, falta_usuario=True,
           erro_auth=True, recusado=False)
    checar("com usuário: exige sim, mas não falta",
           sondar("127.0.0.1", portas["plain"], usuario="joao",
                  senha="segredo"),
           ok=False, exige_usuario=True, falta_usuario=False,
           erro_auth=True, recusado=False)

    print("\n=== servidor só-senha ===")
    checar("senha recusada é erro de auth, não de rede",
           sondar("127.0.0.1", portas["senha"], senha="errada"),
           ok=False, pediu_credencial=True, erro_auth=True,
           exige_usuario=False, falta_usuario=False, recusado=False)

    print("\n=== servidor que RECUSA antes de perguntar (lista negra) ===")
    checar("recusa não pode virar 'senha errada'",
           sondar("127.0.0.1", portas["recusa"], senha="x"),
           ok=False, recusado=True, erro_auth=False,
           pediu_credencial=False)


def main():
    print("lib: %s\n" % os.path.normpath(_caminho))

    print("=== falha de REDE (nao pode ser classificada como auth) ===")
    # 192.0.2.0/24 e reservada para documentacao (RFC 5737): nunca responde
    checar("host inalcancável", sondar("192.0.2.1"),
           ok=False, erro_auth=False, pediu_credencial=False)
    checar("porta fechada", sondar("127.0.0.1", 5999),
           ok=False, erro_auth=False, pediu_credencial=False)

    print()
    _com_servidor_falso()

    alvos = sys.argv[1:]
    if not alvos:
        print("\n(sem alvo real: passe host[,usuario[,senha]] para exercitar\n"
              " os caminhos de autenticação)")
    for alvo in alvos:
        p = (alvo.split(",") + ["", ""])[:3]
        host, usuario, senha = p[0], p[1] or None, p[2] or None
        print("\n=== %s ===" % host)
        r = sondar(host, usuario=usuario, senha=senha)
        checar("usuario=%s senha=%s" % (usuario or "<nenhum>",
                                        "<definida>" if senha else "<nenhuma>"),
               r)
        # o que der para afirmar sem saber o servidor de antemao:
        if r["ok"]:
            checar("  (conectado, sem erro de auth)", r, erro_auth=False)
        elif r["recusado"]:
            # o servidor barrou antes de perguntar (lista negra do
            # UltraVNC, p.ex.): nao pode ser contado como senha errada,
            # senao o app fica insistindo e o bloqueio so aumenta
            checar("  (recusado pelo servidor: não é erro de credencial)", r,
                   erro_auth=False, pediu_credencial=False)
        elif r["exige_usuario"] and not usuario:
            checar("  (exige usuário e não demos: tem de marcar falta)", r,
                   falta_usuario=True, erro_auth=True)
        elif r["pediu_credencial"]:
            checar("  (pediu credencial e falhou: é erro de auth)", r,
                   erro_auth=True)

    print("\n" + "=" * 62)
    if falhas:
        print("%d verificação(ões) falharam:" % len(falhas))
        for f in falhas:
            print("  - %s" % f)
        return 1
    print("tudo conforme o esperado")
    return 0


if __name__ == "__main__":
    sys.exit(main())
