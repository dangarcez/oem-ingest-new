# Arquitetura

Este documento descreve a arquitetura do `oem-ingest-new`, o coletor em Go que
le configuracoes simplificadas, consulta a API do Oracle Enterprise Manager
(OEM), transforma os dados coletados e exporta metricas e logs em OTLP.

O desenho atual preserva a compatibilidade operacional com o coletor legado em
Python onde isso faz parte do contrato de saida, mas remove o reenvio continuo
de todo o repositorio de metricas. A exportacao de metricas agora usa buffer
incremental: cada envio contem apenas datapoints coletados desde o ultimo POST
bem-sucedido.

## Componentes

O codigo fica organizado em pacotes internos com responsabilidades pequenas:

- `cmd/oem-ingest`: entrada do processo, flags `--help` e `--version`, criacao
  do logger e tratamento de SIGINT/SIGTERM.
- `internal/app`: wiring do runtime. Le variaveis de ambiente, carrega
  configuracoes, executa validacao opcional, cria clientes OEM, coletores,
  exportadores, scheduler e pollers de incidente.
- `internal/config`: leitura de ambiente e YAMLs `configTargets.yaml` e
  `configMetrics.yaml`, validacao sintatica e escrita do arquivo corrigido pela
  validacao opcional.
- `internal/auth`: credenciais Basic Auth por `OEM_USER`/`OEM_PASSWORD` ou
  token legado `OEM_TOKEN` com XOR/base64/hash.
- `internal/oem`: cliente HTTP para a API OEM, com Basic Auth, timeouts, retry
  para GET, paginacao por `links.next`, limite compartilhado de concorrencia,
  endpoints tipados e contadores internos de requests.
- `internal/validate`: validacao opcional de configuracao contra a API OEM,
  corrigindo IDs em memoria, removendo targets ausentes, validando correlacoes
  e gerando YAML corrigido e relatorio de mudancas sem sobrescrever o original.
- `internal/scheduler`: criacao de jobs por site, target e grupo de metrica,
  frequencia em minutos, jitter e protecao contra sobreposicao do mesmo job.
- `internal/collect`: cache em memoria de metadata de metric groups, coleta de
  `latestData`, contagem de datapoints e monitoramento da ultima coleta util por
  target.
- `internal/transform`: normalizacao de atributos, metricas numericas, logs
  textuais e metricas customizadas legadas.
- `internal/exporter`: exportadores OTLP HTTP/protobuf de metricas e logs, ambos
  com buffers retidos em caso de falha.
- `internal/incidents`: polling de incidentes OEM, deduplicacao em memoria,
  conversao para logs OTLP e verificacao periodica de fechamento.
- `internal/selfmetrics`: geracao das metricas internas `oem_collector_*`.
- `internal/logging`: configuracao do logger estruturado e parsing de nivel.

## Fluxo De Inicializacao

1. `cmd/oem-ingest` cria o contexto cancelavel por sinal e chama `app.Run`.
2. `app.Run` le as variaveis de ambiente por `config.ReadEnv`.
3. Se `OEM_VALIDATE_CONFIG=true`, a aplicacao carrega `configTargets.yaml`,
   consulta a API OEM e executa as validacoes de IDs e correlacoes.
4. Quando a validacao encontra divergencias, as correcoes e remocoes ficam em
   memoria, sao gravadas em `OEM_VALIDATED_CONFIG_OUTPUT` e sao resumidas em
   `OEM_VALIDATION_REPORT_OUTPUT`. O arquivo original nao e sobrescrito.
5. Se `OTEL_EXPORT_URL` nao estiver definido, o processo encerra apos as
   validacoes explicitas de startup. Se estiver definido, o runtime de coleta e
   exportacao e iniciado.

## Configuracao

A aplicacao consome dois arquivos YAML:

- `configTargets.yaml`: lista sites OEM, endpoints e targets. Cada target deve
  conter `id`, `name`, `typeName` e tags estruturais como `target_name` e
  `target_type`.
