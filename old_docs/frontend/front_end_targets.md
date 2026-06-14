# Frontend de Configuração do Oracle Enterprise Manager

Selecionar targets e métricas diretamente pelo console do Oracle Enterprise Manager pode se tornar uma tarefa lenta e pouco prática, especialmente em ambientes com grande volume de sistemas e targets.

Como alternativa, foi desenvolvido um **web app de configuração**, que utiliza a própria **API do Oracle Enterprise Manager** para automatizar esse processo de forma visual. Esse frontend aplica os conceitos descritos nas seções de **configuração de targets** e **configuração de métricas**, e gera como resultado **arquivos de configuração prontos para uso** pelo script de ingestão de dados do OEM.

---

## Página de Targets

A página de **Targets** é o ponto central para visualização, edição e manutenção da configuração de targets.

Na **parte direita da tela**, são exibidos os targets que já fazem parte da configuração atual. Ao clicar em um target, é possível:
- Visualizar suas tags
- Editar ou complementar o tageamento, quando necessário

Sempre que um target é alterado ou novos targets são adicionados, as mudanças passam a refletir imediatamente nessa seção. A partir daí, o usuário pode:
- Salvar a configuração
- Fazer o download do arquivo atualizado, para uso posterior no script de ingestão em Python

```text
[imagem_da_seção_de_configuração]
```

---

## Seleção do Endpoint OEM

Na **parte superior da página**, o usuário deve selecionar o **endpoint do Oracle Enterprise Manager** a partir do qual os targets serão consultados.

```text
[imagem_seleção_endpoint]
```

Existe também a opção **Recarregar targets**. Por padrão, os targets consultados são armazenados em **cache no backend**, com o objetivo de reduzir chamadas frequentes à API do OEM, já que esse tipo de consulta tende a ser relativamente custosa. Ao utilizar essa opção, o cache é descartado e uma nova consulta completa é realizada.

---

## Formas de Pesquisa de Targets

Após a seleção do endpoint, o frontend disponibiliza **duas formas de pesquisa** para inclusão de targets na configuração:

### Pesquisa Livre

A **pesquisa livre** permite localizar e adicionar targets de forma individual, sem a aplicação de algoritmos de correlação automática com outros targets relacionados.

Essa modalidade é indicada principalmente para:
- Testes
- Casos excepcionais
- Situações onde é necessário incluir um target específico

É importante destacar que targets adicionados de forma avulsa **não favorecem a padronização da monitoração** em etapas posteriores. Ainda assim, um target pode ser localizado, tageado/enriquecido manualmente e incluído na configuração por meio desse método.

```text
[imagem_pesquisa_livre]
```

---

### Pesquisa por Sistema

A **pesquisa por sistema** utiliza os conceitos descritos na seção de configuração de targets para **automatizar a detecção de targets relacionados**, bem como aplicar um **tageamento básico inicial**.

Nesse modo de pesquisa:
- Apenas targets do tipo `rac_database` e `oracle_database` podem ser selecionados como raiz
- A partir deles, o frontend identifica automaticamente os demais targets relacionados ao sistema

```text
[imagem_pesquisa_por_sistema]
```

---

## Aplicação de Tags Globais

Informações adicionais, como:
- Nome do sistema
- Nome da torre ou domínio organizacional

devem ser adicionadas manualmente aos targets.

Para facilitar esse processo, o frontend disponibiliza **campos de aplicação de tags globais**, permitindo que o usuário aplique o mesmo conjunto de tags a **todos os targets atualmente em visualização**, antes de adicioná-los à configuração final.

```text
[imagem_campo_add_global]
```

Essa abordagem reduz esforço manual e contribui para a padronização do tageamento, facilitando correlação, filtragem e uso posterior das métricas em dashboards e alertas.