# ADR 0001: Consent-gated onboarding and typed activity responses

- Status: accepted for the MVP implementation
- Scope: ACO-48, ACO-49, ACO-50, ACO-51
- Date: 2026-07-29

## Context

Patient creation currently makes both the profile and relationship active without
patient consent. The `consent` table does not identify the immutable document that
was accepted, and the generic request audit runs after the request transaction.

Activity submission currently creates an empty response, while review trusts only
the assignment status. The detail endpoint does not return template fields and
typed values. This permits a submitted or reviewed assignment with no integral
patient response.

The implementation must retain the existing Go handler/service/sqlc structure and
the Nuxt server BFF. It must not assume an email provider.

## Decision

### Patient onboarding

`POST /patients` creates one aggregate in the authenticated request transaction:

```text
patient_profile.status      = onboarding
patient_relationship.status = pending
patient_invitation.status   = pending
```

Creation and invitation reissue are idempotent. The API returns a newly generated
high-entropy invitation token once; only its SHA-256 digest is stored. The Nuxt BFF
turns the token into a same-origin URL for manual copy/share. No response claims
that an email was sent.

The invitation page reads published, immutable consent documents from the API.
Acceptance creates the patient user and performs the only onboarding-to-active
transition:

```text
patient user created
accepted consent ledger rows inserted
health_data consent linked to relationship
relationship pending -> active
profile onboarding -> active
invitation pending -> accepted
semantic audit row inserted
```

These writes happen in one restricted `SECURITY DEFINER`
`accept_patient_invitation` function with a fixed `search_path`, no dynamic SQL,
locked invitation/relationship rows, and execute permission only for
`acolhe_app`. Go hashes the password, validates the request, and calls the
function through sqlc. The function validates and consumes the token digest.

An acceptance retry returns the original activation result and writes no second
user, consent, transition, or audit row.

Existing active relationships are not rewritten and no consent evidence is
invented. The migration records them as an explicit legacy cohort that can remain
active, while every relationship created after the migration requires a valid
health-data consent to transition to active. A preflight count is required so the
legacy cohort is visible and can be remediated separately.

### Clinical write boundary

Every patient-bound mutation embeds the active-care predicate inside its
`INSERT` or `UPDATE`; a prior existence read is not authoritative. This applies to
appointments, sessions, documents, check-ins, activity assignment, response
submission, and review.

For consent-gated relationships, active care means:

```text
profile active
relationship active
same organization and owning psychologist/patient
relationship consent points to the patient's accepted health_data document
```

RLS repeats the same ownership and state predicate as a second boundary. API and
worker run as `acolhe_app`, which is `NOSUPERUSER NOBYPASSRLS`; migrations use the
schema owner.

### Activity submission and review

The MVP accepts one final immutable submission per assignment. Draft editing and
revision history are outside this decision.

The request contains a stable client-generated `submissionId`, the pinned
`templateVersion`, and a discriminated value for every ordered field. Go performs
syntactic decoding at the HTTP boundary, then a pure validator checks:

- assignment ownership, organization, patient identity, and active care;
- exact pinned template version;
- exact field ID set, with no missing, extra, or duplicate field;
- field order;
- field kind and configured value constraints.

The database enforces one response per assignment, one value per response/field,
field-to-template coherence, and exactly one typed storage column. Response,
values, and assignment `submitted` status commit together.

`GET /activities/:id` returns a closed detail union:

```text
awaiting_response            -> metadata, no submission
closed_without_submission    -> metadata, no submission
submitted                    -> complete ordered submission
reviewed                     -> same complete submission + reviewedAt
submission_invalid           -> legacy metadata, never reviewable
```

Only `submitted` carries a review action. `PUT /activities/:id/review` is an
idempotent domain command, not a generic status setter. Its SQL update repeats
assignment ownership and complete-response predicates and preserves the first
review timestamp.

### Web boundary and UI

The browser continues to call only Nuxt `server/api/*` routes. New BFF routes:

- validate browser inputs with Zod;
- treat Go responses as `unknown` and parse them with Zod;
- derive TypeScript types from those schemas;
- keep the Go bearer token in the HTTP-only session cookie.

Patient state is a discriminated union. Onboarding pages do not mount clinical
activity or timeline requests and do not render clinical actions. The UI maps to
the project references:

- screen 03: narrow invitation and consent flow;
- screen 05: active/onboarding/archived patient groups;
- screen 06: active-only clinical actions and exact consent metadata;
- screen 14: ordered answers on the left and fail-closed review controls on the
  right.

## Consequences

- A small onboarding module is added because public invitation authentication has
  a different transaction boundary from psychologist patient management.
- Consent documents, consent ledger rows, submitted responses, and audit rows are
  append-only.
- Published template versions and their fields become immutable once assigned.
- Legacy empty responses remain visible only as `submission_invalid`; the
  migration does not fabricate answers.
- Invitation delivery remains manual until a provider is explicitly selected.
- Refusal, consent revocation, response drafts, tags, and review queue navigation
  require separate product decisions.

## Verification required

- PostgreSQL integration tests for onboarding creation, every pre-consent clinical
  write, atomic activation/audit rollback, replay, token rotation, and cross-tenant
  RLS under `acolhe_app`.
- Validator coverage for every supported value kind and missing, extra, duplicate,
  reordered, wrong-kind, invalid-choice, out-of-range, and wrong-version input.
- Integration tests proving response/value/status atomicity, ordered detail,
  fail-closed legacy rows, review rejection, and idempotent review.
- Web typecheck plus BFF contract tests and visual checks of the four reference
  screens, including absence of clinical requests while onboarding.

## Alternatives rejected

- Psychologist-supplied consent or activation: it does not prove patient consent.
- Asynchronous generic audit for activation: it can commit independently.
- A JSON response blob: it weakens typed storage and field ownership.
- Review based on assignment status: it preserves the empty-response bug.
- Pre-consent invited users or automatic login after acceptance: unnecessary
  identity and session scope for these issues.
- A generic activity status patch: it exposes transitions unrelated to review.
