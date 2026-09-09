-- Track delivery without ever storing the opaque invitation token.
ALTER TABLE patient_invitation
  ADD COLUMN delivery_status text NOT NULL DEFAULT 'queued'
    CHECK (delivery_status IN ('queued', 'sending', 'sent', 'failed')),
  ADD COLUMN delivery_attempts integer NOT NULL DEFAULT 0
    CHECK (delivery_attempts >= 0),
  ADD COLUMN last_delivery_at timestamptz;
