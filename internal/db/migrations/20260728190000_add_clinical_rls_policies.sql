CREATE POLICY patient_profile_psychologist_insert ON patient_profile
  FOR INSERT WITH CHECK (
    current_user_role() = 'psychologist'
    AND organization_id = current_organization_id()
  );

CREATE POLICY session_clinical_select ON session
  FOR SELECT USING (
    (current_user_role() = 'psychologist' AND psychologist_id = current_psychologist_id())
    OR (current_user_role() = 'patient' AND patient_id IN (
      SELECT id FROM patient_profile WHERE user_id = current_user_id()
    ))
  );

CREATE POLICY session_psychologist_insert ON session
  FOR INSERT WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  );

CREATE POLICY clinical_record_clinical_select ON clinical_record
  FOR SELECT USING (
    (current_user_role() = 'psychologist' AND psychologist_id = current_psychologist_id())
    OR (current_user_role() = 'patient' AND patient_id IN (
      SELECT id FROM patient_profile WHERE user_id = current_user_id()
    ))
  );

CREATE POLICY clinical_record_psychologist_write ON clinical_record
  FOR ALL USING (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
  ) WITH CHECK (
    current_user_role() = 'psychologist'
    AND psychologist_id = current_psychologist_id()
    AND patient_id IN (
      SELECT patient_id FROM patient_relationship
      WHERE psychologist_id = current_psychologist_id() AND status = 'active'
    )
  );
