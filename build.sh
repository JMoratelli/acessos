#!/usr/bin/env bash
# build.sh — arquivo unico: gera o manifest, os patches e o .desktop, e
# constroi o Flatpak do Acessos.
#
# Uso (a partir da RAIZ do projeto):
#   ./build.sh              constroi e gera o bundle .flatpak
#   ./build.sh --instalar   constroi, gera o bundle e instala ou ATUALIZA
#   ./build.sh --limpar     apaga build/ inteiro, cache incluso, e sai
#                           (terra arrasada: a proxima build recompila TUDO)
#
# Estrutura esperada (a mesma do repositorio, sem mover nada):
#   python/*.py        a aplicacao (acessos, vncwidget, sftp, cofre, rdp,
#                      ssh, massa, massa_ui, tema, dialogo_ui, ...)
#   src/vncshim.c      ponte C para a libvncclient
#   icones/acessos.svg
#
# Tudo o que este script gera vai para ./build — nada e escrito na raiz,
# entao ele nao suja o repositorio nem colide com o instalar.sh.
#
# DECISOES DESTA VERSAO
#   * VNC: so o embutido (libvncserver + vncshim). O gtk-vnc/gtk-vnc2 saiu
#     do projeto — congelava de 2 a 3 minutos e nao volta.
#   * RDP: so o embutido em Wayland nativo (gtk-frdp). Nao ha mais caminho
#     de xfreerdp externo aqui.
#   * O modulo "freerdp" CONTINUA no manifest: nao e um cliente alternativo,
#     e a BIBLIOTECA (freerdp3/winpr3) contra a qual o gtk-frdp linka. O
#     runtime do GNOME nao a traz; sem ela o meson do gtk-frdp nem comeca.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

APPID=org.jj.Acessos
INSTALAR=0
case "${1:-}" in
    --instalar) INSTALAR=1 ;;
    --limpar)
        # Terra arrasada, sob pedido explicito. Leva junto as fontes
        # baixadas, entao a proxima build rebaixa FreeRDP, gtk-frdp e VTE e
        # recompila os tres — conte com uns 40 minutos.
        rm -rf build
        echo "build/ apagado (inclui o cache em build/.flatpak-builder)."
        exit 0 ;;
    "") ;;
    *)  sed -n '4,9p' "$0" | sed 's/^#\s\?//'; exit 0 ;;
esac

# Conferencia de estrutura. A lista nominal de modulos Python foi abolida de
# proposito: ela quebrava calada toda vez que um arquivo novo entrava em
# python/ (foi o que aconteceu com cofre.py, rdp.py e depois com massa.py,
# ssh.py e tema.py). Basta existir python/ com acessos.py dentro.
for f in python/acessos.py src/vncshim.c icones/acessos.svg; do
    [ -f "$f" ] || { echo "ERRO: falta $f." >&2; exit 1; }
done

for c in flatpak flatpak-builder ostree; do
    command -v $c >/dev/null || {
        echo "ERRO: $c nao encontrado." >&2
        echo "  Arch  : sudo pacman -S flatpak flatpak-builder ostree" >&2
        echo "  Fedora: sudo dnf install flatpak flatpak-builder ostree" >&2
        exit 1; }
done

