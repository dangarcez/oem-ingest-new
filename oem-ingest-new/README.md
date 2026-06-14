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
- ponto de entrada que encerra sem iniciar coleta real.

Chamadas OEM, transformacao e exportacao OTLP serao implementadas nas proximas
tarefas do plano.

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
- `OEM_VALIDATE_CONFIG`: `true` ou `false`.
- `OEM_VALIDATED_CONFIG_OUTPUT`: caminho para configuracao corrigida futura.
- `OEM_USER`, `OEM_PASSWORD`, `OEM_TOKEN`, `OEM_AUTH_TOKEN_HASH_FILE`.
- `OTEL_EXPORT_URL`.
- `OEM_EXPORT_INTERVAL_SECONDS`.
- `OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES`.
- `OEM_HTTP_TIMEOUT_SECONDS`.
- `OEM_HTTP_CONNECT_TIMEOUT_SECONDS`.
- `OEM_HTTP_MAX_RETRIES`.
- `OEM_MAX_CONCURRENT_REQUESTS`.
- `OEM_LOG_LEVEL`.

## Comandos

```sh
go test ./...
go vet ./...
go run ./cmd/oem-ingest --help
go run ./cmd/oem-ingest --version
```

Executar `go run ./cmd/oem-ingest` sem argumentos apenas confirma que o
scaffold foi inicializado. Nenhuma chamada externa e feita nesta fase.
