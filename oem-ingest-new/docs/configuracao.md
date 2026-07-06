# Configuracao

Este documento descreve como configurar o `oem-ingest-new` para ler targets e
metricas do Oracle Enterprise Manager (OEM), autenticar na API OEM, validar a
configuracao opcionalmente e exportar dados em OTLP.

Os exemplos versionados ficam em:

- `configs/configTargets.example.yaml`
- `configs/configMetrics.example.yaml`

## Arquivos

A aplicacao consome dois arquivos YAML:

- `configTargets.yaml`: sites OEM, endpoints e targets monitorados.
- `configMetrics.yaml`: grupos de metricas coletados por tipo de target.

Por padrao, os caminhos sao relativos ao diretorio de trabalho:

```sh
./configs/configTargets.yaml
./configs/configMetrics.yaml
```

Use `OEM_CONFIG_TARGETS` e `OEM_CONFIG_METRICS` para informar outros caminhos.
O novo coletor nao gera configuracao a partir de roots como o Python legado
fazia; ele apenas consome o formato simplificado descrito aqui.

## configTargets.yaml

O formato oficial e uma lista de sites. Cada site contem o endpoint OEM e os
targets que serao monitorados nesse endpoint.

```yaml
- name: oraemc
  site: null
  endpoint: http://localhost:8008
  targets:
    - id: "240D79C7320E221DE06400144FFBE115"
      name: "occp40bc"
      typeName: "rac_database"
      tags:
        rac_database: "occp40bc"
        target_name: "occp40bc"
        target_type: "rac_database"
        sistema: "siapx"
        torre: "cartoes"
```

Campos do site:

| Campo | Obrigatorio | Descricao |
| --- | --- | --- |
| `name` | Nao | Nome operacional do site. Usado em logs e atributos quando disponivel. |
| `site` | Nao | Campo livre mantido para compatibilidade com configuracoes existentes. Pode ser `null`. |
| `endpoint` | Sim | URL base da API OEM daquele site, por exemplo `https://oraemc.exemplo`. |
| `targets` | Sim | Lista de targets monitorados nesse site. |

Campos do target:

| Campo | Obrigatorio | Descricao |
| --- | --- | --- |
| `id` | Sim | ID atual do target no OEM. |
| `name` | Sim | Nome do target no OEM. |
| `typeName` | Sim | Tipo do target, como `rac_database`, `oracle_database`, `oracle_pdb`, `host` ou `oracle_listener`. |
| `tags` | Sim | Atributos enviados junto com metricas/logs e usados para correlacao. |

Tags obrigatorias:

- `target_type`: deve ser igual ao `typeName`.
- `target_name`: deve refletir o nome normalizado do target.

Para a maioria dos tipos, `target_name` e igual a `name`. Existem duas
normalizacoes obrigatorias herdadas do legado:

- `host`: use somente o nome curto antes do primeiro ponto. Exemplo:
  `cadecrk01cl01vm03.intra.caixa.gov.br` vira `cadecrk01cl01vm03`.
- `oracle_listener`: remova o prefixo `LISTENER_`, use o host curto e acrescente
  `_lstnr`. Exemplo: `LISTENER_cadecrk01cl01vm03.intra.caixa.gov.br` vira
  `cadecrk01cl01vm03_lstnr`.

Tags externas, como `sistema`, `torre`, ambiente ou dono operacional, sao
preservadas e exportadas como atributos. Tags estruturais comuns sao:

- `oracle_dbsys`
- `rac_database`
- `oracle_pdb`
- `oracle_database`
- `host`
- `oracle_listener`
- `machine_name`
- `listener_name`
- `dg_role`

A hierarquia esperada para targets relacionados e:

```text
oracle_dbsys -> rac_database -> oracle_pdb -> oracle_database -> host/oracle_listener
```

Targets avulsos sao aceitos. A validacao opcional pode expandir automaticamente
targets relacionados quando o target raiz for `rac_database` ou `oracle_pdb` e
os componentes existirem na API OEM.

## configMetrics.yaml

