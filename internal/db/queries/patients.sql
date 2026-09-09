-- name: ListPatientsByOrg :many
SELECT id, full_name, status, created_at
FROM patient_profile
WHERE organization_id = @organization_id
  AND status <> 'deleted'
ORDER BY full_name;

-- name: GetPatient :one
SELECT id, organization_id, full_name, email, birth_date, status, created_at
FROM patient_profile
WHERE id = @id
  AND organization_id = @organization_id
  AND status <> 'deleted';

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
