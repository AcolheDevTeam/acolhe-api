-- Preserva duplicatas antigas; apenas o registro mais recente ocupa a chave diária.
ALTER TABLE checkin ADD COLUMN daily_day date;
ALTER TABLE checkin ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
UPDATE checkin SET updated_at = created_at;
WITH ranked AS (
  SELECT id, (created_at AT TIME ZONE 'America/Fortaleza')::date AS day,
    row_number() OVER (PARTITION BY patient_id, (created_at AT TIME ZONE 'America/Fortaleza')::date ORDER BY created_at DESC, id DESC) AS position
  FROM checkin
)
UPDATE checkin c SET daily_day = ranked.day FROM ranked WHERE c.id = ranked.id AND ranked.position = 1;
ALTER TABLE checkin ALTER COLUMN daily_day SET DEFAULT ((now() AT TIME ZONE 'America/Fortaleza')::date);
ALTER TABLE checkin ADD CONSTRAINT checkin_daily_day_matches_created CHECK (daily_day IS NULL OR daily_day = (created_at AT TIME ZONE 'America/Fortaleza')::date);
CREATE UNIQUE INDEX checkin_patient_daily_unique ON checkin (patient_id, daily_day) WHERE daily_day IS NOT NULL;

ALTER TABLE checkin ENABLE ROW LEVEL SECURITY;
CREATE POLICY checkin_clinical_select ON checkin FOR SELECT USING (
  (current_user_role() = 'patient' AND patient_id IN (
    SELECT id FROM patient_profile WHERE user_id = current_user_id() AND organization_id = current_organization_id()
  )) OR (current_user_role() = 'psychologist' AND has_active_clinical_relationship(patient_id, current_psychologist_id()))
);
CREATE POLICY checkin_clinical_insert ON checkin FOR INSERT WITH CHECK (
  (current_user_role() = 'patient' AND patient_id IN (
    SELECT id FROM patient_profile WHERE user_id = current_user_id() AND organization_id = current_organization_id()
  ) AND EXISTS (SELECT 1 FROM patient_relationship r WHERE r.patient_id = checkin.patient_id AND has_active_clinical_relationship(r.patient_id, r.psychologist_id)))
  OR (current_user_role() = 'psychologist' AND has_active_clinical_relationship(patient_id, current_psychologist_id()))
);
CREATE POLICY checkin_patient_update ON checkin FOR UPDATE USING (
  current_user_role() = 'patient' AND patient_id IN (
    SELECT id FROM patient_profile WHERE user_id = current_user_id() AND organization_id = current_organization_id()
  ) AND daily_day = (now() AT TIME ZONE 'America/Fortaleza')::date
  AND EXISTS (SELECT 1 FROM patient_relationship r WHERE r.patient_id = checkin.patient_id AND has_active_clinical_relationship(r.patient_id, r.psychologist_id))
) WITH CHECK (
  current_user_role() = 'patient' AND patient_id IN (
    SELECT id FROM patient_profile WHERE user_id = current_user_id() AND organization_id = current_organization_id()
  ) AND daily_day = (now() AT TIME ZONE 'America/Fortaleza')::date
);
