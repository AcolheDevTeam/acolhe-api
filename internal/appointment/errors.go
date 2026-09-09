package appointment

import "errors"

// ErrScheduleConflict: já existe agendamento do psicólogo sobrepondo a janela
// solicitada (mapeado para 409 no handler).
var ErrScheduleConflict = errors.New("conflito de horário na agenda")

// ErrPsychologistRequired: usuário autenticado não tem perfil de psicólogo.
var ErrPsychologistRequired = errors.New("ação restrita a psicólogos")

// ErrInvalidInput indica data, paciente, duração ou modalidade inválidos.
var ErrInvalidInput = errors.New("dados do agendamento inválidos")

// ErrNotFound evita diferenciar agendamento inexistente de outro tenant/profissional.
var ErrNotFound = errors.New("agendamento não encontrado")

// ErrInvalidStatusTransition bloqueia reabertura e saltos fora da máquina de estados.
var ErrInvalidStatusTransition = errors.New("transição de status inválida")
