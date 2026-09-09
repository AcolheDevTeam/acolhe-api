-- Complete, typed activity responses are required before clinical review.
ALTER TABLE activity_assignment ADD COLUMN reviewed_at timestamptz;
UPDATE activity_assignment
SET reviewed_at = updated_at
WHERE status = 'reviewed' AND reviewed_at IS NULL;

CREATE OR REPLACE FUNCTION activity_submission_is_complete(
  target_assignment_id uuid
) RETURNS boolean AS $$
  SELECT EXISTS (
    SELECT 1
    FROM activity_assignment a
    JOIN activity_template t ON t.id = a.template_id
    JOIN activity_response r ON r.assignment_id = a.id
      AND NOT r.is_draft
      AND r.submitted_at IS NOT NULL
    WHERE a.id = target_assignment_id
      AND a.template_version = t.version
      AND (
        SELECT count(*) FROM activity_response final
        WHERE final.assignment_id = a.id AND NOT final.is_draft
      ) = 1
      AND (
        SELECT count(*) FROM activity_field expected
        WHERE expected.template_id = a.template_id
      ) > 0
      AND (
        SELECT count(*) FROM activity_response_value actual
        WHERE actual.response_id = r.id
      ) = (
        SELECT count(*) FROM activity_field expected
        WHERE expected.template_id = a.template_id
      )
      AND NOT EXISTS (
        SELECT 1
        FROM activity_response_value value
        LEFT JOIN activity_field field ON field.id = value.field_id
        WHERE value.response_id = r.id
          AND (
            field.id IS NULL
            OR field.template_id <> a.template_id
            OR value.field_code <> field.code
            OR num_nonnulls(
              value.value_text, value.value_number, value.value_boolean,
              value.value_datetime, value.value_json, value.attachment_id
            ) <> 1
          )
      )
      AND NOT EXISTS (
        SELECT 1
        FROM activity_field expected
        WHERE expected.template_id = a.template_id
          AND (
            SELECT count(*)
            FROM activity_response_value value
            WHERE value.response_id = r.id
              AND value.field_id = expected.id
          ) <> 1
      )
  )
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION validate_final_activity_response() RETURNS trigger AS $$
BEGIN
  IF NOT NEW.is_draft AND EXISTS (
    SELECT 1 FROM activity_response existing
    WHERE existing.assignment_id = NEW.assignment_id
      AND NOT existing.is_draft
      AND existing.id <> NEW.id
  ) THEN
    RAISE EXCEPTION 'only one final response is allowed' USING ERRCODE = '23505';
  END IF;
  IF NOT NEW.is_draft AND NEW.submitted_at IS NULL THEN
    RAISE EXCEPTION 'final response requires submitted_at' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER activity_response_final_integrity
  BEFORE INSERT OR UPDATE OF assignment_id, is_draft, submitted_at ON activity_response
  FOR EACH ROW EXECUTE FUNCTION validate_final_activity_response();

CREATE OR REPLACE FUNCTION validate_activity_response_value() RETURNS trigger AS $$
DECLARE
  expected_template_id uuid;
  expected_code text;
BEGIN
  IF num_nonnulls(
    NEW.value_text, NEW.value_number, NEW.value_boolean,
    NEW.value_datetime, NEW.value_json, NEW.attachment_id
  ) <> 1 THEN
    RAISE EXCEPTION 'exactly one typed response value is required' USING ERRCODE = '23514';
  END IF;
  IF EXISTS (
    SELECT 1 FROM activity_response_value existing
    WHERE existing.response_id = NEW.response_id
      AND existing.field_id = NEW.field_id
      AND existing.id <> NEW.id
  ) THEN
    RAISE EXCEPTION 'duplicate response field' USING ERRCODE = '23505';
  END IF;
  SELECT a.template_id, f.code
  INTO expected_template_id, expected_code
  FROM activity_response r
  JOIN activity_assignment a ON a.id = r.assignment_id
  JOIN activity_field f ON f.id = NEW.field_id
  WHERE r.id = NEW.response_id
    AND f.template_id = a.template_id;
  IF expected_template_id IS NULL OR NEW.field_code <> expected_code THEN
    RAISE EXCEPTION 'response field does not belong to assigned template' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER activity_response_value_integrity
  BEFORE INSERT OR UPDATE ON activity_response_value
  FOR EACH ROW EXECUTE FUNCTION validate_activity_response_value();

CREATE OR REPLACE FUNCTION reject_activity_submission_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'submitted activity responses are append-only' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER activity_response_append_only
  BEFORE UPDATE OR DELETE ON activity_response
  FOR EACH ROW
  WHEN (OLD.is_draft = false)
  EXECUTE FUNCTION reject_activity_submission_mutation();
CREATE TRIGGER activity_response_value_append_only
  BEFORE UPDATE OR DELETE ON activity_response_value
  FOR EACH ROW EXECUTE FUNCTION reject_activity_submission_mutation();
