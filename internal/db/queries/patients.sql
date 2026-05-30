-- name: ListPatientsByOrg :many
SELECT id, full_name, status, created_at
FROM patient_profile
WHERE organization_id = @organization_id
  AND status <> 'deleted'
ORDER BY full_name;

-- name: GetPatient :one
SELECT id, organization_id, full_name, birth_date, status, created_at
FROM patient_profile
WHERE id = @id
  AND organization_id = @organization_id
  AND status <> 'deleted';

-- name: CreatePatient :one
INSERT INTO patient_profile (organization_id, full_name, birth_date)
VALUES (@organization_id, @full_name, @birth_date)
RETURNING id, organization_id, full_name, birth_date, status, created_at;
