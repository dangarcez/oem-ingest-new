# Operacao

Este guia descreve como executar, verificar e diagnosticar o `oem-ingest-new`
em desenvolvimento local, container e Docker Compose. A configuracao detalhada
dos YAMLs e variaveis fica em [configuracao.md](configuracao.md); a visao dos
componentes fica em [arquitetura.md](arquitetura.md).

## Pre-requisitos

- Go compativel com o `go.mod` do projeto.
- Docker com suporte a `docker compose`, para execucao em container.
- Acesso HTTP ao Oracle Enterprise Manager quando a execucao nao usar o mock.
- Um endpoint OTLP HTTP quando a execucao nao usar os endpoints fake do mock.

Os comandos abaixo partem do diretorio do projeto novo:

```sh
cd oem-ingest-new
```

## Verificacoes locais

Use estes comandos antes de alterar imagem ou Compose:

```sh
go test ./...
go vet ./...
go run ./cmd/oem-ingest --help
go run ./cmd/oem-ingest --version
```

O teste de integracao usa um mock HTTP em memoria e valida um ciclo real de
coleta, transformacao e POST OTLP:

```sh
go test ./integration -run TestRuntimeIntegrationWithHTTPMockAndExampleConfigs -count=1
```

## Modos de execucao

### Ajuda e versao

```sh
go run ./cmd/oem-ingest --help
go run ./cmd/oem-ingest --version
```

Esses comandos nao leem os arquivos de configuracao nem iniciam coleta.

### Validar configuracao sem coletar

Sem `OTEL_EXPORT_URL`, a aplicacao nao inicia o runtime de coleta/exportacao.
Com `OEM_VALIDATE_CONFIG=true`, ela ainda consulta o OEM, corrige divergencias
em memoria, remove targets ausentes e escreve o arquivo validado e o relatorio
de mudancas nos caminhos configurados.

```sh
export OEM_CONFIG_TARGETS=./configs/configTargets.yaml
export OEM_CONFIG_METRICS=./configs/configMetrics.yaml
export OEM_VALIDATE_CONFIG=true
export OEM_VALIDATED_CONFIG_OUTPUT=./configs/configTargets.validated.yaml
export OEM_VALIDATION_REPORT_OUTPUT=./configs/configTargets.validated.report.jsonl
export OEM_USER=usuario
export OEM_PASSWORD=senha

go run ./cmd/oem-ingest
```

Comportamento esperado:

- imprime um resumo como `validacao de configuracao concluida`;
- escreve o YAML corrigido em `OEM_VALIDATED_CONFIG_OUTPUT`;
- escreve o relatorio JSONL em `OEM_VALIDATION_REPORT_OUTPUT`;
- preserva o arquivo original de targets;
- encerra sem agendar jobs, porque `OTEL_EXPORT_URL` nao foi informado.

### Coleta local com OEM e collector reais

```sh
export OEM_CONFIG_TARGETS=./configs/configTargets.yaml
export OEM_CONFIG_METRICS=./configs/configMetrics.yaml
export OEM_USER=usuario
export OEM_PASSWORD=senha
export OTEL_EXPORT_URL=http://localhost:4318
export OEM_EXPORT_INTERVAL_SECONDS=60
export OEM_SCHEDULER_JITTER_SECONDS=60

go run ./cmd/oem-ingest
```

Comportamento esperado:

- valida a conexao com cada `endpoint` configurado em `configTargets.yaml`;
- cria jobs por site, target e grupo de metrica;
- executa uma rodada inicial de coleta;
- exporta metricas para `${OTEL_EXPORT_URL}/v1/metrics`;
- exporta logs textuais e incidentes para `${OTEL_EXPORT_URL}/v1/logs`;
- mantem jobs periodicos ativos ate receber `SIGINT` ou `SIGTERM`.

Para validar a configuracao e coletar na mesma execucao, defina tambem
`OEM_VALIDATE_CONFIG=true`. Nesse caso, a coleta usa a configuracao corrigida em
memoria durante o processo.

## Docker

Build local:

```sh
docker build -t oem-ingest:dev .
docker run --rm oem-ingest:dev --help
```

Execucao com arquivos reais montados:

```sh
docker run --rm \
  -v "$PWD/configs:/app/configs:ro" \
  -e OEM_USER=usuario \
  -e OEM_PASSWORD=senha \
  -e OTEL_EXPORT_URL=http://otel-collector:4318 \
  oem-ingest:dev
```

