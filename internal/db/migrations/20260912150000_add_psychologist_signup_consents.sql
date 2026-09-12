ALTER TABLE consent_document
  DROP CONSTRAINT consent_document_scope_check,
  ADD CONSTRAINT consent_document_scope_check CHECK (
    scope IN ('health_data','communications','aggregate_statistics','terms_of_use','privacy_policy')
  );

INSERT INTO consent_document (
  scope, version, title, content, content_sha256, required, published_at
)
VALUES
  (
    'terms_of_use', '0.3', 'Termos de Uso',
    'Termos de Uso do Acolhe para cadastro e uso da plataforma.',
    encode(digest('Termos de Uso do Acolhe para cadastro e uso da plataforma.', 'sha256'), 'hex'),
    true, now()
  ),
  (
    'privacy_policy', '0.3', 'Política de Privacidade',
    'Política de Privacidade do Acolhe para tratamento de dados no cadastro.',
    encode(digest('Política de Privacidade do Acolhe para tratamento de dados no cadastro.', 'sha256'), 'hex'),
    true, now()
  )
ON CONFLICT (scope, version) DO NOTHING;
