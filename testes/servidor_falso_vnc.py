#!/usr/bin/env python3
"""Servidor VNC de mentira, só para testar autenticação.

    python3 testes/servidor_falso_vnc.py --esquema plain --porta 5910

Fala o começo do protocolo RFB e para na autenticação — não desenha nada,
não serve tela. Serve para exercitar os caminhos que dependem do SERVIDOR
e que, de outro jeito, só dá para testar contra uma máquina de verdade:

  --esquema senha     autenticação VNC clássica (só senha)
  --esquema plain     VeNCrypt Plain — exige USUÁRIO + senha
  --esquema recusa    manda zero esquemas + "connection has been rejected",
                      que é como o UltraVNC responde quem está na lista
                      negra por tentativas repetidas

POR QUE ISTO EXISTE: o caso "o servidor exige usuário" só aparecia num PDV
com UltraVNC MS-Logon, e testar nele custava caro — algumas tentativas
falhas e o servidor põe o IP numa lista negra, com tempo de bloqueio que
DOBRA a cada reincidência. Ficamos sem poder testar justamente o caminho
que queríamos consertar. Aqui o mesmo caminho roda em 30ms, offline, e
sem bloquear ninguém.
"""
import argparse
import socket
import struct
import sys
import threading

RFB = b"RFB 003.008\n"


def _recv(c, n):
    dados = b""
    while len(dados) < n:
        p = c.recv(n - len(dados))
        if not p:
            raise ConnectionError("cliente sumiu")
        dados += p
    return dados


def _falhar(c, motivo):
    """SecurityResult = falha (1) + motivo, como manda o RFB 3.8."""
    c.sendall(struct.pack(">I", 1))
    c.sendall(struct.pack(">I", len(motivo)) + motivo.encode())


def atender(c, esquema, verboso):
    def diz(*a):
        if verboso:
            print("   [servidor]", *a, flush=True)

    c.sendall(RFB)
    cliente = _recv(c, 12)
    diz("cliente fala", cliente.strip().decode("ascii", "replace"))

    if esquema == "recusa":
        # zero tipos + motivo: a resposta do UltraVNC a quem esta na lista
        # negra. O cliente nem chega a ser perguntado nada.
        c.sendall(bytes([0]))
        motivo = b"Your connection has been rejected."
        c.sendall(struct.pack(">I", len(motivo)) + motivo)
        diz("recusado sem pedir credencial")
        return

    if esquema == "senha":
        c.sendall(bytes([1, 2]))                 # 1 tipo: 2 = VNC Auth
        escolha = _recv(c, 1)[0]
        diz("cliente escolheu tipo", escolha)
        c.sendall(b"\x00" * 16)                  # desafio
        _recv(c, 16)                             # resposta cifrada
        _falhar(c, "password check failed!")
        diz("senha recusada (de propósito)")
        return

    # ---- VeNCrypt Plain: usuario + senha em claro
    c.sendall(bytes([1, 19]))                    # 1 tipo: 19 = VeNCrypt
    escolha = _recv(c, 1)[0]
    diz("cliente escolheu tipo", escolha)

    c.sendall(bytes([0, 2]))                     # VeNCrypt 0.2
    _recv(c, 2)                                  # versao que o cliente aceita
    c.sendall(bytes([0]))                        # 0 = versao ok

    c.sendall(bytes([1]) + struct.pack(">I", 256))   # 1 subtipo: 256 = Plain
    sub = struct.unpack(">I", _recv(c, 4))[0]
    diz("subtipo escolhido", sub)

    tam_u = struct.unpack(">I", _recv(c, 4))[0]
    tam_s = struct.unpack(">I", _recv(c, 4))[0]
    usuario = _recv(c, tam_u).decode("utf-8", "replace") if tam_u else ""
    _recv(c, tam_s)
    diz("recebeu usuario=%r (%d bytes de senha)" % (usuario, tam_s))

    # o teste quer saber se o cliente MANDOU usuario; qualquer um serve
    _falhar(c, "authentication rejected")


def servir(porta, esquema, verboso=True, uma_vez=False):
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", porta))
    srv.listen(5)
    if verboso:
        print("servidor falso (%s) em 127.0.0.1:%d" % (esquema, porta),
              flush=True)
    while True:
        c, _ = srv.accept()
        try:
            atender(c, esquema, verboso)
        except Exception as e:
            if verboso:
                print("   [servidor] conexão terminou:", e, flush=True)
        finally:
            c.close()
        if uma_vez:
            srv.close()
            return


def em_thread(porta, esquema):
    """Sobe o servidor numa thread daemon. Devolve quando estiver ouvindo."""
    t = threading.Thread(target=servir,
                         kwargs=dict(porta=porta, esquema=esquema,
                                     verboso=False),
                         daemon=True)
    t.start()
    return t


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--porta", type=int, default=5910)
    ap.add_argument("--esquema", default="plain",
                    choices=("senha", "plain", "recusa"))
    a = ap.parse_args()
    try:
        servir(a.porta, a.esquema)
    except KeyboardInterrupt:
        sys.exit(0)
