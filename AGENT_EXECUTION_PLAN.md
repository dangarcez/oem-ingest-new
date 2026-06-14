# Plano de Execução para o Projeto OEM Ingest em Go

Este arquivo é o guia operacional para desenvolver o novo coletor em Go em múltiplas sessões com agents diferentes. Ele consolida o PRD, as respostas de decisão do usuário, a documentação antiga e os pontos relevantes do código legado.

## Como Usar

Em cada sessão, o agent deve:

1. Ler este arquivo inteiro.
2. Ler `project.prd`.
3. Verificar o estado do Git com `git status --short`.
4. Se for iniciar uma tarefa nova e estiver em `main`, criar uma branch no formato `task/<id>-<slug-curto>`.
5. Consultar os documentos antigos relevantes antes de alterar código:
   - `old_docs/2-configuracao_targets.md`
   - `old_docs/3-configuracao_metrics.md`
   - `old_docs/4-processo_padrao.md`
   - `old_docs/5-exceções.md`
6. Consultar o código legado apenas quando a tarefa exigir compatibilidade de comportamento:
   - `old_code/script.py`
   - `old_code/oem/tools/oemconnect.py`
   - `old_code/oem/tools/oemalert.py`
   - `old_code/oem/tools/oemapping.py`
   - `old_code/oem/tools/processMapping.py`
   - `old_code/oem/tools/xisou.py`
   - `old_code/oem/otel/customexport.py`
   - `old_code/oem/otel/exportadorlogs.py`
7. Escolher a próxima tarefa pendente que tenha dependências concluídas.
8. Implementar a tarefa, adicionar ou ajustar testes e rodar as verificações possíveis.
9. Atualizar o status da tarefa neste arquivo e registrar uma nota curta em "Registro de Progresso".
10. Criar um commit com a tarefa concluída ou com o progresso parcial relevante.



## Fluxo Git

- A branch principal local deve ser `main`.
- Cada tarefa deve ser feita em uma branch própria no formato `task/<id>-<slug-curto>`, por exemplo `task/0.1-scaffold-go`.
- O commit de cada tarefa deve incluir o código, os testes, a documentação afetada e a atualização correspondente deste arquivo.
- Mensagens de commit devem ser objetivas, por exemplo `task 0.1: scaffold Go project`.
- Antes de iniciar uma tarefa, o agent deve verificar se há mudanças locais não commitadas. Se existirem mudanças que não pertencem à tarefa atual, ele deve preservá-las e evitar sobrescrever trabalho alheio.
- Agents podem fazer commits locais automaticamente quando isso fizer parte do pedido. Push para remoto e abertura de PR só devem acontecer com instrução explícita.

## Decisões Fechadas

- O novo projeto deve ficar em `./oem-ingest-new`.
- O idioma da documentação do novo projeto é português.
- O novo projeto deve ser escrito em Go, usando a versão estável atual no momento da implementação.
- O formato oficial de configuração é o formato simplificado:
  - `configTargets.yaml`
  - `configMetrics.yaml`
- A aplicação não deve gerar configuração a partir de roots como o legado fazia; ela apenas consome arquivos já gerados.
- A configuração de endpoints OEM vem de `configTargets.yaml`; não usar `EM_BASE_URL` nem `PROTOCOL` como contrato da nova aplicação.
- Não manter `USE_TARGET_CONFIG` nem `USE_TARGET_CACHE`.
- Cache permitido apenas em memória.
- A validação opcional de configuração na inicialização deve:
  - consultar a API do OEM;
  - logar warnings para divergências;
  - corrigir os dados em memória;
  - gerar um novo arquivo de configuração corrigido;
  - preservar o arquivo original.
- A validação deve verificar IDs de targets e correlação conforme as regras de `old_docs/2-configuracao_targets.md`.
- Targets avulsos são aceitos. Para targets avulsos dos tipos `rac_database` e `oracle_pdb`, se a validação estiver ativa e targets relacionados existirem na API, a configuração corrigida deve adicionar os componentes do cluster.
- A hierarquia esperada é `oracle_dbsys -> rac_database -> oracle_pdb -> oracle_database -> host/oracle_listener`.
- O nome compatível da métrica customizada de status deve continuar sendo `oem_monitor_stus`, mesmo sendo um nome legado com erro.
- Nomes de métricas exportadas devem ser lowercase, como no exportador legado.
- A mudança de exportação é obrigatória: exportar apenas métricas coletadas desde o último envio bem-sucedido.
- Se o POST OTLP falhar, manter o buffer e tentar reenviar no próximo ciclo.
- Logs textuais devem manter a lógica legada: enviar quando o valor mudar, exceto métricas marcadas como contínuas.
- Incidentes devem ser exportados como logs OTLP, com polling a cada 5 minutos e janela de 1 hora.
- A correção de timestamp de incidentes subtraindo 3 horas deve permanecer. Documentar no código e na documentação que é uma compatibilidade/workaround do ambiente.
- Autenticação contra OEM continua sendo Basic Auth.
- A funcionalidade de token legado deve ser mantida:
  - `old_code/oem/tools/xisou.py` calcula SHA256 de um arquivo fonte;
  - decodifica o token com base64 URL-safe;
  - aplica XOR com o hash para recuperar a senha.
