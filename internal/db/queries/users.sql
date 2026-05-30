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
