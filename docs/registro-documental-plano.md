# Plano de implementação do Registro Documental

## Objetivo e estado

Implementar um espaço privado de escrita da psicóloga, organizado como um caderno
por paciente e categoria. O texto é contínuo, semelhante a um editor simples:
a profissional acrescenta, remove e reorganiza seus pensamentos ao longo do
acompanhamento, sem precisar de sessão ou atendimento para registrar conteúdo.

Este documento consolida as decisões aprovadas na conversa. A implementação na branch `feat/registro-documental` está descrita em
[registro-documental-api.md](registro-documental-api.md); disponibilidade depende
de publicação e configuração por ambiente. A implementação deverá ser entregue em
branches com o mesmo nome, a partir de `develop`, com PRs separados para API e web.
A branch iniciada para esta entrega é `feat/registro-documental`.

O fluxo de referência está na tela 08 do design do frontend, mas as decisões deste
plano substituem a proposta original de entradas separadas, formulário em popup
e arquivamento. Reutilizar os componentes e o padrão visual do projeto.

## 1. Decisões de produto

### Cadernos e categorias

- Cada caderno pertence obrigatoriamente a uma paciente e a uma psicóloga autora.
- Existe no máximo um caderno por combinação de autora, paciente e categoria.
- Não existem anotações gerais sem paciente nem exigência de sessão associada.
- As categorias são fixas: Hipótese, Observação técnica, Planejamento, Transcrição
  e Outras. Reutilizar os códigos existentes no banco: `hypothesis`,
  `technical_observation`, `planning`, `transcription` e `other`.
- Cada categoria contém um texto contínuo; não há lista de entradas por data.
  A categoria identifica o caderno, sem títulos independentes por anotação.
- Conteúdo em texto simples, preservando espaços, quebras de linha e Unicode.
  Transcrição é texto digitado, sem captura ou processamento de áudio.
- Sem anexos, tags, editor com formatação ou categorias personalizadas nesta entrega.

### Dois caminhos para o mesmo editor

**Menu geral → Registro Documental:** habilitar o item existente e mostrar uma lista
paginada de pacientes que possuem pelo menos um caderno salvo pela psicóloga
autenticada. Ao escolher uma paciente, abrir os cadernos separados por categoria.
Não incluir pacientes que só tenham rascunhos não salvos.

**Ficha da paciente → Registro Documental:** acrescentar uma aba ao lado de Visão
geral, Prontuário, Atividades e Check-ins. Abrir diretamente os mesmos cadernos
por categoria em uma página inteira. Esse caminho permite começar um caderno
mesmo quando a paciente ainda não aparece na lista do menu geral.

Os dois caminhos reutilizam o editor, o histórico e as regras de acesso. Não manter
cópias independentes do conteúdo por caminho. Uma categoria ainda não iniciada
abre o editor vazio e só passa a ter um caderno persistido no primeiro salvamento.

### Salvamento manual

- Não implementar autosave.
- Habilitar Salvar somente se o texto atual diferir do último conteúdo persistido,
  se a escrita estiver permitida e se não houver gravação em andamento.
- O primeiro salvamento cria o caderno e sua primeira versão de forma atômica.
- Cada gravação que mudar o texto gera um ponto no histórico. Cliques repetidos ou
  requisições equivalentes não geram cópias idênticas.
- A psicóloga pode apagar todo o texto e salvar. Isso gera uma versão vazia e
  preserva o caderno e as versões anteriores; não é exclusão do documento.
- Não aparar espaços automaticamente para determinar mudanças; comparar o texto
  conforme o conteúdo que será gravado, sem perder sua formatação simples.
- Após sucesso, atualizar a referência salva, a revisão e a lista de versões.
- Em caso de falha, preservar o texto no editor e mostrar erro específico em português.
- Avisar antes de sair, trocar de paciente ou categoria, ou substituir o editor
  quando houver alterações não salvas. Para fechamento/recarregamento, usar a
  proteção que o navegador disponibiliza; não prometer recuperação após fechar.
- Não persistir rascunhos em localStorage, sessionStorage, IndexedDB ou outra
  cópia local. Sem gravação bem-sucedida, o texto existe apenas na página aberta.

### Histórico, visualização e restauração

O histórico é paginado por caderno e mostra os pontos salvos com data e identificação
da versão. Permitir consultar o conteúdo e comparar trechos adicionados e removidos
entre versões. O histórico não é uma coleção de eventos de teclado ou pausas de escrita.

Selecionar uma versão abre um modal grande com seu conteúdo e duas ações:

