# Contrato da área do paciente

Todos os endpoints abaixo exigem um JWT com `role=patient`. O paciente é
resolvido exclusivamente pelo `userId` do JWT; não há `patientId` em query,

## Aceite e identidade

- `POST /onboarding/invitations/:token/accept`
  - Público. Corpo: `{ "password": "...", "acceptedDocumentIds": ["..."] }`.
  - O aceite é idempotente e a resposta identifica apenas as identidades criadas.
- `POST /login`
  - Corpo: `{ "email": "...", "password": "..." }`.
- `GET /me`
  - Para paciente, `user.patient` contém `id`, `fullName`,
    `relationshipStatus` e `consented`.

## Dados da home

- `GET /patient/context`
- `GET /patient/next-session` retorna uma sessão ou `null`.
- `GET /patient/pending-activities` retorna somente metadados de atividades
  pendentes/em andamento, nunca dados de revisão clínica.
- `GET /patient/check-ins` e `POST /patient/check-ins` (`mood` de 1 a 5 e
  `note` opcional).
- `GET /patient/process-summary` retorna contagens agregadas de sessões,
  atividades pendentes e check-ins.

Respostas que não conseguem resolver um vínculo ativo usam uma resposta
genérica. Isso evita enumeração de pacientes e de organizações.
