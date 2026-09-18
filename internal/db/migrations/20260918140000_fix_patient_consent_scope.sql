-- Reescreve accept_patient_invitation para exigir apenas os consentimentos do
-- escopo da paciente. A migration 20260912150000 publicou 'terms_of_use' e
-- 'privacy_policy' com required = true para o cadastro do psicólogo; como esses
-- documentos nunca são oferecidos no convite, a checagem antiga rejeitava todo
-- aceite com 23514 e nenhuma paciente conseguia criar conta.

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

  -- Só os documentos do aceite da paciente entram na exigência. Os documentos
  -- de cadastro do psicólogo ('terms_of_use', 'privacy_policy') moram na mesma
  -- tabela, nunca são oferecidos no convite e travariam todo aceite.
  IF EXISTS (
    SELECT 1 FROM consent_document d
    WHERE d.required AND d.published_at <= now() AND d.retired_at IS NULL
      AND d.scope IN ('health_data', 'communications', 'aggregate_statistics')
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
    AND d.retired_at IS NULL
    AND d.scope IN ('health_data', 'communications', 'aggregate_statistics');

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
