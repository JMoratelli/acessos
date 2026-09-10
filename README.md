# Acessos

Gerenciador de acesso remoto às máquinas — VNC, RDP, SSH e transferência de
arquivos, em abas.

## Instalar

```bash
./instalar.sh
```

Funciona em **Arch** e **Fedora**. Instala tudo em `~/.local`, sem Flatpak,
sem AppImage, sem container. Só pede `sudo` para os pacotes do sistema.

Depois: `acessos` no terminal, ou pelo menu de aplicativos.

| Opção | O que faz |
|---|---|
| `./instalar.sh` | instala ou atualiza |
| `./instalar.sh --sem-rdp` | pula o rdpshim; RDP fica indisponível (sem fallback) |
| `./instalar.sh --verificar` | testa o que já está instalado |
| `./instalar.sh --remover` | desinstala (preserva a configuração) |

## Estrutura

```
instalar.sh        instalação completa
python/            a aplicação
  acessos.py         principal
  vncwidget.py       VNC próprio (libvncclient)
  rdpwidget.py       RDP próprio (libfreerdp3)
  rdp.py             reexporta rdpwidget como "rdp" (nome esperado pelo app)
  sftp.py            transferência de arquivos
  cofre.py           senhas cifradas
src/
  vncshim.c          ponte C entre o Python e a libvncclient
  rdpshim.c          ponte C entre o Python e a libfreerdp3
icones/
  acessos.svg
```

## O que é compilado, e por quê

**vncshim** — o gtk-vnc congela a interface por 2 a 3 segundos após
repinturas grandes. O mesmo defeito aparece no GNOME Connections (que usa a
mesma biblioteca) e não aparece no Remmina (que usa libvncclient). Este
projeto usa libvncclient direto, através de um shim em C.

**rdpshim** — mesma ideia, para RDP: fala direto com a libfreerdp3, sem
GObject Introspection e sem processo externo. Embute em qualquer backend
(X11 ou Wayland), pede confirmação do operador para certificado novo ou
alterado (nunca aceita calado, estilo SSH), sincroniza clipboard de texto e
ajusta a resolução dinamicamente durante a sessão.

Os dois shims precisam ser **compilados na máquina**: as structs internas
das bibliotecas (`rfbClient`, `rdpSettings`) têm blocos condicionais de
compilação, e um binário feito contra outra build teria os offsets
errados — o que causa corrupção de memória silenciosa.

Não há mais segundo motor de RDP como reserva (o projeto já teve dois:
gtk-frdp e, antes disso, xfreerdp externo via `Gtk.Socket`) — sem o
rdpshim compilado, a aba de RDP simplesmente avisa que está indisponível.

## Notas de uso

**Wayland nativo é a configuração recomendada.** O congelamento da interface
do VNC só acontece sob XWayland; em Wayland nativo não ocorre.

**O toggle `x11` no INI não afeta mais o RDP** (ele embute em qualquer
backend). A escolha entre X11/Wayland passou a ser só sobre captura de
teclado: XWayland dá captura total (Super, Alt+Tab inclusos), Wayland
nativo é nítido em qualquer escala mas a captura pode ficar parcial,
dependendo do compositor.

**Certificado por IP:** conectar por IP em servidor cujo certificado foi
emitido para um nome (`redemachado.local`) aciona o diálogo de confirmação
mesmo assim (em vez de recusar direto) — confira a impressão digital antes
de aceitar.

## Diagnóstico

```bash
ACESSOS_PULSO=1 acessos          # avisa se o laço de eventos travar
kill -USR1 $(pgrep -f acessos.py)  # despeja a pilha Python, sem matar
```

Para ver os frames C, que o `faulthandler` não mostra:

```bash
pip install --user py-spy
py-spy dump --native --pid $(pgrep -f acessos.py)
```