echo "== fontes desta build =="
md5sum python/*.py src/vncshim.c | sed 's/^/   /'
echo

rm -rf build/manifest
mkdir -p build/manifest

# --------------------------------------------------------------------
# Arquivos gerados. Delimitadores entre aspas: o conteudo sai LITERAL,
# sem o bash expandir $VAR nem $(comando) que existam la dentro.
# --------------------------------------------------------------------
cat > "build/manifest/$APPID.yml" <<'MANIFEST_ACESSOS_EOF'
app-id: org.jj.Acessos
runtime: org.gnome.Platform
runtime-version: '47'
sdk: org.gnome.Sdk
command: acessos.py

# ---------------------------------------------------------------------------
# PERMISSOES
#
#   --socket=wayland e a interface. O congelamento que custou dias de
#   investigacao so acontece sob XWayland; em Wayland nativo nao ocorre.
#
#   --socket=fallback-x11 NAO e recaida de interface: o lancador forca
#   GDK_BACKEND=wayland, entao o GTK nunca usa X11. Ele existe porque a
#   deteccao de layout de teclado do FreeRDP chama XOpenDisplay
#   (libfreerdp/locale/keyboard_x11.c:118, keyboard_xkbfile.c:308). Sem
#   NENHUM socket X11 essa chamada falha, o FreeRDP assume layout US e o
#   teclado escreve qualquer caractere menos o certo.
#
#   --filesystem=home: o SFTP le e grava arquivos do usuario, e o
#   conexoes.ini vive em ~/.config/acessos.
# ---------------------------------------------------------------------------
finish-args:
  - --share=network
  - --share=ipc
  - --socket=wayland
  - --socket=fallback-x11
  - --socket=pulseaudio          # audio do RDP
  - --device=dri
  - --filesystem=home
  # O FreeRDP guarda os certificados ja aceitos em ~/.config/freerdp. Sem
  # esta linha o sandbox usa o seu proprio armazem, que comeca VAZIO — e o
  # FreeRDP interpreta "nao tenho a chave deste host" como "a chave MUDOU",
  # despejando o alarme de man-in-the-middle e recusando a conexao em todo
  # servidor, inclusive nos nunca acessados.
  - --filesystem=xdg-config/freerdp
  - --talk-name=org.freedesktop.secrets

cleanup:
  - /include
  - /lib/pkgconfig
  - /share/man
  - /share/gtk-doc
  - '*.a'
  - '*.la'

modules:

  # -------------------------------------------------------------------------
  # 1. libvncserver — fornece a libvncclient usada pelo nosso shim.
  #
  # CRITICO: precisa ser compilada AQUI, e nao copiada do host. A struct
  # rfbClient tem blocos condicionais (#ifdef HAVE_LIBZ, LIBJPEG, SASL,
  # MUTEX) cujo tamanho depende das flags de compilacao. O vncshim.c calcula
  # os offsets a partir do header — se o shim for compilado contra uma
  # libvncclient e rodar contra outra, os offsets nao batem, ele escreve nos
  # campos errados e o resultado e corrupcao de memoria silenciosa.
  # -------------------------------------------------------------------------
  - name: libvncserver
    buildsystem: cmake-ninja
    builddir: true
    config-opts:
      # O 0.9.15 declara cmake_minimum_required(VERSION 3.4). O CMake das
      # SDKs atuais recusa isso com "Compatibility with CMake < 3.5 has been
      # removed". Esta flag reabilita as politicas antigas sem editar codigo
      # de terceiros.
      - -DCMAKE_POLICY_VERSION_MINIMUM=3.5
      - -DCMAKE_BUILD_TYPE=Release
      - -DWITH_EXAMPLES=OFF
      - -DWITH_TESTS=OFF
      - -DWITH_FFMPEG=OFF
      - -DWITH_GTK=OFF
    sources:
      - type: archive
        url: https://github.com/LibVNC/libvncserver/archive/refs/tags/LibVNCServer-0.9.15.tar.gz
        sha256: 62352c7795e231dfce044beb96156065a05a05c974e5de9e023d688d8ff675d7

  # -------------------------------------------------------------------------
  # 2. vncshim — a ponte C entre o Python e a libvncclient.
  #    Compilado contra a libvncclient do modulo acima, como deve ser.
  #    Esta e a UNICA implementacao de VNC do projeto.
  # -------------------------------------------------------------------------
  - name: vncshim
    buildsystem: simple
    build-commands:
      - gcc -shared -fPIC -O2 -Wall -o libvncshim.so vncshim.c
        $(pkg-config --cflags --libs libvncclient)
      - install -Dm755 libvncshim.so /app/lib/acessos/libvncshim.so
    sources:
      - type: file
        path: ../../src/vncshim.c

  # -------------------------------------------------------------------------
  # 3. libfuse — dependencia do gtk-frdp.
  #
  # O gtk-frdp exige fuse3 no meson e o runtime do GNOME nao traz. Serve
  # para a copia de ARQUIVOS pela area de transferencia do RDP; texto no
  # clipboard nao depende disto. O pedido e incondicional, entao nao ha
  # opcao de desligar — resta fornecer a biblioteca.
  #
  # useroot=false e obrigatorio: o build nao roda como root e nao pode
  # aplicar setuid.
  # -------------------------------------------------------------------------
  - name: libfuse
    buildsystem: meson
    config-opts:
      - -Duseroot=false
      - -Dutils=false
      - -Dexamples=false
      - -Dtests=false
      - -Dinitscriptdir=
    sources:
      - type: archive
        url: https://github.com/libfuse/libfuse/archive/refs/tags/fuse-3.16.2.tar.gz
        sha256: 1bc306be1a1f4f6c8965fbdd79c9ccca021fdc4b277d501483a711cbd7dbcd6c

  # -------------------------------------------------------------------------
  # 4. FreeRDP 3 — BIBLIOTECA, nao cliente.
  #
  # Nao existe mais caminho de xfreerdp externo neste manifest: o RDP e
  # sempre o widget embutido. Ainda assim o modulo fica, porque o gtk-frdp
  # linka contra freerdp3/winpr3 e o runtime do GNOME nao os traz — sem
  # este modulo o meson do gtk-frdp falha em dependency('freerdp3').
  #
  # WITH_SERVER/SAMPLE/SDL desligados: so as bibliotecas cliente interessam.
  # FFMPEG/SWSCALE fora para nao arrastar a pilha de codecs — RemoteFX e GFX
  # continuam; o que se perde e H.264 dentro da sessao.
  # -------------------------------------------------------------------------
  - name: freerdp
    buildsystem: cmake-ninja
    # O FreeRDP aborta se o CMake rodar dentro da arvore de fontes
    # (PreventInSourceBuilds.cmake) e o flatpak-builder faz isso por padrao.
    builddir: true
    config-opts:
      - -DCMAKE_BUILD_TYPE=Release
      - -DWITH_SERVER=OFF
      - -DWITH_SAMPLE=OFF
      - -DWITH_CLIENT_SDL=OFF
      - -DWITH_SWSCALE=OFF
      - -DWITH_FFMPEG=OFF
      - -DWITH_MANPAGES=OFF
      # WITH_X11=ON apesar de rodarmos em Wayland nativo.
      #
      # Nao e para usar o cliente X11 — e porque em
      # libfreerdp/locale/CMakeLists.txt os fontes que DETECTAM o layout de
      # teclado (keyboard_xkbfile.c, keyboard_x11.c, xkb_layout_ids.c) so
      # entram na compilacao dentro do "if(WITH_X11)". WITH_WAYLAND sozinho
      # apenas define a macro em keyboard.c:304, sem trazer o codigo que faz
      # o trabalho — foi o que tentamos antes e o teclado continuou errado
      # (backspace escrevendo letras, acentos trocados).
      - -DWITH_X11=ON
      - -DWITH_WAYLAND=ON
      # URBDRC e redirecionamento de USB ([MS-RDPEUSB]) e exige libusb, que
      # a SDK nao tem. Nao ha uso para USB local dentro do terminal de um
      # PDV, entao o canal sai inteiro.
      - -DCHANNEL_URBDRC=OFF
      # FUSE fica ON: a libfuse do modulo anterior ja esta disponivel.
    sources:
      - type: archive
        url: https://github.com/FreeRDP/FreeRDP/archive/refs/tags/3.9.0.tar.gz
        sha256: a1d2946c67037bf6bb8aa2f0441c7cacd5e92c835d776cecffb4fcdbaa45ec4f

  # -------------------------------------------------------------------------
  # 5. gtk-frdp — o RDP do projeto, embutido como widget GTK.
  #
  # OS PATCHES SAO OBRIGATORIOS, nao um detalhe. A instalacao nativa
  # (instalar.sh) compila o gtk-frdp CORRIGIDO; se o Flatpak compilar o
  # master puro, o certificado nunca funciona aqui e funciona la — a
  # correcao que liga FreeRDP_IgnoreCertificate mora nesses patches, e sem
  # ela o FreeRDP recusa a conexao ANTES de emitir o sinal de verificacao.
  #
  # Sao seis: cursor (aborta a aplicacao), FUSE do clipboard, update() em
  # segundo plano, SELECT_TIMEOUT (v2, com g_timeout_add), certificado,
  # layout de teclado e scancode. Sao sete.
  # -------------------------------------------------------------------------
  - name: gtk-frdp
    buildsystem: simple
    build-commands:
      - bash aplicar-patches-frdp.sh .
      # -Dlibdir=lib e OBRIGATORIO. Sem ele o meson herda o libdir do host
      # e instala em /app/lib64 — a .so e o GtkFrdp-0.2.typelib vao para um
      # caminho que o Flatpak NAO procura. O sintoma nao e erro de build:
      # o app sobe normalmente, o gi nao acha o namespace GtkFrdp, o rdp.py
      # marca TEM_FRDP=False e a aba cai calada no xfreerdp externo.
      - meson setup _build --prefix=/app --libdir=lib -Dexamples=false
      - ninja -C _build
      - ninja -C _build install
    sources:
      - type: git
        url: https://gitlab.gnome.org/GNOME/gtk-frdp.git
        branch: master
        # PENDENTE: trocar por commit fixo antes de considerar producao.
        # Com 'master' a build muda sozinha entre uma compilacao e outra — e
        # os patches dependem de trechos especificos do fonte. Pegue o sha:
        #   git ls-remote https://gitlab.gnome.org/GNOME/gtk-frdp.git master
      - type: file
        path: aplicar-patches-frdp.sh

  # -------------------------------------------------------------------------
  # 6. VTE (GTK3) — widget de terminal das abas de Shell (python/ssh.py).
  #
  # As runtimes recentes do GNOME trazem so a variante GTK4 do VTE, e o app
  # pede Vte-2.91 (GTK3) — o sintoma e "namespace Vte not available" ao
  # abrir a aba de Shell, que ja aconteceu neste projeto.
  #
  # -Dgir=true e obrigatorio: e o que gera o Vte-2.91.typelib. Sem o typelib
  # o gi nao enxerga o namespace mesmo com a .so instalada.
  # -Dgnutls=true criptografa o scrollback que o VTE despeja em disco —
  # esse buffer contem sessao de producao dos PDVs.
  # -------------------------------------------------------------------------
  - name: vte
    buildsystem: meson
    config-opts:
      # Mesmo cuidado do gtk-frdp: sem --libdir=lib o Vte-2.91.typelib cai
      # em /app/lib64 e o gi nao enxerga o namespace.
      - --libdir=lib
      - -Dgtk3=true
      - -Dgtk4=false
      - -Dgir=true
      - -Dvapi=false
      - -Ddocs=false
      - -Da11y=true
      - -Dgnutls=true
    sources:
      - type: archive
        url: https://github.com/GNOME/vte/archive/refs/tags/0.76.4.tar.gz
        sha256: 88979af0b02bac3c6d0bc95fcbeaf0ee025a7fc7a5b127155188b90718af0e78

  # -------------------------------------------------------------------------
  # 7. ssh, ssh-keygen e sshpass.
  #
  # Sem eles a aba de Shell PEDE A SENHA no terminal, ignorando a que ja
  # esta gravada — o proprio app avisa ("senha definida mas sshpass
  # ausente"). O runtime do GNOME nao traz cliente SSH.
  # -------------------------------------------------------------------------
  - name: openssh
    config-opts:
      - --without-pam
      - --without-selinux
      - --without-kerberos5
      - --disable-strip
    make-install-args:
      - install-nokeys          # so o cliente; nao geramos chaves de host
    post-install:
      - rm -f /app/sbin/sshd /app/bin/ssh-keysign /app/bin/ssh-pkcs11-helper
    sources:
      - type: archive
        url: https://github.com/openssh/openssh-portable/archive/refs/tags/V_9_9_P2.tar.gz
        # O tarball ja traz ./configure pronto, nao precisa de autoreconf.
        sha256: 082dffcf651b9db762ddbe56ca25cc75a0355a7bea41960b47f3c139974c5e3e

  - name: sshpass
    # Compilado direto, sem autotools: num checkout git os timestamps fazem
    # o make entrar em maintainer mode e exigir aclocal-1.15 exatamente
    # nessa versao, que a SDK nao tem. E um unico main.c.
    buildsystem: simple
    build-commands:
      - |
        cat > config.h <<'EOF'
        #define PACKAGE_NAME "sshpass"
        #define PACKAGE_STRING "sshpass 1.06"
        #define VERSION "1.06"
        /* Trecho que o sshpass procura na saida do ssh para reconhecer o
         * prompt de senha. Casa com "Password:" e "password:". */
        #define PASSWORD_PROMPT "assword"
        EOF
      # -DHAVE_CONFIG_H: em main.c o include do config.h esta atras de
      #   "#if HAVE_CONFIG_H"; sem isto o arquivo acima e ignorado.
      # -D_GNU_SOURCE: expoe grantpt/unlockpt/ptsname. Sem isto ptsname
      #   devolve int em vez de char*, o que quebraria em execucao.
      - gcc -O2 -Wall -DHAVE_CONFIG_H -D_GNU_SOURCE -I. -o sshpass main.c
      - install -Dm755 sshpass /app/bin/sshpass
    sources:
      - type: git
        url: https://github.com/kevinburke/sshpass
        # Espelho sem tags, entao commit fixo: tarball de branch pode ser
        # regerado pelo GitHub e invalidar o checksum sem aviso.
        commit: ca7baa670d799b85ff91b4056e0a2bf9772cb2cf

  # -------------------------------------------------------------------------
  # 8. Dependencias Python.
  #
  # pip com rede liberada no build em vez do flatpak-pip-generator: evita
  # manter um requirements.txt com dezenas de wheels fixadas a mao. Para
  # publicar na Flathub (que proibe rede no build), gere com
  #   python3 flatpak-pip-generator paramiko cryptography argon2-cffi
  # e substitua este modulo inteiro pelo json produzido.
  #
  # cryptography vem junto com o paramiko, mas esta explicito porque o
  # cofre.py depende dele diretamente. argon2-cffi e opcional por design
  # (sem ele o cofre cai em PBKDF2), mas resiste bem melhor a GPU.
  # -------------------------------------------------------------------------
  - name: python-deps
    buildsystem: simple
    build-options:
      build-args:
        - --share=network
    build-commands:
      - pip3 install --prefix=/app --no-warn-script-location
        paramiko cryptography argon2-cffi

  # -------------------------------------------------------------------------
  # 9. O aplicativo.
  # -------------------------------------------------------------------------
  - name: acessos
    buildsystem: simple
    build-commands:
      # Wildcard de proposito: a lista nominal quebrava calada toda vez que
      # um modulo novo entrava no projeto (cofre.py, rdp.py, e depois
      # massa.py, massa_ui.py, ssh.py, tema.py, dialogo_ui.py). Se esta em
      # python/, vai para o pacote.
      - install -d /app/lib/acessos
      - install -m644 python/*.py /app/lib/acessos/
      - chmod 755 /app/lib/acessos/acessos.py
      # lancador: garante que os modulos ao lado sejam encontrados
      - install -Dm755 acessos-launcher /app/bin/acessos.py
      - install -Dm644 org.jj.Acessos.desktop
        /app/share/applications/org.jj.Acessos.desktop
      # Icone SVG vai para scalable, onde o tema procura vetor.
      - install -Dm644 icones/acessos.svg
        /app/share/icons/hicolor/scalable/apps/org.jj.Acessos.svg
    sources:
      # Cada diretorio do projeto entra explicitamente, com dest. Usar
      # "path: ." aqui copiava o diretorio do manifest, e o sintoma era:
      #   install: cannot stat 'python/*.py': No such file or directory
      - type: dir
        path: ../../python
        dest: python
      - type: dir
        path: ../../icones
        dest: icones
      # O .desktop fica solto ao lado do manifest, entao precisa da propria
      # entrada — sem ela o build quebra em
      #   install: cannot stat 'org.jj.Acessos.desktop'
      - type: file
        path: org.jj.Acessos.desktop
      - type: script
        dest-filename: acessos-launcher
        commands:
          # PYTHONPATH: todos os modulos ficam ao lado do acessos.py e sao
          # importados por nome.
          - export PYTHONPATH="/app/lib/acessos:$PYTHONPATH"
          # Cinto de seguranca para o libdir: se algum modulo voltar a
          # instalar em /app/lib64, o typelib e a .so continuam sendo
          # encontrados em vez de o app cair calado no caminho externo.
          - export GI_TYPELIB_PATH="/app/lib/girepository-1.0:/app/lib64/girepository-1.0${GI_TYPELIB_PATH:+:$GI_TYPELIB_PATH}"
          - export LD_LIBRARY_PATH="/app/lib:/app/lib64:/app/lib/acessos${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
          # A libvncshim.so e procurada ao lado do vncwidget.py; ambos estao
          # em /app/lib/acessos, entao nada mais e preciso aqui.
          #
          # GTK_MODULES: a sessao do host exporta canberra-gtk-module e
          # pk-gtk-module, isso vaza para o sandbox onde eles nao existem, e
          # o GTK reclama no console. Inofensivo, mas polui o log.
          - unset GTK_MODULES
          # GDK_BACKEND=wayland e a trava que torna o fallback-x11 seguro.
          # Com os dois sockets disponiveis o GTK escolheria sozinho, e se
          # escolhesse X11 voltariamos ao congelamento sob XWayland. Aqui a
          # escolha nao e dele: a interface e Wayland, e o X11 fica so para
          # o XOpenDisplay do FreeRDP ao detectar o layout de teclado.
          - export GDK_BACKEND=wayland
          # LAYOUT DE TECLADO DO RDP EMBUTIDO.
          #
          # Sem DISPLAY no sandbox o FreeRDP nao consegue autodetectar e
          # assume US. O patch do gtk-frdp le esta variavel; 0x416 e
          # pt-BR/ABNT2. Para outro layout, mude aqui ou exporte antes de
          # chamar o app (ex.: 0x409 = US).
          - export ACESSOS_KBD_LAYOUT="${ACESSOS_KBD_LAYOUT:-0x416}"
          # Deslocamento X11 -> evdev do scancode. 8 e o valor correto; 0
          # desliga a correcao caso algum servidor precise do numero cru.
          - export ACESSOS_KBD_OFFSET="${ACESSOS_KBD_OFFSET:-8}"
          # CERTIFICADOS DO FREERDP.
          #
          # O FreeRDP guarda os aceitos em $XDG_CONFIG_HOME/freerdp/server/.
          # Dentro do Flatpak essa variavel aponta para
          # ~/.var/app/<app-id>/config, que comeca VAZIO — e o FreeRDP le
          # "nao tenho a chave deste host" como "a chave MUDOU", despeja o
          # alarme de man-in-the-middle e recusa a conexao. Os certificados
          # ja aceitos nativamente ficam em ~/.config/freerdp, que o sandbox
          # alcanca pelo --filesystem=xdg-config/freerdp.
          #
          # O symlink liga os dois. O _caminho_certificado() do rdp.py, que
          # tambem monta o caminho a partir de XDG_CONFIG_HOME, passa a
          # resolver no mesmo arquivo — entao "esquecer certificado" pela
          # interface apaga o certificado certo.
          - mkdir -p "$HOME/.config/freerdp/server"
          - '[ -e "$XDG_CONFIG_HOME/freerdp" ] || ln -s "$HOME/.config/freerdp" "$XDG_CONFIG_HOME/freerdp"'
          # --wayland e OBRIGATORIO, nao conveniencia. O proprio acessos.py
          # descreve a flag como "forca Wayland nativo: captura de teclado,
          # RDP em janela" — ela muda o caminho de RDP. Sem ela o app
          # autodetecta, e o observado foi a aba de RDP abrir, aceitar o
          # open_host e ficar em "conectando" para sempre, sem disparar
          # nenhum sinal de erro do gtk-frdp.
          - exec python3 /app/lib/acessos/acessos.py --wayland "$@"
MANIFEST_ACESSOS_EOF

cat > build/manifest/aplicar-patches-frdp.sh <<'PATCHES_ACESSOS_EOF'
#!/usr/bin/env bash
# aplicar-patches-frdp.sh — aplica no fonte do gtk-frdp as mesmas correcoes
# que o instalar.sh aplica na instalacao nativa.
#
# Uso:  ./aplicar-patches-frdp.sh <diretorio-do-fonte-gtk-frdp>
#
# Existe para que o Flatpak nao compile gtk-frdp "puro" enquanto a
# instalacao nativa compila corrigido — divergencia que ja custou horas.
#
# ATENCAO A DUPLICACAO: extraido do instalar.sh. Ajustou um patch la,
# ajuste aqui — ou faca o instalar.sh chamar este script, e fica um lugar so.
set -euo pipefail

FONTE_FRDP="${1:?uso: $0 <diretorio-do-fonte-gtk-frdp>}"
[ -d "$FONTE_FRDP/src" ] || {
    echo "ERRO: $FONTE_FRDP nao parece ser o fonte do gtk-frdp." >&2; exit 1; }

# CORRECAO DO CURSOR.
#
# O gtk-frdp so garante priv->scale = 1.0 DEPOIS de ja ter multiplicado
# esse valor em x, y, w e h. Quando a sessao nao usa escala — nosso caso,
# porque ajustamos o tamanho por allow-resize — o scale continua ZERO: a
# superficie do cursor nasce com largura zero e o GDK aborta com
#     gdk_cursor_new_from_surface: assertion
#     '0 <= x && x < cairo_image_surface_get_width (surface)' failed
# derrubando a aplicacao assim que o servidor manda a forma do cursor.
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYFIX'
import sys
caminho = sys.argv[1]
src = open(caminho, encoding="utf-8").read()
guard = """    if (!self->priv->scaling) {
      self->priv->scale = 1.0;
    }
"""
alvo = "    double x = priv->cursor->pointer.xPos * priv->scale;"
if "JA_CORRIGIDO_ACESSOS" in src:
    print("       correção do cursor: já aplicada")
elif alvo not in src:
    print("       AVISO: trecho do cursor não encontrado (upstream mudou);")
    print("       verifique se o bug ainda existe em frdp-session.c")
else:
    i = src.index(alvo)
    depois = src[i:].replace(guard, "", 1)
    marca = ("    /* JA_CORRIGIDO_ACESSOS: scale antes do uso, senão o\n"
             "     * cursor nasce com largura zero e o GDK aborta. */\n")
    open(caminho, "w", encoding="utf-8").write(src[:i] + marca + guard + depois)
    print("       correção do cursor aplicada")
