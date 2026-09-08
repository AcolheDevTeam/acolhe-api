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
FROM appointment a
JOIN patient_profile patient ON patient.id = a.patient_id
WHERE a.psychologist_id = @psychologist_id
  AND patient.organization_id = @organization_id
  AND a.status <> 'canceled'
  AND tstzrange(a.scheduled_for, a.scheduled_for + a.duration_minutes * interval '1 minute')
   && tstzrange(@window_start, @window_end);

-- name: CreateAppointment :one
INSERT INTO appointment (patient_id, psychologist_id, scheduled_for, duration_minutes, modality)
VALUES (@patient_id, @psychologist_id, @scheduled_for, @duration_minutes, @modality)
RETURNING id, patient_id, psychologist_id, scheduled_for, duration_minutes, modality, status, created_at;

-- name: GetAppointmentForPsychologist :one
SELECT a.id, a.patient_id, a.psychologist_id, a.scheduled_for,
       a.duration_minutes, a.modality, a.status, a.created_at
FROM appointment a
JOIN patient_profile patient ON patient.id = a.patient_id
WHERE a.id = @id
  AND a.psychologist_id = @psychologist_id
  AND patient.organization_id = @organization_id;

-- name: UpdateAppointmentStatus :one
UPDATE appointment appointment
SET status = @status, updated_at = now()
FROM patient_profile patient
WHERE appointment.id = @id
  AND patient.id = appointment.patient_id
  AND appointment.psychologist_id = @psychologist_id
  AND patient.organization_id = @organization_id
  AND appointment.status = @current_status
RETURNING appointment.id, appointment.patient_id, appointment.psychologist_id,
          appointment.scheduled_for, appointment.duration_minutes,
          appointment.modality, appointment.status, appointment.created_at;
