# Acessos

Gerenciador de acesso remoto às máquinas — VNC, RDP, SSH e transferência de
arquivos, em abas.

A partir da **2.0** o aplicativo é escrito em **Go**, com interface
desenhada na GPU. O que ele lê e escreve não mudou: os mesmos
`conexoes.ini`, `chaveiro.ini` e `snippets.ini` de sempre, no mesmo lugar.
Instalar a 2.0 por cima da 1.x não pede migração nenhuma.

## A versão em Python (1.x)

Ela não foi apagada. O código continua acessível de duas formas:

```bash
git checkout v1.2.2     # a última versão publicada em Python
git checkout 1.x        # a linha de manutenção, caso precise de correção
```

`master` segue a versão nova. É o arranjo usual quando um projeto troca de
implementação: a linha antiga vive na sua própria branch e nas tags, sem
uma cópia morta ocupando espaço na árvore atual — cópia duplicada envelhece
sem ninguém perceber e não tem histórico próprio.

## Instalar

```bash
./build.sh --instalar     # constrói o Flatpak e instala para o usuário
```

Depois: pelo menu de aplicativos, ou `flatpak run org.jj.Acessos`.

| Comando | O que faz |
|---|---|
| `./build.sh` | constrói e gera `build/org.jj.Acessos-<versão>.flatpak` |
| `./build.sh --instalar` | constrói, gera o bundle e instala ou atualiza |
| `./build.sh --limpar` | apaga `build/` inteiro |

Para desenvolver sem empacotar, com as bibliotecas do sistema
(`libvncclient`, `freerdp3`, `wayland-client`, `xkbcommon`):

```bash
go build -o acessos ./cmd/acessos && ./acessos
```

Sem argumento ele abre o inventário padrão
(`$XDG_CONFIG_HOME/acessos/conexoes.ini`), criando um exemplo comentado na
primeira execução. `-ini` aponta para outro arquivo.

## Estrutura

```
cmd/acessos/          a aplicação (interface, abas, diálogos)
internal/
  vnc/ rdp/           clientes próprios, cgo sobre libvncclient e libfreerdp3
  grab/               teclado, área de transferência e inibição de atalhos
                      por Wayland direto
  conexoes/           leitura e gravação cirúrgica do conexoes.ini
  cofre/ chaveiro/    senhas cifradas e credenciais reutilizáveis
  massa/              execução em lote (portado do Mass SSH Executer)
  hostkey/ vida/      identidade do servidor SSH e sonda de vida
third_party/gio/      Gio com três patches — veja PATCH.md
flatpak/              manifesto, .desktop e metainfo
```

Os comandos em `cmd/` que não são `acessos` são ferramentas de teste
manual de cada protocolo, usadas durante o porte.

## Licença

GNU GPLv3 — veja [LICENSE](LICENSE).
