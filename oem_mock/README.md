# OEM Mock

Mock local em FastAPI para simular endpoints do Oracle Enterprise Manager
usados pelo novo coletor Go durante desenvolvimento e testes locais.

## Execucao

```sh
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
uvicorn api:app --host 0.0.0.0 --port 8008
```

O mock carrega fixtures locais em MessagePack a partir de `targets.msgpack`,
`incidents.msgpack`, `incident.msgpack`, `cachePack/` e `cachePack2/`.

## Endpoints OEM

O mock mantem os endpoints principais usados pelo cliente novo:

- `GET /em/api`
- `GET /em/api/targets`
- `GET /em/api/targets/{targetId}/properties`
- `GET /em/api/targets/{targetId}/metricGroups`
- `GET /em/api/targets/{targetId}/metricGroups/{groupName}`
- `GET /em/api/targets/{targetId}/metricGroups/{groupName}/latestData`
- `GET /em/api/incidents/?ageInHoursLessThanOrEqualTo=1`
- `GET /em/api/incidents/{id}`

## Endpoints OTLP fake

Para permitir um Docker Compose local sem OpenTelemetry Collector real, o mock
tambem expoe:

- `POST /v1/metrics`
- `POST /v1/logs`

Esses endpoints apenas leem payload binario/protobuf e retornam HTTP 200 com a
quantidade de bytes recebida. Eles existem somente para desenvolvimento local e
nao validam nem armazenam dados OTLP.

## Testes

```sh
python -m unittest discover -s oem_mock
```
