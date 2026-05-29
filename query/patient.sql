-- Exemplo de queries para sqlc. Ver https://docs.sqlc.dev

-- name: GetPatient :one
SELECT id, organization_id, full_name, birth_date, status, created_at
FROM patient_profile
WHERE id = $1 AND status <> 'deleted';

-- name: ListPatientsByOrg :many
SELECT id, full_name, status, created_at
FROM patient_profile
WHERE organization_id = $1 AND status <> 'deleted'
ORDER BY full_name;

-- name: CreatePatient :one
INSERT INTO patient_profile (organization_id, full_name, birth_date)
VALUES ($1, $2, $3)
RETURNING id, organization_id, full_name, birth_date, status, created_at;