PYFIX

# CORRECAO DO FUSE DO CLIPBOARD.
#
# frdp-channel-clipboard.c cria o diretorio com
#     g_mkdtemp (g_strdup_printf ("%s/clipboard-XXXXXX/", ...))
# O g_mkdtemp EXIGE que o template termine em exatamente seis X. Aqui
# termina em "XXXXXX/" — ha uma barra depois. A funcao rejeita, devolve
# NULL, fuse_directory fica nulo, e o fuse_session_mount(session, NULL)
# falha com "fuse: reading device: Descritor de arquivo invalido". A
# thread do FUSE fica num estado ruim e trava a abertura da PROXIMA
# sessao — sintoma: so da para abrir um widget.
#
# Tirar a barra corrige tambem a comparacao com g_path_get_dirname, que
# nunca traz barra final — a igualdade jamais era verdadeira.
python3 - "$FONTE_FRDP/src/frdp-channel-clipboard.c" <<'PYFUSE'
import sys
caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li o arquivo do clipboard (%s)" % e)
    sys.exit(0)

ruim = '"%s/clipboard-XXXXXX/"'
bom = '"%s/clipboard-XXXXXX"'
if ruim not in src:
    if bom in src:
        print("       correção do FUSE: já aplicada")
    else:
        print("       AVISO: template do g_mkdtemp não encontrado;")
        print("       verifique se o bug do clipboard ainda existe")
    sys.exit(0)
