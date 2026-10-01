CREATE UNIQUE INDEX session_appointment_unique ON session (appointment_id) WHERE appointment_id IS NOT NULL;

CREATE POLICY session_psychologist_update ON session
  FOR UPDATE USING (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND has_active_clinical_relationship(patient_id, psychologist_id)
  ) WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND has_active_clinical_relationship(patient_id, psychologist_id)
  );
