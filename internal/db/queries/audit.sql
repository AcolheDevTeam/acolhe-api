-- name: WriteAuditLog :exec
INSERT INTO audit_log
  (actor_user_id, organization_id, action, resource_type, resource_id, metadata_jsonb)
VALUES (@actor_user_id, @organization_id, @action, @resource_type, @resource_id, @metadata_jsonb);
