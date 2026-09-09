-- Public invitation requests have no tenant identity, so a direct join against
-- patient_profile is hidden by RLS for the non-superuser application role.
-- Restrict the bypass to a lookup by the invitation's unguessable token digest.
CREATE OR REPLACE FUNCTION get_patient_invitation(
  supplied_token_digest bytea
) RETURNS jsonb AS $$
  SELECT jsonb_build_object(
    'id', i.id,
    'patientId', i.patient_id,
    'relationshipId', i.relationship_id,
    'email', i.email,
    'status', i.status,
    'expiresAt', i.expires_at,
    'patientName', p.full_name,
    'psychologistName', psy.full_name,
    'crpNumber', psy.crp_number,
    'crpState', psy.crp_state
  )
  FROM patient_invitation i
  JOIN patient_profile p ON p.id = i.patient_id
  JOIN patient_relationship r ON r.id = i.relationship_id
  JOIN psychologist_profile psy ON psy.id = r.psychologist_id
  WHERE i.token_digest = supplied_token_digest;
$$ LANGUAGE SQL STABLE SECURITY DEFINER SET search_path = public, pg_temp;
