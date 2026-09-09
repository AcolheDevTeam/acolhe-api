-- All patient portal queries identify the patient from the authenticated user.
-- No client-provided patient_id is accepted by this contract.

-- name: GetPatientPortalContext :one
SELECT p.id, p.full_name, p.status, p.organization_id,
       r.id AS relationship_id, r.status AS relationship_status,
       CASE WHEN r.consent_id IS NOT NULL THEN true ELSE false END AS consented
FROM patient_profile p
JOIN patient_relationship r ON r.patient_id = p.id
WHERE p.user_id = @user_id
  AND p.status <> 'deleted'
  AND r.status = 'active'
  AND r.consent_id IS NOT NULL
ORDER BY r.started_at DESC
LIMIT 1;

-- name: GetPatientNextAppointment :one
SELECT a.id, a.scheduled_for, a.duration_minutes, a.modality, a.status
FROM appointment a
JOIN patient_profile p ON p.id = a.patient_id
WHERE p.user_id = @user_id
  AND p.status <> 'deleted'
  AND a.status = 'scheduled'
  AND a.scheduled_for >= now()
ORDER BY a.scheduled_for
LIMIT 1;

-- name: ListPatientPendingActivities :many
SELECT ag.id, ag.status, ag.scheduled_for, ag.due_at, t.title
FROM activity_assignment ag
JOIN patient_profile p ON p.id = ag.patient_id
JOIN activity_template t ON t.id = ag.template_id
WHERE p.user_id = @user_id
  AND p.status <> 'deleted'
  AND ag.status IN ('pending', 'in_progress')
ORDER BY COALESCE(ag.due_at, ag.scheduled_for, ag.created_at);

-- name: ListPatientCheckins :many
SELECT c.id, c.mood, c.note, c.created_at
FROM checkin c
JOIN patient_profile p ON p.id = c.patient_id
WHERE p.user_id = @user_id
  AND p.status <> 'deleted'
ORDER BY c.created_at DESC
LIMIT 10;

-- name: CreatePatientCheckin :one
INSERT INTO checkin (patient_id, mood, note)
SELECT p.id, @mood, @note
FROM patient_profile p
JOIN patient_relationship r ON r.patient_id = p.id
WHERE p.user_id = @user_id
  AND p.status <> 'deleted'
  AND r.status = 'active'
  AND r.consent_id IS NOT NULL
RETURNING id, patient_id, mood, note, created_at;

-- name: GetPatientProcessSummary :one
SELECT
  COUNT(DISTINCT s.id)::int AS session_count,
  COUNT(DISTINCT ag.id) FILTER (WHERE ag.status IN ('pending', 'in_progress'))::int AS pending_activity_count,
  COUNT(DISTINCT c.id)::int AS checkin_count
FROM patient_profile p
LEFT JOIN session s ON s.patient_id = p.id
LEFT JOIN activity_assignment ag ON ag.patient_id = p.id
LEFT JOIN checkin c ON c.patient_id = p.id
WHERE p.user_id = @user_id
  AND p.status <> 'deleted';
