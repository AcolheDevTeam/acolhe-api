-- Create patient invitation records used by the public acceptance flow.
CREATE TABLE patient_invitation (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id       uuid NOT NULL REFERENCES patient_profile(id),
  relationship_id  uuid NOT NULL REFERENCES patient_relationship(id),
  email            text NOT NULL,
  token_hash       text NOT NULL UNIQUE,
  expires_at       timestamptz NOT NULL,
  accepted_at      timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_patient_invitation_token
  ON patient_invitation (token_hash)
  WHERE accepted_at IS NULL;

CREATE POLICY appointment_patient_read ON appointment
  FOR SELECT USING (patient_id IN (
    SELECT id FROM patient_profile WHERE user_id = current_user_id()
  ));

CREATE POLICY session_patient_read ON session
  FOR SELECT USING (patient_id IN (
    SELECT id FROM patient_profile WHERE user_id = current_user_id()
  ));
