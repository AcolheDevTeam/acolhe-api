-- name: GetActiveRelationship :one
-- patient_relationship não tem organization_id; o isolamento de org é feito
-- pelo join em patient_profile (que carrega organization_id).
SELECT r.id, r.patient_id, r.psychologist_id, r.status, r.started_at
FROM patient_relationship r
JOIN patient_profile p ON p.id = r.patient_id
WHERE r.patient_id = @patient_id
  AND r.psychologist_id = @psychologist_id
  AND p.organization_id = @organization_id
  AND r.status = 'active';

-- name: CreateSession :one
INSERT INTO session (patient_id, psychologist_id, occurred_at, status)
VALUES (@patient_id, @psychologist_id, @occurred_at, @status)
RETURNING id, appointment_id, patient_id, psychologist_id, occurred_at, status, created_at;

-- name: CreateClinicalRecord :exec
INSERT INTO clinical_record (session_id, patient_id, psychologist_id, content_jsonb)
VALUES (@session_id, @patient_id, @psychologist_id, jsonb_build_object('notes', sqlc.arg(notes)::text));

-- name: GetSessionsByPatient :many
-- Isolamento multi-tenant via patient_profile.organization_id.
SELECT s.id, s.patient_id, s.psychologist_id, s.occurred_at, s.status, s.created_at,
       a.modality, a.duration_minutes
FROM session s
JOIN patient_profile p ON p.id = s.patient_id
LEFT JOIN appointment a ON a.id = s.appointment_id
WHERE s.patient_id = @patient_id
  AND s.psychologist_id = @psychologist_id
  AND p.organization_id = @organization_id
ORDER BY s.occurred_at DESC;

-- name: GetSessionsByPsychologist :many
SELECT s.id, s.patient_id, p.full_name AS patient_name, s.psychologist_id,
       s.occurred_at, s.status, s.created_at,
       a.modality, a.duration_minutes
FROM session s
JOIN patient_profile p ON p.id = s.patient_id
LEFT JOIN appointment a ON a.id = s.appointment_id
WHERE p.organization_id = @organization_id
  AND s.psychologist_id = @psychologist_id
ORDER BY s.occurred_at DESC;

-- name: GetSession :one
SELECT s.id, s.patient_id, p.full_name AS patient_name, s.psychologist_id,
       s.occurred_at, s.status, s.created_at,
       CAST(COALESCE(cr.content_jsonb->>'notes', '') AS text) AS notes,
       a.modality, a.duration_minutes
FROM session s
JOIN patient_profile p ON p.id = s.patient_id
LEFT JOIN clinical_record cr ON cr.session_id = s.id
LEFT JOIN appointment a ON a.id = s.appointment_id
WHERE s.id = @id
  AND s.psychologist_id = @psychologist_id
  AND p.organization_id = @organization_id;
