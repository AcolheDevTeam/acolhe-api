package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// Erros próprios da submissão da paciente. ErrSubmissionInvalid é a falha de
// validação (400) e carrega a mensagem específica do campo; as demais são de
// estado e viram 404/409.
var (
	// ErrInvalidSubmission: corpo da submissão não bate com o formulário.
	ErrInvalidSubmission = errors.New("resposta inválida")
	// ErrSubmissionVersionMismatch: o formulário respondido não é a versão pinada.
	ErrSubmissionVersionMismatch = errors.New("a atividade foi atualizada; recarregue o formulário")
	// ErrAlreadySubmitted: já existe resposta final para esta atribuição.
	ErrAlreadySubmitted = errors.New("esta atividade já foi respondida")
	// ErrTemplateWithoutFields: template sem campos não pode ser respondido.
	ErrTemplateWithoutFields = errors.New("esta atividade não tem perguntas para responder")
)

// SubmissionError carrega a mensagem específica do campo, em português (regra A6
// do guia), e responde true a errors.Is(err, ErrInvalidSubmission). É um tipo
// próprio para não se confundir com a validação de template (ValidationError).
type SubmissionError struct {
	Msg string
}

func (e *SubmissionError) Error() string { return e.Msg }
func (e *SubmissionError) Is(target error) bool {
	return target == ErrInvalidSubmission
}

const (
	maxSubmissionValueText = 5000
	maxSubmissionChoices   = 20
	dateLayout             = "2006-01-02"
)

// SubmissionValue é o valor de um campo, discriminado por Kind. O Kind precisa
// bater com o field_type do campo no template: o cliente declara o que está
// mandando e a divergência é erro, não coerção silenciosa (ADR 0001).
type SubmissionValue struct {
	FieldCode string   `json:"fieldCode"`
	Kind      string   `json:"kind"`
	Text      *string  `json:"text,omitempty"`
	Number    *float64 `json:"number,omitempty"`
	Boolean   *bool    `json:"boolean,omitempty"`
	Date      *string  `json:"date,omitempty"`
	Datetime  *string  `json:"datetime,omitempty"`
	Choice    *string  `json:"choice,omitempty"`
	Choices   []string `json:"choices,omitempty"`
}

// SubmissionRequest é o corpo de POST /activities/assignments/:id/responses.
type SubmissionRequest struct {
	SubmissionID    uuid.UUID         `json:"submissionId"`
	TemplateVersion int32             `json:"templateVersion"`
	Values          []SubmissionValue `json:"values"`
}

// PatientActivityField é um campo como a paciente o recebe para montar o form.
type PatientActivityField struct {
	ID           uuid.UUID       `json:"id"`
	Code         string          `json:"code"`
	Label        string          `json:"label"`
	FieldType    string          `json:"fieldType"`
	DisplayOrder int32           `json:"displayOrder"`
	Config       json.RawMessage `json:"config"`
}

// PatientActivityDetail é a atividade da paciente com o formulário da versão pinada.
type PatientActivityDetail struct {
	ID              uuid.UUID              `json:"id"`
	Status          string                 `json:"status"`
	Title           string                 `json:"title"`
	Type            string                 `json:"type"`
	Description     *string                `json:"description"`
	Instructions    *string                `json:"instructions"`
	TemplateVersion int32                  `json:"templateVersion"`
	ScheduledFor    *time.Time             `json:"scheduledFor"`
	DueAt           *time.Time             `json:"dueAt"`
	SubmittedAt     *time.Time             `json:"submittedAt"`
	CanRespond      bool                   `json:"canRespond"`
	Fields          []PatientActivityField `json:"fields"`
}

