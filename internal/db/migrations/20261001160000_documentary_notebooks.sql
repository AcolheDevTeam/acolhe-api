-- Conteúdos antigos têm formato criptográfico não documentado e podem não ter paciente.
-- Preservar integralmente para conversão supervisionada; nunca inferir plaintext/chave.
ALTER TABLE documentary_record RENAME TO documentary_record_legacy;
ALTER INDEX documentary_record_pkey RENAME TO documentary_record_legacy_pkey;
DROP POLICY documentary_record_author_only ON documentary_record_legacy;
CREATE TABLE documentary_record (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organization(id),
  author_id uuid NOT NULL REFERENCES psychologist_profile(id),
  patient_id uuid NOT NULL REFERENCES patient_profile(id),
  category text NOT NULL CHECK (category IN ('hypothesis','technical_observation','planning','transcription','other')),
  content_encrypted bytea NOT NULL,
  revision integer NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id, author_id, patient_id, category),
  UNIQUE (id, organization_id, author_id)
);
CREATE TABLE documentary_record_version (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  record_id uuid NOT NULL REFERENCES documentary_record(id),
  organization_id uuid NOT NULL REFERENCES organization(id),
  author_id uuid NOT NULL REFERENCES psychologist_profile(id),
  revision integer NOT NULL CHECK (revision > 0),
  content_encrypted bytea NOT NULL,
  restored_from integer,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (record_id, revision),
  FOREIGN KEY (record_id, organization_id, author_id) REFERENCES documentary_record(id, organization_id, author_id),
  FOREIGN KEY (record_id, restored_from) REFERENCES documentary_record_version(record_id, revision)
);
CREATE INDEX idx_documentary_version_history ON documentary_record_version(record_id, revision DESC);
ALTER TABLE documentary_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE documentary_record_legacy ENABLE ROW LEVEL SECURITY;
ALTER TABLE documentary_record_version ENABLE ROW LEVEL SECURITY;
CREATE POLICY documentary_legacy_read ON documentary_record_legacy FOR SELECT USING (
 current_user_role() = 'psychologist' AND author_id = current_psychologist_id()
);
CREATE POLICY documentary_record_author_only ON documentary_record FOR ALL USING (
 current_user_role() = 'psychologist' AND author_id = current_psychologist_id()
 AND organization_id = current_organization_id()
) WITH CHECK (
 current_user_role() = 'psychologist' AND author_id = current_psychologist_id()
 AND organization_id = current_organization_id()
);
CREATE POLICY documentary_version_author_only ON documentary_record_version FOR ALL USING (
 current_user_role() = 'psychologist' AND author_id = current_psychologist_id()
 AND organization_id = current_organization_id()
) WITH CHECK (
 current_user_role() = 'psychologist' AND author_id = current_psychologist_id()
 AND organization_id = current_organization_id()
);
CREATE INDEX idx_documentary_notebook_patient ON documentary_record(organization_id, author_id, patient_id);

CREATE TABLE documentary_maintenance_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 database_actor text NOT NULL DEFAULT current_user,
 source_key_id text NOT NULL,
 target_key_id text NOT NULL,
 item_count integer NOT NULL,
 occurred_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE documentary_maintenance_audit ENABLE ROW LEVEL SECURITY;
