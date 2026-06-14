## Oracle Enterprise Manager (OEM)

O **Oracle Enterprise Manager (OEM)** é a plataforma central de monitoramento e gerenciamento da Oracle para bancos de dados, clusters, hosts, middleware e outros componentes de infraestrutura. Além da interface gráfica, o OEM disponibiliza uma **API REST oficial** que permite consultar informações operacionais de forma programática.

Esta documentação tem como foco o uso da **API REST do OEM para coleta de métricas de targets**.

---

## Visão Geral da API REST do OEM

A API REST do Oracle Enterprise Manager permite:

- Autenticação via sessão HTTP
- Consulta de *targets* (bancos, RAC, hosts, etc.)
- Coleta de métricas associadas a esses targets
- Acesso a dados históricos e agregados
- Integração com ferramentas externas (Prometheus, OpenTelemetry, Azure Monitor, etc.)

A comunicação é feita via **HTTP/HTTPS**, com respostas em **JSON**, seguindo um padrão relativamente consistente entre endpoints.

---

## Conceitos Fundamentais

Antes de consumir métricas, é importante entender alguns conceitos básicos usados pelo OEM.

### Target

Um **target** representa qualquer entidade monitorada pelo OEM, como:

- Banco de dados Oracle (Single Instance ou RAC)
- Instâncias ASM
- Hosts (Linux, Windows)
- Listeners
- Clusters
- Middleware (WebLogic, etc.)

Cada target é identificado principalmente por:
- `target_name`
- `target_type`

Esses dois campos são fundamentais para qualquer chamada relacionada a métricas.

---

### Métrica

Uma **métrica** no OEM representa uma medição coletada periodicamente por um agent, como por exemplo:

- Utilização de CPU
- Número de sessões ativas
- Tempo de espera por classe (wait class)
- Uso de tablespace
- I/O por segundo
- Estado do target

As métricas possuem normalmente:
- Nome da métrica (*metric name*)
- Grupo da métrica (*metric group*)
- Uma ou mais colunas (valores numéricos ou textuais)
- Timestamp da coleta

Algumas métricas são simples (um único valor), enquanto outras são **multidimensionais**, retornando múltiplas linhas ou colunas por target.

---

## Coleta de Métricas via API

A API do OEM permite consultar métricas de forma direta, sem necessidade de scraping ou acesso ao banco de dados do repositório.

O fluxo geral de coleta é:

1. Autenticar-se na API e criar uma sessão HTTP
2. Identificar o target desejado (`target_name` e `target_type`)
3. Consultar os endpoints de métricas
4. Processar e normalizar os dados retornados
5. Exportar ou armazenar as métricas em sistemas externos

A API permite tanto a coleta **pontual** (último valor disponível) quanto a coleta **histórica**, dependendo do endpoint utilizado.

---

## Tipos de Métricas Disponíveis

De forma geral, as métricas podem ser classificadas em:

- **Métricas de performance**
  - CPU, memória, I/O
  - Tempo de resposta
  - Sessões e processos

- **Métricas de capacidade**
  - Uso de tablespace
  - Crescimento de dados
  - Espaço em disco

- **Métricas de disponibilidade**
  - Status do target
  - Up/Down
  - Estados intermediários

- **Métricas específicas de banco**
  - Wait classes
  - Throughput
  - Estatísticas de SQL e instância

A disponibilidade exata das métricas varia conforme o `target_type`.

---

## Objetivo Desta Documentação

Este documento descreve como:

- Autenticar corretamente na API do Oracle Enterprise Manager
- Identificar targets monitorados
- Coletar métricas de forma confiável e eficiente
- Interpretar as respostas retornadas pela API
- Integrar os dados coletados com pipelines modernos de observabilidade

O foco será **exclusivamente na coleta de métricas**, sem abordar operações administrativas ou de configuração do OEM.