src = src.replace(ruim, bom, 1)
open(caminho, "w", encoding="utf-8").write(src)
print("       correção do FUSE aplicada (g_mkdtemp sem barra final)")
PYFUSE

# CORRECAO DO update() COM ABA EM SEGUNDO PLANO.
#
# frdp-session.c comeca o update() com
#     if (priv->display != NULL && !gtk_widget_get_mapped (priv->display))
#       return TRUE;
# ou seja: widget nao mapeado nao processa NADA — nem o
# WaitForMultipleObjects que drena os eventos do FreeRDP — mas continua
# agendado. Com DUAS sessoes, a que esta em aba de fundo nunca drena seus
# eventos e a thread principal trava esperando handles que ninguem
# processa. Sintoma: so da para manter um widget.
#
# Pular o DESENHO de uma aba invisivel esta certo; pular o processamento
# de eventos, nao.
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYUPD'
import sys
caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

alvo = """  if (priv->display != NULL && !gtk_widget_get_mapped (priv->display))
    return TRUE;
"""
novo = """  /* JA_CORRIGIDO_ACESSOS_UPDATE
   * Antes havia aqui um "return TRUE" quando o widget não estava mapeado.
   * Isso pulava TAMBÉM o WaitForMultipleObjects abaixo, então uma sessão em
   * aba de fundo nunca drenava seus eventos e travava as demais. Agora só
   * o redesenho é pulado; o processamento de eventos segue sempre. */
  gboolean frdp_visivel = (priv->display == NULL ||
                           gtk_widget_get_mapped (priv->display));
"""
if "JA_CORRIGIDO_ACESSOS_UPDATE" in src:
    print("       correção do update: já aplicada")
    sys.exit(0)
