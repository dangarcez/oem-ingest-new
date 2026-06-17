# Validacao De Configuracao

Este documento descreve a validacao opcional de `configTargets.yaml` contra a
API do Oracle Enterprise Manager (OEM).

## Como Ativar

Configure as credenciais OEM e habilite a validacao no startup:

```sh
export OEM_VALIDATE_CONFIG=true
export OEM_CONFIG_TARGETS=./configs/configTargets.yaml
export OEM_VALIDATED_CONFIG_OUTPUT=./configs/configTargets.validated.yaml
export OEM_VALIDATION_REPORT_OUTPUT=./configs/configTargets.validated.report.yaml
export OEM_USER=usuario
export OEM_PASSWORD=senha
```

`OEM_VALIDATION_REPORT_OUTPUT` e opcional. Quando nao informado, o caminho e
derivado de `OEM_VALIDATED_CONFIG_OUTPUT` inserindo `.report` antes da extensao.
Por exemplo, `configTargets.validated.yaml` gera
`configTargets.validated.report.yaml`.

## Arquivos Gerados

A validacao sempre preserva o arquivo original de targets. Ela gera:

- YAML validado em `OEM_VALIDATED_CONFIG_OUTPUT`, com IDs atualizados, targets
  ausentes removidos, tags estruturais corrigidas e targets relacionados
  adicionados quando a correlacao permite.
- Relatorio YAML em `OEM_VALIDATION_REPORT_OUTPUT`, com resumo e listas
  detalhadas de mudancas.

Nenhum dos caminhos de saida pode apontar para `OEM_CONFIG_TARGETS`. O relatorio
tambem nao pode apontar para o mesmo arquivo do YAML validado.

## Regras De Validacao

A etapa de IDs lista os targets do OEM por site e localiza cada target
configurado pelo par `name` + `typeName`.

- Um match unico com ID diferente atualiza o ID em memoria e no YAML validado.
- Nenhum match remove o target da configuracao validada e da run atual.
- Multiplos matches mantem o target configurado e geram warning, porque nao ha
  uma escolha segura de ID.
- Targets retornados pela API sem ID valido sao tratados como ausentes.

Depois disso, a etapa de correlacao roda sobre a configuracao ja filtrada. Ela
pode adicionar targets relacionados para raizes `rac_database` e `oracle_pdb` e
corrigir tags estruturais conforme as regras legadas.

Sites que ficam sem targets depois das remocoes sao omitidos do YAML validado e
registrados no relatorio.

## Relatorio YAML

O relatorio tem os caminhos de entrada/saida, timestamp e contadores agregados:

```yaml
sourceConfig: ./configs/configTargets.yaml
validatedConfig: ./configs/configTargets.validated.yaml
generatedAt: "2026-06-16T12:30:00Z"
summary:
  idCorrections: 2
  targetsRemoved: 1
  sitesRemoved: 0
  targetsAdded: 3
  tagCorrections: 4
  warnings: 10
idCorrections: []
targetsRemoved: []
sitesRemoved: []
targetsAdded: []
tagCorrections: []
warnings: []
```

As listas detalhadas incluem site, indice original do target, nome, tipo, IDs
antigo/novo quando aplicavel e a mensagem de warning emitida pela validacao.

## Efeito Na Run

Quando `OTEL_EXPORT_URL` esta definido, a mesma execucao usa a configuracao
validada em memoria. Targets removidos nao geram jobs de scheduler e nao fazem
chamadas `latestData`.

Se a validacao remover todos os targets de todos os sites, os arquivos de saida
sao gerados e a coleta nao inicia. O processo retorna erro informando que a
validacao removeu todos os targets.

Sem `OTEL_EXPORT_URL`, a aplicacao apenas valida, escreve os artefatos e encerra
sem iniciar o runtime de coleta/exportacao.