- `configMetrics.yaml`: mapeia cada tipo de target para os grupos de metricas
  que devem ser coletados e a frequencia de coleta em minutos.

O novo projeto nao gera configuracao a partir de roots como o legado. Ele apenas
consome os arquivos simplificados gerados fora do coletor. A validacao opcional
serve para detectar e corrigir divergencias conhecidas entre os arquivos e o
estado atual do OEM.

## Validacao De Targets

Quando ativada, a validacao consulta a API OEM por site e aplica duas etapas:

- IDs: cada target configurado e localizado pelo par `name` + `typeName`. Se o
  ID atual na API divergir do YAML, o ID e corrigido em memoria e registrado no
  arquivo validado. Se o target nao existir na API, ele e removido do arquivo
  validado e da run atual.
- Correlacao: targets `rac_database` e `oracle_pdb` podem ser expandidos com
  componentes relacionados quando a API permite inferir a hierarquia. A
  hierarquia esperada e `oracle_dbsys -> rac_database -> oracle_pdb ->
  oracle_database -> host/oracle_listener`.

Tags externas, como `sistema` e `torre`, sao preservadas. Targets avulsos sao
aceitos; a expansao automatica e limitada aos casos definidos no plano do
projeto. O detalhe operacional da validacao fica em
[`validacao.md`](validacao.md).

## Coleta

O scheduler cria um job para cada combinacao de site, target e grupo de metrica
definida em `configMetrics.yaml` para o `typeName` do target. Cada job respeita
`freq` em minutos, recebe jitter configurado por
`OEM_SCHEDULER_JITTER_SECONDS` (60 segundos por padrao) e nao permite que duas
execucoes do mesmo job rodem em paralelo.

Antes de consultar dados, o coletor busca metadata do grupo em
`/metricGroups/{groupName}` para descobrir as keys e tipos das metricas. Essa
metadata fica em cache em memoria por `targetId + metricGroupName`. Depois, cada
job chama `/latestData?limit=200`, usando a paginacao do cliente OEM quando a
API retorna `links.next`.

As chamadas HTTP ao OEM passam por um limitador compartilhado configurado por
`OEM_MAX_CONCURRENT_REQUESTS` (default 10). O limite e aplicado no cliente OEM,
portanto cobre metadata, `latestData`, paginacao, validacao opcional e polling
de incidentes, mesmo quando ha varios sites configurados.

Uma coleta so atualiza o monitor de resposta do target quando retorna pelo
menos um datapoint util, isto e, campos que nao sao keys. Esse estado alimenta
`oem_monitor_response`, `oem_monitor_stus` e as metricas internas de targets
ativos/inativos.

## Transformacao

`internal/transform` converte cada `collect.Result` em dois tipos de saida:

- `MetricPoint`: metricas numericas que viram gauges OTLP.
- `LogRecord`: valores textuais que viram logs OTLP.

O nome padrao segue o legado:

```text
oem_<metric_group_name>_<metric_name>
```

Espacos sao convertidos para `_` e o nome exportado e sempre lowercase. Campos
marcados como keys pela metadata identificam a serie e nao viram metricas.
Quando a metadata informa `dataType`, ela tem prioridade para decidir se uma
string numerica deve ser tratada como numero ou texto.

Os atributos unem tags do target e keys do item. A normalizacao preserva
compatibilidades legadas, incluindo `instance -> _instance`, `service_name ->
name`, `name -> name_` e a derivacao de `user`/`pod` a partir de
`Username_machine`.

## Metricas Customizadas

Algumas series nao existem diretamente na API OEM e sao geradas no coletor:

- `oem_monitor_response`: gauge que indica se o target teve coleta util dentro
  da tolerancia configurada por `OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES`
  (default de 21 minutos).
- `oem_monitor_stus`: nome legado mantido exatamente, incluindo o erro de
  grafia. Usa regras especificas por `rac_database`, `oracle_database`,
  `oracle_pdb` e `host`.
