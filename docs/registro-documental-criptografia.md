# Registro Documental: criptografia, rotação e resposta a vazamentos

## Estado desta documentação

Este documento registra o desenho aprovado para a implementação do Registro
Documental e seu procedimento operacional. **Não descreve uma funcionalidade já
disponível.** Na data desta documentação, o banco possui `documentary_record` e
uma política de acesso exclusivo à autora, mas não há endpoints de cadernos,
versionamento ou ferramenta de recriptografia implementados.

O helper `encryptPII` em `internal/account/service.go` atende outro fluxo e usa
`PII_ENCRYPTION_KEY`. Ele não implementa este conjunto de chaves nem a rotação
dos cadernos. Não reutilizar ou alterar essa chave para configurar o Registro
Documental. Até a implementação e seus testes estarem concluídos, as variáveis
abaixo não têm efeito na aplicação. Não há comando de rotação pronto para executar.

## 1. Configuração aprovada

O conteúdo de cada caderno e de cada versão será criptografado na API antes de
ser gravado no PostgreSQL. A leitura só poderá decriptar depois de verificar a
identidade da autora, a organização e a associação à paciente. Criptografia não
substitui autorização ou RLS.

```dotenv
DOCUMENTARY_ACTIVE_KEY_ID=v1
DOCUMENTARY_ENCRYPTION_KEYS='{"v1":"<64 caracteres hexadecimais>"}'
```

Os valores acima são placeholders e não funcionam como chaves. Cada valor deve
representar 32 bytes aleatórios, codificados em 64 caracteres hexadecimais. O
identificador é um nome público e estável, não um segredo. Nunca reutilizar um
identificador para um valor diferente: um registro com `keyId=v1` deve continuar
encontrando exatamente a chave que o criptografou.

Para preparar uma rotação:

```dotenv
DOCUMENTARY_ACTIVE_KEY_ID=v2
DOCUMENTARY_ENCRYPTION_KEYS='{"v1":"<chave antiga>","v2":"<chave nova>"}'
```

No `.env`, o JSON fica em uma linha, entre aspas simples. No GitHub Secret
`DOCUMENTARY_ENCRYPTION_KEYS`, o valor será o JSON puro, sem essas aspas externas.
O deploy deverá validar o valor e atualizar o `.env` sem imprimir o segredo,
preservando as demais variáveis e mantendo o arquivo com permissão `600`.

Desenvolvimento, staging e produção terão chaves diferentes. O conjunto de chaves
deve ter uma cópia de recuperação protegida e separada do banco, com acesso
restrito. GitHub Secrets e o `.env` da VM não são o único plano de recuperação.
Não inserir chaves em commits, imagens Docker, argumentos de comando, URLs,
prints, tickets, logs ou histórico do terminal. Uma etapa automatizada pode gerar
a chave com um gerador criptograficamente seguro e gravá-la diretamente em um
arquivo protegido ou no gerenciador de segredos; não deve exibi-la no console.

O workflow atual em `.github/workflows/deploy.yml` já atualiza segredos SMTP no
`.env` por SSH. Acrescentar as variáveis documentais a esse fluxo faz parte da
implementação futura. Isso evita editar o painel da Oracle, mas não elimina a
dependência de SSH do deploy atual. Se a conexão falhar, a rotação não está
publicada: confirmar o resultado do deploy e da configuração, sem mostrar valores.

## 2. Comportamento previsto da aplicação

- Usar AES-256-GCM com nonce aleatório novo em cada criptografia, inclusive na
  recriptografia. Usar a biblioteca padrão, sem algoritmo próprio.
- Persistir uma estrutura versionada com identificador da chave, nonce e
  ciphertext autenticado. Associar organização, autora, paciente, caderno,
  categoria e versão como dados autenticados, para detectar a troca de conteúdo
  entre registros. Manter essas associações estáveis na recriptografia.
- Validar o JSON sem aceitar nomes duplicados, exigir um objeto de identificadores
  válidos e chaves hexadecimais de 32 bytes e exigir que a chave ativa esteja nele.
- Carregar a configuração na inicialização. Alterar `.env` ou GitHub Secrets não
  modifica processos em execução: recriar os containers com a configuração nova.
  Não confiar somente em `docker compose restart` para aplicar variáveis novas.
- Se a configuração estiver ausente ou inválida, deixar os endpoints documentais
  indisponíveis, com erro controlado e sem expor a configuração. Não interromper
  os outros fluxos por esse motivo. Não gravar texto puro ou gerar uma chave
  temporária para contornar o problema.
- Se faltar a chave de um registro ou falhar a autenticação criptográfica, não
  retornar texto, não tratar como caderno vazio e não permitir sobrescrever aquele
  conteúdo. Registrar somente identificadores operacionais e o tipo da falha.
- Novas gravações usam a chave ativa. Leituras usam o identificador guardado em
  cada conteúdo. Clicar em Salvar ou restaurar uma versão também usa a chave ativa.
