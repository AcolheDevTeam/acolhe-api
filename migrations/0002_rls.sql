-- Acolhe — Row-Level Security
-- Segunda camada de defesa (a primeira é o middleware da aplicação).
-- A aplicação seta o contexto por transação via SET LOCAL acolhe.* (ver README).

BEGIN;

-- Contexto do usuário atual ---------------------------------

CREATE OR REPLACE FUNCTION current_organization_id() RETURNS uuid AS $$
  SELECT NULLIF(current_setting('acolhe.organization_id', true), '')::uuid
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION current_user_id() RETURNS uuid AS $$
  SELECT NULLIF(current_setting('acolhe.user_id', true), '')::uuid
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION current_psychologist_id() RETURNS uuid AS $$
  SELECT NULLIF(current_setting('acolhe.psychologist_id', true), '')::uuid
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION current_user_role() RETURNS text AS $$
  SELECT current_setting('acolhe.user_role', true)
$$ LANGUAGE SQL STABLE;

-- Habilitar RLS ---------------------------------------------

ALTER TABLE patient_profile     ENABLE ROW LEVEL SECURITY;
ALTER TABLE appointment         ENABLE ROW LEVEL SECURITY;
ALTER TABLE session             ENABLE ROW LEVEL SECURITY;
ALTER TABLE clinical_record     ENABLE ROW LEVEL SECURITY;
ALTER TABLE documentary_record  ENABLE ROW LEVEL SECURITY;

-- Políticas -------------------------------------------------

-- Psicólogo vê só seus pacientes; paciente vê só a si; org_admin vê listagem da org.
CREATE POLICY patient_isolation ON patient_profile
  FOR SELECT USING (
    CASE current_user_role()
      WHEN 'psychologist' THEN id IN (
        SELECT patient_id FROM patient_relationship
        WHERE psychologist_id = current_psychologist_id()
      )
      WHEN 'patient'   THEN user_id = current_user_id()
      WHEN 'org_admin' THEN organization_id = current_organization_id()
      ELSE false
    END
  );

-- Registro Documental: só o autor, sempre.
CREATE POLICY documentary_record_author_only ON documentary_record
  FOR ALL USING (author_id = current_psychologist_id());

-- ============================================================
-- Append-only / imutabilidade
-- audit_log e consent não devem aceitar UPDATE/DELETE da role da app.
-- Ajuste o nome da role conforme o usuário usado pela aplicação.
-- ============================================================
-- REVOKE UPDATE, DELETE ON audit_log FROM acolhe_app;
-- REVOKE UPDATE, DELETE ON consent   FROM acolhe_app;

COMMIT;
