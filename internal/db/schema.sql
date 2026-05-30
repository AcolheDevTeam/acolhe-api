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
  status          text NOT NULL DEFAULT 'active',
  deleted_at      timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE consent (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          uuid NOT NULL REFERENCES "user"(id),
  scope            text NOT NULL,
  document_version text NOT NULL,
  accepted_at      timestamptz NOT NULL DEFAULT now(),
  ip_address       inet,
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
  end_reason       text CHECK (end_reason IN ('patient_initiated','psychologist_initiated','transfer','archived','other')),
  started_at       timestamptz NOT NULL DEFAULT now(),
  ended_at         timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE appointment (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id       uuid NOT NULL REFERENCES patient_profile(id),
  psychologist_id  uuid NOT NULL REFERENCES psychologist_profile(id),
  scheduled_for    timestamptz NOT NULL,
  duration_minutes integer NOT NULL DEFAULT 50,
  modality         text NOT NULL DEFAULT 'in_person' CHECK (modality IN ('in_person','online')),
  status           text NOT NULL DEFAULT 'scheduled',
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
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE activity_response (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  assignment_id uuid NOT NULL REFERENCES activity_assignment(id),
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

-- ============================================================
-- 5. Constraints não óbvias
-- ============================================================

-- Um Appointment realizado gera no máximo uma Session
CREATE UNIQUE INDEX idx_session_appointment
  ON session (appointment_id) WHERE appointment_id IS NOT NULL;

-- Paciente só pode ter um relacionamento ativo por vez (MVP)
CREATE UNIQUE INDEX idx_relationship_active_unique
  ON patient_relationship (patient_id) WHERE status = 'active';

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

ALTER TABLE patient_profile     ENABLE ROW LEVEL SECURITY;
ALTER TABLE appointment         ENABLE ROW LEVEL SECURITY;
ALTER TABLE session             ENABLE ROW LEVEL SECURITY;
ALTER TABLE clinical_record     ENABLE ROW LEVEL SECURITY;
ALTER TABLE documentary_record  ENABLE ROW LEVEL SECURITY;

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
