-- name: GetUserByEmail :one
SELECT id, organization_id, email, password_hash, role, status
FROM "user"
WHERE lower(email) = lower(@email) AND status = 'active';

-- name: GetUserByID :one
SELECT id, organization_id, email, role, status
FROM "user"
WHERE id = @id;

-- name: GetPsychologistByUser :one
SELECT id, user_id, full_name, crp_number, crp_state, crp_status
FROM psychologist_profile
WHERE user_id = @user_id;

-- name: CreatePatientUser :one
INSERT INTO "user" (organization_id, email, password_hash, role)
VALUES (@organization_id, lower(@email), @password_hash, 'patient')
RETURNING id, organization_id, email, role;

-- name: LinkPatientUser :exec
UPDATE patient_profile SET user_id = @user_id, updated_at = now()
WHERE id = @patient_id AND user_id IS NULL;