| Ação | Resultado |
| --- | --- |
| Restaurar esta versão | Substitui o conteúdo atual e salva imediatamente, criando uma nova versão, sem apagar as anteriores. |
| Usar no editor | Carrega o texto no editor, sem persistir nem criar versão; a profissional poderá ajustar e clicar em Salvar. |

Se houver texto não salvo, ambas pedem confirmação antes de substituí-lo.
Cancelar mantém o editor intacto. Restaurar é uma ação explícita de gravação,
independente do botão Salvar do editor. Se o conteúdo já for idêntico ao atual,
não criar uma versão duplicada.

### Inatividade e privacidade

- Conteúdo disponível apenas à psicóloga autora, inclusive quando houver outros
  profissionais vinculados à mesma paciente. Não expor pela API a pacientes ou
  administradores da aplicação.
- Exigir paciente ativa e vínculo clínico ativo e válido para criar, salvar ou
  restaurar. Enforce essa regra no backend, além de apresentar o estado na interface.
- Quando paciente ou vínculo não estiver ativo, manter os cadernos já existentes
  acessíveis à autora para leitura e consulta ao histórico; bloquear gravações.
  A lista geral continua incluindo esses cadernos, identificados como somente leitura.
- Não implementar arquivamento, exclusão de caderno ou eliminação de versões.
- Não compartilhar cadernos com outra psicóloga em transferência e não incluí-los
  na exportação da paciente. Exclusão/retenção decorrente de eliminação de conta
  ou outros processos institucionais exige política separada, fora desta entrega.

## 2. Conflitos de edição

Usar revisão do caderno para controle de concorrência. Cada gravação envia a revisão
que a profissional leu; o banco só aceita a mudança se essa revisão ainda for atual.

Se outra aba tiver salvo antes, retornar conflito, sem sobrescrever o conteúdo
persistido e sem descartar o texto digitado. A interface deve permitir consultar
a versão mais recente e comparar com seu texto antes de uma nova tentativa
consciente. Não reenviar automaticamente usando a revisão nova.

Restaurar uma versão também precisa conferir a revisão atual e os direitos de
escrita. A decisão sobre pacientes e vínculos ativos é verificada novamente no
momento da gravação, mesmo que a página tenha sido aberta quando estavam ativos.

## 3. Modelo, API e paginação

Adaptar a estrutura existente de `documentary_record` para a identidade única do
caderno e acrescentar armazenamento de versões. A migração deve preservar dados
existentes; se houver registros legados incompatíveis, identificar e definir sua
conversão antes de aplicar uma restrição que os descarte ou impeça a migração.

Responsabilidades necessárias:

1. Listar pacientes com cadernos da autora, sem duplicar a paciente por categoria.
2. Consultar os cadernos/categorias de uma paciente com seu estado de escrita.
3. Criar ou gravar conteúdo com revisão esperada e retornar o estado persistido.
4. Listar versões paginadas e consultar uma versão autorizada.
5. Restaurar uma versão com controle de concorrência e registro de origem.

As rotas implementadas e os limites de conteúdo/página estão documentados em
[registro-documental-api.md](registro-documental-api.md). As listas paginadas retornarão explicitamente:

```json
{
  "items": [],
  "totalCount": 0,
  "totalPages": 0,
  "page": 1,
  "pageSize": 20
}
```

O exemplo define a forma da resposta, não fixa o tamanho padrão da página.
`page` começa em 1; `totalCount` conta todos os itens elegíveis antes da paginação;
`totalPages` é calculado no backend com o mesmo filtro e tamanho de página.
Sem resultados, retornar lista vazia e zero páginas. Usar ordenação estável com
desempate por identificador. Validar parâmetros e limitar `pageSize` no servidor.
O frontend consome os totais retornados, sem recalculá-los a partir de `items`.

Autora e organização vêm do contexto autenticado, nunca do corpo da requisição.
Todas as queries devem manter escopo explícito, além de RLS. Índices e restrições
devem garantir identidade única do caderno e unicidade da numeração de versões.
Atualizar conteúdo e inserir a versão na mesma transação. Validar concorrência
também no primeiro salvamento simultâneo de um caderno ainda inexistente.

O web passa pelo BFF, mantendo o token no cookie HttpOnly. Validar entrada e
resposta com schemas; usar chaves de cache por paciente e caderno, com separação
de contexto autenticado conforme o padrão de sessão. Limpar conteúdo privado
ao trocar de sessão e não reaproveitar cache de uma autora para outra.

## 4. Criptografia e operação

Criptografar conteúdo atual e todas as versões com AES-256-GCM e validar a
configuração antes de habilitar o fluxo. Usar o conjunto em JSON:

```dotenv
DOCUMENTARY_ACTIVE_KEY_ID=v1
DOCUMENTARY_ENCRYPTION_KEYS='{"v1":"<chave hexadecimal de 32 bytes>"}'
```

