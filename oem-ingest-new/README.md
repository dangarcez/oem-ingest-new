# OEM Ingest

Coletor em Go para consumir metricas da API do Oracle Enterprise Manager (OEM)
e exportar dados em OTLP para um OpenTelemetry Collector.

Este diretorio contem o novo projeto. O codigo legado permanece fora daqui, em
`../old_code`, apenas como referencia de compatibilidade para tarefas futuras.

## Escopo inicial

O projeto atual define:

- modulo Go em `oem-ingest-new`;
- comando `cmd/oem-ingest`;
- estrutura de pacotes internos planejada para configuracao, cliente OEM,
  validacao, coleta, transformacao, exportacao, incidentes, metricas internas,
  agendamento e logging;
- leitura de variaveis de ambiente;
- loader YAML para `configTargets.yaml` e `configMetrics.yaml`;
- resolucao de credenciais OEM para Basic Auth, incluindo token legado;
- cliente HTTP OEM com Basic Auth, timeouts, retries, pool de conexoes,
  paginacao por `links.next` e endpoints tipados;
- validacao opcional de IDs de targets na inicializacao;
- ponto de entrada que encerra sem iniciar coleta real.

Scheduler de coleta, transformacao e exportacao OTLP serao implementados nas
proximas tarefas do plano.

## Configuracao

Por padrao, a aplicacao procura os arquivos abaixo no diretorio de trabalho:

- `./configs/configTargets.yaml`
- `./configs/configMetrics.yaml`

Exemplos versionados estao em:

- `configs/configTargets.example.yaml`
- `configs/configMetrics.example.yaml`

Variaveis de ambiente suportadas nesta fase:

- `OEM_CONFIG_TARGETS`: caminho do arquivo de targets.
- `OEM_CONFIG_METRICS`: caminho do arquivo de metricas.
- `OEM_VALIDATE_CONFIG`: `true` ou `false`; quando `true`, consulta a API OEM
  e corrige IDs/correlacoes divergentes em memoria.
- `OEM_VALIDATED_CONFIG_OUTPUT`: caminho para gravar a configuracao corrigida,
  sem sobrescrever o arquivo original.
- `OEM_USER`, `OEM_PASSWORD`, `OEM_TOKEN`, `OEM_AUTH_TOKEN_HASH_FILE`.
- `OTEL_EXPORT_URL`.
- `OEM_EXPORT_INTERVAL_SECONDS`.
- `OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES`.
- `OEM_HTTP_TIMEOUT_SECONDS`.
- `OEM_HTTP_CONNECT_TIMEOUT_SECONDS`.
- `OEM_HTTP_MAX_RETRIES`.
- `OEM_MAX_CONCURRENT_REQUESTS`.
- `OEM_LOG_LEVEL`.

### Autenticacao

O OEM usa HTTP Basic Auth. A aplicacao resolve as credenciais assim:

- `OEM_USER` e obrigatorio.
- `OEM_PASSWORD` e a senha direta e tem prioridade quando tambem existir
  `OEM_TOKEN`.
- `OEM_TOKEN` mantem compatibilidade com o algoritmo legado de
  `old_code/oem/tools/xisou.py`.
- Ao usar `OEM_TOKEN`, informe `OEM_AUTH_TOKEN_HASH_FILE`. O token legado e
  decodificado com XOR usando o SHA-256 hexadecimal desse arquivo como chave.

No Python antigo, o hash era calculado sobre o arquivo fonte do script em
execucao. Em Go, o binario compilado nao tem o mesmo conceito de arquivo fonte;
por isso o arquivo usado no hash e configurado explicitamente. Para preservar
tokens antigos, aponte `OEM_AUTH_TOKEN_HASH_FILE` para o mesmo arquivo usado
quando o token foi gerado.

## Comandos

```sh
go test ./...
go vet ./...
go run ./cmd/oem-ingest --help
go run ./cmd/oem-ingest --version
```

Executar `go run ./cmd/oem-ingest` sem argumentos apenas confirma que o
scaffold foi inicializado. Chamadas externas so sao feitas nesta fase quando
`OEM_VALIDATE_CONFIG=true`.

## Docker

A imagem e construida com multi-stage build e executa como usuario nao-root:

```sh
docker build -t oem-ingest:dev .
docker run --rm oem-ingest:dev --help
```

O diretorio de trabalho do container e `/app`. Por padrao, a aplicacao procura:

- `/app/configs/configTargets.yaml`
- `/app/configs/configMetrics.yaml`
- `/app/configs/configTargets.validated.yaml`

Os exemplos versionados sao copiados para `/app/configs`, mas arquivos reais de
configuracao devem ser montados nesse diretorio em execucao:

```sh
docker run --rm \
  -v "$PWD/configs:/app/configs:ro" \
  -e OEM_USER=usuario \
  -e OEM_PASSWORD=senha \
  -e OTEL_EXPORT_URL=http://otel-collector:4318 \
  oem-ingest:dev
```

Quando `OEM_VALIDATE_CONFIG=true`, a aplicacao grava a configuracao corrigida no
caminho de `OEM_VALIDATED_CONFIG_OUTPUT`. Se `/app/configs` estiver montado como
somente leitura, defina essa variavel para um caminho gravavel, por exemplo
`/tmp/configTargets.validated.yaml`, ou monte um diretorio de saida separado.

Para usar `OEM_TOKEN`, monte tambem o arquivo usado como base de hash e aponte
`OEM_AUTH_TOKEN_HASH_FILE` para o caminho dentro do container, por exemplo
`/app/auth/xisou.py`.
