# Núcleo do Mass SSH Executer (pdvmanager), portado

Origem: `Mass SSH Executer/pdvmanager/internal/{model,executor,runner,logs}`
e `config/{snippets.go,ini.go}`.

Veio **como está**, com os comentários originais inteiros — eles registram
decisões tomadas em campo (por que o canário é obrigatório, por que a
negociação SSH aceita algoritmo antigo, por que falha de conexão nunca
abre diálogo, por que o timeout sem baseline é generoso). Apagar esses
comentários seria jogar fora o motivo de cada escolha e reintroduzir os
mesmos bugs depois.

Ficou de fora só a interface Fyne (`internal/ui`) — a tela nova é
`cmd/acessos/massatab.go`.

## O que mudou em relação ao original

1. Caminho de import: `pdvmanager/internal/...` → `acessos-go/internal/massa/...`.
2. `config` virou dois pacotes: `snippets` (o que o Acessos usa) ficou;
   hosts/filiais/perfis não vieram, porque aqui a lista de máquinas sai do
   `conexoes.ini`, não de `pdvs.csv`.
3. `runner.Config` ganhou **`CredDeHost func(model.Host) model.Credencial`**:
   no pdvmanager a credencial era uma só para todo o lote; aqui cada
   máquina pode ter a sua salva no `conexoes.ini` (e cifrada no cofre).
   Devolver credencial vazia cai no `Cred` padrão da tela, que é o
   comportamento antigo.
4. Os testes de integração do projeto antigo não vieram: dependiam de
   fixtures e de um `pdvs.csv` que não existem aqui.

Ao atualizar o pdvmanager, refazer a cópia e reaplicar esses quatro
pontos — procure por "CredDeHost" para achar o único ponto de código
tocado.