if alvo not in src:
    print("       AVISO: trecho do update() não encontrado (upstream mudou);")
    print("       verifique se a sessão em segundo plano ainda trava")
    sys.exit(0)

src = src.replace(alvo, novo, 1)

# A fila continua sendo ESVAZIADA mesmo invisivel — so nao pedimos
# redesenho. Deixar acumular faria a aba de fundo crescer sem limite. Ao
# reentrar na aba, o acessos.py forca um redesenho completo.
fila = """      rectangle = g_queue_pop_head (priv->area_draw_queue);
      gtk_widget_queue_draw_area (priv->display, rectangle->x, rectangle->y, rectangle->width, rectangle->height);
      g_free (rectangle);"""
fila_nova = """      rectangle = g_queue_pop_head (priv->area_draw_queue);
      if (frdp_visivel)
        gtk_widget_queue_draw_area (priv->display, rectangle->x, rectangle->y, rectangle->width, rectangle->height);
      g_free (rectangle);"""
if fila in src:
    src = src.replace(fila, fila_nova, 1)
else:
    print("       AVISO: fila de redesenho não encontrada; revise o patch")

open(caminho, "w", encoding="utf-8").write(src)
print("       correção do update aplicada (eventos drenam em segundo plano)")
PYUPD

# CORRECAO DO SELECT_TIMEOUT (v2).
#
# O update() roda NA THREAD PRINCIPAL e chama
#     WaitForMultipleObjects (..., SELECT_TIMEOUT)
# com SELECT_TIMEOUT = 20 ms — cada sessao segura o laco de eventos por ate
# 20 ms a cada volta. Com DUAS sao 40 ms por ciclo e o relogio de quadros do
# GTK (~16 ms) nao roda mais: interface RESPONSIVA porem PARADA.
#
# A v1 desta correcao baixava o timeout mas deixava o reagendamento em
# g_idle_add — combinacao que vira laço ocupado. A v2 faz as duas coisas:
# baixa o timeout E troca g_idle_add por g_timeout_add(5 ms).
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYSEL'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

if "JA_CORRIGIDO_ACESSOS_SELECT_V2" in src:
    print("       correção do SELECT_TIMEOUT: já aplicada")
    sys.exit(0)