- `oem_service_status`: status numerico de servicos a partir de
  `service_performance` ou `DBService`.
- `oem_str_service_status`: status textual equivalente, marcado como continuo
  quando a regra legada exige reenvio mesmo sem mudanca de valor.

As metricas internas usam o prefixo `oem_collector_*` e sao geradas a partir de
snapshots de configuracao, monitor de resposta, cliente OEM, coletor e
exportadores. Elas cobrem targets configurados, targets ativos/inativos,
requests OEM, erros, datapoints coletados/exportados, falhas de exportacao e
tamanho de payload.

## Exportacao OTLP

Os exportadores recebem `OTEL_EXPORT_URL` como URL base e montam os endpoints:

- `${OTEL_EXPORT_URL}/v1/metrics`
- `${OTEL_EXPORT_URL}/v1/logs`

Metricas e logs usam `service.name=oemAPIService`. Os payloads sao OTLP
HTTP/protobuf.

O exportador de metricas mantem um buffer de datapoints pendentes. A cada ciclo
de exportacao, ele faz um snapshot do buffer, monta o payload e envia por POST.
Somente uma resposta 2xx remove os datapoints exportados do buffer. Erros de
transporte, erro de montagem ou status HTTP nao-2xx preservam os dados para
retry no proximo ciclo.

O exportador de logs aplica a regra legada para metricas textuais: valores sao
enfileirados quando mudam para a mesma serie, exceto registros marcados como
continuos, que sao sempre enfileirados. Assim como nas metricas, uma falha de
POST preserva os logs pendentes.

## Incidentes

O runtime cria um poller de incidentes por endpoint OEM. O polling consulta
incidentes com janela de 1 hora a cada 5 minutos, deduplica por `id` em memoria
e transforma cada incidente novo em um `LogRecord` com a mensagem no body e os
demais campos como atributos.

Por compatibilidade com o ambiente legado, o timestamp do incidente exportado
subtrai 3 horas. O poller tambem verifica periodicamente detalhes dos incidentes
ja conhecidos; quando o detalhe falha ou retorna `status == Closed`, o ID sai da
deduplicacao em memoria e pode ser exportado novamente se reaparecer em uma
janela futura.

## Diferencas Para O Legado

O contrato de saida foi mantido onde importa para a pipeline: nomes `oem_*`,
lowercase, `service.name=oemAPIService`, atributos normalizados, metricas
customizadas e logs textuais seguem as regras conhecidas do Python.

A diferenca intencional mais importante esta na exportacao de metricas. O
Python mantinha um repositorio completo de metricas e, a cada ciclo, fazia um
snapshot desse repositorio para reenviar todos os datapoints conhecidos. O Go
mantem apenas o buffer de dados coletados desde o ultimo envio bem-sucedido.
Isso reduz payload repetido e evita republicar metricas antigas quando nenhum
job novo coletou dados.

Outra diferenca estrutural e que cache permitido no novo projeto fica somente
em memoria. O processo tambem nao usa `USE_TARGET_CONFIG`, `USE_TARGET_CACHE`,
`EM_BASE_URL` ou `PROTOCOL` como contrato publico; endpoints OEM vem de
`configTargets.yaml`.

## Concorrencia E Encerramento

Cada job de coleta roda em sua goroutine e possui guarda contra sobreposicao. O
runtime compartilha estruturas concorrentes com locks internos nos caches,
exportadores, monitor de resposta, metricas internas e no limitador global de
requests OEM.

No shutdown por contexto, SIGINT ou SIGTERM, o scheduler espera jobs ativos
encerrarem, os pollers de incidentes param e o runtime tenta um flush final dos
buffers de metricas e logs com timeout. Falhas de API OEM ou de exportacao sao
logadas e contabilizadas, mas nao removem dados pendentes nem encerram o
processo quando podem ser tratadas como transitorias.
