# Compatibilidade Com Legado

Este documento registra o contrato de saida mantido pelo `oem-ingest-new` em
relacao ao coletor Python legado e as mudancas intencionais do novo projeto. O
objetivo e permitir substituir o coletor antigo na pipeline preservando nomes,
atributos e semantica operacional onde isso afeta consumidores de metricas e
logs.

As referencias principais sao `old_docs/2-configuracao_targets.md`,
`old_docs/3-configuracao_metrics.md`, `old_docs/4-processo_padrao.md`,
`old_docs/5-exceções.md` e os trechos legados em `old_code/script.py`,
`old_code/oem/otel/customexport.py`, `old_code/oem/otel/exportadorlogs.py`,
`old_code/oem/tools/oemalert.py` e `old_code/oem/tools/processMapping.py`.

## Mantido

Metricas numericas continuam sendo exportadas como gauges OTLP HTTP/protobuf
para `${OTEL_EXPORT_URL}/v1/metrics`. Logs textuais e incidentes continuam sendo
exportados como logs OTLP HTTP/protobuf para `${OTEL_EXPORT_URL}/v1/logs`.
Metricas e logs usam o recurso `service.name=oemAPIService`.

O nome padrao das metricas OEM segue o formato legado:

```text
oem_<metric_group_name>_<metric_name>
```

Espacos sao trocados por `_` e o nome final exportado e sempre lowercase. As
keys do grupo de metrica identificam a serie e nao viram metricas.

Os atributos de cada serie combinam as tags do target com as keys do item
coletado. A ordem e as reescritas de conflito seguem `build_tags` e
`_buildAttributes` do legado:

- `instance` vira `_instance`;
- `service_name` vira `name` e, pela regra seguinte, vira `name_`;
- `name` vira `name_`;
- `Username_machine` gera tambem `user` e `pod` a partir dos dois primeiros
  segmentos separados por `_`.

Quando `service_name` e `name` existem no mesmo item, `service_name` tem
precedencia porque o Python legado tambem sobrescrevia `name` antes de aplicar a
reescrita para `name_`. Os nomes dos atributos preservam o casing vindo da
configuracao/OEM; o lowercase obrigatorio se aplica aos nomes de metricas/logs
exportados.

Tags externas de configuracao, como `sistema`, `torre`, ambiente ou dono
operacional, sao preservadas. As tags estruturais de targets mantem a
normalizacao legada de `processMapping.py`, incluindo `target_name`,
`target_type`, tags por tipo de target, `dg_role`, `machine_name`,
`listener_name`, nome curto de `host` e sufixo `_lstnr` para
`oracle_listener`.

Valores numericos nativos viram gauges. Quando a metadata do grupo informa
`dataType` numerico, strings numericas tambem sao tratadas como numeros. Valores
booleanos seguem a compatibilidade do Python, onde `bool` tambem era tratado
como numero. Sem metadata numerica, strings continuam sendo logs mesmo quando
parecem conter um numero. Valores textuais viram logs com o texto no body e o
atributo `metric` contendo o nome normalizado da metrica.

Logs textuais mantem a regra do legado: a primeira ocorrencia de uma serie e
enviada, valores iguais nao sao reenviados e valores alterados sao enviados
novamente. Metricas textuais marcadas como continuas, como
`oem_str_service_status`, sao sempre enfileiradas mesmo quando o valor nao muda.
A deduplicacao usa o conjunto `metric + target + serie`, preservando series
distintas do mesmo grupo.

## Metricas Customizadas

`oem_monitor_response` continua indicando a saude da propria coleta por target:

- `1`: houve coleta util dentro da tolerancia;
- `0`: o target nunca coletou ou a ultima coleta util esta fora da tolerancia.

A tolerancia padrao permanece 21 minutos, agora configuravel por
`OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES`.

`oem_monitor_stus` mantem exatamente o nome legado com erro de grafia. Os codigos
tambem foram preservados:

- `0`: down ou inativo;
- `1`: sem coleta;
- `2`: up ou coletando.

