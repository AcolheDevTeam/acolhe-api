-- name: GetActiveRelationship :one
-- patient_relationship não tem organization_id; o isolamento de org é feito
-- pelo join em patient_profile (que carrega organization_id).
SELECT r.id, r.patient_id, r.psychologist_id, r.status, r.started_at
FROM patient_relationship r
JOIN patient_profile p ON p.id = r.patient_id
WHERE r.patient_id = @patient_id
  AND r.psychologist_id = @psychologist_id
  AND p.organization_id = @organization_id
  AND has_active_clinical_relationship(r.patient_id, r.psychologist_id);

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
       s.occurred_at, s.status, s.created_at, s.appointment_id,
       COALESCE(cr.version, 1)::integer AS version, cr.locked_at,
       CAST(COALESCE(cr.content_jsonb->>'notes', '') AS text) AS notes,
       a.modality, a.duration_minutes
FROM session s
JOIN patient_profile p ON p.id = s.patient_id
LEFT JOIN clinical_record cr ON cr.session_id = s.id
LEFT JOIN appointment a ON a.id = s.appointment_id
WHERE s.id = @id
  AND s.psychologist_id = @psychologist_id
  AND p.organization_id = @organization_id;

-- name: CreateSessionFromAppointment :one
INSERT INTO session (appointment_id, patient_id, psychologist_id, occurred_at, status)
SELECT a.id, a.patient_id, a.psychologist_id, a.scheduled_for, CASE WHEN a.status = 'completed' THEN 'completed' ELSE 'pending' END
FROM appointment a JOIN patient_profile patient ON patient.id = a.patient_id
WHERE a.id = @appointment_id AND a.psychologist_id = @psychologist_id
  AND patient.organization_id = @organization_id
  AND a.status IN ('scheduled', 'confirmed', 'completed') AND a.scheduled_for <= now()
ON CONFLICT (appointment_id) WHERE appointment_id IS NOT NULL
DO UPDATE SET appointment_id = EXCLUDED.appointment_id
RETURNING id, appointment_id, patient_id, psychologist_id, occurred_at, status, created_at;

-- name: SaveSessionNotes :one
WITH current_record AS (
  SELECT cr.id, cr.content_jsonb, cr.version
  FROM clinical_record cr JOIN patient_profile patient ON patient.id = cr.patient_id
  WHERE cr.session_id = @session_id AND cr.psychologist_id = @psychologist_id
    AND patient.organization_id = @organization_id
    AND cr.locked_at IS NULL AND cr.version = @version
  FOR UPDATE OF cr
), snapshot AS (
  INSERT INTO clinical_record_version (clinical_record_id, version_number, content_snapshot, changed_by, change_reason)
  SELECT id, version, content_jsonb, @user_id, 'Atualização da evolução da sessão'
  FROM current_record RETURNING clinical_record_id
)
UPDATE clinical_record cr
SET content_jsonb = jsonb_set(cr.content_jsonb, '{notes}', to_jsonb(sqlc.arg(notes)::text)),
    version = cr.version + 1, updated_at = now()
FROM snapshot
WHERE cr.id = snapshot.clinical_record_id
RETURNING cr.version;

-- name: CompleteAppointmentSession :exec
UPDATE session s SET status = 'completed', updated_at = now()
FROM patient_profile patient
WHERE s.appointment_id = @appointment_id AND patient.id = s.patient_id
  AND s.psychologist_id = @psychologist_id AND patient.organization_id = @organization_id;

-- name: CreateClinicalRecordIfMissing :exec
INSERT INTO clinical_record (session_id, patient_id, psychologist_id, content_jsonb)
VALUES (@session_id, @patient_id, @psychologist_id, jsonb_build_object('notes', ''))
ON CONFLICT (session_id) DO NOTHING;