O diretorio de trabalho do container e `/app`. Por padrao, a imagem usa:

- `/app/configs/configTargets.yaml`;
- `/app/configs/configMetrics.yaml`;
- `/app/configs/configTargets.validated.yaml`;
- `/app/configs/configTargets.validated.report.jsonl`.

Quando `OEM_VALIDATE_CONFIG=true`, nao monte o diretorio de configuracao apenas
como leitura se `OEM_VALIDATED_CONFIG_OUTPUT` ou
`OEM_VALIDATION_REPORT_OUTPUT` apontarem para dentro dele. Use caminhos
gravaveis, por exemplo `/tmp/configTargets.validated.yaml` e
`/tmp/configTargets.validated.report.jsonl`, ou monte um volume separado para as
saidas da validacao.

Se usar `OEM_TOKEN`, monte tambem o arquivo usado para hash e configure
`OEM_AUTH_TOKEN_HASH_FILE` para o caminho dentro do container:

```sh
docker run --rm \
  -v "$PWD/configs:/app/configs:ro" \
  -v "$PWD/../old_code/oem/tools/xisou.py:/app/auth/xisou.py:ro" \
  -e OEM_USER=usuario \
  -e OEM_TOKEN="$OEM_TOKEN" \
  -e OEM_AUTH_TOKEN_HASH_FILE=/app/auth/xisou.py \
  -e OTEL_EXPORT_URL=http://otel-collector:4318 \
  oem-ingest:dev
```

A imagem runtime e distroless e roda como usuario nao-root. Ela nao possui shell
para depuracao interativa dentro do container.

## Docker Compose local

O Compose sobe apenas dois servicos:

- `oem-mock`: FastAPI com endpoints OEM e endpoints fake `/v1/metrics` e
  `/v1/logs`;
- `oem-ingest`: coletor Go apontando para o mock.

Subir o ambiente:

```sh
docker compose up --build
```

Ver estado dos servicos:

```sh
docker compose ps
docker compose logs -f oem-ingest
docker compose logs -f oem-mock
```

Encerrar e limpar recursos do Compose:

```sh
docker compose down --remove-orphans
```

Para uma verificacao curta e reproduzivel:

```sh
timeout 90s docker compose up --build
docker compose down --remove-orphans
```

Comportamento esperado no Compose:

- `oem-mock` fica saudavel depois de responder `GET /em/api`;
- `oem-ingest` imprime `oem-ingest: coleta iniciada com <N> jobs`;
- o mock recebe chamadas `GET /em/api/.../latestData`;
- o mock recebe POSTs em `/v1/metrics` e `/v1/logs`;
- os logs do app mostram batches OTLP exportados.

Antes de subir, este comando valida a sintaxe final do Compose:

```sh
docker compose config
```

## Logs

Mensagens operacionais do processo usam texto estruturado no `stderr`. Algumas
mensagens de status de startup usam `stdout`, como:

- `oem-ingest: scaffold inicializado; coleta nao iniciada sem OTEL_EXPORT_URL`;
- `validacao de configuracao concluida: ...`;
- `configuracao validada escrita em ...`;
- `relatorio de validacao escrito em ...`;
- `oem-ingest: coleta iniciada com <N> jobs`.

Eventos esperados em operacao normal:

- `conexao OEM validada`: conexao inicial com um endpoint OEM passou;
- `job de coleta registrado`: scheduler registrou um job por target/grupo;
- `batch OTLP exportado`: um batch de metricas ou logs recebeu resposta 2xx;
- `polling de incidentes OEM concluido`: consulta periodica de incidentes
  terminou.

Warnings comuns:

- `metadata de grupo de metrica indisponivel; job de coleta sera ignorado`:
  o grupo nao existe naquele target ou retornou 404;
- `latestData de grupo de metrica indisponivel`: a coleta daquele grupo falhou
  de forma nao fatal;
- `falha ao exportar batch OTLP`: o POST OTLP falhou e o buffer foi preservado
  para retry;
- `falha ao consultar incidentes OEM`: o polling de incidentes falhou naquele
  ciclo.

Logs nao imprimem senha ou token. A variavel `OEM_LOG_LEVEL` define o nivel
minimo emitido pelo processo e aceita `debug`, `info`, `warn`/`warning` ou
`error`, sem diferenciar maiusculas e minusculas.

