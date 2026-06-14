# Configuração de Targets para Ingestão de Métricas

O processo de ingestão de métricas por meio da API do Oracle Enterprise Manager se baseia em duas relações principais:

- Os **targets** a serem monitorados
- As **métricas** que serão coletadas a partir desses targets

Este documento descreve o processo de identificação, relacionamento e organização dos targets que compõem um sistema de banco de dados Oracle, bem como a estrutura do arquivo de configuração utilizado pelo script de ingestão de métricas.

---

## Levantamento e Organização de Targets

A monitoração de bancos de dados Oracle e de seus componentes relacionados exige o levantamento de todos os **targets associados a um determinado sistema**, bem como o **tageamento e enriquecimento prévio desses targets**. Esse processo facilita etapas posteriores de correlação, agregação e construção de dashboards.

A API do Oracle Enterprise Manager, por si só, **não oferece um mecanismo direto para relacionar automaticamente todos os targets pertencentes a um mesmo sistema**. Dessa forma, torna-se necessário utilizar **padrões de nomenclatura** adotados no ambiente para estabelecer essas relações.

Esses padrões, no entanto, **não são totalmente consistentes**, exigindo inspeção prévia e validação após processos de automação de descoberta e tageamento.

---

## Identificação de Targets por Padrão de Nomenclatura

De forma geral, os targets seguem um padrão de nomenclatura baseado no nome do **RAC Database** (`rac_database`). Esse nome pode ser utilizado como referência para identificar a maioria dos demais componentes relacionados ao sistema de banco de dados.

Considerando um sistema composto pelos seguintes tipos de target:

- `oracle_dbsys`
- `rac_database`
- `oracle_pdb`
- `oracle_database`
- `host`
- `oracle_listener`

A relação entre eles costuma seguir os padrões descritos a seguir.

---

### Oracle DB System

```
oracle_dbsys:
<nome_do_rac_primario_ou_standby>_sys

Exemplo:
cdbp51bc_sys
```

---

### Targets do RAC Primário

```
rac_database:
<nome_do_rac_primario>
Exemplo: cdbp51bc

oracle_pdb:
<nome_do_rac_primario>_<NOME_RAC_EM_MAIÚSCULO><número_3_dígitos>
Exemplo: cdbp51bc_CDBP51BCPDB001

oracle_database:
<nome_do_rac_primario>_<nome_do_rac_primario><número_1_dígito>
Exemplo: cdbp51bc_cdbp51bc2

host:
<nome_do_host>
Exemplo: cadecrk01cl01vm02.intra.caixa.gov.br

oracle_listener:
LISTENER_<nome_do_host>
Exemplo: LISTENER_cadecrk01cl01vm02.intra.caixa.gov.br
```

---

### Targets do RAC Standby

```
rac_database:
<nome_do_rac_standby>
Exemplo: cdbs51bc

oracle_pdb:
<nome_do_rac_standby>_<NOME_RAC_PRIMÁRIO_EM_MAIÚSCULO><número_3_dígitos>
Exemplo: cdbs51bc_CDBP51BCPDB001

oracle_database:
<nome_do_rac_standby>_<nome_do_rac_standby><número_1_dígito>
Exemplo: cdbs51bc_cdbs51bc2

host:
<nome_do_host>
Exemplo: dadecrk01cl01vm02.intra.caixa.gov.br

oracle_listener:
LISTENER_<nome_do_host>
Exemplo: LISTENER_dadecrk01cl01vm02.intra.caixa.gov.br
```

Como pode ser observado, a diferenciação entre os ambientes **primário** e **standby** ocorre, em geral, pela substituição do caractere `p` por `s` no nome do RAC.

---

## Identificação de Host e Listener

Os nomes dos targets do tipo `host` e `oracle_listener` **não podem ser inferidos diretamente** a partir da nomenclatura do RAC.

Para esses casos, deve-se utilizar o **endpoint de propriedades do target `oracle_database`**, onde é possível obter informações como:

- `machine_name` — nome do host onde o banco está em execução

A partir do valor de `machine_name`, é possível inferir tanto o **host** quanto o **oracle_listener** associado ao banco de dados.

---

## Relacionamento e Tageamento de Targets

Após a identificação dos targets, recomenda-se realizar o **tageamento estruturado** para facilitar o processo de correlação em etapas posteriores.

Uma abordagem recomendada é a construção de uma **hierarquia pai–filho**, onde cada target contém, além de suas informações próprias, **tags no formato chave:valor** com referências aos targets superiores na hierarquia.

A hierarquia sugerida é:

```
oracle_dbsys
└── rac_database
    └── oracle_pdb
        └── oracle_database
            └── host
            └── oracle_listener
```

Além disso, podem ser adicionadas **tags externas**, não disponíveis diretamente nos dados do target, como:

- Nome do sistema
- Nome da torre ou domínio organizacional

Exemplo:

```
sistema: siapx
torre: cartoes
```

Essas tags devem seguir um **padrão previamente definido**, a fim de facilitar automações e correlações futuras.

---

## Estrutura Final do Arquivo de Configuração

Seguindo o processo descrito acima, o arquivo de configuração utilizado pelo script de ingestão de métricas deve seguir um padrão semelhante ao exemplo abaixo:

```yaml
- site:
    endpoint: https://oraemc.caixa
    name: oraemc
    targets:
      - id: 240D79C7320E221DE06400144FFBE115
        name: occp40bc
        typeName: rac_database
        tags:
          rac_database: occp40bc
          target_name: occp40bc
          target_type: rac_database
          sistema: siapx
          torre: cartoes
```

Os demais targets do sistema seguem a mesma estrutura, contendo:

- Identificação única (`id`)
- Nome do target
- Tipo (`typeName`)
- Informações específicas (quando aplicável)
- Conjunto de tags já enriquecidas

Essa padronização permite que o script de ingestão opere de forma determinística, facilitando a correlação de métricas, a criação de dashboards e a integração com outras ferramentas de observabilidade.