// PatientActivity devolve o formulário da versão pinada para a paciente responder.
func (s *Service) PatientActivity(ctx context.Context, assignmentID uuid.UUID) (*PatientActivityDetail, error) {
	assignment, fields, _, err := s.patientAssignment(ctx, assignmentID)
	if err != nil {
		return nil, err
	}

	detail := &PatientActivityDetail{
		ID: assignment.ID, Status: assignment.Status, Title: assignment.Title,
		Type: assignment.Type, Description: assignment.Description,
		Instructions: assignment.Instructions, TemplateVersion: assignment.TemplateVersion,
		ScheduledFor: assignment.ScheduledFor, DueAt: assignment.DueAt,
		SubmittedAt: assignment.SubmittedAt,
		CanRespond: assignment.ResponseID == nil &&
			(assignment.Status == "pending" || assignment.Status == "in_progress"),
		Fields: make([]PatientActivityField, 0, len(fields)),
	}
	for _, field := range fields {
		config := json.RawMessage(field.Config)
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		detail.Fields = append(detail.Fields, PatientActivityField{
			ID: field.ID, Code: field.Code, Label: field.Label,
			FieldType: field.FieldType, DisplayOrder: field.DisplayOrder, Config: config,
		})
	}
	return detail, nil
}

// patientAssignment resolve a atribuição da paciente autenticada e os campos da
// versão pinada do template. Devolve também o id do paciente, usado na escrita.
func (s *Service) patientAssignment(ctx context.Context, assignmentID uuid.UUID) (
	db.GetPatientAssignmentForResponseRow, []db.ListActivityFieldsRow, uuid.UUID, error,
) {
	var empty db.GetPatientAssignmentForResponseRow
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "patient" {
		return empty, nil, uuid.Nil, ErrPatientRequired
	}
	q := tenant.Queries(ctx, s.q)
	patient, err := q.GetPatientByUserInOrg(ctx, db.GetPatientByUserInOrgParams{
		UserID: &id.UserID, OrganizationID: id.OrgID,
	})
	if err != nil {
		return empty, nil, uuid.Nil, ErrPatientRequired
	}
	assignment, err := q.GetPatientAssignmentForResponse(ctx, db.GetPatientAssignmentForResponseParams{
		ID: assignmentID, PatientID: patient.ID, OrganizationID: id.OrgID,
	})
	if err != nil {
		return empty, nil, uuid.Nil, ErrAssignmentNotFound
	}
	// A atribuição pina uma versão; editar um template já atribuído cria uma linha
	// nova, então a versão do template apontado tem que continuar sendo a pinada.
	if assignment.TemplateVersion != assignment.TemplateCurrentVersion {
		return empty, nil, uuid.Nil, ErrSubmissionVersionMismatch
	}
	fields, err := q.ListActivityFields(ctx, assignment.TemplateID)
	if err != nil {
		return empty, nil, uuid.Nil, err
	}
	return assignment, fields, patient.ID, nil
}

// preparedValue é um valor já validado, pronto para virar activity_response_value.
type preparedValue struct {
	fieldID   uuid.UUID
	fieldCode string
	text      *string
	number    *float64
	boolean   *bool
	datetime  *time.Time
	jsonValue []byte
}

