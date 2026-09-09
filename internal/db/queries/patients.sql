-- name: ListPatientsByOrg :many
SELECT p.id, p.full_name, p.status, p.created_at,
       COALESCE((SELECT r.status FROM patient_relationship r WHERE r.patient_id = p.id ORDER BY r.created_at DESC LIMIT 1), 'pending')::text AS relationship_status
FROM patient_profile p
WHERE p.organization_id = @organization_id
  AND p.status <> 'deleted'
ORDER BY p.full_name;

-- name: GetPatient :one
SELECT p.id, p.organization_id, p.full_name, p.email, p.birth_date, p.status, p.created_at,
       COALESCE((SELECT r.status FROM patient_relationship r WHERE r.patient_id = p.id ORDER BY r.created_at DESC LIMIT 1), 'pending')::text AS relationship_status
FROM patient_profile p
WHERE p.id = @id
  AND p.organization_id = @organization_id
  AND p.status <> 'deleted';

-- name: CreatePatient :one
INSERT INTO patient_profile (organization_id, full_name, email, birth_date)
VALUES (@organization_id, @full_name, @email, @birth_date)
RETURNING id, organization_id, full_name, email, birth_date, status, created_at;

-- name: CreatePatientRelationship :exec
INSERT INTO patient_relationship (patient_id, psychologist_id, status)
VALUES (@patient_id, @psychologist_id, 'pending');

-- name: GetInvitationForPatient :one
SELECT id, patient_id, organization_id, email, token_hash, token_ciphertext, expires_at,
       status, delivery_status, delivery_attempts, last_delivery_error, sent_at
FROM patient_invitation WHERE patient_id = @patient_id AND organization_id = @organization_id
ORDER BY created_at DESC LIMIT 1;

-- name: RevokePendingInvitations :exec
UPDATE patient_invitation SET status = 'revoked', updated_at = now()
WHERE patient_id = @patient_id AND organization_id = @organization_id AND status = 'pending';

-- name: CreateInvitation :one
INSERT INTO patient_invitation (patient_id, organization_id, email, token_hash, token_ciphertext, expires_at)
VALUES (@patient_id, @organization_id, @email, @token_hash, @token_ciphertext, @expires_at)
RETURNING id, patient_id, organization_id, email, token_hash, token_ciphertext, expires_at,
          status, delivery_status, delivery_attempts, last_delivery_error, sent_at;

-- name: GetInvitationByID :one
SELECT id, patient_id, organization_id, email, token_hash, token_ciphertext, expires_at,
       status, delivery_status, delivery_attempts, last_delivery_error, sent_at
FROM patient_invitation WHERE id = @id;

-- name: MarkInvitationSent :exec
UPDATE patient_invitation SET delivery_status = 'sent', delivery_attempts = delivery_attempts + 1,
  last_delivery_error = NULL, sent_at = now(), updated_at = now() WHERE id = @id;

-- name: MarkInvitationFailed :exec
UPDATE patient_invitation SET delivery_status = 'failed', delivery_attempts = delivery_attempts + 1,
  last_delivery_error = @last_delivery_error, updated_at = now() WHERE id = @id;

-- name: GetInvitationByTokenHash :one
SELECT id, patient_id, organization_id, email, token_hash, token_ciphertext, expires_at,
       status, delivery_status, delivery_attempts, last_delivery_error, sent_at
FROM patient_invitation WHERE token_hash = @token_hash;

-- name: AcceptInvitation :exec
UPDATE patient_invitation SET status = 'accepted', updated_at = now()
WHERE id = @id AND status = 'pending';

-- name: AttachPatientUser :exec
UPDATE patient_profile SET user_id = @user_id, status = 'active', updated_at = now()
WHERE id = @patient_id;

-- name: ActivatePatientRelationship :exec
UPDATE patient_relationship SET status = 'active'
WHERE patient_id = @patient_id AND status = 'pending';

-- name: CreatePatientUser :one
INSERT INTO "user" (organization_id, email, password_hash, role)
VALUES (@organization_id, @email, @password_hash, 'patient')
RETURNING id;
