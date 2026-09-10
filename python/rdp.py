#!/usr/bin/env python3
# Copyright (C) 2026 Jurandir Moratelli
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.
"""RdpWidget — reexporta o motor proprio (rdpwidget.py, sobre rdpshim.c +
libfreerdp3) como "rdp", que e o nome que acessos.py e instalar.sh esperam.

SEM FALLBACK, DE PROPOSITO.

Ate aqui este modulo teve uma segunda implementacao por baixo do gtk-frdp
(GObject Introspection), usada quando o rdpshim nao estava compilado. Essa
segunda via saiu: o RDP embutido agora depende so do rdpshim, compilado
contra a MESMA libfreerdp3 que ja usavamos para portar a logica de
certificado/pipeline grafico/entrada — ter dois caminhos que podiam se
comportar diferente (um aceitava certificado calado, o outro perguntava; um
media teclado por keysym, o outro por scancode) e risco, nao seguranca.
Sem o rdpshim compilado, RDP embutido fica indisponivel e a aba cai no
xfreerdp externo (AbaRdp) — nunca num segundo motor GTK.
"""

from rdpwidget import RdpWidget, TEM_FRDP_SHIM as TEM_FRDP

ERRO_FRDP = "" if TEM_FRDP else "librdpshim.so indisponível (compile com ./instalar.sh)"
