-- name: GetPatientExportAccess :one
SELECT p.id, p.organization_id, requester.email AS requester_email
FROM patient_profile p
JOIN "user" requester ON requester.id = @requested_by
  AND requester.organization_id = p.organization_id
  AND requester.status = 'active'
WHERE p.id = @patient_id
  AND p.organization_id = @organization_id
  AND (
    p.user_id = @requested_by
    OR EXISTS (
      SELECT 1
      FROM patient_relationship relationship
      JOIN psychologist_profile psychologist
        ON psychologist.id = relationship.psychologist_id
      WHERE relationship.patient_id = p.id
        AND psychologist.user_id = @requested_by
    )
  );

-- name: CreateLGPDExportRequest :one
INSERT INTO lgpd_export_request (
  id, patient_id, organization_id, requested_by, requested_at, sla_deadline
) VALUES (
  @id, @patient_id, @organization_id, @requested_by, @requested_at, @sla_deadline
)
RETURNING id, patient_id, organization_id, requested_by, requested_at,
          sla_deadline, status, attempts, object_key, artifact_sha256,
          completed_at, notified_at, last_error, created_at, updated_at;

-- name: MarkLGPDExportQueueFailed :execrows
UPDATE lgpd_export_request
SET status = 'failed', last_error = @last_error, updated_at = now()
WHERE id = @id AND requested_by = @requested_by AND status = 'queued';

-- name: GetLGPDExportRequest :one
SELECT id, patient_id, organization_id, requested_by, requested_at,
       sla_deadline, status, attempts, object_key, artifact_sha256,
       completed_at, notified_at, last_error, created_at, updated_at
FROM lgpd_export_request
WHERE id = @id
  AND patient_id = @patient_id
  AND organization_id = @organization_id
  AND requested_by = @requested_by;

-- name: MarkLGPDExportProcessing :execrows
UPDATE lgpd_export_request
SET status = 'processing',
    attempts = attempts + 1,
    last_error = NULL,
    updated_at = now()
WHERE id = @id
  AND patient_id = @patient_id
  AND organization_id = @organization_id
  AND requested_by = @requested_by
  AND status IN ('queued','failed','processing');

-- name: MarkLGPDExportStored :execrows
UPDATE lgpd_export_request
SET status = 'stored',
    object_key = @object_key,
    artifact_sha256 = @artifact_sha256,
    last_error = NULL,
    updated_at = now()
WHERE id = @id
  AND requested_by = @requested_by
  AND status = 'processing';

-- name: MarkLGPDExportCompleted :execrows
UPDATE lgpd_export_request
SET status = 'completed',
    completed_at = @completed_at,
    notified_at = @notified_at,
    last_error = NULL,
    updated_at = now()
WHERE id = @id
  AND requested_by = @requested_by
  AND status = 'stored';

-- name: MarkLGPDExportFailed :execrows
UPDATE lgpd_export_request
SET status = CASE WHEN status = 'stored' THEN 'stored' ELSE 'failed' END,
    last_error = @last_error,
    updated_at = now()
WHERE id = @id
  AND requested_by = @requested_by
  AND status NOT IN ('completed');

-- name: GetLGPDExportSLAMetric :one
SELECT organization_id, breached_pending, breached_completed, pending,
       max_duration_seconds
FROM lgpd_export_sla_metric
WHERE organization_id = @organization_id;