- Não criar versões repetidas quando não houver mudança de conteúdo. Recriptografar
  é manutenção do armazenamento, não alteração clínica: não criar um ponto no
  histórico da psicóloga nem alterar autoria, conteúdo, ordem, revisão ou datas
  clínicas. Registrar a manutenção em auditoria separada, sem texto ou chaves.
- Não fornecer endpoints públicos de rotação ou de acesso às chaves. Somente uma
  rotina administrativa controlada executará a manutenção criptográfica.

O navegador precisa receber o texto autorizado para exibi-lo. Esta solução não é
criptografia ponta a ponta: a API e quem controla o processo com suas chaves podem
decriptar os registros. Nomes de pacientes, categorias e outros metadados não
ficam automaticamente protegidos pela criptografia do conteúdo. HTTPS, controle
de acesso, proteção da VM e redaction de logs continuam necessários.

## 3. Rotação planejada, sem incidente

1. Registrar ambiente, responsável, identificador atual e identificador de destino.
   Inventariar dependências por chave: conteúdo atual, todas as versões e backups.
2. Confirmar uma cópia recuperável do banco e das chaves, guardadas separadamente.
   Testar o procedimento em staging com chaves e dados exclusivos desse ambiente.
3. Gerar a chave nova em ambiente confiável. Publicar o conjunto com ambas as
   chaves e a nova ativa. Validar previamente a configuração e recriar todos os
   processos que leem ou gravam conteúdo documental, mantendo a antiga para leitura.
4. Confirmar que todos os escritores usam o novo identificador. Uma instância
   antiga não pode continuar criando registros com a chave anterior. Verificar
   leitura de conteúdo antigo e novo sem imprimir o texto nos logs.
5. Executar a rotina administrativa de recriptografia descrita abaixo, primeiro
   em modo de inventário e depois em lotes. Guardar seus resultados operacionais.
6. Conferir que o banco não tem conteúdos dependentes da chave antiga, que todas
   as versões continuam decriptáveis e que não houve mudança de conteúdo clínico.
7. Resolver a dependência dos backups antes de retirar a chave antiga do conjunto
   ativo. Guardar a chave em recuperação restrita se ainda for necessária para
   backups válidos. Destruição definitiva exige confirmar que não existe mais
   nenhuma cópia retida cuja recuperação dependa dela.

Retirar uma chave não é um passo de rollback. Enquanto a chave antiga estiver
disponível para leitura, uma falha no processamento pode ser corrigida e a rotina
retomada. Não reverter a chave ativa para uma chave comprometida em um incidente.

## 4. Requisitos da rotina administrativa de recriptografia

Esta rotina ainda deverá ser implementada e testada antes de executar em produção.
Ela deve oferecer inventário sem mutações e execução em lotes, com origem,
destino, progresso, resumo e código de saída indicando falhas. Receber chaves pela
configuração protegida, nunca como argumentos de linha de comando.

Processar o conteúdo atual e **todas as versões**, inclusive de pacientes inativas.
Para cada item, decriptar com a chave de origem, criptografar com a de destino e
conferir a recuperação do mesmo texto antes de persistir. Atualizar ciphertext e
identificador juntos, em operação atômica. Não manter texto decriptado em arquivos
temporários, relatórios ou logs.

Evitar transação longa sobre todo o banco. Usar proteção contra gravação concorrente
para não sobrescrever alterações feitas pela psicóloga durante a manutenção. Se um
item mudar, reler seu estado ou marcá-lo para nova tentativa. Impedir execuções
administrativas conflitantes. Uma execução interrompida deve ser retomável:
conteúdos já migrados não são processados novamente desnecessariamente.

Uma falha de decriptação não permite pular silenciosamente o registro nem apagar
seu conteúdo. Contabilizar a falha e impedir que a operação seja declarada completa.
Verificar integridade e equivalência em memória; não publicar hashes de texto clínico
como prova de validação. Os totais finais devem abranger todos os conteúdos e versões.

## 5. Procedimento em caso de suspeita ou confirmação de vazamento

### 5.1 Identificar e preservar evidências

Abrir um registro de incidente restrito e nomear responsáveis pela contenção,
recuperação e comunicação. Anotar descoberta, ambiente, identificadores de chaves,
origem da exposição, período provável e recursos atingidos. Não copiar a chave ou
anotações clínicas para o registro do incidente.

Preservar evidências de acesso e implantação em armazenamento restrito antes de
remover logs ou artefatos expostos, sem atrasar contenção urgente. Uma chave
publicada no Git continua comprometida após remover o arquivo: clones, caches,
artefatos e cópias podem existir. Não considerar apagar a mensagem ou reescrever
o histórico como substituto da rotação.

Determinar se houve exposição da chave, do banco/ciphertexts, do `.env` inteiro,
da VM, de credenciais de deploy ou de textos já decriptados. Se o `.env` completo
vazou, incluir credenciais de banco, JWT, SMTP e demais segredos afetados no plano;
trocar apenas a chave documental não resolve essas exposições.

