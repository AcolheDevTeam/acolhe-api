ALTER TABLE appointment
  ADD CONSTRAINT appointment_status_allowed
  CHECK (status IN ('scheduled','confirmed','completed','canceled','no_show'))
  NOT VALID;

ALTER TABLE appointment VALIDATE CONSTRAINT appointment_status_allowed;

CREATE POLICY appointment_psychologist_update ON appointment
  FOR UPDATE USING (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  ) WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  );