- Métricas e logs OTLP devem usar a mesma URL base, com `/v1/metrics` e `/v1/logs`.
- `service.name` deve continuar `oemAPIService`.
- Métricas internas da aplicação devem usar prefixo `oem_collector_`.
- Métricas internas mínimas do primeiro release:
  - targets configurados;
  - targets ativos;
  - targets sem coleta;
  - requests OEM;
  - requests OEM com erro;
  - datapoints coletados;
  - datapoints exportados;
  - falhas de exportação;
  - tamanho do payload exportado.
- A tolerância para considerar target sem coleta deve ser configurável, com default de 21 minutos.
- Frequências de `configMetrics.yaml` são em minutos.
- O mock `oem_mock` pode continuar Python/FastAPI e pode ser adaptado para testes.
- O Docker Compose deve subir apenas a aplicação e o mock.
- CI deve ser planejado e implementado no projeto.

## Pontos de Atenção

- O token legado depende do hash de um arquivo. Em Go compilado, o conceito de "arquivo fonte do script" não é idêntico ao Python. Implementar isso como uma opção documentada, por exemplo `OEM_AUTH_TOKEN_HASH_FILE`, com fallback explícito e teste cobrindo o algoritmo. Se for necessário preservar tokens já gerados para o Python, o arquivo usado no hash precisa ser o mesmo do token original.
- A documentação antiga chama a métrica de status de `oem_monitor_status`, mas a decisão final é manter `oem_monitor_stus`.
- O legado usa `oem_monitor_response` com tolerância fixa de 21 minutos; a nova aplicação deve tornar isso configurável.
- O legado reenvia o repositório inteiro de gauges a cada exportação. A nova aplicação deve usar buffer incremental e limpar apenas após sucesso.
- A classificação número versus texto deve ser validada com dados reais/mock. Sempre que possível, usar os metadados de `metricGroups/{group}` (`dataType`) para evitar tratar números como logs por causa de representação textual na API.

## Arquitetura Alvo

Estrutura sugerida:

```text
oem-ingest-new/
  cmd/oem-ingest/
    main.go
  internal/auth/
  internal/config/
  internal/oem/
  internal/validate/
  internal/collect/
  internal/transform/
  internal/exporter/
  internal/incidents/
  internal/selfmetrics/
  internal/scheduler/
  internal/logging/
  docs/
    arquitetura.md
    configuracao.md
    operacao.md
    compatibilidade_legado.md
  configs/
    configTargets.example.yaml
    configMetrics.example.yaml
  Dockerfile
  docker-compose.yml
  go.mod
  go.sum
  README.md
```

Componentes:

- `auth`: Basic Auth, senha direta e token legado XOR/base64/hash.
- `config`: leitura, validação sintática e defaults de configuração.
- `oem`: cliente HTTP OEM com timeout, retries, paginação e endpoints tipados.
- `validate`: validação opcional de IDs e correlação de targets.
- `scheduler`: agendamento de coletas por target/grupo com jitter, limite de concorrência e shutdown limpo.
- `collect`: execução das chamadas `latestData` e obtenção/cache de keys dos grupos.
- `transform`: normalização de nomes, atributos, métricas numéricas, logs textuais e métricas customizadas.
- `exporter`: exportação OTLP HTTP/protobuf incremental para métricas e logs.
- `incidents`: polling e exportação de incidentes como logs.
- `selfmetrics`: métricas internas `oem_collector_*`.
- `logging`: logs estruturados para operação da aplicação.

## Contratos de Configuração

### `configTargets.yaml`

Formato oficial:

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
```

Requisitos:

- `endpoint` é obrigatório por site.
- `targets` é obrigatório por site.
- `id`, `name`, `typeName` e `tags` são obrigatórios por target.
- `tags.target_name` e `tags.target_type` devem existir e refletir a normalização esperada.
- Tags externas como `sistema` e `torre` devem ser preservadas.

### `configMetrics.yaml`

Formato oficial:

```yaml
host:
  - freq: 5
    metric_group_name: Load
oracle_database:
  - freq: 2
    metric_group_name: Response
