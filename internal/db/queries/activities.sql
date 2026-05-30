-- name: ListActivityTemplates :many
-- Templates da organização (organization_id pode ser nulo p/ templates globais).
SELECT id, type_id, organization_id, author_id, title, description, version, is_archived, created_at
FROM activity_template
WHERE (organization_id = @organization_id OR organization_id IS NULL)
  AND is_archived = false
ORDER BY title;

-- name: ListAssignmentsByPatient :many
SELECT ag.id, ag.template_id, ag.template_version, ag.patient_id, ag.assigner_id,
       ag.status, ag.scheduled_for, ag.due_at, ag.created_at
FROM activity_assignment ag
JOIN patient_profile p ON p.id = ag.patient_id
WHERE ag.patient_id = @patient_id
  AND p.organization_id = @organization_id
ORDER BY ag.created_at DESC;

-- name: CreateAssignment :one
INSERT INTO activity_assignment
  (template_id, template_version, patient_id, assigner_id, status, scheduled_for, due_at)
VALUES (@template_id, @template_version, @patient_id, @assigner_id, 'pending', @scheduled_for, @due_at)
RETURNING id, template_id, template_version, patient_id, assigner_id, status, scheduled_for, due_at, created_at;

-- name: GetAssignmentInOrg :one
-- Confirma que o assignment existe e pertence à organização (via paciente).
SELECT ag.id, ag.patient_id, ag.status
FROM activity_assignment ag
JOIN patient_profile p ON p.id = ag.patient_id
WHERE ag.id = @id AND p.organization_id = @organization_id;

-- name: SubmitResponse :one
-- Cria (submete) a resposta de uma atividade e marca o assignment como submitted.
INSERT INTO activity_response (assignment_id, submitted_at, is_draft, summary_score)
VALUES (@assignment_id, now(), false, @summary_score)
RETURNING id, assignment_id, submitted_at, is_draft, summary_score, created_at;

-- name: MarkAssignmentSubmitted :exec
UPDATE activity_assignment SET status = 'submitted', updated_at = now()
WHERE id = @id;

-- name: ListResponsesByAssignment :many
SELECT id, assignment_id, submitted_at, is_draft, summary_score, created_at
FROM activity_response
WHERE assignment_id = @assignment_id
ORDER BY created_at DESC;