if "JA_CORRIGIDO_ACESSOS_SELECT" in src:
    # v1 deixou SELECT_TIMEOUT 0 com g_idle_add — laço ocupado. Desfazemos
    # a v1 no proprio texto e seguimos para aplicar a v2 logo abaixo.
    src = re.sub(r"/\* JA_CORRIGIDO_ACESSOS_SELECT:.*?\*/\n", "", src,
                 count=1, flags=re.S)
    src = re.sub(r"#define\s+SELECT_TIMEOUT\s+\d+",
                 "#define SELECT_TIMEOUT 20", src, count=1)
    print("       correção do SELECT_TIMEOUT: versão antiga desfeita")

achou = re.search(r"#define\s+SELECT_TIMEOUT\s+(\d+)", src)
if not achou:
    print("       AVISO: SELECT_TIMEOUT não encontrado (upstream mudou)")
    sys.exit(0)

valor = achou.group(1)

# O padrao precisa aceitar o prefixo: no fonte a linha e
#     self->priv->update_id = g_idle_add ((GSourceFunc) update, self);
# Um regex ancorado em "update_id\s*=" sem \b escolhia 0 — justamente o
# valor que vira laço ocupado com g_idle_add.
usa_idle = re.search(r"update_id\s*=\s*g_idle_add\b", src) is not None
alvo_ms = "1" if usa_idle else "0"

comentario = (
    "/* JA_CORRIGIDO_ACESSOS_SELECT_V2: era %s ms.\n"
    " * update() roda na thread principal e bloqueia aqui a cada volta.\n"
    " * Com duas sessões são %s ms por ciclo dentro do poll, e o relógio de\n"
    " * quadros do GTK (~16 ms) não roda mais: a interface fica responsiva\n"
    " * porém sem repintar nada. Consultar os handles sem bloquear devolve\n"
    " * o controle ao laço, sem perder eventos. */\n"
    "#define SELECT_TIMEOUT %s" % (valor, int(valor) * 2, alvo_ms))

src = src.replace(achou.group(0), comentario, 1)

# TROCAR g_idle_add POR g_timeout_add no reagendamento do update().
#
# Com g_idle_add o update() volta a rodar assim que o laço fica ocioso — ou
# seja, continuamente. Ele monopoliza o despacho e as demais sources do
# GLib ficam esperando: o aviso de término do ssh de outra aba só era
# entregue quando a sessão RDP fechava.
alvo_idle = re.search(
    r"(update_id\s*=\s*)g_idle_add \(\(GSourceFunc\) update, self\)", src)
if alvo_idle:
    src = src.replace(
        alvo_idle.group(0),
        alvo_idle.group(1) +
        "g_timeout_add (5, (GSourceFunc) update, self)", 1)
    print("       reagendamento do update: g_idle_add -> g_timeout_add(5 ms)")
elif "g_timeout_add (5, (GSourceFunc) update" in src:
    print("       reagendamento do update: já ajustado")
else:
    print("       AVISO: reagendamento do update() não encontrado;")
    print("       se a interface engasgar com RDP aberto, revise isto")

open(caminho, "w", encoding="utf-8").write(src)
print("       correção do SELECT_TIMEOUT aplicada (%s ms -> %s)"
      % (valor, alvo_ms))
PYSEL

# CORRECAO DO CERTIFICADO.
#
# O gtk-frdp emite rdp-needs-certificate-verification, mas o FreeRDP JA
# RECUSOU antes de o sinal chegar — o log mostra a ordem:
#     x509_utils_from_pem: BIO_new failed for certificate
#     Host key verification failed.
#     rdp: certificado novo, aceitando        <- tarde demais
# Responder ao sinal nao adianta, e o certificado nunca e gravado: o erro
# se repete para sempre e maquina nova nunca conecta.
#
# Agrava o caso de conectar por IP: o certificado e emitido para um NOME
# (redemachado.local), entao ha name mismatch.
#
# A biblioteca nao expoe opcao para afrouxar isso. Ligamos a flag do
# proprio FreeRDP — o equivalente ao /cert:ignore ja adotado neste projeto.
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYCERT'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

if "JA_CORRIGIDO_ACESSOS_CERT" in src:
    print("       correção do certificado: já aplicada")
    sys.exit(0)

# ancora: uma linha de configuracao que sabemos existir nesta versao
ancora = re.search(
    r"[ \t]*freerdp_settings_set_bool \(settings, FreeRDP_SupportDisplayControl, TRUE\);",
    src)
if not ancora:
    print("       AVISO: ponto de configuração não encontrado (upstream mudou);")
    print("       o certificado continuará sendo verificado com rigor")
    sys.exit(0)

linha = ancora.group(0)
ident = linha[:len(linha) - len(linha.lstrip())]
extra = (
    linha + "\n"
    + ident + "/* JA_CORRIGIDO_ACESSOS_CERT\n"
    + ident + " * Parque interno com certificados autoassinados, e conexões\n"
    + ident + " * feitas por IP contra certificados emitidos para nome — o\n"
    + ident + " * FreeRDP recusaria antes mesmo de emitir o sinal de\n"
    + ident + " * verificação, e o certificado nunca seria gravado.\n"
    + ident + " * Equivale ao /cert:ignore já usado no xfreerdp aqui. */\n"
    + ident + "freerdp_settings_set_bool (settings, FreeRDP_IgnoreCertificate, TRUE);\n"
    # Só IgnoreCertificate: AutoAcceptCertificate não existe em toda versão
    # do FreeRDP 3, e como FreeRDP_* são valores de ENUM (não macros) um
    # #ifdef não protegeria — a compilação quebraria do mesmo jeito.
)
open(caminho, "w", encoding="utf-8").write(src.replace(linha, extra, 1))
print("       correção do certificado aplicada (verificação relaxada)")
PYCERT

# CORRECAO DO LAYOUT DE TECLADO.
#
# O gtk-frdp nunca define FreeRDP_KeyboardLayout: ele confia na
# autodeteccao do FreeRDP, que so existe pelo caminho X11 (XOpenDisplay em
# libfreerdp/locale/keyboard_x11.c). Dentro do Flatpak o DISPLAY chega
# VAZIO — verificado: `echo $DISPLAY` no sandbox nao devolve nada, porque
# a interface e Wayland e o fallback-x11 nao exporta a variavel. Sem
# DISPLAY a deteccao nem tenta, o FreeRDP assume o layout US e o teclado
# ABNT2 escreve qualquer coisa menos o certo.
#
# O xfreerdp externo acerta porque ELE e um cliente X11 e a deteccao roda
# no processo dele. No caminho embutido nao ha esse luxo, entao paramos de
# adivinhar e dizemos o layout.
#
# O valor vem de ACESSOS_KBD_LAYOUT (hex), com 0x416 = pt-BR como padrao —
# assim quem tiver maquina com outro layout muda pela variavel, sem
# recompilar. KeyboardType 7 / SubType 2 e o ABNT2.
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYKBD'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

