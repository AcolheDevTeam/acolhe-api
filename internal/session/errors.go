package session

import "errors"

// ErrNoActiveRelationship: não há vínculo ativo psicólogo↔paciente. Registrar
// sessão sem vínculo viola a regra clínica (mapeado para 403 no handler).
var ErrNoActiveRelationship = errors.New("sem vínculo ativo com o paciente")

// ErrPsychologistRequired: o usuário autenticado não é um psicólogo com perfil.
var ErrPsychologistRequired = errors.New("ação restrita a psicólogos")

// ErrNotFound indica sessão inexistente ou fora da organização do requisitante.
var ErrNotFound = errors.New("sessão não encontrada")

// ErrInvalidInput indica campos ausentes ou inválidos no registro da sessão.
var ErrInvalidInput = errors.New("dados da sessão inválidos")
