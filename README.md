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
| `./instalar.sh --sem-rdp` | pula o gtk-frdp; RDP usa o xfreerdp externo |
| `./instalar.sh --verificar` | testa o que já está instalado |
| `./instalar.sh --remover` | desinstala (preserva a configuração) |

## Estrutura

```
instalar.sh        instalação completa
python/            a aplicação
  acessos.py         principal
  vncwidget.py       VNC próprio (libvncclient)
  rdp.py             RDP embutido (gtk-frdp)
  sftp.py            transferência de arquivos
  cofre.py           senhas cifradas
src/
  vncshim.c          ponte C entre o Python e a libvncclient
icones/
  acessos.svg
```

## O que é compilado, e por quê

**vncshim** — o gtk-vnc congela a interface por 2 a 3 segundos após
repinturas grandes. O mesmo defeito aparece no GNOME Connections (que usa a
mesma biblioteca) e não aparece no Remmina (que usa libvncclient). Este
projeto usa libvncclient direto, através de um shim em C.

O shim precisa ser **compilado na máquina**: a struct `rfbClient` tem blocos
condicionais de compilação, e um binário feito contra outra build teria os
offsets errados — o que causa corrupção de memória silenciosa.

**gtk-frdp** — dá o RDP embutido na aba sob Wayland, coisa que o
`xfreerdp` + `Gtk.Socket` não consegue (XEmbed só existe em X11). Não é
empacotado em nenhuma distro; o `instalar.sh` compila e aplica uma correção
de um bug do cursor que derruba a aplicação.

## Notas de uso

**Wayland nativo é a configuração recomendada.** O congelamento da interface
só acontece sob XWayland. Se você tinha `x11 = 1` no `conexoes.ini`, remova.

**O RDP embutido só entra em Wayland nativo.** Sob X11 o Acessos usa o
`xfreerdp`, que já embute na aba por outro caminho, e é mais maduro.

**Certificado por IP:** conectar por IP em servidor cujo certificado foi
emitido para um nome (`redemachado.local`) faz o FreeRDP recusar. Conecte
pelo nome, quando possível.

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

Não use `G_DEBUG=fatal-criticals` junto com o RDP embutido: o gtk-frdp emite
avisos inofensivos que, com essa flag, viram abort.
