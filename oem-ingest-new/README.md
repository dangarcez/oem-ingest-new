# OEM Ingest

Coletor em Go para consumir metricas da API do Oracle Enterprise Manager (OEM)
e exportar dados em OTLP para um OpenTelemetry Collector.

Este diretorio contem o novo projeto. O codigo legado permanece fora daqui, em
`../old_code`, apenas como referencia de compatibilidade para tarefas futuras.

## Escopo inicial

O scaffold atual define:

- modulo Go em `oem-ingest-new`;
- comando `cmd/oem-ingest`;
- estrutura de pacotes internos planejada para configuracao, cliente OEM,
  validacao, coleta, transformacao, exportacao, incidentes, metricas internas,
  agendamento e logging;
- ponto de entrada que encerra sem iniciar coleta real.

Funcionalidades de leitura de configuracao, chamadas OEM, transformacao e
exportacao OTLP serao implementadas nas proximas tarefas do plano.

## Comandos

```sh
go test ./...
go run ./cmd/oem-ingest --help
go run ./cmd/oem-ingest --version
```

Executar `go run ./cmd/oem-ingest` sem argumentos apenas confirma que o
scaffold foi inicializado. Nenhuma chamada externa e feita nesta fase.
