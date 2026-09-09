-- Persist the LGPD export lifecycle so the 24-hour SLA is measurable.
CREATE TABLE lgpd_export_request (
  id              uuid PRIMARY KEY,
  patient_id      uuid NOT NULL REFERENCES patient_profile(id),
  organization_id uuid NOT NULL REFERENCES organization(id),
  requested_by    uuid NOT NULL REFERENCES "user"(id),
  requested_at    timestamptz NOT NULL,
  sla_deadline    timestamptz NOT NULL,
  status          text NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued','processing','stored','completed','failed')),
  attempts        integer NOT NULL DEFAULT 0,
  object_key      text,
  artifact_sha256 text CHECK (artifact_sha256 IS NULL OR artifact_sha256 ~ '^[0-9a-f]{64}$'),
  completed_at    timestamptz,
  notified_at     timestamptz,
  last_error      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CHECK (sla_deadline = requested_at + interval '24 hours'),
  CHECK (
    (status IN ('stored','completed') AND object_key IS NOT NULL AND artifact_sha256 IS NOT NULL)
    OR status IN ('queued','processing','failed')
  ),
  CHECK (
    (status = 'completed' AND completed_at IS NOT NULL AND notified_at IS NOT NULL)
    OR status <> 'completed'
  )
);

CREATE INDEX idx_lgpd_export_sla_pending
  ON lgpd_export_request (sla_deadline)
  WHERE status IN ('queued','processing','stored');
CREATE INDEX idx_lgpd_export_patient_time
  ON lgpd_export_request (patient_id, requested_at DESC);

ALTER TABLE lgpd_export_request ENABLE ROW LEVEL SECURITY;
CREATE POLICY lgpd_export_request_actor_only ON lgpd_export_request
  FOR ALL USING (
    requested_by = current_user_id()
    AND organization_id = current_organization_id()
  ) WITH CHECK (
    requested_by = current_user_id()
    AND organization_id = current_organization_id()
  );

CREATE VIEW lgpd_export_sla_metric
  WITH (security_invoker = true) AS
SELECT organization_id,
       count(*) FILTER (
         WHERE status <> 'completed' AND now() > sla_deadline
       )::integer AS breached_pending,
       count(*) FILTER (
         WHERE status = 'completed' AND completed_at > sla_deadline
       )::integer AS breached_completed,
       count(*) FILTER (
         WHERE status IN ('queued','processing','stored')
       )::integer AS pending,
       max(EXTRACT(epoch FROM (
         COALESCE(completed_at, now()) - requested_at
       ))) AS max_duration_seconds
FROM lgpd_export_request
GROUP BY organization_id;