if "JA_CORRIGIDO_ACESSOS_KBD" in src:
    print("       correção do teclado: já aplicada")
    sys.exit(0)

ancora = re.search(
    r"[ \t]*freerdp_settings_set_bool \(settings, FreeRDP_SupportDisplayControl, TRUE\);",
    src)
if not ancora:
    print("       AVISO: ponto de configuração não encontrado (upstream mudou);")
    print("       o teclado continuará dependendo da autodetecção (US)")
    sys.exit(0)

linha = ancora.group(0)
ident = linha[:len(linha) - len(linha.lstrip())]
i = ident
extra = (
    linha + "\n"
    + i + "/* JA_CORRIGIDO_ACESSOS_KBD\n"
    + i + " * Sem DISPLAY dentro do Flatpak a autodetecção de layout do\n"
    + i + " * FreeRDP não roda e ele assume US — o ABNT2 escrevia\n"
    + i + " * caracteres aleatórios. Fixamos o layout, com escape por\n"
    + i + " * ACESSOS_KBD_LAYOUT para quem usa outro. Equivale ao\n"
    + i + " * /kbd:layout: do xfreerdp. */\n"
    + i + "{\n"
    + i + "  const char *acessos_kbd = g_getenv (\"ACESSOS_KBD_LAYOUT\");\n"
    + i + "  guint32 acessos_layout = 0x00000416;  /* pt-BR ABNT2 */\n"
    + i + "  if (acessos_kbd != NULL && *acessos_kbd != '\\0')\n"
    + i + "    acessos_layout = (guint32) g_ascii_strtoull (acessos_kbd, NULL, 0);\n"
    + i + "  freerdp_settings_set_uint32 (settings, FreeRDP_KeyboardLayout, acessos_layout);\n"
    + i + "  freerdp_settings_set_uint32 (settings, FreeRDP_KeyboardType, 7);\n"
    + i + "  freerdp_settings_set_uint32 (settings, FreeRDP_KeyboardSubType, 2);\n"
    + i + "}\n"
)
open(caminho, "w", encoding="utf-8").write(src.replace(linha, extra, 1))
print("       correção do teclado aplicada (layout fixo, padrão pt-BR)")
PYKBD

# CORRECAO DO SCANCODE (deslocamento de 8).
#
# SINTOMA MEDIDO: digitando "q w e r t" o servidor Windows recebia
# "o p ´ [ Enter"; digitando "1 2 3 4 5" recebia "9 0 - = Backspace". Em
# ambos, a tecla da posicao N chegava como a da posicao N+8 — deslocamento
# constante, nao aleatorio.
#
# CAUSA: keycode_x11 = keycode_evdev + 8. O GDK entrega
# event->hardware_keycode no padrao X11 (mesmo em Wayland). O gtk-frdp
# repassa esse numero ao FreeRDP, que so o converte corretamente se
# freerdp_keyboard_init() tiver montado a tabela X11 — o que exige
# XOpenDisplay. Sem DISPLAY no sandbox a tabela nao existe, o FreeRDP trata
# o numero como se ja fosse evdev, e sobram os 8.
#
# Contra servidor Linux (xrdp) isso passa despercebido: ele reinterpreta
# pelo caractere. O Windows obedece o scancode literalmente.
#
# CORRECAO: subtrair o deslocamento antes da conversao. O valor sai de
# ACESSOS_KBD_OFFSET (padrao 8) para o caso de alguma combinacao nao
# precisar dele — ACESSOS_KBD_OFFSET=0 desliga sem recompilar.
python3 - "$FONTE_FRDP/src/frdp-session.c" <<'PYSCAN'
import re
import sys

caminho = sys.argv[1]
try:
    src = open(caminho, encoding="utf-8").read()
except OSError as e:
    print("       AVISO: não li frdp-session.c (%s)" % e)
    sys.exit(0)

if "JA_CORRIGIDO_ACESSOS_SCAN" in src:
    print("       correção do scancode: já aplicada")
    sys.exit(0)

# A funcao auxiliar entra logo depois do ultimo #include, para estar
# declarada antes de qualquer uso.
inc = list(re.finditer(r"^#include[^\n]*\n", src, flags=re.M))
if not inc:
    print("       AVISO: nenhum #include em frdp-session.c; patch abortado")
    sys.exit(0)

helper = """
/* JA_CORRIGIDO_ACESSOS_SCAN
 * keycode_x11 = keycode_evdev + 8. O GDK entrega hardware_keycode no
 * padrão X11 mesmo sob Wayland; sem DISPLAY o FreeRDP não monta a tabela
 * de tradução e trata o número como evdev, deslocando TODAS as teclas em
 * 8 posições (medido: "qwert" chegava como "op´[Enter"). Descontamos aqui.
 * ACESSOS_KBD_OFFSET=0 desliga sem recompilar. */
static guint
acessos_keycode (guint hardware_keycode)
{
  static gint desloc = -1;

  if (desloc < 0)
    {
      const char *env = g_getenv ("ACESSOS_KBD_OFFSET");
      desloc = (env != NULL && *env != '\\0') ? (gint) g_ascii_strtoll (env, NULL, 10) : 8;
    }

  if ((gint) hardware_keycode > desloc)
    return hardware_keycode - desloc;
  return hardware_keycode;
}
"""

pos = inc[-1].end()
src = src[:pos] + helper + src[pos:]

# Ancoramos na CHAMADA de conversao, nao no nome da variavel — no fonte
# atual o keycode chega como key->hardware_keycode, mas o nome ja mudou
# antes e o patch anterior errou justamente por causa disso.
#
# So esta chamada e tocada. A outra, GetVirtualKeyCodeFromKeycode com
# WINPR_KEYCODE_TYPE_XKB, espera keycode X11 DE VERDADE: descontar 8 la
# quebraria o codigo virtual. O deslocamento so existe no caminho do
# scancode, que e o que o Windows obedece literalmente.
alvo = re.search(
    r"freerdp_keyboard_get_rdp_scancode_from_x11_keycode\s*\(\s*([^)]+?)\s*\)",
    src)
if not alvo:
    print("       AVISO: chamada de conversão de scancode não encontrada;")
    print("       o teclado continuará deslocado contra servidores Windows")
    sys.exit(0)

src = src.replace(
    alvo.group(0),
    "freerdp_keyboard_get_rdp_scancode_from_x11_keycode (acessos_keycode (%s))"
    % alvo.group(1), 1)

