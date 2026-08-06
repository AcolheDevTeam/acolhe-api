-- name: LockPatientCreationKey :one
SELECT pg_advisory_xact_lock(hashtextextended(@idempotency_key::text, 0));

-- name: GetInvitationByCreationKey :one
SELECT i.id, i.patient_id, i.relationship_id, i.email, i.status, i.expires_at
FROM patient_invitation i
WHERE i.created_by_user_id = @created_by_user_id
  AND i.idempotency_key = @idempotency_key;

-- name: CreatePatientInvitation :one
INSERT INTO patient_invitation (
  patient_id, relationship_id, email, token_digest, idempotency_key,
  created_by_user_id, expires_at
) VALUES (
  @patient_id, @relationship_id, lower(@email), @token_digest,
  @idempotency_key, @created_by_user_id, @expires_at
)
RETURNING id, expires_at;

-- name: ReissuePatientInvitation :one
UPDATE patient_invitation
SET token_digest = @token_digest,
    status = 'pending',
    expires_at = @expires_at,
    updated_at = now()
WHERE id = @id
  AND status IN ('pending', 'expired')
RETURNING id, expires_at;

-- name: GetReissuableInvitationForPatient :one
SELECT i.id, i.email
FROM patient_invitation i
JOIN patient_relationship r ON r.id = i.relationship_id
JOIN patient_profile p ON p.id = i.patient_id
WHERE i.patient_id = @patient_id
  AND p.organization_id = @organization_id
  AND r.psychologist_id = @psychologist_id
  AND r.status = 'pending'
  AND i.status IN ('pending', 'expired')
ORDER BY i.created_at DESC
LIMIT 1
FOR UPDATE OF i;

-- name: GetInvitationByDigest :one
WITH invitation AS (
  SELECT get_patient_invitation(@token_digest) AS result
)
SELECT (result->>'id')::uuid AS id,
       (result->>'patientId')::uuid AS patient_id,
       (result->>'relationshipId')::uuid AS relationship_id,
       (result->>'email')::text AS email,
       (result->>'status')::text AS status,
       (result->>'expiresAt')::timestamptz AS expires_at,
       (result->>'patientName')::text AS patient_name,
       (result->>'psychologistName')::text AS psychologist_name,
       (result->>'crpNumber')::text AS crp_number,
       (result->>'crpState')::text AS crp_state
FROM invitation
WHERE result IS NOT NULL;

-- name: ListPublishedConsentDocuments :many
SELECT id, scope, version, title, content, content_sha256, required, published_at
FROM consent_document
WHERE published_at <= now() AND retired_at IS NULL
ORDER BY required DESC, published_at, scope;

-- name: AcceptPatientInvitation :one
WITH accepted AS (
  SELECT accept_patient_invitation(
    @token_digest,
    @password_hash,
    @accepted_document_ids::uuid[],
    @ip_address,
    @user_agent
  ) AS result
)
SELECT (result->>'patientId')::uuid AS patient_id,
       (result->>'userId')::uuid AS user_id,
       (result->>'relationshipId')::uuid AS relationship_id,
       (result->>'alreadyAccepted')::boolean AS already_accepted
FROM accepted;

-- name: DeclinePatientInvitation :one
SELECT decline_patient_invitation(@token_digest, @ip_address, @user_agent) AS patient_id;