O arquivo de metricas mapeia cada tipo de target para uma lista de grupos de
metricas do OEM.

```yaml
host:
  - freq: 5
    metric_group_name: Load
  - freq: 10
    metric_group_name: Filesystems

oracle_database:
  - freq: 2
    metric_group_name: Response
  - freq: 10
    metric_group_name: instance_throughput

rac_database:
  - freq: 5
    metric_group_name: Availability
  - freq: 15
    metric_group_name: service_performance
```

Campos de cada grupo:

| Campo | Obrigatorio | Descricao |
| --- | --- | --- |
| `freq` | Sim | Frequencia de coleta em minutos. Deve ser maior que zero. |
| `metric_group_name` | Sim | Nome do grupo de metricas conforme registrado no OEM. |

A coleta sempre solicita todas as metricas do grupo. Se um grupo configurado
nao existir para um target especifico, o job desse target/grupo e descartado ou
logado sem derrubar a aplicacao inteira.

## Variaveis de ambiente

| Variavel | Default | Descricao |
| --- | --- | --- |
| `OEM_CONFIG_TARGETS` | `./configs/configTargets.yaml` | Caminho do arquivo de targets. |
| `OEM_CONFIG_METRICS` | `./configs/configMetrics.yaml` | Caminho do arquivo de metricas. |
| `OEM_VALIDATE_CONFIG` | `false` | Ativa validacao de IDs e correlacoes contra a API OEM. Aceita somente `true` ou `false`. |
| `OEM_VALIDATED_CONFIG_OUTPUT` | `./configs/configTargets.validated.yaml` | Caminho do YAML corrigido quando a validacao esta ativa. |
| `OEM_VALIDATION_REPORT_OUTPUT` | derivado de `OEM_VALIDATED_CONFIG_OUTPUT` | Caminho do relatorio JSONL com as alteracoes feitas pela validacao. |
| `OEM_USER` | vazio | Usuario de Basic Auth no OEM. Obrigatorio para coleta ou validacao opcional. |
| `OEM_PASSWORD` | vazio | Senha direta de Basic Auth. Tem prioridade sobre `OEM_TOKEN`. |
| `OEM_TOKEN` | vazio | Token legado usado para recuperar a senha. |
| `OEM_AUTH_TOKEN_HASH_FILE` | vazio | Arquivo usado para calcular o SHA-256 hexadecimal do token legado. Obrigatorio quando `OEM_TOKEN` for a credencial usada, sem `OEM_PASSWORD`. |
| `OTEL_EXPORT_URL` | vazio | URL base do endpoint OTLP HTTP. A aplicacao usa `/v1/metrics` e `/v1/logs`. |
| `OTEL_EXPORT_TIMEOUT_SECONDS` | `30` | Timeout total dos POSTs OTLP HTTP para metricas e logs, em segundos. |
| `OEM_EXPORT_INTERVAL_SECONDS` | `60` | Intervalo de exportacao dos buffers OTLP, em segundos. |
| `OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES` | `21` | Margem, em minutos, somada a frequencia do job que teve coleta util para calcular a validade de `oem_monitor_response`. Ver `docs/metricas_customizadas.md`. |
| `OEM_MONITOR_STATUS_WARMUP_MINUTES` | `0` | Tempo extra, apos a coleta inicial, em que `oem_monitor_stus` trata estados sem coleta como `3` para indicar estado do coletor/script. |
| `OEM_RUNTIME_ID_RECHECK_INTERVAL_SECONDS` | `86400` | Intervalo minimo entre revalidacoes runtime de ID por target apos `404`. |
| `OEM_HTTP_TIMEOUT_SECONDS` | `30` | Timeout total das chamadas HTTP ao OEM, em segundos. |
| `OEM_HTTP_CONNECT_TIMEOUT_SECONDS` | `10` | Timeout de conexao HTTP ao OEM, em segundos. |
| `OEM_HTTP_MAX_RETRIES` | `3` | Numero de retries para GETs ao OEM. Pode ser `0`. |
| `OEM_TLS_VERIFY` | `true` | Valida o certificado TLS do endpoint OEM. Use `false` somente para ambientes com certificado interno/self-signed. |
| `OEM_MAX_CONCURRENT_REQUESTS` | `10` | Limite global de chamadas HTTP simultaneas ao OEM no processo. |
| `OEM_SCHEDULER_JITTER_SECONDS` | `60` | Jitter maximo aleatorio dos jobs de coleta, em segundos. Use `0` para desabilitar. |
| `OEM_DIAGNOSTICS_INTERVAL_SECONDS` | `0` | Intervalo para logar um resumo operacional com buffers, coletas, requests OEM e exportacoes. Use `0` para desabilitar. |
| `OEM_LOG_LEVEL` | `info` | Nivel minimo de log do processo. Aceita `debug`, `info`, `warn`/`warning` ou `error`, sem diferenciar maiusculas e minusculas. |

