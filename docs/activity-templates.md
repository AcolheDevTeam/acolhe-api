# Contrato da biblioteca de templates de atividade (ACO-66)

Todos os endpoints exigem JWT de `role=psychologist`. O tenant vem do token; nunca
há `organizationId` em query ou corpo. Regras de produto em
`acolhe-web/docs/guia-implementacao.md`, seção C4.

## Conceitos

- **Template** é o modelo de uma tarefa entre sessões (título, instrução, tipo base
  e campos ordenados). **Atividade** é um template atribuído a uma paciente
  (`POST /activities`), que pina `template_id` e `template_version`.
- **Versão.** Editar um template que já foi atribuído cria uma linha nova
  (`parentTemplateId` aponta para a anterior, `version` incrementa). Atividades
  antigas continuam apontando para a versão que a paciente viu. Um template
  sem atribuição é editado no lugar.
- **Arquivar** tira o template da biblioteca e impede novas atribuições. As
  atividades já criadas continuam válidas.
- **Global** (`isGlobal: true`, `organization_id IS NULL`) é curado pela Acolhe,
  visível a todas as organizações e somente leitura. Hoje não existe nenhum
  (seed fica para ACO-67).
- **Tipo base** (`typeCode`): `record` (Formulário), `scale` (Escala),
  `checklist` (Checklist), `checkin` (Check-in). Seed na migration
  `20260909210000_seed_activity_types`.

## Tipos de campo (`fieldType`) e configuração

| `fieldType` | Entrada aceita | `config` gravado |
|---|---|---|
| `short_text` | `maxLength` opcional (1–500, padrão 200) | `{required, helpText?, maxLength}` |
| `long_text` | `maxLength` opcional (1–5000, padrão 2000) | `{required, helpText?, maxLength}` |
| `scale` | `min` e `max` obrigatórios, `min < max`, até 100 pontos; `minLabel`/`maxLabel` opcionais | `{required, helpText?, min, max, minLabel?, maxLabel?}` |
| `single_choice` | `options`: 2–20 textos únicos, até 120 caracteres | `{required, helpText?, options}` |
| `multiple_choice` | idem | idem |
| `boolean` | nada | `{required, helpText?}` |
| `date` | nada | `{required, helpText?}` |
| `datetime` | nada | `{required, helpText?}` |

Comum a todos: `label` (1–200), `helpText` (até 300), `required` (padrão `true`).
O `code` é derivado do `label` (`"Descreva a situação"` → `descreva_a_situacao`),
único dentro do template. Um template tem de 1 a 30 campos; `displayOrder` segue a
ordem do array.

## Endpoints

### `GET /activities/templates`

Biblioteca visível: templates da organização + globais, só a versão mais recente
de cada linhagem, sem arquivados. Ordenado por título.

```json
[{
  "id": "…", "title": "Registro de pensamentos", "type": "record",
  "description": null, "instructions": "…", "version": 2,
  "isGlobal": false, "ownedByMe": true, "fieldCount": 4,
  "createdAt": "…", "updatedAt": "…"
}]
```

### `POST /activities/templates` → 201

```json
{
  "title": "Registro de pensamentos",
  "description": "opcional",
  "instructions": "opcional",
  "typeCode": "record",
  "fields": [
    { "label": "Descreva a situação", "fieldType": "long_text", "helpText": "Quando, onde e com quem?" },
    { "label": "Intensidade da emoção", "fieldType": "scale", "min": 1, "max": 10 },
    { "label": "Qual distorção você reconhece?", "fieldType": "multiple_choice",
      "options": ["Catastrofização", "Leitura mental"], "required": false }
  ]
}
```

Resposta: o detalhe abaixo.

### `GET /activities/templates/:id`

Detalhe com campos. Inclui versões antigas e arquivados (para leitura).

```json
{
  "id": "…", "title": "…", "type": "record", "description": null, "instructions": "…",
  "version": 1, "isGlobal": false, "isArchived": false, "superseded": false,
  "ownedByMe": true, "editable": true, "parentTemplateId": null,
  "assignmentCount": 0,
  "fields": [{
    "id": "…", "code": "descreva_a_situacao", "label": "Descreva a situação",
    "fieldType": "long_text", "displayOrder": 1,
    "config": { "required": true, "helpText": "…", "maxLength": 2000 }
  }],
  "createdAt": "…", "updatedAt": "…"
}
```

`editable` já resume as regras: autora, não global, não arquivado, sem versão mais
nova. `superseded: true` significa que existe versão mais nova; `assignmentCount`
diz se a próxima edição vai gerar versão nova (`> 0`) ou editar no lugar.

### `PUT /activities/templates/:id` → 200

Mesmo corpo do POST (substitui título, instrução, tipo e **todos** os campos).

- `assignmentCount == 0`: edita no lugar; resposta tem o mesmo `id`.
- `assignmentCount > 0`: cria a versão seguinte; resposta tem `id` **novo** e
  `parentTemplateId` = id enviado. O web deve navegar para o novo id.

### `POST /activities/templates/:id/archive` → 200

Devolve o detalhe com `isArchived: true`.

## Erros

| Status | Quando | Mensagem |
|---|---|---|
| 400 | corpo inválido | específica, em português, com a posição do campo: `"Campo 3: a escala precisa de valor mínimo e máximo"`, `"Tipo base desconhecido"` |
| 403 | não é psicóloga; template global; template de outra autora | `perfil de psicólogo obrigatório` / `este template não pode ser alterado por você` |
| 404 | template inexistente ou de outra organização | `template não encontrado` |
| 409 | arquivado; existe versão mais nova | `template arquivado não pode ser alterado` / `existe uma versão mais nova deste template` |

## Efeitos em endpoints existentes

- `GET /activities/templates` ganhou `instructions`, `isGlobal`, `ownedByMe`,
  `fieldCount`, `updatedAt` e passou a esconder versões antigas.
- `POST /activities` (atribuir) recusa versões antigas e arquivados com 404.

## Pendências relacionadas

- ACO-68: a paciente ainda não consegue responder (o `POST .../responses` grava
  resposta vazia). O `config.required` gravado aqui é o insumo para essa validação.
- ACO-67: seed de templates globais.
