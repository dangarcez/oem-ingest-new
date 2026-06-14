# Tratamento Especial e Métricas Customizadas

Para a maioria das métricas, o fluxo geral descrito na documentação é suficiente: a métrica chega aos destinos finais contendo essencialmente as mesmas informações coletadas no Oracle Enterprise Manager, apenas com o formato reformulado.

No entanto, algumas informações essenciais para a monitoração de determinados targets **não são disponibilizadas de forma direta ou padronizada**, exigindo um tratamento especial durante o processo de ingestão para a extração de **métricas customizadas**.

Esta seção descreve os principais casos em que esse tratamento adicional é necessário.

---

## Status de Serviço – `oem_service_status`

Para verificar o status de serviços associados a diferentes tipos de targets, são utilizadas métricas distintas conforme o tipo do componente monitorado:

- Para targets do tipo `rac_database`, utiliza-se o grupo de métricas **service_performance**, que já disponibiliza uma métrica de status.
- Para targets do tipo `oracle_pdb`, utiliza-se o grupo de métricas **DBService**, que não possui uma métrica explícita de status.

No caso de `oracle_pdb`, o status do serviço é inferido a partir do campo:

- `DBTime_delta`
  - Valor **maior que 0** → serviço ativo
  - Valor **igual a 0** → serviço inativo

Com base nesses critérios, é construída uma **métrica unificada de status de serviço**, denominada:

```text
oem_service_status
```

---

## Monitoração da Coleta de Targets – `oem_monitor_response`

Uma informação relevante para monitoração, mas que **não está disponível diretamente nas métricas do target**, é o status da própria coleta.

Para isso, é avaliado periodicamente o **timestamp da última coleta bem-sucedida** de métricas para cada target:

- Se a última coleta ocorreu dentro de um intervalo considerado aceitável, o target é marcado como **em coleta**
- Caso contrário, o target é marcado como **sem coleta**

Essa lógica resulta na métrica customizada:

```text
oem_monitor_response
```

---

## Status do Target – `oem_monitor_status`

Determinar o status geral de um target pode ser complexo, pois nem sempre é claro distinguir entre falhas de coleta, problemas no agente ou indisponibilidade real do serviço.

Para lidar com esse cenário, são utilizados algoritmos que correlacionam informações de múltiplas fontes, gerando a métrica customizada:

```text
oem_monitor_status
```

As regras de cálculo variam conforme o tipo de target.

---

### Status do `rac_database`

- Utiliza o grupo de métricas **Availability**
- Caso o grupo retorne dados, o target é considerado **inativo**
- Caso não retorne dados:
  - `oem_monitor_response == 1` → target em coleta
  - `oem_monitor_response == 0` → target sem coleta

---

### Status do `oracle_database`

- Utiliza o grupo de métricas **Response**
- Caso o grupo não retorne dados:
  - O status é determinado com base em `oem_monitor_response`
- Caso retorne dados:
  - Verifica-se a presença dos campos `Status` ou `DatabaseStatus`
  - O campo disponível é utilizado para definir o status final

---

### Status do `oracle_pdb`

- Utiliza o grupo de métricas **Response**
- Caso o grupo não retorne dados:
  - Target considerado **sem coleta**
- Caso retorne dados:
  - Se `Status == 0` → target **inativo**
  - Caso `Status` não exista:
    - Se `State != OPEN` → target **inativo**

---

### Status do `host`

- Utiliza o grupo de métricas **Response**
- Caso o grupo não retorne dados:
  - O status é determinado com base em `oem_monitor_response`
- Caso retorne dados:
  - Se `Status == 0` → retorna status **0**
  - Caso contrário → retorna status **2**

---

Esse conjunto de regras permite uma avaliação mais precisa do estado real dos targets, reduzindo falsos positivos e fornecendo uma visão mais confiável da saúde do ambiente monitorado.