Variaveis numericas de tempo e concorrencia aceitam inteiros positivos, exceto
`OEM_HTTP_MAX_RETRIES`, `OEM_SCHEDULER_JITTER_SECONDS` e
`OEM_DIAGNOSTICS_INTERVAL_SECONDS`, que aceitam inteiro maior ou igual a zero.

O jitter do scheduler e aplicado antes da primeira execucao periodica de cada
job e tambem somado aos ciclos seguintes, sempre como um atraso aleatorio entre
`0` e `OEM_SCHEDULER_JITTER_SECONDS`. A rodada inicial de coleta executada no
startup nao usa esse jitter.

## Autenticacao

O OEM usa HTTP Basic Auth. Para coleta real ou validacao opcional, configure:

```sh
export OEM_USER=usuario
export OEM_PASSWORD=senha
```

Quando `OEM_PASSWORD` esta definido, ele e usado diretamente e `OEM_TOKEN` e
ignorado.

Para manter compatibilidade com tokens do Python legado, quando `OEM_PASSWORD`
nao estiver definido:

```sh
export OEM_USER=usuario
export OEM_TOKEN='token_urlsafe_base64'
export OEM_AUTH_TOKEN_HASH_FILE=/caminho/para/arquivo-base
```

O algoritmo legado:

1. Calcula SHA-256 do arquivo indicado e usa o digest hexadecimal como chave.
2. Decodifica `OEM_TOKEN` como base64 URL-safe.
3. Aplica XOR byte a byte entre o token decodificado e a chave repetida.
4. Usa o resultado como senha de Basic Auth.

No Python antigo, o hash era calculado sobre um arquivo fonte do script. Em Go,
o binario compilado nao tem o mesmo conceito de arquivo fonte em execucao; por
isso `OEM_AUTH_TOKEN_HASH_FILE` torna o arquivo base explicito. Para preservar
tokens ja gerados, aponte essa variavel para o mesmo arquivo que foi usado na
geracao do token original.

## Validacao opcional

Ative a validacao na inicializacao com:

```sh
export OEM_VALIDATE_CONFIG=true
export OEM_VALIDATED_CONFIG_OUTPUT=./configs/configTargets.validated.yaml
export OEM_VALIDATION_REPORT_OUTPUT=./configs/configTargets.validated.report.jsonl
```

Quando ativa, a aplicacao:

1. Carrega `configTargets.yaml`.
2. Lista targets na API OEM para cada site.
3. Localiza cada target configurado por `name` + `typeName`.
4. Corrige em memoria IDs divergentes.
5. Remove targets que nao existem mais na API OEM.
6. Registra warnings para target ausente, duplicado ou divergente.
7. Valida correlacoes para raizes `rac_database` e `oracle_pdb`.
8. Usa propriedades de `oracle_database`, principalmente `MachineName` e
   `DataGuardStatus`, para inferir `host`, `oracle_listener` e `dg_role`.
9. Adiciona targets relacionados ausentes quando eles existem na API OEM.
10. Escreve um novo YAML corrigido no caminho de `OEM_VALIDATED_CONFIG_OUTPUT`.
11. Escreve um relatorio JSONL no caminho de `OEM_VALIDATION_REPORT_OUTPUT`.

