-- name: GetPatientTimeline :many
-- Timeline unificada do paciente: sessões + agendamentos + atividades.
-- Isolamento multi-tenant via patient_profile.organization_id em cada ramo do UNION.
SELECT kind, item_id, occurred_at, status
FROM (
  SELECT 'session'::text AS kind, s.id AS item_id, s.occurred_at AS occurred_at, s.status AS status
  FROM session s
  JOIN patient_profile p ON p.id = s.patient_id
  WHERE s.patient_id = @patient_id AND p.organization_id = @organization_id

  UNION ALL

  SELECT 'appointment'::text, a.id, a.scheduled_for, a.status
  FROM appointment a
  JOIN patient_profile p ON p.id = a.patient_id
  WHERE a.patient_id = @patient_id AND p.organization_id = @organization_id

  UNION ALL

  SELECT 'activity'::text, ag.id, COALESCE(ag.scheduled_for, ag.created_at), ag.status
  FROM activity_assignment ag
  JOIN patient_profile p ON p.id = ag.patient_id
  WHERE ag.patient_id = @patient_id AND p.organization_id = @organization_id
) t
ORDER BY occurred_at DESC;