-- name: GetPatientLGPDExportData :one
SELECT request.id AS request_id,
       requester.email AS recipient_email,
       patient.full_name AS patient_name,
       jsonb_build_object(
         'exportVersion', '1.0',
         'request', jsonb_build_object(
           'id', request.id,
           'requestedAt', request.requested_at,
           'slaDeadline', request.sla_deadline
         ),
         'patient', jsonb_build_object(
           'id', patient.id,
           'fullName', patient.full_name,
           'birthDate', patient.birth_date,
           'status', patient.status,
           'deletedAt', patient.deleted_at,
           'createdAt', patient.created_at,
           'updatedAt', patient.updated_at
         ),
         'account', CASE WHEN patient_account.id IS NULL THEN NULL ELSE jsonb_build_object(
           'id', patient_account.id,
           'email', patient_account.email,
           'role', patient_account.role,
           'status', patient_account.status,
           'twoFactorEnabled', patient_account.two_factor_enabled,
           'lastLoginAt', patient_account.last_login_at,
           'locale', patient_account.locale,
           'createdAt', patient_account.created_at,
           'updatedAt', patient_account.updated_at
         ) END,
         'consents', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', consent.id,
             'scope', document.scope,
             'documentVersion', document.version,
             'documentTitle', document.title,
             'documentContent', document.content,
             'documentSha256', document.content_sha256,
             'accepted', consent.accepted,
             'decidedAt', consent.decided_at,
             'ipAddress', consent.ip_address,
             'userAgent', consent.user_agent
           ) ORDER BY consent.decided_at), '[]'::jsonb)
           FROM consent
           JOIN consent_document document ON document.id = consent.document_id
           WHERE consent.patient_id = patient.id
         ),
         'relationships', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', relationship.id,
             'psychologist', jsonb_build_object(
               'id', psychologist.id,
               'fullName', psychologist.full_name,
               'crpNumber', psychologist.crp_number,
               'crpState', psychologist.crp_state
             ),
             'status', relationship.status,
             'requiresHealthConsent', relationship.requires_health_consent,
             'endReason', relationship.end_reason,
             'startedAt', relationship.started_at,
             'endedAt', relationship.ended_at,
             'createdAt', relationship.created_at
           ) ORDER BY relationship.created_at), '[]'::jsonb)
           FROM patient_relationship relationship
           JOIN psychologist_profile psychologist
             ON psychologist.id = relationship.psychologist_id
           WHERE relationship.patient_id = patient.id
         ),
         'invitations', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', invitation.id,
             'email', invitation.email,
             'status', invitation.status,
             'expiresAt', invitation.expires_at,
             'acceptedAt', invitation.accepted_at,
             'declinedAt', invitation.declined_at,
             'createdAt', invitation.created_at,
             'updatedAt', invitation.updated_at
           ) ORDER BY invitation.created_at), '[]'::jsonb)
           FROM patient_invitation invitation
           WHERE invitation.patient_id = patient.id
         ),
         'appointments', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', appointment.id,
             'psychologistId', appointment.psychologist_id,
             'scheduledFor', appointment.scheduled_for,
             'durationMinutes', appointment.duration_minutes,
             'modality', appointment.modality,
             'status', appointment.status,
             'createdAt', appointment.created_at,
             'updatedAt', appointment.updated_at
           ) ORDER BY appointment.scheduled_for), '[]'::jsonb)
           FROM appointment
           WHERE appointment.patient_id = patient.id
         ),
         'sessions', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', session.id,
             'appointmentId', session.appointment_id,
             'psychologistId', session.psychologist_id,
             'occurredAt', session.occurred_at,
             'status', session.status,
             'createdAt', session.created_at,
             'updatedAt', session.updated_at
           ) ORDER BY session.occurred_at), '[]'::jsonb)
           FROM session
           WHERE session.patient_id = patient.id
         ),
         'clinicalRecords', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', record.id,
             'sessionId', record.session_id,
             'psychologistId', record.psychologist_id,
             'content', record.content_jsonb,
             'version', record.version,
             'lockedAt', record.locked_at,
             'lockedReason', record.locked_reason,
             'createdAt', record.created_at,
             'updatedAt', record.updated_at,
             'history', (
               SELECT COALESCE(jsonb_agg(jsonb_build_object(
                 'id', version.id,
                 'versionNumber', version.version_number,
                 'content', version.content_snapshot,
                 'changedBy', version.changed_by,
                 'changeReason', version.change_reason,
                 'createdAt', version.created_at
               ) ORDER BY version.version_number), '[]'::jsonb)
               FROM clinical_record_version version
               WHERE version.clinical_record_id = record.id
             )
           ) ORDER BY record.created_at), '[]'::jsonb)
           FROM clinical_record record
           WHERE record.patient_id = patient.id
         ),
         'activities', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', assignment.id,
             'templateId', assignment.template_id,
             'templateVersion', assignment.template_version,
             'title', template.title,
             'type', type.code,
             'status', assignment.status,
             'scheduledFor', assignment.scheduled_for,
             'dueAt', assignment.due_at,
             'reviewedAt', assignment.reviewed_at,
             'createdAt', assignment.created_at,
             'updatedAt', assignment.updated_at,
             'responses', (
               SELECT COALESCE(jsonb_agg(jsonb_build_object(
                 'id', response.id,
                 'submittedAt', response.submitted_at,
                 'isDraft', response.is_draft,
                 'summaryScore', response.summary_score,
                 'createdAt', response.created_at,
                 'values', (
                   SELECT COALESCE(jsonb_agg(jsonb_build_object(
                     'fieldId', value.field_id,
                     'fieldCode', value.field_code,
                     'valueText', value.value_text,
                     'valueNumber', value.value_number,
                     'valueBoolean', value.value_boolean,
                     'valueDatetime', value.value_datetime,
                     'valueJson', value.value_json,
                     'attachmentId', value.attachment_id,
                     'createdAt', value.created_at
                   ) ORDER BY field.display_order), '[]'::jsonb)
                   FROM activity_response_value value
                   JOIN activity_field field ON field.id = value.field_id
                   WHERE value.response_id = response.id
                 ),
                 'sharedComments', (
                   SELECT COALESCE(jsonb_agg(jsonb_build_object(
                     'id', comment.id,
                     'authorId', comment.author_id,
                     'content', comment.content,
                     'createdAt', comment.created_at
                   ) ORDER BY comment.created_at), '[]'::jsonb)
                   FROM activity_comment comment
                   WHERE comment.response_id = response.id
                     AND comment.visibility = 'shared'
                 )
               ) ORDER BY response.created_at), '[]'::jsonb)
               FROM activity_response response
               WHERE response.assignment_id = assignment.id
             )
           ) ORDER BY assignment.created_at), '[]'::jsonb)
           FROM activity_assignment assignment
           JOIN activity_template template ON template.id = assignment.template_id
           JOIN activity_type type ON type.id = template.type_id
           WHERE assignment.patient_id = patient.id
         ),
         'checkins', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', checkin.id,
             'mood', checkin.mood,
             'note', checkin.note,
             'createdAt', checkin.created_at
           ) ORDER BY checkin.created_at), '[]'::jsonb)
           FROM checkin
           WHERE checkin.patient_id = patient.id
         ),
         'documents', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', document.id,
             'psychologistId', document.psychologist_id,
             'type', document.type,
             'pdfUrl', document.pdf_url,
             'signatureHash', document.signature_hash,
             'createdAt', document.created_at
           ) ORDER BY document.created_at), '[]'::jsonb)
           FROM document
           WHERE document.patient_id = patient.id
         ),
         'attachments', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', attachment.id,
             'ownerId', attachment.owner_id,
             'mimeType', attachment.mime_type,
             'sizeBytes', attachment.size_bytes,
             'scanStatus', attachment.scan_status,
             'createdAt', attachment.created_at
           ) ORDER BY attachment.created_at), '[]'::jsonb)
           FROM attachment
           WHERE attachment.patient_id = patient.id
         ),
         'notifications', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', notification.id,
             'channel', notification.channel,
             'templateName', notification.template_name,
             'payload', notification.payload_jsonb,
             'sentAt', notification.sent_at,
             'readAt', notification.read_at,
             'createdAt', notification.created_at
           ) ORDER BY notification.created_at), '[]'::jsonb)
           FROM notification
           WHERE notification.user_id = patient.user_id
         ),
         'auditEvents', (
           SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'id', audit.id,
             'action', audit.action,
             'resourceType', audit.resource_type,
             'resourceId', audit.resource_id,
             'occurredAt', audit.occurred_at
           ) ORDER BY audit.occurred_at), '[]'::jsonb)
           FROM audit_log audit
           WHERE audit.organization_id = patient.organization_id
             AND (
               (audit.resource_type = 'patient' AND audit.resource_id = patient.id::text)
               OR audit.actor_user_id = patient.user_id
             )
         ),
         'scopeNotes', jsonb_build_array(
           'Senhas, segredos de autenticação e chaves internas não são exportados.',
           'Registro documental privativo do psicólogo não integra o prontuário acessível ao paciente.'
         )
       ) AS export_data
FROM lgpd_export_request request
JOIN patient_profile patient ON patient.id = request.patient_id
JOIN "user" requester ON requester.id = request.requested_by
LEFT JOIN "user" patient_account ON patient_account.id = patient.user_id
WHERE request.id = @id
  AND request.patient_id = @patient_id
  AND request.organization_id = @organization_id
  AND request.requested_by = @requested_by
  AND patient.organization_id = @organization_id;
