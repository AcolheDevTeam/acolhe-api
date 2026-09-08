CREATE POLICY appointment_clinical_select ON appointment
  FOR SELECT USING (
    (current_user_role() = 'psychologist' AND psychologist_id = current_psychologist_id())
    OR (current_user_role() = 'patient' AND patient_id IN (
      SELECT id FROM patient_profile WHERE user_id = current_user_id()
    ))
  );

CREATE POLICY appointment_psychologist_insert ON appointment
  FOR INSERT WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  );