As regras por tipo de target seguem a logica do Python:

- `rac_database`: usa `Availability`; se houver item, status `0`; se vier vazio,
  usa `oem_monitor_response` para decidir `2` ou `1`.
- `oracle_database`: usa `Response`; se vier vazio, usa
  `oem_monitor_response`; se houver item, usa `Status` ou `DatabaseStatus`.
- `oracle_pdb`: usa `Response`; se vier vazio, status `1`; se houver `Status`,
  `0` gera status `0` e outros valores geram `2`; sem `Status`, `State != OPEN`
  gera status `0`, caso contrario `2`.
- `host`: usa `Response`; se vier vazio, usa `oem_monitor_response`; se houver
  item, `Status == 0` gera `0`, caso contrario `2`.

Para manter a compatibilidade com o Python, os grupos que alimentam essas
metricas customizadas sao adicionados aos jobs de coleta quando faltam no
`configMetrics.yaml`: `rac_database/Availability`,
`oracle_database/Response`, `oracle_pdb/Response` e `host/Response`. Esses jobs
custom usam metadata vazia e tratam respostas HTTP sem datapoints como coleta
vazia, preservando a emissao de `oem_monitor_stus`.

`oem_service_status` e `oem_str_service_status` continuam unificando status de
servicos de `rac_database/service_performance` e `oracle_pdb/DBService`:

- `DBTime_delta > 0` indica ativo;
- `status == "Up"` indica ativo e tem prioridade quando presente;
- a metrica numerica usa `1` para ativo e `0` para inativo;
- a versao textual usa `ativo` e `inativo` e e marcada como continua.

## Incidentes

Incidentes continuam sendo exportados como logs OTLP com o nome normalizado
`oem_incident`. O polling usa a janela de 1 hora e roda a cada 5 minutos. Cada
incidente novo e deduplicado em memoria pelo `id`, usa `message` como body do
log e envia os demais campos como atributos. O campo `message` nao e duplicado
como atributo, seguindo o comportamento do logger legado.

O novo coletor preserva o workaround legado de timestamp: `timeCreated` e
`timeUpdated` sao exportados com 3 horas subtraidas. O timestamp do log tambem
usa `timeCreated` corrigido quando ele esta presente e valido.

Campos de target do primeiro item de `targets` sao expostos como `target_id`,
`target_name`, `target_type` e `target_type_display_name`, alem de `targets`
serializado em JSON. Campos extras retornados pelo OEM tambem sao preservados
quando nao colidem com atributos ja normalizados. Logs de incidente usam
severidade WARN.

A verificacao periodica de fechamento tambem foi mantida. Por default, ela roda
a cada 1 hora, consulta o detalhe de incidentes conhecidos e remove o `id` da
deduplicacao quando o endpoint falha ou quando `status == Closed`.

## Mudancas Intencionais

A principal mudanca e a exportacao incremental de metricas. O Python mantinha
um repositorio completo de gauges e reenviava todos os datapoints conhecidos em
cada ciclo de exportacao. O Go mantem somente um buffer de datapoints coletados
desde o ultimo POST bem-sucedido. Depois de resposta 2xx, o buffer enviado e
limpo; em erro de transporte ou status nao-2xx, o buffer permanece para retry no
proximo ciclo.

Essa mudanca reduz payload repetido. Como consequencia, consumidores nao devem
esperar que uma metrica OEM antiga seja publicada de novo se nenhum job coletou
um novo datapoint para ela desde o ultimo envio bem-sucedido.

O novo coletor nao gera `configTargets.yaml` a partir de roots como o Python
fazia. Ele consome o formato simplificado ja gerado por outra aplicacao. Tambem
nao usa `USE_TARGET_CONFIG`, `USE_TARGET_CACHE`, `EM_BASE_URL` nem `PROTOCOL`
como contrato publico. O endpoint OEM vem de `configTargets.yaml`, e caches do
coletor sao apenas em memoria.

