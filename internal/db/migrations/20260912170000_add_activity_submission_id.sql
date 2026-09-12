-- Submissão tipada da paciente (ACO-68). O ADR 0001 exige um submissionId estável
-- gerado pelo cliente para que reenvio da mesma submissão seja idempotente, em vez
-- de esbarrar na constraint de resposta única e virar erro.
ALTER TABLE activity_response ADD COLUMN submission_id uuid;

CREATE UNIQUE INDEX activity_response_submission_id_key
  ON activity_response (submission_id)
  WHERE submission_id IS NOT NULL;
