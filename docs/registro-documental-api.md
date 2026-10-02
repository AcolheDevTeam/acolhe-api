# Registro Documental — contrato implementado

Entrega em `feat/registro-documental`, sobre `develop`. A API deve ser publicada
com migração e configuração criptográfica antes do frontend. Não há autosave,
exclusão, arquivamento ou exportação documental.

## Contrato HTTP

Todas as rotas exigem JWT de psicóloga. Organização e autoria são resolvidas pelo
servidor. Respostas privadas usam `Cache-Control: private, no-store`.

| Método e rota | Corpo / resultado |
| --- | --- |
| GET `/documentary/patients?page=1&pageSize=20` | Pacientes com cadernos da autora: `id`, `fullName`, `writable` |
| GET `/documentary/patients/:patientId` | `{patient: {id, fullName, writable}, items: Notebook[]}` |
| PUT `/documentary/patients/:patientId/:category` | `{content, expectedRevision}` → Notebook |
| GET `/documentary/notebooks/:id/versions?page=1&pageSize=20` | Histórico sem texto, paginado |
| GET `/documentary/notebooks/:id/versions/:revision` | Versão com `content` |
| POST `/documentary/patients/:patientId/:category/restore` | `{revision, expectedRevision}` → Notebook |

`Notebook`: `id`, `patientId`, `category`, `content`, `revision`, `updatedAt`.
Versão: `id`, `revision`, `createdAt`, `restoredFrom` (nulo ou revisão original).
Datas são RFC3339, incluindo offset de fuso e frações de segundo.

Categorias: `hypothesis`, `technical_observation`, `planning`, `transcription`,
`other`. Texto UTF-8 de até 200.000 bytes; não aparar espaços. Revisão inicial
esperada: zero. Reenvio de conteúdo idêntico retorna o estado atual sem criar
versão; conteúdo divergente com revisão obsoleta retorna 409.

As duas listas retornam `{items,totalCount,totalPages,page,pageSize}`. Página
inicial 1, padrão 20, tamanho máximo 100, página máxima 1.000.000. Lista vazia:
`items: []`, `totalCount: 0`, `totalPages: 0`. Ordenação de pacientes por nome/id;
versões por revisão decrescente/id. Os totais não dependem do tamanho da página.

Erros: 400 entrada inválida; 401 sessão inválida; 403 papel ou escrita não
permitida; 404 recurso fora do escopo; 409 conflito de revisão; 503 configuração
ou integridade criptográfica indisponível. Nenhum erro devolve conteúdo/chaves.

## Transações e isolamento

O domínio abre sua própria transação com o contexto RLS e confirma a gravação
antes de serializar o resultado. O middleware de transação genérico ignora
somente `/documentary/`. A auditoria HTTP continua ativa, sem registrar texto.

Um advisory lock por organização/autora/paciente/categoria serializa também o
primeiro salvamento. Locks de linha no paciente, vínculo e consentimento impedem
inativação concorrente durante a gravação. A revisão é conferida após adquirir
os locks; conteúdo atual e snapshot são gravados na mesma transação. Cada envelope
recebe nonce independente. RLS e filtros explícitos limitam autoria/organização.
O histórico tem FK composta para impedir divergência de escopo com o caderno.

Leituras de cadernos existentes não exigem vínculo ativo. Gravações exigem
paciente ativa, sem `deleted_at`, e vínculo validado por
`has_active_clinical_relationship`. Nenhum endpoint é exposto ao paciente ou a
administradores da aplicação.

## Registros legados

O modelo anterior aceitava paciente nula, várias entradas da mesma categoria,
sessão e tags. Não existia código de gravação/leitura nem formato criptográfico
publicado. Interpretar bytes antigos como plaintext ou como o novo envelope
seria inseguro.

A migração renomeia a tabela antiga para `documentary_record_legacy`, conserva
**todos os IDs, bytes, datas, sessões e tags**, aplica acesso de leitura exclusivo
à autora e cria a nova tabela de cadernos. O inventário administrativo informa
`legacy`; esses registros não são descartados nem fingem ser cadernos vazios.

Antes de ativar em um ambiente com `legacy > 0`, inventariar as origens e resolver
um mapeamento supervisionado: identificar formato/chave antigos; obter uma
paciente válida para entradas soltas; agrupar por organização/autora/paciente/
categoria; ordenar por `created_at,id`; preservar cada texto como snapshot,
consolidando o conteúdo contínuo com a autora. Sessões, títulos e tags antigos
continuam no acervo legado. Não há conversor automático para um formato de
criptografia desconhecido. A migração estrutural não fica bloqueada nem perde
registros; a conversão clínica exige essa identificação antes da ativação.

## Validação

- `go test ./...`
- `go test -tags integration ./internal/app -run TestDocumentary -count=1`
- `WORKFLOW_TEST_DATABASE_URL=...` permite executar esses testes em PostgreSQL
  local; cada teste cria e remove seu próprio banco, sem usar dados do ambiente.
- `sqlc generate` (v1.31.1), `atlas migrate validate`, `golangci-lint run ./...`.

Os testes documentais cobrem texto/Unicode, integridade autenticada, configuração
inválida, RLS real sem BYPASSRLS, autorização, versões, restauração, paginação,
concorrência inicial e posterior, paciente/vínculo inativo, recusa de sobrescrever
ciphertext corrompido, migração de legados e rotação em lotes retomável.