A validacao opcional de configuracao e nova no runtime Go. Quando
`OEM_VALIDATE_CONFIG=true`, o coletor consulta o OEM, corrige IDs e correlacoes
em memoria, remove targets ausentes, escreve um novo arquivo em
`OEM_VALIDATED_CONFIG_OUTPUT`, gera relatorio JSONL em
`OEM_VALIDATION_REPORT_OUTPUT` e preserva o arquivo original. Durante a coleta,
`404` em metadata ou `latestData` tambem pode disparar uma revalidacao de ID e
reescrever o YAML validado quando houver match unico seguro.

Metricas internas da aplicacao sao novas e usam o prefixo `oem_collector_*`.
Elas descrevem o proprio coletor e hoje incluem:

- `oem_collector_targets_configured`;
- `oem_collector_targets_active`;
- `oem_collector_targets_inactive`;
- `oem_collector_oem_requests_total`;
- `oem_collector_oem_request_errors_total`;
- `oem_collector_datapoints_collected_total`;
- `oem_collector_datapoints_exported_total`;
- `oem_collector_logs_exported_total`;
- `oem_collector_export_failures_total`;
- `oem_collector_export_payload_bytes`;
- `oem_collector_export_duration_seconds`.

## Autenticacao Legada

Basic Auth continua sendo o mecanismo de autenticacao contra o OEM. A senha pode
vir diretamente de `OEM_PASSWORD` ou de `OEM_TOKEN`, que preserva o algoritmo
legado de XOR/base64 URL-safe com o SHA-256 hexadecimal de um arquivo.

No Python, o arquivo usado no hash era o arquivo fonte do script em execucao. No
Go, esse arquivo deve ser informado explicitamente por
`OEM_AUTH_TOKEN_HASH_FILE`. Para reaproveitar tokens existentes, aponte essa
variavel para o mesmo arquivo usado na geracao original do token.

## Fora Do Contrato

O novo projeto nao preserva detalhes internos do Python que nao afetam a saida
OTLP, como estrutura de diretorios, arquivos `msgpack` de cache, dumps em
`output/`, variaveis antigas de controle de cache e o mecanismo de scheduler do
APScheduler.

## Comparacao Com Mock

A tarefa 9.1 executou uma comparacao end-to-end do contrato de saida com o mock
HTTP de integracao em `TestLegacyCompatibilityComparisonWithHTTPMockAndExampleConfigs`.
Esse mock usa os exemplos do projeto e payloads representativos do `oem_mock`,
mas tambem decodifica os protobufs OTLP para permitir comparar nomes, atributos
e logs. O `oem_mock` Python continua sendo usado como smoke dos endpoints OEM e
dos stubs `/v1/metrics` e `/v1/logs`, mas ele apenas aceita payload binario e nao
inspeciona o conteudo OTLP.

O cenario confirmou:

- `service.name=oemAPIService` em metricas e logs;
- nomes exportados em lowercase no formato legado, incluindo
  `oem_availability_status`, `oem_response_status`,
  `oem_instance_throughput_callspersec`, `oem_monitor_response`,
  `oem_monitor_stus` e `oem_service_status`;
- preservacao de tags externas como `sistema` e `torre`;
- atributos principais e conflitos legados, incluindo `instance` para
  `_instance` e `name` para `name_`;
- logs textuais com atributo `metric`, body textual e severidade `INFO`;
- `oem_str_service_status` como log textual continuo com body `ativo`;
- incidentes como log `oem_incident`, severidade `WARN`, `message` no body,
  atributos de target preservados e `timeCreated`/`timeUpdated` corrigidos em
  menos 3 horas, incluindo o timestamp OTLP do log baseado em `timeCreated`
  corrigido.

Nao foram encontradas divergencias nao intencionais nesse cenario. As
divergencias observadas permanecem as intencionais ja descritas neste documento:
exportacao incremental de metricas, metricas internas `oem_collector_*`,
validacao opcional de configuracao e ausencia de geracao de configuracao a
partir de roots.

## Lacunas Conhecidas

O contrato de compatibilidade e a forma dos dados exportados. Diferencas
operacionais documentadas acima sao intencionais e fazem parte da refatoracao.
