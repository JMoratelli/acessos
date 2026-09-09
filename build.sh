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
runtime-version: '50'
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
  # flatpak-spawn --host, usado por atualizador.py para baixar o .flatpak
  # do release e rodar 'flatpak install --reinstall' com ele no host, e
  # depois reabrir o app (distribuicao propria, fora do Flathub: essa
  # permissao normalmente nao passa na revisao la).
  - --talk-name=org.freedesktop.Flatpak

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
      # 3.9.0 tinha um bug de sintaxe em codecs.h (ordem de
      # WINPR_DEPRECATED_VAR/FREERDP_API) que o GCC 15 rejeita; corrigido
      # em versoes posteriores. 3.31.1 e a mais recente no momento.
      - type: archive
        url: https://github.com/FreeRDP/FreeRDP/archive/refs/tags/3.31.1.tar.gz
        sha256: 254de9fe176758e9787347469fb310523782f03c61130508b51b266e374eb6c1

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
        # Commit fixo (era 'branch: master') porque os patches em
        # aplicar-patches-frdp.sh dependem de trechos especificos do fonte;
        # com 'master' flutuando a build muda sozinha entre uma compilacao
        # e outra e pode quebrar os patches sem aviso. Para atualizar,
        # rode e cole o novo sha:
        #   git ls-remote https://gitlab.gnome.org/GNOME/gtk-frdp.git master
        commit: 83854a24e31d1c07519f6e4393fe280d3b59e080
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
  # Gerado com flatpak-pip-generator (--requirements-file com paramiko,
  # cryptography, argon2-cffi contra org.gnome.Sdk//50), fontes fixadas por
  # sha256 em vez de pip com rede liberada no build. A Flathub proibe rede
  # durante o build; para regenerar apos atualizar alguma versao:
  #   python3 flatpak-pip-generator.py --requirements-file=requirements.txt \
  #     --runtime='org.gnome.Sdk//50' \
  #     --prefer-wheels=bcrypt,cryptography,argon2-cffi-bindings,pynacl \
  #     -o python3-modules
  # (requirements.txt com "paramiko", "cryptography" e "argon2-cffi", uma
  # por linha) e substitua os tres modulos abaixo pelo json produzido.
  # --prefer-wheels e obrigatorio para bcrypt e cryptography (nucleo em
  # Rust desde as versoes recentes; o sdist exige maturin, ausente no
  # runtime do GNOME), para argon2-cffi-bindings (sdist exige
  # scikit-build-core/cmake) e para pynacl (o sdist compila a libsodium
  # vendorizada, mas a extensao cffi _sodium nao fica instalada sob
  # --no-build-isolation; a wheel ja traz tudo pronto). A wheel manylinux
  # evita precisar desses toolchains/passos extras no sandbox de build.
  #
  # cryptography vem junto com o paramiko, mas esta explicito porque o
  # cofre.py depende dele diretamente. argon2-cffi e opcional por design
  # (sem ele o cofre cai em PBKDF2), mas resiste bem melhor a GPU.
  # -------------------------------------------------------------------------
  - name: python3-paramiko
    buildsystem: simple
    build-commands:
    - pip3 install --verbose --exists-action=i --no-index --find-links="file://${PWD}" --prefix=${FLATPAK_DEST} "paramiko" --no-build-isolation
    sources:
    - type: file
      url: https://files.pythonhosted.org/packages/3b/71/427945e6ead72ccffe77894b2655b695ccf14ae1866cd977e185d606dd2f/bcrypt-5.0.0-cp38-abi3-manylinux2014_x86_64.manylinux_2_17_x86_64.whl
      sha256: 560ddb6ec730386e7b3b26b8b4c88197aaed924430e7b74666a586ac997249ef
      only-arches:
      - x86_64
    - type: file
      url: https://files.pythonhosted.org/packages/45/b6/4c1205dde5e464ea3bd88e8742e19f899c16fa8916fb8510a851fae985b5/bcrypt-5.0.0-cp38-abi3-manylinux2014_aarch64.manylinux_2_17_aarch64.whl
      sha256: c2388ca94ffee269b6038d48747f4ce8df0ffbea43f31abfa18ac72f0218effb
      only-arches:
      - aarch64
    - type: file
      url: https://files.pythonhosted.org/packages/9e/ef/008a1939e372c06329a3fce4279c02f328488f3526744906eeec3da7ad5f/cffi-2.1.1.tar.gz
      sha256: dd31f52ea1086513bb9df30f8fcee9b8918323ae067a3d5b78bc826a000712be
    - type: file
      url: https://files.pythonhosted.org/packages/57/26/e6d4fc8512a51a5f9ee7bfdbfb853bce1197087df40c9ad993ad370b846f/cryptography-50.0.1-cp311-abi3-manylinux2014_x86_64.manylinux_2_17_x86_64.whl
      sha256: ff838d62ec1bfce4f9ba7fa16f4a7b554cd8d0c299e6be37502161a660c84eef
      only-arches:
      - x86_64
    - type: file
      url: https://files.pythonhosted.org/packages/90/34/9ce9a62ed9dc82ca9fd6a34445b6904af56e5f38b3eae2ed32e49c36053d/cryptography-50.0.1-cp311-abi3-manylinux2014_aarch64.manylinux_2_17_aarch64.whl
      sha256: 53e279950892dc102c6b4e52af03ae5ea92fac572a1ddab78ca73a997f62b69f
      only-arches:
      - aarch64
    - type: file
      url: https://files.pythonhosted.org/packages/5a/de/bbc12563bbf979618d17625a4e753ff7a078523e28d870d3626daa97261a/invoke-3.0.3-py3-none-any.whl
      sha256: f11327165e5cbb89b2ad1d88d3292b5113332c43b8553b494da435d6ec6f5053
    - type: file
      url: https://files.pythonhosted.org/packages/82/5b/eadf6d45de38d30ab603f49393b6cd2cbe7e233af8cf90197e32782b68a9/paramiko-5.0.0-py3-none-any.whl
      sha256: b7044611c30140d9a75261653210e2002977b71a0497ff3ba0d98d7edbf62f7c
    - type: file
      url: https://files.pythonhosted.org/packages/0c/c3/44f3fbbfa403ea2a7c779186dc20772604442dde72947e7d01069cbe98e3/pycparser-3.0-py3-none-any.whl
      sha256: b727414169a36b7d524c1c3e31839a521725078d7b2ff038656844266160a992
    - type: file
      url: https://files.pythonhosted.org/packages/7f/81/d60984052df5c97b1d24365bc1e30024379b42c4edcd79d2436b1b9806f2/pynacl-1.6.2-cp38-abi3-manylinux2014_x86_64.manylinux_2_17_x86_64.whl
      sha256: 22de65bb9010a725b0dac248f353bb072969c94fa8d6b1f34b87d7953cf7bbe4
      only-arches:
      - x86_64
    - type: file
      url: https://files.pythonhosted.org/packages/1e/b4/e927e0653ba63b02a4ca5b4d852a8d1d678afbf69b3dbf9c4d0785ac905c/pynacl-1.6.2-cp38-abi3-manylinux2014_aarch64.manylinux_2_17_aarch64.whl
      sha256: 8845c0631c0be43abdd865511c41eab235e0be69c81dc66a50911594198679b0
      only-arches:
      - aarch64
  - name: python3-cryptography
    buildsystem: simple
    build-commands:
    - pip3 install --verbose --exists-action=i --no-index --find-links="file://${PWD}" --prefix=${FLATPAK_DEST} "cryptography" --no-build-isolation
    sources:
    - type: file
      url: https://files.pythonhosted.org/packages/9e/ef/008a1939e372c06329a3fce4279c02f328488f3526744906eeec3da7ad5f/cffi-2.1.1.tar.gz
      sha256: dd31f52ea1086513bb9df30f8fcee9b8918323ae067a3d5b78bc826a000712be
    - type: file
      url: https://files.pythonhosted.org/packages/57/26/e6d4fc8512a51a5f9ee7bfdbfb853bce1197087df40c9ad993ad370b846f/cryptography-50.0.1-cp311-abi3-manylinux2014_x86_64.manylinux_2_17_x86_64.whl
      sha256: ff838d62ec1bfce4f9ba7fa16f4a7b554cd8d0c299e6be37502161a660c84eef
      only-arches:
      - x86_64
    - type: file
      url: https://files.pythonhosted.org/packages/90/34/9ce9a62ed9dc82ca9fd6a34445b6904af56e5f38b3eae2ed32e49c36053d/cryptography-50.0.1-cp311-abi3-manylinux2014_aarch64.manylinux_2_17_aarch64.whl
      sha256: 53e279950892dc102c6b4e52af03ae5ea92fac572a1ddab78ca73a997f62b69f
      only-arches:
      - aarch64
    - type: file
      url: https://files.pythonhosted.org/packages/0c/c3/44f3fbbfa403ea2a7c779186dc20772604442dde72947e7d01069cbe98e3/pycparser-3.0-py3-none-any.whl
      sha256: b727414169a36b7d524c1c3e31839a521725078d7b2ff038656844266160a992
  - name: python3-argon2-cffi
    buildsystem: simple
    build-commands:
    - pip3 install --verbose --exists-action=i --no-index --find-links="file://${PWD}" --prefix=${FLATPAK_DEST} "argon2-cffi" --no-build-isolation
    sources:
    - type: file
      url: https://files.pythonhosted.org/packages/4f/d3/a8b22fa575b297cd6e3e3b0155c7e25db170edf1c74783d6a31a2490b8d9/argon2_cffi-25.1.0-py3-none-any.whl
      sha256: fdc8b074db390fccb6eb4a3604ae7231f219aa669a2652e0f20e16ba513d5741
    - type: file
      url: https://files.pythonhosted.org/packages/6f/86/5363df11b86d02cf3662208e7406496327649cc90eb365bf6f4e8a54a41f/argon2_cffi_bindings-26.1.0-cp310-abi3-manylinux_2_26_x86_64.manylinux_2_28_x86_64.whl
      sha256: 27f1821903e2ceadcb88ec2b45ef190897b7682449c772f4d9b53e42c520cf29
      only-arches:
      - x86_64
    - type: file
      url: https://files.pythonhosted.org/packages/7e/e4/ad91d8297638aa2258aad4501c306aca99480dfe76ccd638173fa3702db9/argon2_cffi_bindings-26.1.0-cp310-abi3-manylinux_2_26_aarch64.manylinux_2_28_aarch64.whl
      sha256: 78de2d65e0b9ea7ce9d1b1c3e87297b2d7305a02c266ee2a2d6910daddd7ee69
      only-arches:
      - aarch64
    - type: file
      url: https://files.pythonhosted.org/packages/9e/ef/008a1939e372c06329a3fce4279c02f328488f3526744906eeec3da7ad5f/cffi-2.1.1.tar.gz
      sha256: dd31f52ea1086513bb9df30f8fcee9b8918323ae067a3d5b78bc826a000712be
    - type: file
      url: https://files.pythonhosted.org/packages/0c/c3/44f3fbbfa403ea2a7c779186dc20772604442dde72947e7d01069cbe98e3/pycparser-3.0-py3-none-any.whl
      sha256: b727414169a36b7d524c1c3e31839a521725078d7b2ff038656844266160a992

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
      - install -Dm644 org.jj.Acessos.metainfo.xml
        /app/share/metainfo/org.jj.Acessos.metainfo.xml
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
      - type: file
        path: org.jj.Acessos.metainfo.xml
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

cp flatpak/aplicar-patches-frdp.sh build/manifest/aplicar-patches-frdp.sh
chmod +x build/manifest/aplicar-patches-frdp.sh

cp flatpak/org.jj.Acessos.desktop "build/manifest/$APPID.desktop"
cp flatpak/org.jj.Acessos.metainfo.xml "build/manifest/$APPID.metainfo.xml"

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
