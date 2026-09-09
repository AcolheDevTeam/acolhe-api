# Cadastro público do psicólogo

`POST /signup` é público e cria, na mesma transação, uma organização, um
usuário `psychologist`, seu `psychologist_profile` e o consentimento
`psychologist_signup` da versão `0.3`.

## Request

```json
{
  "email": "mariana@example.com",
  "password": "uma-senha-segura",
  "fullName": "Mariana Sá",
  "crpNumber": "123456",
  "crpState": "06",
  "cpf": "123.456.789-00",
  "approach": "TCC",
  "acceptTerms": true,
  "acceptPrivacy": true,
  "termsVersion": "0.3",
  "privacyVersion": "0.3"
}
```

`email` e CRP são normalizados antes das restrições de unicidade. O e-mail deve
ter formato estrito ASCII com domínio pontuado. A região do CRP aceita `01` a
`24` e entradas como `6` ou `CRP-06` são normalizadas para `06`. O CRP é
criado com status `pending`; a API não libera aprovação automaticamente.
`cpf` é opcional e só é persistido cifrado com AES-GCM quando
`PII_ENCRYPTION_KEY` (32 bytes em hexadecimal) está configurada. O comprovante
do CRP não faz parte deste contrato: ele não é aceito nem armazenado até que
um fluxo de upload com armazenamento privado, limite de tamanho, varredura e
expiração seja disponibilizado.

## Responses

- `201`: `{ token, user, psychologistId, crpStatus, onboardingStatus, termsVersion, privacyVersion }`.
- `400`: corpo inválido ou termos ausentes/desatualizados.
- `409`: cadastro não pôde ser concluído. A mesma resposta é usada para e-mail
  ou CRP já existentes, sem revelar qual registro causou o conflito. Repetir
  uma requisição já concluída não cria novos registros.
- `503`: banco, geração de senha ou contrato de criptografia indisponível.

Os dois aceites são obrigatórios e cada documento registra a versão enviada;
esta versão deve ser `0.3` para Termos e Política de Privacidade. O `token` é destinado ao BFF, que deve colocá-lo em cookie `HttpOnly`; clientes
de navegador não devem expô-lo a JavaScript. Nenhum hash, CPF, comprovante,
IP ou detalhe de banco aparece na resposta ou em mensagens de erro.