Chaves independentes por ambiente; conteúdo guarda o identificador necessário
para leitura. Não usar chave de CPF ou JWT e não implementar fallback para texto
puro. Falha de decriptação não deve resultar em caderno vazio editável.

Incluir adaptação do deploy via GitHub Secrets e rotina administrativa de inventário
e recriptografia em lotes, retomável e segura diante de gravações concorrentes.
Sem KMS, rotação automática ou interface de administração de chaves nesta entrega.
A manutenção criptográfica não gera versões clínicas nem altera suas datas.

Detalhes do formato, validação, rotação, backups e resposta a vazamentos estão em
[Registro Documental: criptografia, rotação e resposta a vazamentos](registro-documental-criptografia.md).

## 5. Sequência de implementação

1. **Banco e contratos:** fechar parâmetros e limites; migrar o modelo, versões,
   índices e políticas; regenerar sqlc e definir os contratos paginados.
2. **Criptografia:** implementar conjunto de chaves, validação, criptografia e
   decriptação autenticadas, com proteção de logs e testes.
3. **API:** implementar consultas, gravação, histórico e restauração com autorização,
   transações, concorrência e auditoria sem conteúdo clínico.
4. **Web:** implementar componentes reutilizáveis do editor e histórico, modal de
   versões, aba na paciente e lista no menu geral, BFF e proteção de texto não salvo.
5. **Operação:** adaptar deploy, entregar ferramenta de recriptografia e atualizar
   o procedimento operacional com comandos reais e verificados.
6. **Validação e revisão:** executar checks e testes relevantes, conferir interfaces,
   revisar os dois PRs e registrar limitações. Publicar API/configuração/migrações
   antes de ativar o frontend. Não fazer merge automaticamente.

## 6. Critérios de aceite

- [x] Cada autora tem no máximo um caderno por paciente/categoria, independente de sessão.
- [x] Os dois caminhos abrem o mesmo conteúdo; categorias vazias podem ser iniciadas pela ficha.
- [x] A lista geral inclui apenas pacientes com caderno salvo pela autora, com paginação completa.
- [x] Não existe autosave; Salvar depende de mudança, autorização e ausência de gravação em curso.
- [x] Gravar e limpar texto criam versões corretas; reenvios sem mudança não duplicam versões.
- [x] Histórico paginado permite ver conteúdo e diferenças em modal grande.
- [x] Restaurar grava uma versão nova; Usar no editor não grava; texto pendente exige confirmação.
- [x] Conflitos entre abas preservam o texto e não sobrescrevem uma revisão mais recente.
- [x] Paciente ou vínculo inativo permite leitura da autora e bloqueia escrita e restauração.
- [x] Outra psicóloga, paciente e administrador não acessam conteúdo ou versões pela API.
- [x] Conteúdo e versões são criptografados; falhas não geram plaintext, logs sensíveis ou perda silenciosa.
- [x] Rotação cobre histórico e conteúdo atual, preservando dados e versões clínicas.
- [x] Avisos de saída protegem texto pendente, sem rascunhos persistidos no navegador.
- [x] Erros são específicos em português e caches não misturam pacientes ou autoras.
- [x] Testes cobrem API, RLS, concorrência, paginação, criptografia e recuperação/rotação.
- [x] Web passa typecheck/testes e interfaces seguem o design, com conferência mobile conforme o guia.
- [x] PRs API e web apontam para develop, incluem validação e ordem de publicação.

Este plano implementa Registro Documental. A emissão de declarações/PDFs na aba
Documentos é outra funcionalidade e não integra esta entrega.

## Validação da implementação — 02/10/2026

API: [PR #34](https://github.com/AcolheDevTeam/acolhe-api/pull/34).
Web: [PR #39](https://github.com/AcolheDevTeam/acolhe-web/pull/39).
Ambos usam `feat/registro-documental`, com base em `develop`, sem merge automático.

Verificações locais: testes Go, integração documental com PostgreSQL/RLS e race
detector, lint, aplicação de migrations, testes do configurador, CLI de inventário
/rotação e recuperação de backup isolado. Web: typecheck, 94 testes, build e fluxo
real em Chromium, incluindo conflito entre abas e conferência visual em 390 px.

Os critérios acima registram comportamento implementado e verificado localmente;
não significam publicação em staging/produção. A ativação exige configuração das
chaves e verificação do inventário por ambiente. Registros legados são preservados
integralmente; sua conversão depende de identificar o formato e a chave de origem,
conforme o procedimento do contrato. Custódia e recuperação dos backups reais
permanecem tarefas operacionais do ambiente.
