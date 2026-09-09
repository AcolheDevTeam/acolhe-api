-- name: CreatePatientInvitation :one
INSERT INTO patient_invitation
  (patient_id, relationship_id, email, token_hash, expires_at)
VALUES (@patient_id, @relationship_id, @email, @token_hash, @expires_at)
RETURNING id, patient_id, relationship_id, email, expires_at;

-- name: CreatePendingRelationship :one
INSERT INTO patient_relationship (patient_id, psychologist_id, status)
VALUES (@patient_id, @psychologist_id, 'pending')
RETURNING id, patient_id, psychologist_id, status;

-- name: GetInvitationForAcceptance :one
SELECT i.id, i.patient_id, i.relationship_id, i.email, i.expires_at,
       p.organization_id, r.status
FROM patient_invitation i
JOIN patient_profile p ON p.id = i.patient_id
JOIN patient_relationship r ON r.id = i.relationship_id
WHERE i.token_hash = @token_hash
  AND i.accepted_at IS NULL
  AND i.expires_at > now()
  AND r.status = 'pending'
  AND p.status <> 'deleted';

-- name: MarkInvitationAccepted :exec
UPDATE patient_invitation SET accepted_at = now()
WHERE id = @id AND accepted_at IS NULL;

-- name: AcceptPatientRelationship :exec
UPDATE patient_relationship
SET status = 'active', consent_id = @consent_id, started_at = now()
WHERE id = @id AND status = 'pending';

-- name: CreatePatientConsent :one
INSERT INTO consent (user_id, scope, document_version)
VALUES (@user_id, @scope, @document_version)
RETURNING id;
