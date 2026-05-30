-- name: CreateCheckin :one
-- Isolamento de org garantido pela checagem do paciente na mesma org (no service).
INSERT INTO checkin (patient_id, mood, note)
VALUES (@patient_id, @mood, @note)
RETURNING id, patient_id, mood, note, created_at;

-- name: ListCheckinsByPatient :many
SELECT c.id, c.patient_id, c.mood, c.note, c.created_at
FROM checkin c
JOIN patient_profile p ON p.id = c.patient_id
WHERE c.patient_id = @patient_id
  AND p.organization_id = @organization_id
ORDER BY c.created_at DESC;

-- name: PatientInOrg :one
-- Confirma que o paciente pertence à organização (usado antes de criar check-in).
SELECT EXISTS (
  SELECT 1 FROM patient_profile
  WHERE id = @patient_id AND organization_id = @organization_id AND status <> 'deleted'
) AS in_org;