### 5.2 Conter antes de distribuir uma chave nova

Restringir o acesso indevido, revogar credenciais comprometidas e fechar a origem
do vazamento. Suspender acessos ou gravações documentais se não for possível
garantir que o ambiente está confiável. Não gerar nem implantar a chave nova em
uma VM ou pipeline ainda sob controle do atacante.

Quando houver comprometimento da VM ou do pipeline, recuperar ou reconstruir o
ambiente confiável e suas permissões antes da rotação. A restrição de acesso aos
registros ainda precisa funcionar; criptografia não impede um atacante com o
controle da API de obter texto em uma leitura autorizada pelo processo.

### 5.3 Substituir a chave e migrar os conteúdos

Gerar chave independente em ambiente confiável e ativá-la nos escritores.
Disponibilizar a antiga apenas onde for necessário para recuperação/migração,
com acesso restrito. Dependendo da exposição, essa etapa pode exigir manter o
Registro Documental indisponível até concluir a migração; disponibilidade não
justifica reintroduzir uma chave comprometida em um ambiente inseguro.

Recriptografar conteúdo atual e histórico pelo procedimento das seções 3 e 4.
Remover a chave comprometida do ambiente de execução quando não houver dependências
ativas, sem perder o único meio de recuperar dados ainda não migrados. Tratar
backups separadamente. Se o processamento falhar, preservar o ciphertext original
e corrigir a causa em ambiente confiável; não apagar registros para zerar o contador.

### 5.4 Tratar backups e avaliar o alcance

Ativar `v2` não protege uma cópia antiga criptografada com `v1`. Se alguém obteve
essa cópia e a chave antiga, recriptografar o banco atual não desfaz a exposição.
Também não há mecanismo para revogar remotamente a cópia de uma chave simétrica.

Inventariar snapshots, dumps, réplicas e cópias externas. Quando necessário,
restaurar backups em ambiente isolado, recriptografar e produzir novos backups
verificados. Backups substituídos só podem ser descartados conforme a política
de retenção e preservação de evidências. Se uma cópia tiver de ser mantida, sua
chave necessária também precisa de custódia restrita; isso limita a possibilidade
de destruir definitivamente a chave antiga.

O responsável pelo incidente deve avaliar com quem responde pela privacidade as
obrigações de comunicação aplicáveis, com base no alcance e nas evidências. A
aplicação não notificará pessoas automaticamente nem declarará que o incidente
está resolvido apenas porque houve uma rotação.

### 5.5 Validar recuperação e encerrar

Antes de liberar o fluxo normal, confirmar ambiente confiável, nova chave ativa em
todos os escritores, ausência de dependências ativas da antiga, leitura de conteúdo
e histórico, restauração de versões, isolamento por autora e recuperação de backup.
Monitorar falhas de autenticação, decriptação e acessos inesperados.

Registrar cronologia, causa, alcance conhecido e ainda incerto, ações executadas,
responsáveis e medidas preventivas. Conservar as evidências de acordo com a política
do incidente. Não afirmar ausência de vazamento de conteúdo sem evidência suficiente.

## 6. Checklist para implementação e operação

- [ ] Parser rejeita JSON inválido, identificadores duplicados, chave de tamanho
  incorreto e identificador ativo ausente; mensagens não contêm segredos.
- [ ] Chaves de ambientes diferentes não são intercambiáveis.
- [ ] Criptografia e decriptação preservam texto vazio, Unicode e quebras de linha.
- [ ] Conteúdo adulterado ou transplantado para outra associação é rejeitado.
- [ ] API não grava plaintext nem expõe chave, configuração ou texto nos logs.
- [ ] Chave ausente ou desconhecida não resulta em conteúdo vazio ou sobrescrita.
- [ ] Autorizações são verificadas antes de decriptar ou restaurar versões.
- [ ] Rotação alcança conteúdo atual e histórico sem mudar versões clínicas.
- [ ] Processamento interrompido é retomável e seguro diante de edições concorrentes.
- [ ] Inventário final identifica falhas e bloqueia retirada prematura de uma chave.
- [ ] Deploy aplica configuração sem expor segredo e recria os processos necessários.
- [ ] Recuperação de backups e custódia das chaves foram testadas.

## Referências

As recomendações gerais de proteger segredos durante distribuição, investigar
exposições e substituir segredos comprometidos estão descritas na
[OWASP Secrets Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html).
A escolha de criptografia autenticada e a necessidade de planejar o ciclo de vida
das chaves seguem a
[OWASP Cryptographic Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html).
A organização de preparação, resposta e recuperação tem como referência o
[NIST SP 800-61 Rev. 3](https://csrc.nist.gov/pubs/sp/800/61/r3/final).
Este procedimento é o desenho operacional do Acolhe; não certifica conformidade
legal nem substitui a avaliação específica de um incidente.
