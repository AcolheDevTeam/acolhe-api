-- name: GetDocumentaryNotebook :one
SELECT id, patient_id, category, revision, updated_at, content_encrypted
FROM documentary_record
WHERE id = @id AND organization_id = @organization_id AND author_id = @author_id;

-- name: ListDocumentaryVersions :many
SELECT id, revision, created_at, restored_from
FROM documentary_record_version
WHERE record_id = @record_id AND organization_id = @organization_id AND author_id = @author_id
ORDER BY revision DESC, id LIMIT @page_size OFFSET @page_offset;