O arquivo original nunca e sobrescrito. O caminho de saida tambem nao pode ser
o mesmo arquivo original, inclusive por symlink ou hardlink. O relatorio tambem
nao pode usar o mesmo caminho do YAML validado. Durante a mesma execucao, a
coleta usa a configuracao corrigida em memoria; targets removidos nao geram
jobs nem chamadas `latestData`. Em uma proxima execucao, rode a validacao
novamente ou aponte `OEM_CONFIG_TARGETS` para o YAML validado, conforme o
processo operacional escolhido.

Com a validacao ativa, o runtime tambem reage a `404` em metadata ou
`latestData`: se `oem_monitor_response` indicar que o target ainda tem coleta
util valida, a checagem de ID e pulada; caso contrario, o runtime lista targets,
corrige o ID quando houver match unico por `name` + `typeName`, reescreve o YAML
validado e anexa um evento `id_correction` ao JSONL. Quando a checagem nao muda
o ID, novas tentativas para o mesmo target respeitam
`OEM_RUNTIME_ID_RECHECK_INTERVAL_SECONDS`.

Veja detalhes, estrutura do relatorio e casos extremos em
[`docs/validacao.md`](validacao.md).

## Execucao local

Um exemplo minimo com arquivos locais:

```sh
cd oem-ingest-new

export OEM_CONFIG_TARGETS=./configs/configTargets.example.yaml
export OEM_CONFIG_METRICS=./configs/configMetrics.example.yaml
export OEM_USER=usuario
export OEM_PASSWORD=senha
export OTEL_EXPORT_URL=http://localhost:4318

go run ./cmd/oem-ingest
```

Esse comando presume que o `endpoint` informado em `configTargets.yaml` aponta
para um OEM acessivel e que `OTEL_EXPORT_URL` aponta para um endpoint OTLP HTTP
acessivel. Para testar com os exemplos versionados sem OEM real nem collector
externo, use o Docker Compose local:

```sh
cd oem-ingest-new

docker compose up --build
```

Ou rode o teste de integracao com mock HTTP em memoria:

```sh
cd oem-ingest-new

go test ./integration -run TestRuntimeIntegrationWithHTTPMockAndExampleConfigs -count=1
```

Para apenas validar e gerar um arquivo corrigido, sem iniciar coleta OTLP:

```sh
cd oem-ingest-new

export OEM_CONFIG_TARGETS=./configs/configTargets.yaml
export OEM_VALIDATE_CONFIG=true
export OEM_VALIDATED_CONFIG_OUTPUT=./configs/configTargets.validated.yaml
export OEM_VALIDATION_REPORT_OUTPUT=./configs/configTargets.validated.report.jsonl
export OEM_USER=usuario
export OEM_PASSWORD=senha

go run ./cmd/oem-ingest
```

Sem `OTEL_EXPORT_URL`, o processo nao inicia o runtime de coleta/exportacao.
Com `OEM_VALIDATE_CONFIG=true`, ele ainda executa a validacao de startup antes
de encerrar. Essa validacao consulta os endpoints OEM configurados no arquivo de
targets, portanto eles tambem precisam estar acessiveis.

## Erros comuns

- `site[0].endpoint: campo obrigatorio`: o site nao tem `endpoint`.
- `site[0].targets: informe ao menos um target`: o site nao tem targets.
- `tags.target_type: esperado ...`: a tag `target_type` diverge de `typeName`.
- `tags.target_name: esperado ...`: a tag `target_name` nao segue a
  normalizacao esperada para o tipo do target.
- `freq: deve ser maior que zero minutos`: o grupo de metricas tem frequencia
  invalida.
- `OEM_USER: campo obrigatorio para autenticacao`: coleta ou validacao opcional
  foi iniciada sem usuario OEM.
- `OEM_AUTH_TOKEN_HASH_FILE: campo obrigatorio ao usar OEM_TOKEN`: token legado
  foi usado como credencial sem arquivo base para hash.