// SubmitTyped grava a resposta final da paciente com um valor por campo.
// Resposta, valores e status 'submitted' entram na mesma transação da requisição
// (middleware TenantTx), como exige o ADR 0001.
func (s *Service) SubmitTyped(ctx context.Context, assignmentID uuid.UUID, req SubmissionRequest) (*Response, error) {
	assignment, fields, patientID, err := s.patientAssignment(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	id, _ := tenant.FromContext(ctx)
	q := tenant.Queries(ctx, s.q)

	// Replay: mesma submissionId devolve a resposta original em vez de duplicar.
	if assignment.ResponseID != nil {
		if assignment.SubmissionID != nil && *assignment.SubmissionID == req.SubmissionID {
			return &Response{
				ID: *assignment.ResponseID, AssignmentID: assignment.ID,
				SubmittedAt: assignment.SubmittedAt, IsDraft: false,
			}, nil
		}
		return nil, ErrAlreadySubmitted
	}
	if req.SubmissionID == uuid.Nil {
		return nil, submissionErrorf("informe o identificador da submissão")
	}
	if req.TemplateVersion != assignment.TemplateVersion {
		return nil, ErrSubmissionVersionMismatch
	}
	if len(fields) == 0 {
		return nil, ErrTemplateWithoutFields
	}

	prepared, err := validateSubmission(fields, req.Values)
	if err != nil {
		return nil, err
	}

	claimed, err := q.ClaimAssignmentForSubmission(ctx, db.ClaimAssignmentForSubmissionParams{
		ID: assignmentID, PatientID: patientID, OrganizationID: id.OrgID,
	})
	if err != nil {
		return nil, err
	}
	if claimed != 1 {
		return nil, ErrSubmissionNotAllowed
	}

	row, err := q.SubmitTypedResponse(ctx, db.SubmitTypedResponseParams{
		AssignmentID: assignmentID,
		SubmissionID: &req.SubmissionID,
		SummaryScore: pgtype.Numeric{}, // pontuação automática está fora deste escopo
	})
	if err != nil {
		return nil, err
	}
	for _, value := range prepared {
		number := pgtype.Numeric{}
		if value.number != nil {
			if err := number.Scan(formatNumber(*value.number)); err != nil {
				return nil, submissionErrorf("valor numérico inválido em %q", value.fieldCode)
			}
		}
		if err := q.CreateActivityResponseValue(ctx, db.CreateActivityResponseValueParams{
			ResponseID: row.ID, FieldID: value.fieldID, FieldCode: value.fieldCode,
			ValueText: value.text, ValueNumber: number, ValueBoolean: value.boolean,
			ValueDatetime: value.datetime, ValueJson: value.jsonValue,
		}); err != nil {
			return nil, err
		}
	}
	return &Response{
		ID: row.ID, AssignmentID: row.AssignmentID, SubmittedAt: row.SubmittedAt,
		IsDraft: row.IsDraft, CreatedAt: row.CreatedAt,
	}, nil
}

// validateSubmission é puro: recebe os campos da versão pinada e os valores
// enviados e devolve as linhas a gravar. Cobre conjunto exato de campos, ordem,
// tipo declarado e as restrições de config (obrigatório, tamanho, faixa, opções).
func validateSubmission(fields []db.ListActivityFieldsRow, values []SubmissionValue) ([]preparedValue, error) {
	if len(values) != len(fields) {
		return nil, submissionErrorf("a atividade tem %d perguntas e foram enviadas %d respostas", len(fields), len(values))
	}
	seen := make(map[string]struct{}, len(values))
	prepared := make([]preparedValue, 0, len(fields))

	for index, field := range fields {
		value := values[index]
		code := strings.TrimSpace(value.FieldCode)
		// Ordem: o cliente responde na ordem em que recebeu o formulário.
		if code != field.Code {
			return nil, submissionErrorf("a pergunta %d deveria ser %q e veio %q", index+1, field.Code, code)
		}
		if _, duplicated := seen[code]; duplicated {
			return nil, submissionErrorf("a pergunta %q foi respondida mais de uma vez", code)
		}
		seen[code] = struct{}{}
		if value.Kind != field.FieldType {
			return nil, submissionErrorf("a pergunta %q é do tipo %q e veio como %q", code, field.FieldType, value.Kind)
		}
		config, err := decodeFieldConfig(field.Config)
		if err != nil {
			return nil, submissionErrorf("a pergunta %q está com configuração inválida", code)
		}
		ready, err := prepareValue(field, config, value)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, ready)
	}
	return prepared, nil
}

func decodeFieldConfig(raw []byte) (FieldConfig, error) {
	config := FieldConfig{}
	if len(raw) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return FieldConfig{}, err
	}
	return config, nil
}

