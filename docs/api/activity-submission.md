# Paciente responde atividade

Fecha o ciclo biblioteca → atribuir → **responder** → revisão (ACO-68). Implementa
a parte de submissão do ADR 0001, que até aqui só existia no lado da leitura.

Os dois endpoints são do portal da paciente: exigem papel `patient` e só enxergam
atribuições da própria paciente, na própria organização.

## `GET /patient/activities/:id`

Devolve o formulário da **versão pinada** do template, para a paciente montar a tela.

```json
{
  "id": "9c38ea43-...",
  "status": "pending",
  "title": "Registro de pensamentos",
  "type": "record",
  "description": null,
  "instructions": "Preencha logo após a situação.",
  "templateVersion": 1,
  "scheduledFor": null,
  "dueAt": "2026-09-14T12:00:00Z",
  "submittedAt": null,
  "canRespond": true,
  "fields": [
    {
      "id": "0f1e...",
      "code": "descreva_a_situacao",
      "label": "Descreva a situação",
      "fieldType": "long_text",
      "displayOrder": 1,
      "config": { "required": true, "maxLength": 2000 }
    }
  ]
}
```

`canRespond` é `true` só quando ainda não há resposta final e o status é `pending`
ou `in_progress`. Os campos vêm ordenados por `displayOrder` — é a ordem em que a
paciente responde e a ordem exigida no envio.

Erros: `403` sem papel de paciente, `404` atribuição inexistente ou de outra
paciente/organização, `409` se o template foi editado e a versão pinada não é mais
a atual (`recarregue o formulário`).

## `POST /activities/assignments/:id/responses`

Envio final. Não existe rascunho no servidor neste escopo.

```json
{
  "submissionId": "6b1f...",
  "templateVersion": 1,
  "values": [
    { "fieldCode": "descreva_a_situacao", "kind": "long_text", "text": "..." },
    { "fieldCode": "intensidade_da_emocao", "kind": "scale", "number": 8 },
    { "fieldCode": "qual_distorcao", "kind": "multiple_choice", "choices": ["Catastrofização"] }
  ]
}
```

`submissionId` é um UUID gerado pelo cliente e é o que torna o reenvio seguro:
repetir a mesma submissão devolve a resposta original em vez de criar outra ou
falhar. Um `submissionId` **diferente** numa atividade já respondida é `409`.

`kind` precisa ser igual ao `fieldType` do campo. O cliente declara o que está
mandando e a divergência é erro, não coerção silenciosa — assim um bug de tela
aparece como erro claro em vez de gravar lixo tipado.

### Valor por tipo de campo

| `fieldType` | chave no valor | coluna gravada | o que a revisão lê |
|---|---|---|---|
| `short_text`, `long_text` | `text` | `value_text` | `text` |
| `single_choice` | `choice` | `value_text` | `text` |
| `scale` | `number` (inteiro) | `value_number` | `number` |
| `boolean` | `boolean` | `value_boolean` | `boolean` |
| `date` (`AAAA-MM-DD`) | `date` | `value_datetime` | `datetime` |
| `datetime` (RFC 3339) | `datetime` | `value_datetime` | `datetime` |
| `multiple_choice` | `choices` | `value_json` | `json` |

`date` é gravado como meia-noite UTC do dia informado. O `fieldType` viaja junto na
revisão, então a tela distingue data de data-hora mesmo com a mesma coluna.

### Validação

Conforme o ADR 0001, o corpo é recusado inteiro (`400`, nenhuma escrita) quando:

- a quantidade de valores difere da quantidade de campos;
- algum `fieldCode` não corresponde ao campo daquela posição (ordem importa);
- há campo repetido;
- `kind` não bate com o `fieldType`;
- um valor viola o `config` do campo: obrigatório em branco, texto acima do
  `maxLength`, escala fora de `min`/`max` ou fracionada, opção inexistente, opção
  repetida em múltipla escolha, data ou data-hora em formato inválido.

Campo **opcional em branco também é recusado**: o banco exige exatamente um valor
por campo (trigger de `20260729072000`), então não existe resposta parcial. A
mensagem diz isso em vez de alegar obrigatoriedade.

Toda mensagem de erro nomeia a pergunta e o motivo, em português, e vai inteira
para a tela (regra A6 do guia do frontend).

### Atomicidade e integridade

Resposta, valores e o status `submitted` entram na **mesma transação da
requisição** (middleware `TenantTx`): uma recusa não deixa rastro — nem resposta
órfã, nem status trocado.

O banco é a segunda camada e já existia desde ACO-50/51: os triggers de
`20260729072000` garantem uma resposta final por atribuição, exatamente uma coluna
tipada por valor, um valor por campo, coerência entre campo e template da
atribuição, e append-only depois de submetido.

Depois do envio, `activity_submission_is_complete` passa a devolver `true` e
`PUT /activities/:id/review` fica liberado para a psicóloga.

## Fora deste escopo

Rascunho persistido no servidor, comentário da paciente, campos Arquivo e Tags,
pontuação automática (`summary_score` é gravado nulo) e recorrência.
