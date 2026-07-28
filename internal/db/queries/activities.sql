-- name: ListActivityTemplates :many
-- Templates da organização (organization_id pode ser nulo p/ templates globais).
SELECT t.id, t.title, ty.code AS type, t.description, t.version, t.created_at
FROM activity_template t
JOIN activity_type ty ON ty.id = t.type_id
WHERE (t.organization_id = @organization_id OR t.organization_id IS NULL)
  AND t.is_archived = false
ORDER BY t.title;

-- name: GetActivityTemplateInOrg :one
SELECT t.id, t.title, ty.code AS type, t.version
FROM activity_template t
JOIN activity_type ty ON ty.id = t.type_id
WHERE t.id = @id
  AND (t.organization_id = @organization_id OR t.organization_id IS NULL)
  AND t.is_archived = false;

-- name: ListAssignmentsByPatient :many
SELECT ag.id, ag.template_id, ag.patient_id, p.full_name AS patient_name,
       ag.status, ag.due_at, ag.created_at,
       t.title, ty.code AS type, latest.submitted_at AS responded_at
FROM activity_assignment ag
JOIN patient_profile p ON p.id = ag.patient_id
JOIN activity_template t ON t.id = ag.template_id
JOIN activity_type ty ON ty.id = t.type_id
LEFT JOIN LATERAL (
  SELECT submitted_at
  FROM activity_response
  WHERE assignment_id = ag.id AND is_draft = false
  ORDER BY submitted_at DESC NULLS LAST
  LIMIT 1
) latest ON true
WHERE ag.patient_id = @patient_id
  AND p.organization_id = @organization_id
  AND ag.assigner_id = @assigner_id
ORDER BY ag.created_at DESC;

-- name: ListAssignmentsByPsychologist :many
SELECT ag.id, ag.template_id, ag.patient_id, p.full_name AS patient_name,
       ag.status, ag.due_at, ag.created_at,
       t.title, ty.code AS type, latest.submitted_at AS responded_at
FROM activity_assignment ag
JOIN patient_profile p ON p.id = ag.patient_id
JOIN activity_template t ON t.id = ag.template_id
JOIN activity_type ty ON ty.id = t.type_id
LEFT JOIN LATERAL (
  SELECT submitted_at
  FROM activity_response
  WHERE assignment_id = ag.id AND is_draft = false
  ORDER BY submitted_at DESC NULLS LAST
  LIMIT 1
) latest ON true
WHERE p.organization_id = @organization_id
  AND ag.assigner_id = @assigner_id
ORDER BY ag.created_at DESC;

-- name: CreateAssignment :one
INSERT INTO activity_assignment
  (template_id, template_version, patient_id, assigner_id, status, scheduled_for, due_at)
VALUES (@template_id, @template_version, @patient_id, @assigner_id, 'pending', @scheduled_for, @due_at)
RETURNING id, template_id, template_version, patient_id, assigner_id, status, scheduled_for, due_at, created_at;

-- name: GetAssignmentInOrg :one
-- Confirma que o assignment existe e pertence à organização (via paciente).
SELECT ag.id, ag.patient_id, ag.assigner_id, ag.status
FROM activity_assignment ag
JOIN patient_profile p ON p.id = ag.patient_id
WHERE ag.id = @id AND p.organization_id = @organization_id;

-- name: GetAssignmentDetailInOrg :one
SELECT ag.id, ag.template_id, ag.patient_id, p.full_name AS patient_name,
       ag.status, ag.due_at, ag.created_at,
       t.title, ty.code AS type, latest.submitted_at AS responded_at
FROM activity_assignment ag
JOIN patient_profile p ON p.id = ag.patient_id
JOIN activity_template t ON t.id = ag.template_id
JOIN activity_type ty ON ty.id = t.type_id
LEFT JOIN LATERAL (
  SELECT submitted_at
  FROM activity_response
  WHERE assignment_id = ag.id AND is_draft = false
  ORDER BY submitted_at DESC NULLS LAST
  LIMIT 1
) latest ON true
WHERE ag.id = @id
  AND p.organization_id = @organization_id
  AND ag.assigner_id = @assigner_id;

-- name: MarkAssignmentReviewed :execrows
UPDATE activity_assignment ag
SET status = 'reviewed', updated_at = now()
FROM patient_profile p
WHERE ag.id = @id
  AND p.id = ag.patient_id
  AND p.organization_id = @organization_id
  AND ag.assigner_id = @assigner_id
  AND ag.status = 'submitted';

-- name: SubmitResponse :one
-- Cria (submete) a resposta de uma atividade e marca o assignment como submitted.
INSERT INTO activity_response (assignment_id, submitted_at, is_draft, summary_score)
VALUES (@assignment_id, now(), false, @summary_score)
RETURNING id, assignment_id, submitted_at, is_draft, summary_score, created_at;

-- name: ClaimAssignmentForSubmission :execrows
UPDATE activity_assignment ag
SET status = 'submitted', updated_at = now()
FROM patient_profile p
WHERE ag.id = @id
  AND p.id = ag.patient_id
  AND ag.patient_id = @patient_id
  AND p.organization_id = @organization_id
  AND ag.status IN ('pending', 'in_progress');

-- name: MarkAssignmentSubmitted :exec
UPDATE activity_assignment SET status = 'submitted', updated_at = now()
WHERE id = @id;

-- name: ListResponsesByAssignment :many
SELECT id, assignment_id, submitted_at, is_draft, summary_score, created_at
FROM activity_response
WHERE assignment_id = @assignment_id
ORDER BY created_at DESC;