// prepareValue valida um campo e escolhe a coluna tipada. O mapeamento espelha o
// que a revisão já lê (reviewField): textos e escolha única em value_text, escala
// em value_number, booleano em value_boolean, data e data-hora em value_datetime,
// múltipla escolha em value_json.
func prepareValue(field db.ListActivityFieldsRow, config FieldConfig, value SubmissionValue) (preparedValue, error) {
	ready := preparedValue{fieldID: field.ID, fieldCode: field.Code}
	label := field.Code

	switch field.FieldType {
	case FieldShortText, FieldLongText:
		if value.Text == nil {
			return ready, requiredError(label, config)
		}
		text := strings.TrimSpace(*value.Text)
		if text == "" {
			return ready, requiredError(label, config)
		}
		limit := maxSubmissionValueText
		if config.MaxLength != nil && *config.MaxLength > 0 {
			limit = *config.MaxLength
		}
		if len([]rune(text)) > limit {
			return ready, submissionErrorf("a resposta de %q passa de %d caracteres", label, limit)
		}
		ready.text = &text

	case FieldScale:
		if value.Number == nil {
			return ready, requiredError(label, config)
		}
		number := *value.Number
		if number != float64(int64(number)) {
			return ready, submissionErrorf("a resposta de %q precisa ser um número inteiro", label)
		}
		if config.Min != nil && number < float64(*config.Min) {
			return ready, submissionErrorf("a resposta de %q precisa ser no mínimo %d", label, *config.Min)
		}
		if config.Max != nil && number > float64(*config.Max) {
			return ready, submissionErrorf("a resposta de %q precisa ser no máximo %d", label, *config.Max)
		}
		ready.number = &number

	case FieldBoolean:
		if value.Boolean == nil {
			return ready, requiredError(label, config)
		}
		ready.boolean = value.Boolean

	case FieldDate:
		if value.Date == nil || strings.TrimSpace(*value.Date) == "" {
			return ready, requiredError(label, config)
		}
		parsed, err := time.ParseInLocation(dateLayout, strings.TrimSpace(*value.Date), time.UTC)
		if err != nil {
			return ready, submissionErrorf("a data de %q precisa estar no formato AAAA-MM-DD", label)
		}
		ready.datetime = &parsed

	case FieldDatetime:
		if value.Datetime == nil || strings.TrimSpace(*value.Datetime) == "" {
			return ready, requiredError(label, config)
		}
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value.Datetime))
		if err != nil {
			return ready, submissionErrorf("a data e hora de %q estão em formato inválido", label)
		}
		utc := parsed.UTC()
		ready.datetime = &utc

	case FieldSingleChoice:
		if value.Choice == nil || strings.TrimSpace(*value.Choice) == "" {
			return ready, requiredError(label, config)
		}
		choice := strings.TrimSpace(*value.Choice)
		if !containsOption(config.Options, choice) {
			return ready, submissionErrorf("a opção escolhida em %q não existe nesta pergunta", label)
		}
		ready.text = &choice

	case FieldMultipleChoice:
		if len(value.Choices) == 0 {
			return ready, requiredError(label, config)
		}
		if len(value.Choices) > maxSubmissionChoices {
			return ready, submissionErrorf("%q aceita no máximo %d opções", label, maxSubmissionChoices)
		}
		chosen := make([]string, 0, len(value.Choices))
		for _, raw := range value.Choices {
			choice := strings.TrimSpace(raw)
			if !containsOption(config.Options, choice) {
				return ready, submissionErrorf("a opção escolhida em %q não existe nesta pergunta", label)
			}
			if containsOption(chosen, choice) {
				return ready, submissionErrorf("a opção %q foi escolhida duas vezes em %q", choice, label)
			}
			chosen = append(chosen, choice)
		}
		encoded, err := json.Marshal(chosen)
		if err != nil {
			return ready, submissionErrorf("não foi possível registrar as opções de %q", label)
		}
		ready.jsonValue = encoded

	default:
		return ready, submissionErrorf("a pergunta %q é de um tipo não suportado", label)
	}
	return ready, nil
}

// requiredError distingue campo obrigatório vazio de campo opcional vazio. Como o
// banco exige exatamente um valor por campo (trigger de 20260729072000), campo
// opcional em branco também não pode ser gravado — a mensagem explica isso.
func requiredError(label string, config FieldConfig) error {
	if config.Required {
		return submissionErrorf("responda %q para enviar a atividade", label)
	}
	return submissionErrorf("a resposta de %q ficou em branco; responda todas as perguntas para enviar", label)
}

func containsOption(options []string, candidate string) bool {
	for _, option := range options {
		if option == candidate {
			return true
		}
	}
	return false
}

func formatNumber(value float64) string {
	return fmt.Sprintf("%d", int64(value))
}

func submissionErrorf(format string, args ...any) error {
	return &SubmissionError{Msg: fmt.Sprintf(format, args...)}
}
