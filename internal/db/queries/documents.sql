-- name: ListDocumentsByPatient :many
SELECT d.id, d.patient_id, d.psychologist_id, d.template_id, d.type, d.pdf_url, d.created_at
FROM document d
JOIN patient_profile p ON p.id = d.patient_id
WHERE d.patient_id = @patient_id
  AND p.organization_id = @organization_id
ORDER BY d.created_at DESC;

-- name: CreateDocument :one
INSERT INTO document (patient_id, psychologist_id, template_id, type, pdf_url)
VALUES (@patient_id, @psychologist_id, @template_id, @type, @pdf_url)
RETURNING id, patient_id, psychologist_id, template_id, type, pdf_url, created_at;
