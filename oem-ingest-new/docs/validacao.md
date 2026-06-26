# Validacao De Configuracao

Este documento descreve a validacao opcional de `configTargets.yaml` contra a
API do Oracle Enterprise Manager (OEM).

## Como Ativar

Configure as credenciais OEM e habilite a validacao no startup:

```sh
export OEM_VALIDATE_CONFIG=true
export OEM_CONFIG_TARGETS=./configs/configTargets.yaml
export OEM_VALIDATED_CONFIG_OUTPUT=./configs/configTargets.validated.yaml
export OEM_VALIDATION_REPORT_OUTPUT=./configs/configTargets.validated.report.jsonl
export OEM_USER=usuario
export OEM_PASSWORD=senha
```

`OEM_VALIDATION_REPORT_OUTPUT` e opcional. Quando nao informado, o caminho e
derivado de `OEM_VALIDATED_CONFIG_OUTPUT` substituindo a extensao por
`.report.jsonl`.
Por exemplo, `configTargets.validated.yaml` gera
`configTargets.validated.report.jsonl`.

## Arquivos Gerados

A validacao sempre preserva o arquivo original de targets. Ela gera:

- YAML validado em `OEM_VALIDATED_CONFIG_OUTPUT`, com IDs atualizados, targets
  ausentes removidos, tags estruturais corrigidas e targets relacionados
  adicionados quando a correlacao permite.
- Relatorio JSONL em `OEM_VALIDATION_REPORT_OUTPUT`, com um evento JSON por
  mudanca ou warning.

Nenhum dos caminhos de saida pode apontar para `OEM_CONFIG_TARGETS`. O relatorio
tambem nao pode apontar para o mesmo arquivo do YAML validado.

## Regras De Validacao

A etapa de IDs lista os targets do OEM por site e localiza cada target
configurado pelo par `name` + `typeName`.

- Um match unico com ID diferente atualiza o ID em memoria e no YAML validado.
- Nenhum match remove o target da configuracao validada e da run atual.
- Multiplos matches mantem o target configurado e geram warning, porque nao ha
  uma escolha segura de ID.
- Targets retornados pela API sem ID valido sao tratados como ausentes.

Depois disso, a etapa de correlacao roda sobre a configuracao ja filtrada. Ela
pode adicionar targets relacionados para raizes `rac_database` e `oracle_pdb` e
corrigir tags estruturais conforme as regras legadas.

Sites que ficam sem targets depois das remocoes sao omitidos do YAML validado e
registrados no relatorio.

## Logs De Progresso

Com `OEM_LOG_LEVEL=INFO`, a validacao registra progresso no log operacional
antes de executar chamadas demoradas ao OEM. A sequencia esperada inclui
`validacao de configuracao iniciada`, inicio/conclusao das fases de IDs e
correlacoes, e mensagens por site antes e depois de listar targets no OEM.

O log `conexao OEM validada` e emitido assim que a primeira chamada real a
`/em/api/targets` de um endpoint termina com sucesso durante a validacao. A
validacao nao faz uma chamada extra de ping apenas para produzir esse log.

## Relatorio JSONL

O relatorio e append-only e contem uma linha JSON por evento. Todos os eventos
incluem `timestamp`, `event`, `phase`, `sourceConfig` e `validatedConfig`.

```jsonl
{"timestamp":"2026-06-16T12:30:00Z","event":"id_correction","phase":"startup","sourceConfig":"./configs/configTargets.yaml","validatedConfig":"./configs/configTargets.validated.yaml","siteIndex":0,"targetIndex":1,"siteName":"oraemc","targetName":"db1","targetType":"oracle_database","oldID":"old-id","newID":"new-id"}
{"timestamp":"2026-06-16T12:30:00Z","event":"warning","phase":"startup","sourceConfig":"./configs/configTargets.yaml","validatedConfig":"./configs/configTargets.validated.yaml","code":"id_divergent","siteIndex":0,"targetIndex":1,"siteName":"oraemc","targetName":"db1","targetType":"oracle_database","configID":"old-id","currentID":"new-id","message":"ID do target \"db1\" tipo \"oracle_database\" diverge da API OEM"}
```

Eventos de startup usam `phase:"startup"` e podem ter `event` igual a
`id_correction`, `target_removed`, `site_removed`, `target_added`,
`tag_correction` ou `warning`. Correcoes feitas durante a coleta usam
`phase:"runtime"`, `event:"id_correction"`, `trigger:"metric_404"` e incluem
`metricGroupName`.

## Efeito Na Run

Quando `OTEL_EXPORT_URL` esta definido, a mesma execucao usa a configuracao
validada em memoria. Targets removidos nao geram jobs de scheduler e nao fazem
chamadas `latestData`.

Com `OEM_VALIDATE_CONFIG=true`, a coleta tambem pode revalidar um target em
runtime quando uma busca de metadata ou `latestData` retornar `404`. Antes de
listar targets no OEM, o runtime verifica `oem_monitor_response`: se o target
teve coleta util dentro de `OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES`, a
revalidacao e pulada. Caso contrario, a API de targets e consultada e, havendo
match unico por `name` + `typeName` com ID diferente, o ID e atualizado em
memoria, o YAML validado e reescrito e um evento JSONL e anexado ao relatorio.

Quando a verificacao confirma que o ID nao mudou, ou nao encontra uma correcao
segura, novas verificacoes para o mesmo target ficam bloqueadas por
`OEM_RUNTIME_ID_RECHECK_INTERVAL_SECONDS` (padrao `86400`).

Se a validacao remover todos os targets de todos os sites, os arquivos de saida
sao gerados e a coleta nao inicia. O processo retorna erro informando que a
validacao removeu todos os targets.

Sem `OTEL_EXPORT_URL`, a aplicacao apenas valida, escreve os artefatos e encerra
sem iniciar o runtime de coleta/exportacao.
