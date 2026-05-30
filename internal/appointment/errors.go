package appointment

import "errors"

// ErrScheduleConflict: já existe agendamento do psicólogo sobrepondo a janela
// solicitada (mapeado para 409 no handler).
var ErrScheduleConflict = errors.New("conflito de horário na agenda")

// ErrPsychologistRequired: usuário autenticado não tem perfil de psicólogo.
var ErrPsychologistRequired = errors.New("ação restrita a psicólogos")
