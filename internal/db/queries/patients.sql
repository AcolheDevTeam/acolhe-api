-- name: ListPatientsByOrg :many
SELECT id, full_name, status, created_at
FROM patient_profile
WHERE organization_id = @organization_id
  AND status <> 'deleted'
ORDER BY full_name;

-- name: ListPatientsByPsychologist :many
SELECT p.id, p.full_name, p.status, p.created_at
FROM patient_profile p
JOIN patient_relationship r ON r.patient_id = p.id
WHERE p.organization_id = @organization_id
  AND r.psychologist_id = @psychologist_id
  AND r.status = 'active'
  AND p.status <> 'deleted'
ORDER BY p.full_name;

-- name: GetPatient :one
SELECT id, organization_id, full_name, birth_date, status, created_at
FROM patient_profile
WHERE id = @id
  AND organization_id = @organization_id
  AND status <> 'deleted';

-- name: GetPatientForPsychologist :one
SELECT p.id, p.organization_id, p.full_name, p.birth_date, p.status, p.created_at
FROM patient_profile p
JOIN patient_relationship r ON r.patient_id = p.id
WHERE p.id = @id
  AND p.organization_id = @organization_id
  AND r.psychologist_id = @psychologist_id
  AND r.status = 'active'
  AND p.status <> 'deleted';

-- name: GetPatientByUserInOrg :one
SELECT id, organization_id, full_name, birth_date, status, created_at
FROM patient_profile
WHERE user_id = @user_id
  AND organization_id = @organization_id
  AND status <> 'deleted';

-- name: CreatePatient :exec
INSERT INTO patient_profile (id, organization_id, full_name, birth_date)
VALUES (@id, @organization_id, @full_name, @birth_date);

-- name: CreatePatientRelationship :exec
INSERT INTO patient_relationship (patient_id, psychologist_id, status)
VALUES (@patient_id, @psychologist_id, 'active');
