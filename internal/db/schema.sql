-- Acolhe — schema (PostgreSQL)
-- Fonte ÚNICA da verdade: Atlas gera migrations daqui e sqlc gera tipos Go daqui.
-- Derivado do ERD v0.1. Cobre os quatro bounded contexts:
-- Identidade & Multi-tenancy, Clínico, Atividades, Operacional.
--
-- NOTA: sem BEGIN/COMMIT — Atlas e sqlc esperam DDL puro (declarativo).

CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- gen_random_uuid()

-- ============================================================
-- 1. Identidade & Multi-tenancy
-- ============================================================

CREATE TABLE plan (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code          text UNIQUE NOT NULL,
  name          text NOT NULL,
  max_patients  integer,
  price_cents   integer NOT NULL DEFAULT 0,
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE organization (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text NOT NULL,
  slug        text UNIQUE NOT NULL,
  cnpj        text,
  plan_id     uuid REFERENCES plan(id),
  status      text NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE "user" (
  id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id             uuid REFERENCES organization(id),   -- nulo p/ platform_admin
  email                       text NOT NULL,
  password_hash               text NOT NULL,
  role                        text NOT NULL CHECK (role IN ('platform_admin','org_admin','psychologist','patient')),
  status                      text NOT NULL DEFAULT 'active',
  two_factor_enabled          boolean NOT NULL DEFAULT false,
  two_factor_secret_encrypted bytea,
  last_login_at               timestamptz,
  locale                      text NOT NULL DEFAULT 'pt-BR',
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE psychologist_profile (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid NOT NULL REFERENCES "user"(id),
  full_name     text NOT NULL,
  crp_number    text NOT NULL,
  crp_state     text NOT NULL,
  crp_status    text NOT NULL DEFAULT 'active',
  approach      text,
  cpf_encrypted bytea,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE patient_profile (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         uuid REFERENCES "user"(id),   -- nulo até aceitar convite
  organization_id uuid NOT NULL REFERENCES organization(id),
  full_name       text NOT NULL,
  cpf_encrypted   bytea,
  birth_date      date,
  status          text NOT NULL DEFAULT 'onboarding'
                    CHECK (status IN ('onboarding','active','archived','deleted')),
  deleted_at      timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE consent_document (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  scope          text NOT NULL CHECK (scope IN ('health_data','communications','aggregate_statistics','terms_of_use','privacy_policy')),
  version        text NOT NULL,
  title          text NOT NULL,
  content        text NOT NULL,
  content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
  required       boolean NOT NULL DEFAULT false,
  published_at   timestamptz NOT NULL,
  retired_at     timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (scope, version)
);

CREATE TABLE consent (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          uuid NOT NULL REFERENCES "user"(id),
  patient_id       uuid REFERENCES patient_profile(id),
  document_id      uuid NOT NULL REFERENCES consent_document(id),
  accepted         boolean NOT NULL,
  decided_at       timestamptz NOT NULL DEFAULT now(),
  ip_address       inet,
  user_agent       text,
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE subscription (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     uuid NOT NULL REFERENCES organization(id),
  plan_id             uuid NOT NULL REFERENCES plan(id),
  status              text NOT NULL DEFAULT 'active',
  current_period_end  timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

-- ============================================================
-- 2. Clínico (Agenda & Prontuário)
-- ============================================================

CREATE TABLE patient_relationship (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id       uuid NOT NULL REFERENCES patient_profile(id),
  psychologist_id  uuid NOT NULL REFERENCES psychologist_profile(id),
  consent_id       uuid REFERENCES consent(id),
  status           text NOT NULL CHECK (status IN ('pending','active','paused','ended','transferred')),
  requires_health_consent boolean NOT NULL DEFAULT true,
  end_reason       text CHECK (end_reason IN ('patient_initiated','psychologist_initiated','transfer','archived','other')),
  started_at       timestamptz,
  ended_at         timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE patient_invitation (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id         uuid NOT NULL REFERENCES patient_profile(id),
  relationship_id    uuid NOT NULL REFERENCES patient_relationship(id),
  email              text NOT NULL,
  token_digest       bytea UNIQUE NOT NULL,
  idempotency_key    uuid NOT NULL,
  created_by_user_id uuid NOT NULL REFERENCES "user"(id),
  status             text NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','accepted','declined','revoked','expired')),
  expires_at         timestamptz NOT NULL,
  accepted_at        timestamptz,
  declined_at        timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (created_by_user_id, idempotency_key)
);

CREATE TABLE appointment (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id       uuid NOT NULL REFERENCES patient_profile(id),
  psychologist_id  uuid NOT NULL REFERENCES psychologist_profile(id),
  scheduled_for    timestamptz NOT NULL,
  duration_minutes integer NOT NULL DEFAULT 50,
  modality         text NOT NULL DEFAULT 'in_person' CHECK (modality IN ('in_person','online')),
  status           text NOT NULL DEFAULT 'scheduled'
                     CHECK (status IN ('scheduled','confirmed','completed','canceled','no_show')),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE session (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  appointment_id   uuid REFERENCES appointment(id),   -- nulo p/ sessões avulsas
  patient_id       uuid NOT NULL REFERENCES patient_profile(id),
  psychologist_id  uuid NOT NULL REFERENCES psychologist_profile(id),
  occurred_at      timestamptz NOT NULL,
  status           text NOT NULL DEFAULT 'completed',
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Prontuário: acessível ao paciente (CFP 01/2009 Art. 5º, II)
CREATE TABLE clinical_record (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id       uuid NOT NULL UNIQUE REFERENCES session(id),
  patient_id       uuid NOT NULL REFERENCES patient_profile(id),
  psychologist_id  uuid NOT NULL REFERENCES psychologist_profile(id),
  content_jsonb    jsonb NOT NULL DEFAULT '{}'::jsonb,
  version          integer NOT NULL DEFAULT 1,
  locked_at        timestamptz,
  locked_reason    text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE clinical_record_version (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinical_record_id uuid NOT NULL REFERENCES clinical_record(id),
  version_number     integer NOT NULL,
  content_snapshot   jsonb NOT NULL,
  changed_by         uuid NOT NULL REFERENCES "user"(id),
  change_reason      text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (clinical_record_id, version_number)
);

-- Registro Documental: estritamente do psicólogo. Tabela separada de propósito.
CREATE TABLE documentary_record (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  author_id         uuid NOT NULL REFERENCES psychologist_profile(id),
  patient_id        uuid REFERENCES patient_profile(id),   -- nulo p/ anotações soltas
  session_id        uuid REFERENCES session(id),
  category          text NOT NULL CHECK (category IN ('hypothesis','technical_observation','planning','transcription','other')),
  content_encrypted bytea NOT NULL,
  tags              text[],
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE document_template (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code             text UNIQUE NOT NULL,
  type             text NOT NULL,
  content_template text NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE document (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id       uuid NOT NULL REFERENCES patient_profile(id),
  psychologist_id  uuid NOT NULL REFERENCES psychologist_profile(id),
  template_id      uuid REFERENCES document_template(id),
  type             text NOT NULL,
  pdf_url          text,
  signature_hash   text,
  created_at       timestamptz NOT NULL DEFAULT now()
);

-- ============================================================
-- 3. Atividades (sistema extensível)
-- ============================================================

CREATE TABLE activity_type (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code       text UNIQUE NOT NULL,
  name       text NOT NULL,
  is_active  boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE activity_template (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  type_id            uuid NOT NULL REFERENCES activity_type(id),
  organization_id    uuid REFERENCES organization(id),
  author_id          uuid NOT NULL REFERENCES psychologist_profile(id),
  parent_template_id uuid REFERENCES activity_template(id),   -- nulo p/ 1ª versão
  title              text NOT NULL,
  description        text,
  instructions       text,
  recurrence_config  jsonb,
  scoring_config     jsonb,
  version            integer NOT NULL DEFAULT 1,
  is_archived        boolean NOT NULL DEFAULT false,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE activity_field (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  template_id   uuid NOT NULL REFERENCES activity_template(id),
  code          text NOT NULL,
  label         text NOT NULL,
  field_type    text NOT NULL,
  config        jsonb,
  display_order integer NOT NULL DEFAULT 0,
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE activity_assignment (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  template_id          uuid NOT NULL REFERENCES activity_template(id),
  template_version     integer NOT NULL,
  patient_id           uuid NOT NULL REFERENCES patient_profile(id),
  assigner_id          uuid NOT NULL REFERENCES psychologist_profile(id),
  recurrence_parent_id uuid REFERENCES activity_assignment(id),
  status               text NOT NULL CHECK (status IN ('pending','in_progress','submitted','reviewed','expired','canceled')),
  scheduled_for        timestamptz,
  due_at               timestamptz,
  reviewed_at          timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE activity_response (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  assignment_id uuid NOT NULL REFERENCES activity_assignment(id),
  submission_id uuid,                                -- gerado pelo cliente; replay idempotente
  submitted_at  timestamptz,
  is_draft      boolean NOT NULL DEFAULT true,
  summary_score numeric,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE attachment (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id    uuid NOT NULL REFERENCES "user"(id),
  patient_id  uuid REFERENCES patient_profile(id),
  mime_type   text NOT NULL,
  size_bytes  integer NOT NULL DEFAULT 0,
  storage_key text NOT NULL,
  scan_status text NOT NULL DEFAULT 'pending' CHECK (scan_status IN ('pending','clean','infected')),
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE activity_response_value (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  response_id    uuid NOT NULL REFERENCES activity_response(id),
  field_id       uuid NOT NULL REFERENCES activity_field(id),
  field_code     text NOT NULL,
  value_text     text,
  value_number   numeric,
  value_boolean  boolean,
  value_datetime timestamptz,
  value_json     jsonb,
  attachment_id  uuid REFERENCES attachment(id),   -- só em campos do tipo file
  created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE activity_comment (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  response_id uuid NOT NULL REFERENCES activity_response(id),
  author_id   uuid NOT NULL REFERENCES "user"(id),
  visibility  text NOT NULL DEFAULT 'shared' CHECK (visibility IN ('shared','private')),
  content     text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- Check-in: auto-registro de humor/estado do paciente entre sessões.
CREATE TABLE checkin (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id  uuid NOT NULL REFERENCES patient_profile(id),
  mood        integer NOT NULL CHECK (mood BETWEEN 1 AND 5),
  note        text,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_checkin_patient_date ON checkin (patient_id, created_at DESC);

-- ============================================================
-- 4. Operacional & Observabilidade
-- ============================================================

CREATE TABLE notification (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid NOT NULL REFERENCES "user"(id),
  channel       text NOT NULL,
  template_name text NOT NULL,
  payload_jsonb jsonb,             -- sem dados sensíveis
  sent_at       timestamptz,
  read_at       timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_user_id   uuid NOT NULL REFERENCES "user"(id),
  organization_id uuid REFERENCES organization(id),
  action          text NOT NULL,
  resource_type   text NOT NULL,
  resource_id     text NOT NULL,
  ip_address      inet,
  user_agent      text,
  metadata_jsonb  jsonb,
  occurred_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE lgpd_export_request (
  id              uuid PRIMARY KEY,
  patient_id      uuid NOT NULL REFERENCES patient_profile(id),
  organization_id uuid NOT NULL REFERENCES organization(id),
  requested_by    uuid NOT NULL REFERENCES "user"(id),
  requested_at    timestamptz NOT NULL,
  sla_deadline    timestamptz NOT NULL,
  status          text NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued','processing','stored','completed','failed')),
  attempts        integer NOT NULL DEFAULT 0,
  object_key      text,
  artifact_sha256 text CHECK (artifact_sha256 IS NULL OR artifact_sha256 ~ '^[0-9a-f]{64}$'),
  completed_at    timestamptz,
  notified_at     timestamptz,
  last_error      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CHECK (sla_deadline = requested_at + interval '24 hours'),
  CHECK (
    (status IN ('stored','completed') AND object_key IS NOT NULL AND artifact_sha256 IS NOT NULL)
    OR status IN ('queued','processing','failed')
  ),
  CHECK (
    (status = 'completed' AND completed_at IS NOT NULL AND notified_at IS NOT NULL)
    OR status <> 'completed'
  )
);

-- Consultável pelo pipeline de observabilidade sem expor conteúdo do artefato.
-- A view herda RLS da tabela base (security_invoker, PostgreSQL 15+).
CREATE VIEW lgpd_export_sla_metric
  WITH (security_invoker = true) AS
SELECT organization_id,
       count(*) FILTER (
         WHERE status <> 'completed' AND now() > sla_deadline
       )::integer AS breached_pending,
       count(*) FILTER (
         WHERE status = 'completed' AND completed_at > sla_deadline
       )::integer AS breached_completed,
       count(*) FILTER (
         WHERE status IN ('queued','processing','stored')
       )::integer AS pending,
       max(EXTRACT(epoch FROM (
         COALESCE(completed_at, now()) - requested_at
       ))) AS max_duration_seconds
FROM lgpd_export_request
GROUP BY organization_id;

-- ============================================================
-- 5. Constraints não óbvias
-- ============================================================

-- Um Appointment realizado gera no máximo uma Session
CREATE UNIQUE INDEX idx_session_appointment
  ON session (appointment_id) WHERE appointment_id IS NOT NULL;

-- Paciente só pode ter um relacionamento ativo por vez (MVP)
CREATE UNIQUE INDEX idx_relationship_active_unique
  ON patient_relationship (patient_id) WHERE status = 'active';

CREATE UNIQUE INDEX idx_consent_patient_document
  ON consent (patient_id, document_id);

CREATE UNIQUE INDEX idx_invitation_pending_relationship
  ON patient_invitation (relationship_id) WHERE status = 'pending';

-- Email único globalmente (case-insensitive)
CREATE UNIQUE INDEX idx_user_email ON "user" (lower(email));

-- CRP único
CREATE UNIQUE INDEX idx_psychologist_crp ON psychologist_profile (crp_number, crp_state);

-- ============================================================
-- 6. Índices essenciais
-- ============================================================

CREATE INDEX idx_user_organization ON "user" (organization_id);
CREATE INDEX idx_psychologist_user ON psychologist_profile (user_id);
CREATE INDEX idx_patient_organization ON patient_profile (organization_id);
CREATE INDEX idx_patient_user ON patient_profile (user_id);
CREATE INDEX idx_patient_status ON patient_profile (organization_id, status);

CREATE INDEX idx_relationship_patient ON patient_relationship (patient_id);
CREATE INDEX idx_relationship_psychologist ON patient_relationship (psychologist_id);
CREATE INDEX idx_invitation_patient ON patient_invitation (patient_id);
CREATE INDEX idx_invitation_expires ON patient_invitation (expires_at) WHERE status = 'pending';

CREATE INDEX idx_appointment_psychologist_date ON appointment (psychologist_id, scheduled_for);
CREATE INDEX idx_appointment_patient_date ON appointment (patient_id, scheduled_for);
CREATE INDEX idx_appointment_status ON appointment (status);

CREATE INDEX idx_session_patient_date ON session (patient_id, occurred_at DESC);
CREATE INDEX idx_session_psychologist_date ON session (psychologist_id, occurred_at DESC);
CREATE INDEX idx_clinical_version_record_version
  ON clinical_record_version (clinical_record_id, version_number DESC);

CREATE INDEX idx_documentary_author ON documentary_record (author_id);
CREATE INDEX idx_documentary_patient ON documentary_record (patient_id) WHERE patient_id IS NOT NULL;

CREATE INDEX idx_template_author ON activity_template (author_id);
CREATE INDEX idx_template_type ON activity_template (type_id);
CREATE INDEX idx_template_org ON activity_template (organization_id);
CREATE INDEX idx_field_template_order ON activity_field (template_id, display_order);
CREATE INDEX idx_assignment_patient_status ON activity_assignment (patient_id, status);
CREATE INDEX idx_assignment_due ON activity_assignment (due_at)
  WHERE status IN ('pending', 'in_progress');
CREATE INDEX idx_assignment_recurrence_parent ON activity_assignment (recurrence_parent_id)
  WHERE recurrence_parent_id IS NOT NULL;
CREATE INDEX idx_response_assignment ON activity_response (assignment_id);
CREATE UNIQUE INDEX activity_response_submission_id_key
  ON activity_response (submission_id)
  WHERE submission_id IS NOT NULL;
CREATE INDEX idx_response_value_response ON activity_response_value (response_id);
CREATE INDEX idx_response_value_field ON activity_response_value (field_id);
CREATE INDEX idx_response_value_field_time ON activity_response_value (field_id, response_id);

CREATE INDEX idx_attachment_patient ON attachment (patient_id);
CREATE INDEX idx_attachment_scan_status ON attachment (scan_status)
  WHERE scan_status IN ('pending', 'infected');

CREATE INDEX idx_notification_user_unread ON notification (user_id, read_at) WHERE read_at IS NULL;

CREATE INDEX idx_audit_actor_time ON audit_log (actor_user_id, occurred_at DESC);
CREATE INDEX idx_audit_org_time ON audit_log (organization_id, occurred_at DESC);
CREATE INDEX idx_audit_resource ON audit_log (resource_type, resource_id);
CREATE INDEX idx_lgpd_export_sla_pending
  ON lgpd_export_request (sla_deadline)
  WHERE status IN ('queued','processing','stored');
CREATE INDEX idx_lgpd_export_patient_time
  ON lgpd_export_request (patient_id, requested_at DESC);

-- ============================================================
-- 7. Row-Level Security (2ª camada de defesa)
-- A 1ª camada é o middleware da aplicação (organization_id explícito nas queries).
-- A aplicação seta o contexto por transação via SET LOCAL acolhe.* (ver middleware/tenant.go).
-- ============================================================

CREATE OR REPLACE FUNCTION current_organization_id() RETURNS uuid AS $$
  SELECT NULLIF(current_setting('acolhe.organization_id', true), '')::uuid
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION current_user_id() RETURNS uuid AS $$
  SELECT NULLIF(current_setting('acolhe.user_id', true), '')::uuid
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION current_psychologist_id() RETURNS uuid AS $$
  SELECT NULLIF(current_setting('acolhe.psychologist_id', true), '')::uuid
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION current_user_role() RETURNS text AS $$
  SELECT current_setting('acolhe.user_role', true)
$$ LANGUAGE SQL STABLE;

-- Consents are legal evidence: append-only for every application role.
CREATE OR REPLACE FUNCTION reject_consent_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'consent records are append-only' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER consent_append_only
  BEFORE UPDATE OR DELETE ON consent
  FOR EACH ROW EXECUTE FUNCTION reject_consent_mutation();

-- A clinical write can only target an active relationship. New relationships
-- additionally require an accepted, published health-data consent. The
-- explicit false branch preserves the pre-consent legacy cohort.
CREATE OR REPLACE FUNCTION has_active_clinical_relationship(
  target_patient_id uuid,
  target_psychologist_id uuid
) RETURNS boolean AS $$
  SELECT EXISTS (
    SELECT 1
    FROM patient_relationship r
    WHERE r.patient_id = target_patient_id
      AND r.psychologist_id = target_psychologist_id
      AND r.status = 'active'
      AND (
        NOT r.requires_health_consent
        OR EXISTS (
          SELECT 1
          FROM consent c
          JOIN consent_document d ON d.id = c.document_id
          WHERE c.id = r.consent_id
            AND c.patient_id = r.patient_id
            AND c.accepted
            AND d.scope = 'health_data'
            AND d.published_at <= c.decided_at
            AND (d.retired_at IS NULL OR c.decided_at < d.retired_at)
        )
      )
  )
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION enforce_active_clinical_relationship() RETURNS trigger AS $$
DECLARE
  target_psychologist uuid;
BEGIN
  target_psychologist := (to_jsonb(NEW)->>TG_ARGV[0])::uuid;
  IF NOT has_active_clinical_relationship(NEW.patient_id, target_psychologist) THEN
    RAISE EXCEPTION 'active consented relationship required' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION enforce_relationship_activation() RETURNS trigger AS $$
BEGIN
  IF NEW.status = 'active' AND NEW.requires_health_consent AND (
    NEW.consent_id IS NULL OR NOT EXISTS (
      SELECT 1
      FROM consent c
      JOIN consent_document d ON d.id = c.document_id
      WHERE c.id = NEW.consent_id
        AND c.patient_id = NEW.patient_id
        AND c.accepted
        AND d.scope = 'health_data'
        AND d.published_at <= c.decided_at
        AND (d.retired_at IS NULL OR c.decided_at < d.retired_at)
    )
  ) THEN
    RAISE EXCEPTION 'valid health consent required for activation' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER relationship_consent_activation
  BEFORE INSERT OR UPDATE OF status, consent_id ON patient_relationship
  FOR EACH ROW EXECUTE FUNCTION enforce_relationship_activation();

CREATE TRIGGER appointment_active_relationship
  BEFORE INSERT OR UPDATE OF patient_id, psychologist_id ON appointment
  FOR EACH ROW EXECUTE FUNCTION enforce_active_clinical_relationship('psychologist_id');
CREATE TRIGGER session_active_relationship
  BEFORE INSERT OR UPDATE OF patient_id, psychologist_id ON session
  FOR EACH ROW EXECUTE FUNCTION enforce_active_clinical_relationship('psychologist_id');
CREATE TRIGGER clinical_record_active_relationship
  BEFORE INSERT OR UPDATE OF patient_id, psychologist_id ON clinical_record
  FOR EACH ROW EXECUTE FUNCTION enforce_active_clinical_relationship('psychologist_id');
CREATE TRIGGER assignment_active_relationship
  BEFORE INSERT OR UPDATE OF patient_id, assigner_id ON activity_assignment
  FOR EACH ROW EXECUTE FUNCTION enforce_active_clinical_relationship('assigner_id');
CREATE TRIGGER document_active_relationship
  BEFORE INSERT OR UPDATE OF patient_id, psychologist_id ON document
  FOR EACH ROW EXECUTE FUNCTION enforce_active_clinical_relationship('psychologist_id');

-- Public invitation reads cannot carry tenant identity. Keep the lookup behind
-- a security-definer boundary so patient_profile RLS remains closed while the
-- unguessable token digest grants access only to the matching invitation.
CREATE OR REPLACE FUNCTION get_patient_invitation(
  supplied_token_digest bytea
) RETURNS jsonb AS $$
  SELECT jsonb_build_object(
    'id', i.id,
    'patientId', i.patient_id,
    'relationshipId', i.relationship_id,
    'email', i.email,
    'status', i.status,
    'expiresAt', i.expires_at,
    'patientName', p.full_name,
    'psychologistName', psy.full_name,
    'crpNumber', psy.crp_number,
    'crpState', psy.crp_state
  )
  FROM patient_invitation i
  JOIN patient_profile p ON p.id = i.patient_id
  JOIN patient_relationship r ON r.id = i.relationship_id
  JOIN psychologist_profile psy ON psy.id = r.psychologist_id
  WHERE i.token_digest = supplied_token_digest;
$$ LANGUAGE SQL STABLE SECURITY DEFINER SET search_path = public, pg_temp;

-- The public invitation endpoint supplies only an opaque token digest. The
-- entire identity/consent/relationship transition happens under one row lock.
CREATE OR REPLACE FUNCTION accept_patient_invitation(
  supplied_token_digest bytea,
  supplied_password_hash text,
  accepted_document_ids uuid[],
  supplied_ip inet,
  supplied_user_agent text
) RETURNS jsonb AS $$
DECLARE
  invitation patient_invitation%ROWTYPE;
  profile patient_profile%ROWTYPE;
  health_consent_id uuid;
  created_user_id uuid;
BEGIN
  SELECT * INTO invitation
  FROM patient_invitation
  WHERE token_digest = supplied_token_digest
  FOR UPDATE;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'invitation not found' USING ERRCODE = 'P0002';
  END IF;

  IF invitation.status = 'accepted' THEN
    SELECT p.user_id INTO created_user_id
    FROM patient_profile p WHERE p.id = invitation.patient_id;
    RETURN jsonb_build_object(
      'patientId', invitation.patient_id,
      'userId', created_user_id,
      'relationshipId', invitation.relationship_id,
      'alreadyAccepted', true
    );
  END IF;

  IF invitation.status <> 'pending' OR invitation.expires_at <= now() THEN
    IF invitation.status = 'pending' THEN
      UPDATE patient_invitation SET status = 'expired', updated_at = now()
      WHERE id = invitation.id;
    END IF;
    RAISE EXCEPTION 'invitation unavailable' USING ERRCODE = '22023';
  END IF;

  SELECT * INTO profile FROM patient_profile
  WHERE id = invitation.patient_id FOR UPDATE;

  IF EXISTS (
    SELECT 1 FROM consent_document d
    WHERE d.required AND d.published_at <= now() AND d.retired_at IS NULL
      AND NOT (d.id = ANY(accepted_document_ids))
  ) THEN
    RAISE EXCEPTION 'required consent missing' USING ERRCODE = '23514';
  END IF;

  IF EXISTS (SELECT 1 FROM "user" u WHERE lower(u.email) = lower(invitation.email)) THEN
    RAISE EXCEPTION 'email already registered' USING ERRCODE = '23505';
  END IF;

  INSERT INTO "user" (organization_id, email, password_hash, role)
  VALUES (profile.organization_id, lower(invitation.email), supplied_password_hash, 'patient')
  RETURNING id INTO created_user_id;

  UPDATE patient_profile
  SET user_id = created_user_id, status = 'active', updated_at = now()
  WHERE id = profile.id;

  INSERT INTO consent (
    user_id, patient_id, document_id, accepted, ip_address, user_agent
  )
  SELECT created_user_id, profile.id, d.id, true, supplied_ip, supplied_user_agent
  FROM consent_document d
  WHERE d.id = ANY(accepted_document_ids)
    AND d.published_at <= now()
    AND d.retired_at IS NULL;

  SELECT c.id INTO health_consent_id
  FROM consent c
  JOIN consent_document d ON d.id = c.document_id
  WHERE c.patient_id = profile.id
    AND c.user_id = created_user_id
    AND c.accepted
    AND d.scope = 'health_data'
  ORDER BY d.published_at DESC
  LIMIT 1;

  IF health_consent_id IS NULL THEN
    RAISE EXCEPTION 'health consent missing' USING ERRCODE = '23514';
  END IF;

  UPDATE patient_relationship
  SET status = 'active', consent_id = health_consent_id, started_at = now()
  WHERE id = invitation.relationship_id AND status = 'pending';

  IF NOT FOUND THEN
    RAISE EXCEPTION 'relationship unavailable' USING ERRCODE = '55000';
  END IF;

  UPDATE patient_invitation
  SET status = 'accepted', accepted_at = now(), updated_at = now()
  WHERE id = invitation.id;

  INSERT INTO audit_log (
    actor_user_id, organization_id, action, resource_type, resource_id,
    ip_address, user_agent, metadata_jsonb
  ) VALUES (
    created_user_id, profile.organization_id, 'patient_onboarding_accepted',
    'patient_relationship', invitation.relationship_id::text,
    supplied_ip, supplied_user_agent,
    jsonb_build_object(
      'patientId', profile.id,
      'invitationId', invitation.id,
      'consentDocumentIds', accepted_document_ids
    )
  );

  RETURN jsonb_build_object(
    'patientId', profile.id,
    'userId', created_user_id,
    'relationshipId', invitation.relationship_id,
    'alreadyAccepted', false
  );
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp;

CREATE OR REPLACE FUNCTION decline_patient_invitation(
  supplied_token_digest bytea,
  supplied_ip inet,
  supplied_user_agent text
) RETURNS uuid AS $$
DECLARE
  invitation patient_invitation%ROWTYPE;
  profile patient_profile%ROWTYPE;
BEGIN
  SELECT * INTO invitation FROM patient_invitation
  WHERE token_digest = supplied_token_digest FOR UPDATE;
  IF NOT FOUND OR invitation.status <> 'pending' OR invitation.expires_at <= now() THEN
    RAISE EXCEPTION 'invitation unavailable' USING ERRCODE = 'P0002';
  END IF;
  SELECT * INTO profile FROM patient_profile
  WHERE id = invitation.patient_id FOR UPDATE;
  UPDATE patient_invitation
  SET status = 'declined', declined_at = now(), updated_at = now()
  WHERE id = invitation.id;
  UPDATE patient_relationship
  SET status = 'ended', ended_at = now(), end_reason = 'patient_initiated'
  WHERE id = invitation.relationship_id AND status = 'pending';
  UPDATE patient_profile SET status = 'archived', updated_at = now()
  WHERE id = invitation.patient_id;
  INSERT INTO audit_log (
    actor_user_id, organization_id, action, resource_type, resource_id,
    ip_address, user_agent, metadata_jsonb
  )
  SELECT invitation.created_by_user_id, profile.organization_id,
    'patient_onboarding_declined', 'patient_relationship',
    invitation.relationship_id::text, supplied_ip, supplied_user_agent,
    jsonb_build_object('patientId', profile.id, 'invitationId', invitation.id);
  RETURN invitation.patient_id;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp;

-- A response is reviewable only if there is exactly one final submission and
-- exactly one well-typed value for every field of the pinned template.
CREATE OR REPLACE FUNCTION activity_submission_is_complete(
  target_assignment_id uuid
) RETURNS boolean AS $$
  SELECT EXISTS (
    SELECT 1
    FROM activity_assignment a
    JOIN activity_template t ON t.id = a.template_id
    JOIN activity_response r ON r.assignment_id = a.id
      AND NOT r.is_draft
      AND r.submitted_at IS NOT NULL
    WHERE a.id = target_assignment_id
      AND a.template_version = t.version
      AND (
        SELECT count(*) FROM activity_response final
        WHERE final.assignment_id = a.id AND NOT final.is_draft
      ) = 1
      AND (
        SELECT count(*) FROM activity_field expected
        WHERE expected.template_id = a.template_id
      ) > 0
      AND (
        SELECT count(*) FROM activity_response_value actual
        WHERE actual.response_id = r.id
      ) = (
        SELECT count(*) FROM activity_field expected
        WHERE expected.template_id = a.template_id
      )
      AND NOT EXISTS (
        SELECT 1
        FROM activity_response_value value
        LEFT JOIN activity_field field ON field.id = value.field_id
        WHERE value.response_id = r.id
          AND (
            field.id IS NULL
            OR field.template_id <> a.template_id
            OR value.field_code <> field.code
            OR num_nonnulls(
              value.value_text, value.value_number, value.value_boolean,
              value.value_datetime, value.value_json, value.attachment_id
            ) <> 1
          )
      )
      AND NOT EXISTS (
        SELECT 1
        FROM activity_field expected
        WHERE expected.template_id = a.template_id
          AND (
            SELECT count(*)
            FROM activity_response_value value
            WHERE value.response_id = r.id
              AND value.field_id = expected.id
          ) <> 1
      )
  )
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION validate_final_activity_response() RETURNS trigger AS $$
BEGIN
  IF NOT NEW.is_draft AND EXISTS (
    SELECT 1 FROM activity_response existing
    WHERE existing.assignment_id = NEW.assignment_id
      AND NOT existing.is_draft
      AND existing.id <> NEW.id
  ) THEN
    RAISE EXCEPTION 'only one final response is allowed' USING ERRCODE = '23505';
  END IF;
  IF NOT NEW.is_draft AND NEW.submitted_at IS NULL THEN
    RAISE EXCEPTION 'final response requires submitted_at' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER activity_response_final_integrity
  BEFORE INSERT OR UPDATE OF assignment_id, is_draft, submitted_at ON activity_response
  FOR EACH ROW EXECUTE FUNCTION validate_final_activity_response();

CREATE OR REPLACE FUNCTION validate_activity_response_value() RETURNS trigger AS $$
DECLARE
  expected_template_id uuid;
  expected_code text;
BEGIN
  IF num_nonnulls(
    NEW.value_text, NEW.value_number, NEW.value_boolean,
    NEW.value_datetime, NEW.value_json, NEW.attachment_id
  ) <> 1 THEN
    RAISE EXCEPTION 'exactly one typed response value is required' USING ERRCODE = '23514';
  END IF;
  IF EXISTS (
    SELECT 1 FROM activity_response_value existing
    WHERE existing.response_id = NEW.response_id
      AND existing.field_id = NEW.field_id
      AND existing.id <> NEW.id
  ) THEN
    RAISE EXCEPTION 'duplicate response field' USING ERRCODE = '23505';
  END IF;
  SELECT a.template_id, f.code
  INTO expected_template_id, expected_code
  FROM activity_response r
  JOIN activity_assignment a ON a.id = r.assignment_id
  JOIN activity_field f ON f.id = NEW.field_id
  WHERE r.id = NEW.response_id
    AND f.template_id = a.template_id;
  IF expected_template_id IS NULL OR NEW.field_code <> expected_code THEN
    RAISE EXCEPTION 'response field does not belong to assigned template' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER activity_response_value_integrity
  BEFORE INSERT OR UPDATE ON activity_response_value
  FOR EACH ROW EXECUTE FUNCTION validate_activity_response_value();

CREATE OR REPLACE FUNCTION reject_activity_submission_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'submitted activity responses are append-only' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER activity_response_append_only
  BEFORE UPDATE OR DELETE ON activity_response
  FOR EACH ROW
  WHEN (OLD.is_draft = false)
  EXECUTE FUNCTION reject_activity_submission_mutation();
CREATE TRIGGER activity_response_value_append_only
  BEFORE UPDATE OR DELETE ON activity_response_value
  FOR EACH ROW EXECUTE FUNCTION reject_activity_submission_mutation();

ALTER TABLE patient_profile     ENABLE ROW LEVEL SECURITY;
ALTER TABLE appointment         ENABLE ROW LEVEL SECURITY;
ALTER TABLE session             ENABLE ROW LEVEL SECURITY;
ALTER TABLE clinical_record     ENABLE ROW LEVEL SECURITY;
ALTER TABLE documentary_record  ENABLE ROW LEVEL SECURITY;
ALTER TABLE lgpd_export_request ENABLE ROW LEVEL SECURITY;

-- Psicólogo vê só seus pacientes; paciente vê só a si; org_admin vê listagem da org.
CREATE POLICY patient_isolation ON patient_profile
  FOR SELECT USING (
    CASE current_user_role()
      WHEN 'psychologist' THEN id IN (
        SELECT patient_id FROM patient_relationship
        WHERE psychologist_id = current_psychologist_id()
      )
      WHEN 'patient'   THEN user_id = current_user_id()
      WHEN 'org_admin' THEN organization_id = current_organization_id()
      ELSE false
    END
  );

-- Registro Documental: só o autor, sempre.
CREATE POLICY documentary_record_author_only ON documentary_record
  FOR ALL USING (author_id = current_psychologist_id());

CREATE POLICY lgpd_export_request_actor_only ON lgpd_export_request
  FOR ALL USING (
    requested_by = current_user_id()
    AND organization_id = current_organization_id()
  ) WITH CHECK (
    requested_by = current_user_id()
    AND organization_id = current_organization_id()
  );

CREATE POLICY patient_profile_psychologist_insert ON patient_profile
  FOR INSERT WITH CHECK (
    current_user_role() = 'psychologist'
    AND organization_id = current_organization_id()
  );

CREATE POLICY appointment_clinical_select ON appointment
  FOR SELECT USING (
    (current_user_role() = 'psychologist' AND psychologist_id = current_psychologist_id())
    OR (current_user_role() = 'patient' AND patient_id IN (
      SELECT id FROM patient_profile WHERE user_id = current_user_id()
    ))
  );

CREATE POLICY appointment_psychologist_insert ON appointment
  FOR INSERT WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  );

CREATE POLICY appointment_psychologist_update ON appointment
  FOR UPDATE USING (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  ) WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  );

CREATE POLICY session_clinical_select ON session
  FOR SELECT USING (
    (current_user_role() = 'psychologist' AND psychologist_id = current_psychologist_id())
    OR (current_user_role() = 'patient' AND patient_id IN (
      SELECT id FROM patient_profile WHERE user_id = current_user_id()
    ))
  );

CREATE POLICY session_psychologist_insert ON session
  FOR INSERT WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  );

CREATE POLICY clinical_record_clinical_select ON clinical_record
  FOR SELECT USING (
    (current_user_role() = 'psychologist' AND psychologist_id = current_psychologist_id())
    OR (current_user_role() = 'patient' AND patient_id IN (
      SELECT id FROM patient_profile WHERE user_id = current_user_id()
    ))
  );

CREATE POLICY clinical_record_psychologist_write ON clinical_record
  FOR ALL USING (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
  ) WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  );