```

Requisitos:

- `freq` é em minutos.
- Coleta sempre solicita todas as métricas do grupo.
- Grupos indisponíveis em um target específico devem ser descartados/logados sem derrubar a aplicação inteira.

## Variáveis de Ambiente Propostas

Manter pequeno e documentado:

- `OEM_CONFIG_TARGETS`: caminho do `configTargets.yaml`. Default: `./configs/configTargets.yaml`.
- `OEM_CONFIG_METRICS`: caminho do `configMetrics.yaml`. Default: `./configs/configMetrics.yaml`.
- `OEM_VALIDATE_CONFIG`: `true` ou `false`. Default: `false`.
- `OEM_VALIDATED_CONFIG_OUTPUT`: caminho para o arquivo corrigido. Default: `./configs/configTargets.validated.yaml`.
- `OEM_USER`: usuário Basic Auth.
- `OEM_PASSWORD`: senha Basic Auth direta.
- `OEM_TOKEN`: token legado que decodifica a senha.
- `OEM_AUTH_TOKEN_HASH_FILE`: arquivo usado para calcular o hash do token legado.
- `OTEL_EXPORT_URL`: URL base do collector, sem `/v1/metrics` ou `/v1/logs`.
- `OEM_EXPORT_INTERVAL_SECONDS`: intervalo de exportação. Default sugerido: `60`.
- `OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES`: default `21`.
- `OEM_HTTP_TIMEOUT_SECONDS`: timeout de leitura OEM.
- `OEM_HTTP_CONNECT_TIMEOUT_SECONDS`: timeout de conexão OEM.
- `OEM_HTTP_MAX_RETRIES`: retries para GET OEM.
- `OEM_MAX_CONCURRENT_REQUESTS`: limite global de concorrência de chamadas OEM.
- `OEM_LOG_LEVEL`: default `info`.

## Backlog de Implementação

### Fase 0 - Fundação do Projeto

#### Tarefa 0.1 - Criar scaffold Go

Status: Concluída

Dependências: nenhuma.

Escopo:

- Criar `./oem-ingest-new`.
- Inicializar `go.mod`.
- Criar `cmd/oem-ingest/main.go`.
- Criar estrutura `internal/*` conforme arquitetura alvo.
- Adicionar README inicial com escopo e comando básico.

Critérios de aceite:

- `go test ./...` roda sem falhas.
- `go run ./cmd/oem-ingest --help` ou comando equivalente executa sem iniciar coleta real.
- Estrutura não mistura código novo com `old_code`.

#### Tarefa 0.2 - Configuração e defaults

Status: Concluída

Dependências: 0.1.

Escopo:

- Implementar leitura de variáveis de ambiente.
- Implementar loader YAML para `configTargets.yaml` e `configMetrics.yaml`.
- Validar campos obrigatórios.
- Adicionar exemplos em `configs/`.

Critérios de aceite:

- Testes unitários cobrem arquivo válido, arquivo ausente, site sem endpoint, target sem campos obrigatórios e métrica sem `freq`.
- Erros de configuração são claros e acionáveis.

#### Tarefa 0.3 - Autenticação Basic Auth e token legado

Status: Concluída

Dependências: 0.1.

Escopo:

- Implementar Basic Auth com `OEM_USER` + `OEM_PASSWORD`.
- Implementar decodificação `OEM_TOKEN` compatível com `old_code/oem/tools/xisou.py`.
- Documentar a limitação do hash de arquivo no Go.

Critérios de aceite:

- Teste unitário comprova roundtrip do algoritmo XOR/base64/hash.
- Se `OEM_PASSWORD` e `OEM_TOKEN` existirem, documentar e testar prioridade.
- Nenhum log imprime senha ou token.

### Fase 1 - Cliente OEM

#### Tarefa 1.1 - Cliente HTTP OEM

Status: Concluída

Dependências: 0.2, 0.3.

Escopo:

- Implementar cliente com Basic Auth, TLS insecure configurável se necessário, timeout, retry para GET e pool de conexões.
- Implementar endpoints:
  - `GET /em/api`
  - `GET /em/api/targets`
  - `GET /em/api/targets/{targetId}/properties`
  - `GET /em/api/targets/{targetId}/metricGroups`
  - `GET /em/api/targets/{targetId}/metricGroups/{groupName}`
  - `GET /em/api/targets/{targetId}/metricGroups/{groupName}/latestData?limit=200`
  - `GET /em/api/incidents/?ageInHoursLessThanOrEqualTo=1`
  - `GET /em/api/incidents/{id}`
- Implementar paginação por `links.next`.

Critérios de aceite:

- Testes com `httptest.Server` cobrem sucesso, 401, 404, retry e paginação.
- Contadores internos de requests e erros OEM são atualizados.

#### Tarefa 1.2 - Adaptar mock para testes locais

Status: Concluída

Dependências: 1.1.

Escopo:

- Manter `oem_mock` em Python/FastAPI.
- Corrigir/adaptar o mock apenas no necessário para testes do novo projeto.
- Adicionar endpoints fake `/v1/metrics` e `/v1/logs` que aceitam payload protobuf/binário e retornam 200, para uso no Docker Compose sem subir collector real.
- Documentar que esses endpoints são apenas para desenvolvimento local.

Critérios de aceite:

- `docker compose up` futuro consegue iniciar aplicação e mock sem collector externo.
- O mock continua respondendo aos endpoints OEM existentes.

### Fase 2 - Validação de Configuração

#### Tarefa 2.1 - Validação de IDs

Status: Concluída

Dependências: 1.1.

Escopo:

- Quando `OEM_VALIDATE_CONFIG=true`, buscar lista de targets da API por site.
- Para cada target configurado, localizar target atual por `name` + `typeName`.
- Se o ID divergir, logar warning, corrigir em memória e marcar para arquivo corrigido.
- Se target não existir na API, logar warning e manter target original, salvo decisão futura diferente.

Critérios de aceite:

- Testes cobrem ID correto, ID divergente, target ausente e target duplicado.
- Arquivo original nunca é sobrescrito.

#### Tarefa 2.2 - Validação de correlação e inclusão de relacionados

Status: Pendente

Dependências: 2.1.

Escopo:

- Implementar regras de correlação de `old_docs/2-configuracao_targets.md` e `old_code/oem/tools/oemapping.py`.
- Para `rac_database` e `oracle_pdb`, verificar componentes relacionados:
  - `oracle_dbsys`;
  - `rac_database` primário/standby quando inferível;
  - `oracle_pdb`;
  - `oracle_database`;
  - `host`;
  - `oracle_listener`.
- Usar propriedades de `oracle_database`, especialmente `MachineName` e `DataGuardStatus`, para validar host/listener e `dg_role`.
- Adicionar targets relacionados ausentes no arquivo corrigido quando existirem na API.
- Preservar targets avulsos sem forçar correlação completa, exceto para expansão de `rac_database` e `oracle_pdb`.

Critérios de aceite:

- Testes cobrem RAC com instância faltando, host errado, listener errado, PDB com standby e target avulso que deve ser preservado.
- Tags geradas/corrigidas seguem `old_code/oem/tools/processMapping.py`.

#### Tarefa 2.3 - Escrita de configuração corrigida

Status: Pendente

Dependências: 2.1, 2.2.

Escopo:

- Escrever `OEM_VALIDATED_CONFIG_OUTPUT` quando validação estiver ativa.
- Preservar tags externas.
- Manter formato oficial simplificado.
- Registrar resumo de alterações no log: IDs corrigidos, targets adicionados, warnings.

Critérios de aceite:

- Teste compara YAML corrigido com fixture esperada.
- Arquivo original permanece inalterado.

### Fase 3 - Coleta e Transformação

#### Tarefa 3.1 - Scheduler de coletas

Status: Pendente

Dependências: 0.2, 1.1.

Escopo:

- Criar jobs por site + target + grupo de métrica.
- Respeitar `freq` em minutos.
- Adicionar jitter para evitar rajadas.
- Garantir no máximo uma execução simultânea do mesmo job.
- Implementar shutdown limpo por contexto/sinal.

Critérios de aceite:

- Testes com clock fake ou intervalos controlados validam agendamento básico, jitter e não sobreposição.
- Logs indicam jobs registrados e falhas por target/grupo.

#### Tarefa 3.2 - Cache em memória de keys de metric group

Status: Pendente

Dependências: 1.1.

Escopo:

- Consultar `GET /metricGroups/{group}` antes da primeira coleta do par target/grupo.
- Guardar keys em memória por `targetId + metricGroupName`.
- Reusar keys nas coletas seguintes.
- Para métricas bodyless/custom, permitir keys vazias quando necessário.

Critérios de aceite:

- Teste garante uma chamada de metadata para múltiplas coletas do mesmo grupo.
- 404 em metadata gera warning e impede job específico sem derrubar o processo.

#### Tarefa 3.3 - Coleta latestData

Status: Pendente

Dependências: 3.1, 3.2.

Escopo:

- Coletar `latestData` por target/grupo.
- Aplicar paginação.
- Atualizar monitoramento de resposta do target quando houver coleta útil.
- Tratar 404 como grupo indisponível para aquele target.
- Tratar erros transitórios com log e métricas internas.

Critérios de aceite:

- Testes cobrem payload com items, payload vazio, paginação, 404 e erro 500.

#### Tarefa 3.4 - Normalização de atributos

Status: Pendente

Dependências: 3.2, 3.3.

Escopo:

- Unir tags do target com keys do item.
- Manter compatibilidade dos conflitos do legado:
  - `instance` vira `_instance`;
  - `service_name` vira `name`;
  - `name` vira `name_`;
  - `Username_machine` gera `user` e `pod`.
- Preservar tags externas.

Critérios de aceite:

- Testes reproduzem os casos de `build_tags` e `_buildAttributes` do legado.

#### Tarefa 3.5 - Normalização de métricas numéricas e logs textuais

Status: Pendente

Dependências: 3.2, 3.3, 3.4.

Escopo:

- Nome padrão: `oem_<metric_group_name>_<metric_name>`.
- Substituir espaços por `_`.
- Exportar lowercase.
- Campos que são keys não viram métricas.
- Métricas numéricas viram gauges OTLP.
- Valores textuais viram logs OTLP.
- Usar metadados de metric group para decidir tipo quando disponível; validar compatibilidade com dados do mock e legado.

Critérios de aceite:

- Testes cobrem nome, lowercase, keys, número, texto e valores numéricos representados como string.
- Saída com fixture do mock é compatível com a intenção do legado.

### Fase 4 - Métricas Customizadas e Estado de Coleta

#### Tarefa 4.1 - `oem_monitor_response`

Status: Pendente

Dependências: 3.3, 3.5.

Escopo:

- Criar gauge `oem_monitor_response`.
- Valor 1 quando a última coleta bem-sucedida do target estiver dentro da tolerância configurável.
- Valor 0 quando estiver fora da tolerância ou nunca tiver coletado.
- Default da tolerância: 21 minutos.

Critérios de aceite:

- Testes cobrem target nunca coletado, dentro da janela e fora da janela.

#### Tarefa 4.2 - `oem_monitor_stus`

Status: Pendente

Dependências: 4.1.

Escopo:

- Implementar métrica customizada com nome legado `oem_monitor_stus`.
- Regras:
  - `rac_database`: usar `Availability`; se retorna dados, status 0; se vazio, status 2 quando `oem_monitor_response=1`, senão 1.
  - `oracle_database`: usar `Response`; se vazio, usar monitor response; se retorna, usar `Status` ou `DatabaseStatus`.
  - `oracle_pdb`: usar `Response`; se vazio, status 1; se retorna, `Status == 0` ou `State != OPEN` indicam 0, caso contrário 2.
  - `host`: usar `Response`; se vazio, usar monitor response; se retorna, `Status == 0` indica 0, caso contrário 2.
- Manter códigos do legado: 0 down/inativo, 1 sem coleta, 2 up/coletando.

Critérios de aceite:

- Testes unitários cobrem cada tipo e cada branch documentado.
- Nome exportado é exatamente `oem_monitor_stus`.

#### Tarefa 4.3 - `oem_service_status` e `oem_str_service_status`

Status: Pendente

Dependências: 3.5.

Escopo:

- Para `rac_database`, usar `service_performance`.
- Para `oracle_pdb`, usar `DBService`.
- `DBTime_delta > 0` indica ativo.
- `status == "Up"` indica ativo.
- Exportar:
  - `oem_service_status` numérica;
  - `oem_str_service_status` textual.
- Marcar a textual como contínua onde o legado fazia isso.

Critérios de aceite:

- Testes cobrem `DBTime_delta`, `status`, valor ativo/inativo e comportamento contínuo.

#### Tarefa 4.4 - Métricas internas `oem_collector_*`

Status: Pendente

Dependências: 3.5, 4.1.

Escopo:

- Implementar as métricas internas mínimas:
  - `oem_collector_targets_configured`;
  - `oem_collector_targets_active`;
  - `oem_collector_targets_inactive`;
  - `oem_collector_oem_requests_total`;
  - `oem_collector_oem_request_errors_total`;
  - `oem_collector_datapoints_collected_total`;
  - `oem_collector_datapoints_exported_total`;
  - `oem_collector_export_failures_total`;
  - `oem_collector_export_payload_bytes`.
- Definir atributos úteis e estáveis, sem cardinalidade explosiva.

Critérios de aceite:

- Testes validam incremento/atualização.
- Métricas internas não usam prefixo `oem_` sozinho.

### Fase 5 - Exportação OTLP

#### Tarefa 5.1 - Exportador OTLP de métricas incremental

Status: Pendente

Dependências: 3.5.

Escopo:

- Exportar para `${OTEL_EXPORT_URL}/v1/metrics`.
- Usar OTLP HTTP/protobuf.
- Montar `service.name=oemAPIService`.
- Usar gauges para métricas numéricas.
- Manter buffer de datapoints coletados desde o último sucesso.
- Limpar buffer apenas após POST 2xx.
- Em falha, manter buffer para retry no próximo ciclo.
- Exportar names lowercase.

Critérios de aceite:

- Testes com server fake cobrem sucesso, falha e retry.
- Após sucesso, segundo ciclo sem novas coletas não envia payload de métricas OEM antigas.

#### Tarefa 5.2 - Exportador OTLP de logs

Status: Pendente

Dependências: 3.5.

Escopo:

- Exportar para `${OTEL_EXPORT_URL}/v1/logs`.
- Logs de métricas textuais usam atributos normalizados e body com valor textual.
- Manter estado de último valor por série textual.
- Enviar novamente quando valor mudar.
- Enviar sempre quando a métrica textual for marcada como contínua.
- Em falha, reter logs pendentes para retry.

Critérios de aceite:

- Testes cobrem valor igual, valor alterado, contínua, falha e retry.

#### Tarefa 5.3 - Perfil e observabilidade do exportador

Status: Pendente

Dependências: 5.1, 5.2, 4.4.

Escopo:

- Registrar tamanho do payload, contagem de datapoints/logs e duração do export.
- Alimentar métricas internas de exportação.
- Evitar logs verbosos por datapoint em operação normal.

Critérios de aceite:

- Testes ou integração validam contadores de exportação.
- Logs são úteis sem expor dados sensíveis.

### Fase 6 - Incidentes

#### Tarefa 6.1 - Polling de incidentes

Status: Pendente

Dependências: 1.1, 5.2.

Escopo:

- Polling a cada 5 minutos.
- Buscar incidentes com janela de 1 hora.
- Evitar duplicidade por `id` em memória.
- Para cada incidente novo, exportar log com `message` no body e demais campos como atributos.
- Aplicar correção de timestamp subtraindo 3 horas.
- Documentar no código que essa correção é compatibilidade com o ambiente legado.

Critérios de aceite:

- Testes cobrem incidente novo, duplicado e correção de timestamp.

#### Tarefa 6.2 - Monitoramento de fechamento de incidentes

Status: Pendente

Dependências: 6.1.

Escopo:

- Reproduzir comportamento legado: agendar verificação periódica de incidente conhecido.
- Quando endpoint retorna erro ou `status == Closed`, remover da lista em memória.
- Evitar vazamento de jobs ou goroutines.

Critérios de aceite:

- Testes cobrem incidente aberto, fechado e erro de API.

### Fase 7 - Integração, Docker e CI

#### Tarefa 7.1 - Dockerfile

Status: Pendente

Dependências: 5.1, 5.2, 6.1.

Escopo:

- Criar Dockerfile multi-stage para build Go e imagem runtime mínima.
- Copiar exemplos de config ou documentar volume.
- Garantir que `OEM_AUTH_TOKEN_HASH_FILE`, se usado, tenha um caminho viável dentro do container.

Critérios de aceite:

- `docker build` conclui.
- Container inicia com `--help` ou modo validação sem coletar.

#### Tarefa 7.2 - Docker Compose com app e mock

Status: Pendente

Dependências: 1.2, 7.1.

Escopo:

- Criar `docker-compose.yml` com:
  - app Go;
  - `oem_mock` Python/FastAPI.
- Configurar app para usar endpoint do mock via `configTargets.yaml`.
- Configurar `OTEL_EXPORT_URL` apontando para os endpoints fake do mock, ou outro arranjo equivalente sem subir collector real.

Critérios de aceite:

- `docker compose up` sobe os dois serviços.
- App consegue autenticar/testar conexão, coletar do mock e fazer POST OTLP fake.

#### Tarefa 7.3 - Teste de integração com mock

Status: Pendente

Dependências: 7.2.

Escopo:

- Criar teste ou script de integração que:
  - sobe mock ou usa `httptest`;
  - carrega configs exemplo;
  - executa pelo menos um ciclo de coleta;
  - valida chamadas a `/v1/metrics` e `/v1/logs`.

Critérios de aceite:

- Teste roda localmente com comando documentado.
- O teste não depende de OEM real.

#### Tarefa 7.4 - CI

Status: Pendente

Dependências: 7.3.

Escopo:

- Adicionar workflow de CI planejado para:
  - `go test ./...`;
  - `go vet ./...`;
  - build Docker;
  - teste de integração quando viável.
- Manter o workflow versionado no repositório.

Critérios de aceite:

- Workflow ou script equivalente documentado.
- Comandos locais passam.

### Fase 8 - Documentação

#### Tarefa 8.1 - Documentação de arquitetura

Status: Pendente

Dependências: 0.1.

Escopo:

- Criar `docs/arquitetura.md`.
- Explicar componentes, fluxo de coleta, buffer incremental, exportação OTLP e diferenças para o legado.

Critérios de aceite:

- Documento descreve o caminho completo: config -> validação -> coleta -> transformação -> export.

#### Tarefa 8.2 - Documentação de configuração

Status: Pendente

Dependências: 0.2, 2.3.

Escopo:

- Criar `docs/configuracao.md`.
- Documentar `configTargets.yaml`, `configMetrics.yaml`, variáveis de ambiente, token legado e validação opcional.

Critérios de aceite:

- Um usuário consegue montar configs usando apenas a documentação e os exemplos.

#### Tarefa 8.3 - Documentação operacional

Status: Pendente

Dependências: 7.2.

Escopo:

- Criar `docs/operacao.md`.
- Documentar execução local, Docker, Docker Compose, logs, troubleshooting e métricas internas.

Critérios de aceite:

- Inclui comandos reais e comportamento esperado.

#### Tarefa 8.4 - Compatibilidade com legado

Status: Pendente

Dependências: 5.2, 6.1.

Escopo:

- Criar `docs/compatibilidade_legado.md`.
- Documentar nomes de métricas, lowercase, atributos, logs textuais, incidentes, `oem_monitor_stus` e mudança de não reenviar tudo a cada export.

Critérios de aceite:

- Lista explicitamente o que foi mantido e o que mudou.

### Fase 9 - Endurecimento Final

#### Tarefa 9.1 - Comparação com legado usando mock

Status: Pendente

Dependências: 7.3, 8.4.

Escopo:

- Rodar cenário com `oem_mock`.
- Comparar nomes de métricas, atributos principais e logs com o comportamento esperado do legado.
- Registrar divergências intencionais.

Critérios de aceite:

- Relatório curto em `docs/compatibilidade_legado.md` ou seção equivalente.
- Divergências não intencionais viram tarefas novas.

#### Tarefa 9.2 - Revisão de concorrência e shutdown

Status: Pendente

Dependências: 7.3.

Escopo:

- Revisar goroutines, locks, buffers e context cancellation.
- Garantir que falhas de API/export não derrubam o processo inteiro sem necessidade.
- Garantir que o shutdown tenta flush final com timeout.

Critérios de aceite:

- Testes ou revisão documentada cobrem shutdown e retry.

#### Tarefa 9.3 - Release candidate

Status: Pendente

Dependências: 9.1, 9.2, 8.1, 8.2, 8.3, 8.4.

Escopo:

- Rodar verificações finais.
- Atualizar README.
- Confirmar Dockerfile, Compose, docs e exemplos.
- Marcar pendências conhecidas.

Critérios de aceite:

- `go test ./...` passa.
- Build Docker passa.
- Compose local com mock passa.
- Documentação cobre instalação, configuração e operação.

## Ordem Recomendada

1. Fase 0.
2. Fase 1.
3. Fase 2 em paralelo com Fase 3 se houver agents separados.
4. Fase 4.
5. Fase 5.
6. Fase 6.
7. Fase 7.
8. Fase 8 pode começar cedo, mas deve ser revisada após Fase 7.
9. Fase 9.

## Definição de Pronto Global

O projeto estará pronto quando:

- O código novo estiver em `./oem-ingest-new`.
- A aplicação consumir `configTargets.yaml` e `configMetrics.yaml`.
- A validação opcional corrigir IDs/correlação em memória e gerar YAML corrigido sem sobrescrever o original.
- A coleta usar API OEM e respeitar frequências em minutos.
- A exportação OTLP for compatível com o legado, exceto pela mudança desejada de buffer incremental.
- Métricas numéricas, logs textuais e incidentes forem exportados.
- Métricas customizadas legadas forem implementadas, incluindo `oem_monitor_stus`.
- Métricas internas `oem_collector_*` existirem.
- Dockerfile, Docker Compose e documentação em português existirem.
- Testes unitários e integração com mock cobrirem os fluxos principais.
- CI estiver configurado no repositório.

## Registro de Progresso

Use este formato ao final de cada sessão:

```text
- Data: YYYY-MM-DD
  Agent: <identificação se houver>
  Tarefa: <id e título>
  Status: concluída | parcial | bloqueada
  Verificações: <comandos rodados e resultado>
  Notas: <decisões, pendências ou arquivos principais>
```

Entradas:

- Data: 2026-06-14
  Agent: Codex
  Tarefa: planejamento inicial
  Status: concluída
  Verificações: leitura de `project.prd`, `respostas.txt`, docs antigos e trechos centrais do código legado
  Notas: plano criado para orientar execução em múltiplas sessões.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: inicialização Git
  Status: concluída
  Verificações: `git init`, criação de `.gitignore`, atualização do fluxo Git no plano
  Notas: repositório local inicializado para permitir branches, diffs e commits por tarefa.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: 0.1 - Criar scaffold Go
  Status: concluída
  Verificações: `GOCACHE=/tmp/oem-go-build-cache go test ./...`, `GOCACHE=/tmp/oem-go-build-cache go vet ./...`, `GOCACHE=/tmp/oem-go-build-cache go run ./cmd/oem-ingest --help`
  Notas: scaffold Go criado em `oem-ingest-new`; branch `task/0.1-scaffold-go` criada e commit `769e254` registrado.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: revisão técnica da tarefa 0.1 - Criar scaffold Go
  Status: concluída
  Verificações: `GOCACHE=/tmp/oem-go-build-cache go test ./...`, `GOCACHE=/tmp/oem-go-build-cache go vet ./...`, `GOCACHE=/tmp/oem-go-build-cache go run ./cmd/oem-ingest --help`, `GOCACHE=/tmp/oem-go-build-cache go run ./cmd/oem-ingest`
  Notas: workspace estava limpo antes da revisão; não foram encontradas regressões de compatibilidade com o legado no scaffold; adicionada cobertura para flag inválida e corrigida nota de progresso que ainda indicava bloqueio de commit.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: 0.2 - Configuração e defaults
  Status: concluída
  Verificações: `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: loader YAML e leitura de variáveis de ambiente implementados em `internal/config`; exemplos adicionados em `configs/`.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: revisão técnica da tarefa 0.2 - Configuração e defaults
  Status: concluída
  Verificações: `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: workspace estava limpo antes da revisão; corrigida validação de `tags.target_type` e `tags.target_name` com normalização compatível com o legado para `host` e `oracle_listener`; adicionada cobertura para exemplos versionados e tags inconsistentes.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: 0.3 - Autenticação Basic Auth e token legado
  Status: concluída
  Verificações: `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: pacote `internal/auth` implementa Basic Auth, prioridade de `OEM_PASSWORD` sobre `OEM_TOKEN` e decodificação XOR/base64/hash compatível com `old_code/oem/tools/xisou.py`; README documenta a limitação do hash de arquivo em Go.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: revisão técnica da tarefa 0.3 - Autenticação Basic Auth e token legado
  Status: concluída
  Verificações: `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: workspace estava limpo antes da revisão; substituído hash de arquivo por leitura em streaming compatível com o legado e adicionada fixture estática do algoritmo Python para evitar cobertura apenas por roundtrip interno.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: 1.1 - Cliente HTTP OEM
  Status: concluída
  Verificações: `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: cliente OEM implementado em `internal/oem` com Basic Auth, timeouts, pool HTTP, TLS insecure configurável, retry de GET, paginação por `links.next` e contadores de requests/erros.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: revisão técnica da tarefa 1.1 - Cliente HTTP OEM
  Status: concluída
  Verificações: `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: workspace estava limpo antes da revisão; corrigida preservação de paths escapados para IDs/grupos OEM com caracteres especiais e paginação com `links.next` apenas em query string; incidentes agora preservam campos extras para compatibilidade futura com logs; README atualizado para refletir o cliente OEM implementado.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: 1.2 - Adaptar mock para testes locais
  Status: concluída
  Verificações: `oem_mock/.venv/bin/python -m unittest discover -s oem_mock`, `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: mock FastAPI agora responde `/em/api`, carrega fixtures por caminho absoluto do modulo e aceita payloads binarios em `/v1/metrics` e `/v1/logs`; adicionada documentacao local do mock.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: revisão técnica da tarefa 1.2 - Adaptar mock para testes locais
  Status: concluída
  Verificações: `oem_mock/.venv/bin/python -m unittest discover -s oem_mock`, `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: workspace estava limpo antes da revisão; não foram encontradas regressões objetivas no mock; adicionada cobertura para `properties`, `latestData?limit=200`, detalhe de incidente e 404 de target ausente.
- Data: 2026-06-14
  Agent: Codex
  Tarefa: 2.1 - Validação de IDs
  Status: concluída
  Verificações: `go test ./internal/validate`, `go test ./...`, `go vet ./...`, `go run ./cmd/oem-ingest --help`
  Notas: validação opcional de IDs implementada em `internal/validate`, com correção em memória por `name` + `typeName`, warnings para divergência/ausência/duplicidade e preservação da configuração original.