open(caminho, "w", encoding="utf-8").write(src)
print("       correção do scancode aplicada (deslocamento 8)")
PYSCAN

PATCHES_ACESSOS_EOF
chmod +x build/manifest/aplicar-patches-frdp.sh

cat > "build/manifest/$APPID.desktop" <<'DESKTOP_ACESSOS_EOF'
[Desktop Entry]
Type=Application
Name=Acessos
Comment=Acesso remoto às máquinas (VNC, RDP, SSH, arquivos)
Exec=acessos.py
Icon=org.jj.Acessos
Categories=Network;RemoteAccess;System;
Terminal=false
StartupNotify=true
DESKTOP_ACESSOS_EOF

# Nada de symlinks aqui: o manifest referencia ../../python, ../../src e
# ../../icones diretamente, e o flatpak-builder resolve esses caminhos em
# relacao ao proprio manifest. Symlink dentro do diretorio do manifest
# viraria link quebrado dentro do sandbox de build.

RUNTIME_VER="$(grep -m1 'runtime-version:' "build/manifest/$APPID.yml" \
               | tr -d "' " | cut -d: -f2)"
echo "-- runtime GNOME $RUNTIME_VER --"
flatpak remote-add --if-not-exists --user flathub \
    https://dl.flathub.org/repo/flathub.flatpakrepo
flatpak install --user --noninteractive flathub \
    "org.gnome.Platform//$RUNTIME_VER" "org.gnome.Sdk//$RUNTIME_VER"

# SANIDADE DO CACHE, ANTES de gastar tempo.
#
# O cache do flatpak-builder e um repositorio OSTree em .flatpak-builder/
# cache. Quando ele fica pela metade — build interrompida no meio, disco
# cheio, Ctrl+C na hora errada — o flatpak-builder NAO percebe na entrada:
# ele constroi tudo e so falha la na frente, depois de meia hora. Foi o que
# aconteceu com o build/repo, e a mesma armadilha existe aqui.
#
# Entao conferimos agora: se a pasta existe e nao e um repo valido, ela vai
# embora e a build segue do zero. So essa pasta — downloads/ e git/, que
# guardam os tarballs e os checkouts, ficam intactos. Perder o cache de
# compilacao custa tempo; perder as fontes custa tempo E banda.
CACHE=build/.flatpak-builder
if [ -d "$CACHE/cache" ]; then
    if ! ostree --repo="$CACHE/cache" fsck --quiet >/dev/null 2>&1; then
        echo "-- cache de compilacao corrompido: descartando --"
        rm -rf "$CACHE/cache"
    fi
fi

echo "-- construindo (freerdp e gtk-frdp demoram) --"
cd build/manifest
# SEM --repo aqui, de proposito.
#
# O flatpak-builder recente nao inicializa o repositorio de destino: ele
# espera um repo OSTree pronto e, se nao houver, a build roda INTEIRA e so
# morre no fim com
#   error: opening repo: openat(config): No such file or directory
# Criar o repo antes com 'ostree init' resolveria, mas depende do ostree
# estar instalado e com a linha de comando na ordem que aquela versao
# aceita — o que ja falhou nas duas ordens nesta maquina.
#
# O caminho sem surpresa e separar as duas etapas: o flatpak-builder so
# constroi a arvore em builder-out, e o 'flatpak build-export' exporta —
# este CRIA o repositorio sozinho quando ele nao existe.
# --state-dir e obrigatorio aqui, nao e refinamento.
#
# Por padrao o flatpak-builder guarda cache, tarballs e checkouts num
# .flatpak-builder DENTRO do diretorio de onde ele roda — que aqui e
# build/manifest, apagado por 'rm -rf build/manifest' no comeco deste
# script. Resultado: o cache era destruido antes de cada build e o FreeRDP
# recompilava sempre, sem ninguem entender por que.
#
# Apontando para build/.flatpak-builder o estado sobrevive entre builds.
flatpak-builder --force-clean --user --disable-rofiles-fuse \
    --state-dir=../.flatpak-builder \
    ../builder-out "$APPID.yml"
cd ../..

# REPO DE EXPORTACAO.
#
# Nem o flatpak-builder nem o build-export consertam um repo pela metade: se
# build/repo EXISTE mas nao tem o arquivo config, nenhum dos dois cria e
# nenhum dos dois abre — os dois morrem em
#   error: opening repo: openat(config): No such file or directory
# Foi exatamente o que aconteceu: um build/repo interrompido numa tentativa
# anterior travou todas as seguintes.
#
# Repo e conteudo derivado, nao dado do usuario: apagar e recriar do zero a
# cada build custa alguns segundos e elimina a classe inteira de problema.
# O cache de compilacao NAO esta aqui — ele vive em .flatpak-builder e
# continua intacto, entao o FreeRDP nao recompila por causa disto.
#
# O --repo vem ANTES do subcomando: e a ordem que o ostree desta maquina
# aceita.
rm -rf build/repo
ostree --repo=build/repo init --mode=archive-z2

echo "-- exportando para o repo --"
flatpak build-export build/repo build/builder-out

VERSION="$(date +%Y%m%d-%H%M)"
BUNDLE="build/Acessos-$VERSION.flatpak"
flatpak build-bundle build/repo "$BUNDLE" "$APPID"

if [ "$INSTALAR" = "1" ]; then
    # --reinstall cobre os dois casos: instala se nao houver, e substitui a
    # versao anterior se ja estiver instalado. Os dados do usuario em
    # ~/.var/app/$APPID ficam intactos nos dois casos.
    if flatpak info --user "$APPID" >/dev/null 2>&1; then
        echo "-- ja instalado: atualizando --"
    else
        echo "-- instalando --"
    fi
    flatpak install --user --noninteractive --reinstall "$BUNDLE"
    echo
    echo "rode com:  flatpak run $APPID"
fi

# LIMPEZA DO QUE JA CUMPRIU O PAPEL.
#
# builder-out e a arvore intermediaria (centenas de MB) e o repo e conteudo
# derivado — o bundle .flatpak ja carrega os dois. Bundles antigos tambem
# saem: guardamos os tres ultimos, que e o bastante para voltar uma versao.
#
# O cache de compilacao e as fontes baixadas NAO sao tocados aqui: sao eles
# que evitam recompilar FreeRDP e gtk-frdp na proxima vez. Para apagar
# tambem, ./build.sh --limpar.
rm -rf build/builder-out build/repo
ls -1t build/*.flatpak 2>/dev/null | tail -n +4 | xargs -r rm -f

echo
echo "== feito =="
ls -la build/*.flatpak
