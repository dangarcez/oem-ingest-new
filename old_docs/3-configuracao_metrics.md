# Configuração de Métricas

Além da definição dos targets, é necessário configurar **quais métricas serão coletadas** para cada tipo de target, bem como a **frequência** com que essa coleta será realizada.

As métricas no Oracle Enterprise Manager são organizadas em **grupos de métricas** (*metric groups*). Por meio dos endpoints da API do OEM, é possível:
- Buscar todas as métricas pertencentes a um grupo
- Consultar métricas individuais de forma específica

Para fins de simplicidade e padronização, o processo atual de ingestão utiliza sempre a **coleta de todas as métricas de um grupo**. Vale ressaltar, entretanto, que a coleta de métricas individuais é uma alternativa a ser avaliada futuramente, especialmente em grupos que possuem um grande volume de métricas, quando apenas um subconjunto delas é necessário.

---

## Templates e Disponibilidade de Métricas

Os targets geralmente compartilham **templates de métricas comuns** para cada tipo de target. Ainda assim, podem existir diferenças:

- Na disponibilidade de métricas entre targets do mesmo tipo
- Na frequência com que essas métricas são coletadas pelo OMS

A padronização desses templates e métricas deve ser tratada em conjunto com a equipe responsável pelo Oracle Enterprise Manager, com o objetivo de manter **consistência na coleta** e previsibilidade dos dados obtidos.

---

## Estratégia de Coleta

Dado esse cenário, foi definida uma estratégia de configuração baseada em **grupos de métricas comuns** à maioria dos targets.

Durante o processo de ingestão, o script:
- Solicita os dados de todos os grupos configurados para o tipo de target
- Descarta automaticamente métricas que não estejam disponíveis para um target específico

Essa abordagem simplifica a configuração e reduz a necessidade de tratamento manual por target.

---

## Descoberta de Grupos de Métricas

Para verificar os grupos de métricas registrados para um target específico, pode ser utilizado o endpoint:

```
GET http(s)://EM_HOST/targets/{targetId}/metricGroups
```

Atualmente, os **intervalos de coleta configurados para cada grupo** ainda devem ser verificados diretamente no console do Oracle Enterprise Manager.

---

## Estrutura da Configuração de Métricas

Após a definição dos grupos de métricas e das frequências de coleta, a configuração utilizada pelo script de ingestão segue um padrão semelhante ao exemplo abaixo:

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
  - freq: 15
    metric_group_name: topWaitEvents
  - freq: 10
    metric_group_name: Database_Resource_Usage
  - freq: 15
    metric_group_name: UserAudit
  - freq: 15
    metric_group_name: wait_sess_cls
  - freq: 5
    metric_group_name: alertLogStatus
  - freq: 10
    metric_group_name: wait_bottlenecks
  - freq: 10
    metric_group_name: instance_efficiency
  - freq: 5
    metric_group_name: adrAlertLogIncidentError

oracle_listener:
  - freq: 5
    metric_group_name: Load
  - freq: 5
    metric_group_name: General Status

oracle_pdb:
  - freq: 1440
    metric_group_name: DATABASE_SIZE
  - freq: 1440
    metric_group_name: tbspAllocation
  - freq: 15
    metric_group_name: DBService

rac_database:
  - freq: 1440
    metric_group_name: DATABASE_SIZE
  - freq: 15
    metric_group_name: service_performance
  - freq: 1440
    metric_group_name: tbspAllocation
  - freq: 10
    metric_group_name: UserLocks
  - freq: 5
    metric_group_name: UserBlock
```

Nessa estrutura:
- `freq` representa a frequência de coleta, em minutos
- `metric_group_name` identifica o grupo de métricas conforme definido no Oracle Enterprise Manager

Essa configuração permite ao script de ingestão controlar de forma centralizada **quais métricas são coletadas** e **com que periodicidade**, mantendo flexibilidade para ajustes futuros.