# Fluxo de Ingestão Contínua de Métricas

Com a configuração de **targets** e **métricas** concluída, esta seção descreve o processo de **ingestão contínua das métricas**.
Trata-se de uma documentação de **alto nível**, com foco no fluxo de ingestão e transformação dos dados, sem entrar em detalhes técnicos sobre as tecnologias específicas utilizadas.

---

## Fluxo Padrão

Para alguns cenários específicos, conforme descrito em outras seções da documentação, pode ser necessário aplicar tratamentos especiais aos dados. No entanto, para a maioria das métricas, é seguido um **fluxo padrão de ingestão**.

---

## Etapa de Ingestão

Antes de iniciar as chamadas aos endpoints de coleta de métricas, é necessário identificar quais são as **keys (chaves)** associadas a cada grupo de métricas.

Ao consultar um grupo de métricas, é comum que a API retorne **arrays de objetos**, onde cada objeto representa uma instância da métrica com valores distintos. Exemplo:

```json
"example": [
  {
    "mountPoint": "/",
    "available": "29004.7",
    "fileSystem": "/dev/vda1",
    "pctAvailable": "60.82",
    "size": "50267.62"
  },
  {
    "mountPoint": "/scratch",
    "available": "65508.38",
    "fileSystem": "/dev/vdb",
    "pctAvailable": "28.55",
    "size": "241776.06"
  }
]
```

Cada objeto desse array representa um **item distinto**, identificado por uma ou mais keys. Esses itens definem sobre qual entidade a métrica está associada, sempre em conjunto com o target consultado.

Para identificar corretamente quais campos representam keys e quais representam métricas, é necessário consultar previamente o endpoint:

```text
GET /targets/{targetId}/metricGroups/{metricGroupName}
```

Exemplo de resposta:

```json
{
  "id": "34E542C4F1CF2743327ED2F8563D1E4B",
  "name": "Filesystems",
  "displayName": "File Systems",
  "keys": [
    { "name": "MountPoint", "displayName": "Mount Point" },
    { "name": "FileSystem", "displayName": "Filesystem" }
  ],
  "metrics": [
    {
      "name": "SpaceUsedPct",
      "dataType": "NUMBER"
    },
    {
      "name": "pctAvailable",
      "dataType": "NUMBER"
    }
  ]
}
```

---

## Coleta de Dados

Com as keys definidas, para cada par de **grupo de métricas** e **target**, deve ser realizada uma chamada periódica ao endpoint:

```text
GET /targets/{targetId}/metricGroups/{metricGroupName}/latestData
```

Essa chamada deve respeitar o intervalo definido na configuração.

Exemplo de payload:

```json
{
  "metricGroupName": "topWaitEvents",
  "items": [
    {
      "waitEventName": "control file sequential read",
      "waitClassName": "System I/O",
      "averageWaitTime": 0.0,
      "totalWaitTime": 5.0
    }
  ]
}
```

As keys do exemplo acima são:
- `waitEventName`
- `waitClassName`

Todos os demais campos são tratados como métricas individuais.

---

## Normalização das Métricas

Cada métrica é normalizada no formato:

```text
oem_<metric_group_name>_<metric_name>
```

As métricas são exportadas acompanhadas de atributos que incluem:
- Tags do target
- Informações básicas do target
- Keys que identificam o item da métrica

Exemplo:

```json
{
  "nome_metrica": "oem_topWaitEvents_averageWaitTime",
  "atributos": {
    "waitEventName": "control file sequential read",
    "waitClassName": "System I/O",
    "target_name": "cdbs51bc_cdbs51bc1",
    "target_type": "oracle_database"
  }
}
```

---

## Tipos de Métricas Exportadas

### Métricas Numéricas

Valores numéricos são exportados como **métricas OTLP**, normalmente utilizando o tipo **Gauge**. Ao final do fluxo, tornam-se séries temporais em ferramentas como o Prometheus.

---

### Métricas Textuais (Logs)

Valores textuais são exportados como **logs OTLP**, onde o valor representa um evento ou estado. Ao final do fluxo, esses dados podem ser indexados em ferramentas como o Elasticsearch.