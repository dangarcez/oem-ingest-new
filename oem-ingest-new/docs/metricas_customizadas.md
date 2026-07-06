# Metricas Customizadas

Metricas customizadas sao series geradas pelo coletor, sem depender de uma
metrica OEM com o mesmo nome retornada diretamente por `latestData`. Elas usam
os resultados das coletas configuradas, metadata do target e estado interno do
runtime.

Uma coleta so e considerada util para freshness quando `latestData` retorna ao
menos um datapoint que nao seja key da serie. Itens vazios ou itens contendo
apenas keys nao renovam o monitor de resposta.

## oem_monitor_response

`oem_monitor_response` e um gauge por target:

- `1`: o target tem alguma coleta util ainda valida;
- `0`: o target nunca coletou ou a validade da ultima coleta util expirou.

Quando uma coleta util termina, o coletor calcula um prazo de validade para o
target:

```text
active_until = collectedAt + frequencia_do_job + OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES
```

`OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES` continua com default de 21 minutos,
mas agora funciona como margem somada a frequencia do grupo que respondeu. A
comparacao e estrita: no instante exato de `active_until`, o target ja e
considerado sem resposta.

O monitor guarda somente estado por target: ultima coleta util e maior
`active_until` conhecido. Ele nao guarda payloads nem historico por grupo. Se
um grupo lento abre uma validade maior, uma coleta posterior de grupo rapido nao
encurta esse prazo.

Exemplo: um target tem dois grupos com frequencia de 6 horas e um grupo com
frequencia de 10 minutos. Se um grupo de 6 horas retorna dados as 09:00, o
target fica com resposta ate 15:21. Se a metrica de 10 minutos parar de retornar
datapoints as 10:00, ela nao derruba `oem_monitor_response` enquanto a validade
aberta pela coleta de 6 horas ainda estiver ativa. Isso preserva a intencao da
metrica: indicar se o target retorna dados para alguma metrica util.

Falhas de um grupo especifico devem ser acompanhadas por metricas ou alertas do
proprio grupo. `oem_monitor_response` nao indica que todos os grupos do target
estao saudaveis.

## oem_monitor_stus

`oem_monitor_stus` mantem o nome legado, inclusive o erro de grafia. Ele e um
gauge por target com os codigos:

- `0`: down ou inativo;
- `1`: sem coleta;
- `2`: up ou coletando;
- `3`: estado do coletor/script, sem confirmacao do target.

As regras por tipo de target sao:

- `rac_database`: usa `Availability`; se houver item, exporta `0`; se vier
  vazio, usa `oem_monitor_response` para decidir `2` ou `1`.
- `oracle_database`: usa `Response`; se vier vazio, usa
  `oem_monitor_response`; se houver item, usa `Status` ou `DatabaseStatus`.
- `oracle_pdb`: usa `Response`; se vier vazio, usa `oem_monitor_response`; se
  houver `Status`, `0` gera `0` e outros valores geram `2`; sem `Status`,
  `State != OPEN` gera `0`, caso contrario `2`.
- `host`: usa `Response`; se vier vazio, usa `oem_monitor_response`; se houver
  item, `Status == 0` gera `0`, caso contrario `2`.

Durante a coleta inicial, estados sem coleta viram `3`, indicando que o coletor
ainda esta formando o estado inicial. Depois que a coleta inicial termina,
`OEM_MONITOR_STATUS_WARMUP_MINUTES` define por quantos minutos esse warm-up
continua ativo. Com o valor padrao `0`, ele termina junto com a coleta inicial.
O encerramento gera log `INFO` com a mensagem
`warm-up de oem_monitor_stus concluido`.

Status explicitos de down retornados pelo OEM continuam gerando `0`, mesmo
durante o warm-up. Respostas `401` ou `403` nos jobs custom de status geram
`3`, pois indicam estado do coletor/script sem confirmacao confiavel do target.

Para compatibilidade com o legado, os grupos que alimentam esta metrica sao
adicionados ao scheduler quando faltam no `configMetrics.yaml`:
`rac_database/Availability`, `oracle_database/Response`, `oracle_pdb/Response`
e `host/Response`. Esses jobs custom usam metadata vazia e tratam respostas
HTTP sem datapoints como coleta vazia, preservando a emissao de
`oem_monitor_stus`.

## oem_service_status

`oem_service_status` e `oem_str_service_status` unificam status de servicos de
`rac_database/service_performance` e `oracle_pdb/DBService`.

- `DBTime_delta > 0` indica servico ativo;
- `status == "Up"` indica ativo e tem prioridade quando presente;
- `oem_service_status` exporta gauge numerico, com `1` para ativo e `0` para
  inativo;
- `oem_str_service_status` exporta log textual, com `ativo` ou `inativo`, e e
  marcado como continuo quando a regra legada exige reenvio mesmo sem mudanca
  de valor.

## Metricas internas

Metricas com prefixo `oem_collector_*` tambem sao geradas pelo codigo, mas
representam saude operacional do processo, nao dados de targets OEM. Elas usam
snapshots de configuracao, monitor de resposta, cliente OEM, coletor e
exportadores para contar targets configurados, ativos/inativos, requests OEM,
erros, datapoints coletados/exportados, falhas de exportacao e tamanho de
payload.
