ALTER TABLE patient_profile ADD COLUMN email text;
UPDATE patient_profile SET email = concat('unknown+', id, '@invalid.local') WHERE email IS NULL;
ALTER TABLE patient_profile ALTER COLUMN email SET NOT NULL;

CREATE TABLE patient_invitation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  patient_id uuid NOT NULL REFERENCES patient_profile(id),
  organization_id uuid NOT NULL REFERENCES organization(id),
  email text NOT NULL,
  token_hash bytea NOT NULL UNIQUE,
  token_ciphertext bytea NOT NULL,
  expires_at timestamptz NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','expired','revoked')),
  delivery_status text NOT NULL DEFAULT 'queued' CHECK (delivery_status IN ('queued','sending','sent','failed')),
  delivery_attempts integer NOT NULL DEFAULT 0,
  last_delivery_error text,
  sent_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_patient_invitation_patient ON patient_invitation(patient_id, created_at DESC);
CREATE INDEX idx_patient_invitation_org ON patient_invitation(organization_id, created_at DESC);
