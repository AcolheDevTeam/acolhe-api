-- name: ListAppointmentsByPsychologist :many
SELECT a.id, a.patient_id, a.psychologist_id, a.scheduled_for, a.duration_minutes,
       a.modality, a.status, a.created_at
FROM appointment a
JOIN patient_profile p ON p.id = a.patient_id
WHERE a.psychologist_id = @psychologist_id
  AND p.organization_id = @organization_id
ORDER BY a.scheduled_for;

-- name: CountAppointmentConflicts :one
-- Conta agendamentos do psicólogo cujo intervalo se sobrepõe à janela informada.
-- tstzrange(...) && tstzrange(...) testa interseção de intervalos.
-- O service calcula window_end = scheduled_for + duration; assim a query só recebe timestamptz.
SELECT count(*) AS conflict_count
FROM appointment
WHERE psychologist_id = @psychologist_id
  AND status <> 'canceled'
  AND tstzrange(scheduled_for, scheduled_for + duration_minutes * interval '1 minute')
   && tstzrange(@window_start, @window_end);

-- name: CreateAppointment :one
INSERT INTO appointment (patient_id, psychologist_id, scheduled_for, duration_minutes, modality)
VALUES (@patient_id, @psychologist_id, @scheduled_for, @duration_minutes, @modality)
RETURNING id, patient_id, psychologist_id, scheduled_for, duration_minutes, modality, status, created_at;
