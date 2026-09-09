-- Vocabulário do sistema para a biblioteca de templates (ACO-66). São os quatro
-- tipos base que o web já conhece (types/index.ts: ActivityType). Idempotente.
INSERT INTO activity_type (code, name) VALUES
  ('record',    'Formulário'),
  ('scale',     'Escala'),
  ('checklist', 'Checklist'),
  ('checkin',   'Check-in')
ON CONFLICT (code) DO NOTHING;
