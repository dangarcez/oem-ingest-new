# Frontend de Configuração do Oracle Enterprise Manager

Selecionar targets e métricas diretamente pelo console do Oracle Enterprise Manager pode se mostrar uma tarefa lenta e pouco prática. Uma forma mais ágil de realizar essa configuração é por meio de automações utilizando a própria **API do OEM**.

Com esse objetivo, foi desenvolvido um **frontend de configuração**, responsável por orquestrar essas automações de forma visual. O frontend utiliza os conceitos apresentados nas seções de **configuração de targets** e **configuração de métricas**, gerando como resultado **arquivos de configuração prontos** para utilização pelo script de ingestão de dados do OEM.

---

## Página de Métricas

A página de **Métricas** é o local onde é possível verificar e atualizar a configuração de métricas utilizadas na ingestão.

Na **seção à direita da tela**, são exibidas as métricas já presentes na configuração atual, juntamente com a **frequência (em minutos)** com que cada métrica é coletada a partir dos endpoints do Oracle Enterprise Manager.

Nessa área, o usuário pode:
- Remover métricas da configuração
- Editar a frequência de coleta
- Verificar a disponibilidade das métricas entre os targets configurados

A verificação de disponibilidade é exibida na seção **Disponibilidade de métricas**, sendo necessário clicar em **Buscar disponibilidade** para iniciar a consulta.

```text
<imagem-Metrics_configuracao_atual>
```

---

## Pesquisa de Métricas

Na seção **Pesquisa de métricas**, o usuário pode consultar quais métricas estão disponíveis para um tipo específico de target.

O fluxo básico é:
1. Selecionar o **tipo de target**
2. Executar a pesquisa de métricas disponíveis

Inicialmente, a pesquisa considera apenas os targets já configurados para monitoração. No entanto, ao selecionar a opção **Todos os targets**, é possível realizar a busca considerando todos os targets disponíveis no Oracle Enterprise Manager.

```text
<imagem-Metrics_Caixa_pesquisa>
```

Após a pesquisa, é exibida uma lista de métricas disponíveis para o target selecionado.

Nem sempre todas as métricas configuradas estão habilitadas ou retornando dados. Ao clicar em **Verificar disponibilidade**, o frontend realiza uma chamada para cada métrica e apresenta uma indicação visual de disponibilidade:

- **Verde**: métrica disponível  
- **Cinza**: métrica sem dados  
- **Vermelho**: métrica indisponível  

Além disso, para validar o conteúdo retornado por uma métrica específica, o usuário pode clicar no botão **Search**, que exibe os dados na seção **Dados do grupo de métricas**.

Para adicionar uma métrica à configuração, basta clicar em **Adicionar**.

> **Observação:** ao adicionar uma métrica à configuração, o script de ingestão passará a coletar essa métrica para **todos os targets do mesmo tipo**. Por esse motivo, cada métrica precisa ser adicionada apenas uma única vez.

```text
<imagem-Metricas_Resultado_pesquisa>
```

---

## Disponibilidade de Métricas

Ao realizar uma pesquisa por métrica ou ao clicar no ícone de lupa em uma métrica já configurada, a métrica em questão é selecionada automaticamente.

Na seção **Disponibilidade de métricas**, é possível visualizar a disponibilidade dessa métrica entre **todos os targets do mesmo tipo**, bastando clicar em **Buscar disponibilidade**.

```text
<imagem-Metricas_Disponibilidade_metricas>
```

---

## Exportação da Configuração

Após a conclusão das configurações desejadas, o frontend permite o **download do arquivo YAML**, que deve ser utilizado diretamente pelo script de ingestão de métricas do Oracle Enterprise Manager.