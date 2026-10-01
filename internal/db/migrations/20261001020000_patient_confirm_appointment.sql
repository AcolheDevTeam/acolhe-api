CREATE POLICY appointment_patient_confirm ON appointment
  FOR UPDATE USING (
    current_user_role() = 'patient'
    AND patient_id IN (
      SELECT id FROM patient_profile
      WHERE user_id = current_user_id() AND organization_id = current_organization_id()
    )
    AND status IN ('scheduled', 'confirmed')
    AND scheduled_for > now()
    AND has_active_clinical_relationship(patient_id, psychologist_id)
  ) WITH CHECK (
    current_user_role() = 'patient'
    AND patient_id IN (
      SELECT id FROM patient_profile
      WHERE user_id = current_user_id() AND organization_id = current_organization_id()
    )
    AND status = 'confirmed'
    AND scheduled_for > now()
    AND has_active_clinical_relationship(patient_id, psychologist_id)
  );