Para troubleshooting de ambiente, defina `OEM_LOG_LEVEL=info` e
`OEM_DIAGNOSTICS_INTERVAL_SECONDS=60`. O log `diagnostico runtime` mostra
quantos jobs/endpoints estao ativos, quantos itens estao pendentes nos buffers
OTLP, requests/erros OEM, coletas realizadas, grupos indisponiveis e falhas de
exportacao. Se `OTEL_EXPORT_URL` estiver errado, os warnings de exportacao
incluem o endpoint final (`/v1/metrics` ou `/v1/logs`), tamanho do lote e
quantidade ainda pendente no buffer. Para falhar mais rapido durante testes de
rede, reduza `OTEL_EXPORT_TIMEOUT_SECONDS`.

No Docker Compose local, `OEM_LOG_LEVEL` vale apenas para o servico
`oem-ingest`. O servico `oem-mock` usa `uvicorn` com nivel `warning` e access log
desabilitado para evitar ruido de `INFO` do mock.

## Encerramento

O processo trata `SIGINT` e `SIGTERM`. No encerramento:

1. o contexto do runtime e cancelado;
2. o scheduler aguarda jobs ativos por ate 10 segundos;
3. pollers de incidentes aguardam encerramento por ate 10 segundos;
4. metricas e logs pendentes passam por um flush final com timeout de 10
   segundos.

Falhas no flush final sao registradas, mas dados pendentes em memoria se perdem
quando o processo termina. Em operacao normal, prefira encerrar o container com
o sinal padrao do Docker para permitir esse caminho de shutdown.

## Metricas internas

As metricas internas sao exportadas no mesmo endpoint OTLP de metricas e usam o
prefixo `oem_collector_*`:

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

Metricas de targets sao agregadas por `site`, `endpoint` e `target_type` para
evitar cardinalidade alta. Metricas globais usam o atributo `scope=global`.

## Troubleshooting

`coleta nao iniciada sem OTEL_EXPORT_URL`

O processo esta em modo de validacao/scaffold. Defina `OTEL_EXPORT_URL` para
iniciar coleta e exportacao.

`carregar targets "./configs/configTargets.yaml": no such file or directory`

O arquivo nao existe no diretorio de trabalho. Defina `OEM_CONFIG_TARGETS` ou
monte o arquivo esperado em `/app/configs/configTargets.yaml` no container.

`OEM_USER: campo obrigatorio para autenticacao`

Coleta ou validacao opcional foi iniciada sem usuario OEM. Configure
`OEM_USER` junto com `OEM_PASSWORD` ou `OEM_TOKEN`.

`validar conexao OEM "<endpoint>": ...`

A aplicacao nao conseguiu acessar `GET /em/api` no endpoint do site. Verifique
URL, rede, certificado, usuario e senha.

`nenhum job de coleta foi criado a partir da configuracao`

Os targets existem, mas nenhum `typeName` deles possui grupos em
`configMetrics.yaml`, ou o arquivo de metricas esta vazio para os tipos
configurados.

`nenhum job de coleta concluiu com sucesso`

A rodada inicial falhou para todos os jobs. Verifique logs de 401/404/500,
metadados dos grupos, endpoint OEM e credenciais.

`falha ao exportar batch OTLP`

O endpoint OTLP rejeitou ou nao recebeu o payload. Verifique
`OTEL_EXPORT_URL`, conectividade e suporte a OTLP HTTP/protobuf. O app mantem o
buffer para tentar novamente no proximo ciclo enquanto o processo continuar.

`OEM_VALIDATED_CONFIG_OUTPUT ... deve ser diferente de OEM_CONFIG_TARGETS`

O arquivo validado foi configurado para sobrescrever o original, ou aponta para
o mesmo arquivo via link. Escolha outro caminho.

`OEM_VALIDATION_REPORT_OUTPUT ... deve ser diferente ...`

O relatorio foi configurado para sobrescrever o arquivo original de targets ou
o YAML validado. Escolha um caminho proprio para o relatorio.

Compose nao sobe por timeout no mock

Confira os logs do servico:

```sh
docker compose logs oem-mock
```

O mock instala dependencias Python no startup do container; a primeira execucao
pode demorar mais.
