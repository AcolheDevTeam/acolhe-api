-- Consent-gated onboarding. Existing active relationships are explicitly
-- classified as a legacy cohort; only newly-created relationships require the
-- versioned health-data consent.

CREATE TABLE consent_document (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  scope text NOT NULL CHECK (scope IN ('health_data','communications','aggregate_statistics')),
  version text NOT NULL,
  title text NOT NULL,
  content text NOT NULL,
  content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
  required boolean NOT NULL DEFAULT false,
  published_at timestamptz NOT NULL,
  retired_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (scope, version)
);

WITH documents(scope, version, title, content, required) AS (
  VALUES
    (
      'health_data',
      '0.3',
      'Dados de saúde',
      'Você autoriza que sua psicóloga registre prontuário, atividades e respostas, conforme Resolução CFP 01/2009.',
      true
    ),
    (
      'communications',
      '0.3',
      'Comunicações',
      'Receber lembretes de sessão e atividades por e-mail (sem conteúdo sensível no corpo da mensagem).',
      false
    ),
    (
      'aggregate_statistics',
      '0.3',
      'Estatística agregada',
      'Uso anônimo do Acolhe para métricas operacionais. Nunca cruzado com dados clínicos.',
      false
    )
)
INSERT INTO consent_document (
  scope, version, title, content, content_sha256, required, published_at
)
SELECT scope, version, title, content,
       encode(digest(content, 'sha256'), 'hex'), required,
       '2026-05-12T00:00:00-03:00'::timestamptz
FROM documents;

ALTER TABLE patient_profile
  ADD CONSTRAINT patient_profile_status_check
    CHECK (status IN ('onboarding','active','archived','deleted')),
  ALTER COLUMN status SET DEFAULT 'onboarding';

-- Preserve any pre-existing consent evidence before replacing its free-form
-- scope/version columns with immutable document references.
INSERT INTO consent_document (
  scope, version, title, content, content_sha256, required, published_at
)
SELECT DISTINCT
  CASE
    WHEN c.scope IN ('health_data','communications','aggregate_statistics') THEN c.scope
    ELSE 'health_data'
  END,
  c.document_version,
  'Documento de consentimento legado',
  'Registro legado migrado; consulte a evidência de auditoria original.',
  encode(digest('Registro legado migrado; consulte a evidência de auditoria original.', 'sha256'), 'hex'),
  CASE WHEN c.scope = 'health_data' THEN true ELSE false END,
  min(c.accepted_at)
FROM consent c
WHERE NOT EXISTS (
  SELECT 1 FROM consent_document d
  WHERE d.scope = CASE
      WHEN c.scope IN ('health_data','communications','aggregate_statistics') THEN c.scope
      ELSE 'health_data'
    END
    AND d.version = c.document_version
)
GROUP BY c.scope, c.document_version;

ALTER TABLE consent
  ADD COLUMN patient_id uuid REFERENCES patient_profile(id),
  ADD COLUMN document_id uuid REFERENCES consent_document(id),
  ADD COLUMN accepted boolean,
  ADD COLUMN decided_at timestamptz,
  ADD COLUMN user_agent text;

UPDATE consent c
SET patient_id = COALESCE(
      (SELECT r.patient_id FROM patient_relationship r WHERE r.consent_id = c.id LIMIT 1),
      (SELECT p.id FROM patient_profile p WHERE p.user_id = c.user_id LIMIT 1)
    ),
    document_id = d.id,
    accepted = true,
    decided_at = c.accepted_at
FROM consent_document d
WHERE d.scope = CASE
    WHEN c.scope IN ('health_data','communications','aggregate_statistics') THEN c.scope
    ELSE 'health_data'
  END
  AND d.version = c.document_version;

ALTER TABLE consent
  ALTER COLUMN document_id SET NOT NULL,
  ALTER COLUMN accepted SET NOT NULL,
  ALTER COLUMN decided_at SET NOT NULL,
  ALTER COLUMN decided_at SET DEFAULT now(),
  DROP COLUMN scope,
  DROP COLUMN document_version,
  DROP COLUMN accepted_at;

CREATE UNIQUE INDEX idx_consent_patient_document
  ON consent (patient_id, document_id);

ALTER TABLE patient_relationship
  ALTER COLUMN started_at DROP NOT NULL,
  ALTER COLUMN started_at DROP DEFAULT,
  ADD COLUMN requires_health_consent boolean;
UPDATE patient_relationship SET requires_health_consent = false;
ALTER TABLE patient_relationship
  ALTER COLUMN requires_health_consent SET DEFAULT true,
  ALTER COLUMN requires_health_consent SET NOT NULL;

CREATE TABLE patient_invitation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id uuid NOT NULL REFERENCES patient_profile(id),
  relationship_id uuid NOT NULL REFERENCES patient_relationship(id),
  email text NOT NULL,
  token_digest bytea UNIQUE NOT NULL,
  idempotency_key uuid NOT NULL,
  created_by_user_id uuid NOT NULL REFERENCES "user"(id),
  status text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','accepted','declined','revoked','expired')),
  expires_at timestamptz NOT NULL,
  accepted_at timestamptz,
  declined_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (created_by_user_id, idempotency_key)
);
CREATE UNIQUE INDEX idx_invitation_pending_relationship
  ON patient_invitation (relationship_id) WHERE status = 'pending';
CREATE INDEX idx_invitation_patient ON patient_invitation (patient_id);
CREATE INDEX idx_invitation_expires
  ON patient_invitation (expires_at) WHERE status = 'pending';

CREATE OR REPLACE FUNCTION reject_consent_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'consent records are append-only' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER consent_append_only
  BEFORE UPDATE OR DELETE ON consent
  FOR EACH ROW EXECUTE FUNCTION reject_consent_mutation();

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
  SELECT * INTO invitation FROM patient_invitation
  WHERE token_digest = supplied_token_digest FOR UPDATE;
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
    AND d.published_at <= now() AND d.retired_at IS NULL;
  SELECT c.id INTO health_consent_id
  FROM consent c
  JOIN consent_document d ON d.id = c.document_id
  WHERE c.patient_id = profile.id AND c.user_id = created_user_id
    AND c.accepted AND d.scope = 'health_data'
  ORDER BY d.published_at DESC LIMIT 1;
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
    jsonb_build_object('patientId', profile.id, 'invitationId', invitation.id,
      'consentDocumentIds', accepted_document_ids)
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
