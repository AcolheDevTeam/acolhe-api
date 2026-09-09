package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

// Biblioteca de templates (ACO-66). Regras decididas em
// acolhe-web/docs/guia-implementacao.md, seção C4:
//
//   - vocabulário fechado de tipos de campo, validado aqui;
//   - só a autora edita ou arquiva; templates globais são somente leitura;
//   - editar um template já atribuído gera nova versão (linha nova com
//     parent_template_id); sem atribuição, edita no lugar;
//   - a listagem mostra só a versão mais recente de cada linhagem.

var (
	// ErrInvalidTemplate agrupa todo erro de validação do corpo (HTTP 400).
	ErrInvalidTemplate = errors.New("template inválido")
	// ErrTemplateReadOnly: global ou de outra autora (HTTP 403).
	ErrTemplateReadOnly = errors.New("este template não pode ser alterado por você")
	// ErrTemplateArchived: arquivado não aceita edição nem novo arquivamento (HTTP 409).
	ErrTemplateArchived = errors.New("template arquivado não pode ser alterado")
	// ErrTemplateSuperseded: existe versão mais nova; edite a versão atual (HTTP 409).
	ErrTemplateSuperseded = errors.New("existe uma versão mais nova deste template")
)

// ValidationError carrega uma mensagem específica em português para o usuário
// (regra A6 do guia) e responde true a errors.Is(err, ErrInvalidTemplate).
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }
func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidTemplate
}

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Tipos de campo aceitos. Cobrem 9 dos 11 do design (Escala 1–10 e 1–5 são
// `scale` com max diferente; "Hora" vira `datetime`). Arquivo e Tags ficam fora.
const (
	FieldShortText      = "short_text"
	FieldLongText       = "long_text"
	FieldScale          = "scale"
	FieldSingleChoice   = "single_choice"
	FieldMultipleChoice = "multiple_choice"
	FieldBoolean        = "boolean"
	FieldDate           = "date"
	FieldDatetime       = "datetime"
)

// FieldTypes lista os tipos aceitos, na ordem em que o builder os oferece.
var FieldTypes = []string{
	FieldShortText, FieldLongText, FieldScale, FieldSingleChoice,
	FieldMultipleChoice, FieldBoolean, FieldDate, FieldDatetime,
}

const (
	maxTemplateTitle        = 120
	maxTemplateDescription  = 1000
	maxTemplateInstructions = 2000
	maxFieldsPerTemplate    = 30
	maxFieldLabel           = 200
	maxFieldHelpText        = 300
	defaultShortTextLength  = 200
	maxShortTextLength      = 500
	defaultLongTextLength   = 2000
	maxLongTextLength       = 5000
	maxScaleRange           = 100
	minChoiceOptions        = 2
	maxChoiceOptions        = 20
	maxChoiceOptionLength   = 120
	maxFieldCodeLength      = 40
)

// FieldInput é um campo como chega no corpo de POST/PUT.
type FieldInput struct {
	Label     string   `json:"label"`
	FieldType string   `json:"fieldType"`
	Required  *bool    `json:"required"`
	HelpText  string   `json:"helpText"`
	MaxLength *int     `json:"maxLength"`
	Min       *int     `json:"min"`
	Max       *int     `json:"max"`
	MinLabel  string   `json:"minLabel"`
	MaxLabel  string   `json:"maxLabel"`
	Options   []string `json:"options"`
}

// FieldConfig é o que vai para activity_field.config (jsonb), já normalizado.
type FieldConfig struct {
	Required  bool     `json:"required"`
	HelpText  string   `json:"helpText,omitempty"`
	MaxLength *int     `json:"maxLength,omitempty"`
	Min       *int     `json:"min,omitempty"`
	Max       *int     `json:"max,omitempty"`
	MinLabel  string   `json:"minLabel,omitempty"`
	MaxLabel  string   `json:"maxLabel,omitempty"`
	Options   []string `json:"options,omitempty"`
}

// TemplateRequest é o corpo de POST e PUT /activities/templates.
type TemplateRequest struct {
	Title        string       `json:"title"`
	Description  *string      `json:"description"`
	Instructions *string      `json:"instructions"`
	TypeCode     string       `json:"typeCode"`
	Fields       []FieldInput `json:"fields"`
}

// TemplateField é a projeção pública de um campo.
type TemplateField struct {
	ID           uuid.UUID       `json:"id"`
	Code         string          `json:"code"`
	Label        string          `json:"label"`
	FieldType    string          `json:"fieldType"`
	DisplayOrder int32           `json:"displayOrder"`
	Config       json.RawMessage `json:"config"`
}

// TemplateDetail é a projeção pública completa de um template.
type TemplateDetail struct {
	ID               uuid.UUID       `json:"id"`
	Title            string          `json:"title"`
	Type             string          `json:"type"`
	Description      *string         `json:"description"`
	Instructions     *string         `json:"instructions"`
	Version          int32           `json:"version"`
	IsGlobal         bool            `json:"isGlobal"`
	IsArchived       bool            `json:"isArchived"`
	Superseded       bool            `json:"superseded"`
	OwnedByMe        bool            `json:"ownedByMe"`
	Editable         bool            `json:"editable"`
	ParentTemplateID *uuid.UUID      `json:"parentTemplateId"`
	AssignmentCount  int64           `json:"assignmentCount"`
	Fields           []TemplateField `json:"fields"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

// normalizedField é o resultado da validação de um FieldInput.
type normalizedField struct {
	Code      string
	Label     string
	FieldType string
	Config    []byte
}

// normalizedTemplate é o resultado da validação de um TemplateRequest.
type normalizedTemplate struct {
	Title        string
	Description  *string
	Instructions *string
	TypeCode     string
	Fields       []normalizedField
}

// normalizeTemplate valida e normaliza o corpo. Cada erro traz uma mensagem
// específica; a posição do campo é 1-based, como o builder numera.
func normalizeTemplate(req TemplateRequest) (*normalizedTemplate, error) {
	title := strings.TrimSpace(req.Title)
	if len(title) < 2 {
		return nil, invalid("Informe um título com pelo menos 2 caracteres")
	}
	if len(title) > maxTemplateTitle {
		return nil, invalid("O título pode ter no máximo %d caracteres", maxTemplateTitle)
	}
	description, err := optionalText(req.Description, "descrição", maxTemplateDescription)
	if err != nil {
		return nil, err
	}
	instructions, err := optionalText(req.Instructions, "instrução", maxTemplateInstructions)
	if err != nil {
		return nil, err
	}
	typeCode := strings.TrimSpace(req.TypeCode)
	if typeCode == "" {
		return nil, invalid("Selecione o tipo base do template")
	}
	if len(req.Fields) == 0 {
		return nil, invalid("Adicione pelo menos um campo")
	}
	if len(req.Fields) > maxFieldsPerTemplate {
		return nil, invalid("Um template pode ter no máximo %d campos", maxFieldsPerTemplate)
	}
	fields := make([]normalizedField, 0, len(req.Fields))
	used := map[string]bool{}
	for index, input := range req.Fields {
		field, err := normalizeField(index+1, input)
		if err != nil {
			return nil, err
		}
		field.Code = uniqueCode(fieldCode(field.Label), used)
		fields = append(fields, field)
	}
	return &normalizedTemplate{
		Title: title, Description: description, Instructions: instructions,
		TypeCode: typeCode, Fields: fields,
	}, nil
}

func optionalText(value *string, name string, limit int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if len(trimmed) > limit {
		return nil, invalid("A %s pode ter no máximo %d caracteres", name, limit)
	}
	return &trimmed, nil
}

func normalizeField(position int, input FieldInput) (normalizedField, error) {
	label := strings.TrimSpace(input.Label)
	if label == "" {
		return normalizedField{}, invalid("Campo %d: informe a pergunta", position)
	}
	if len(label) > maxFieldLabel {
		return normalizedField{}, invalid("Campo %d: a pergunta pode ter no máximo %d caracteres", position, maxFieldLabel)
	}
	helpText := strings.TrimSpace(input.HelpText)
	if len(helpText) > maxFieldHelpText {
		return normalizedField{}, invalid("Campo %d: o texto de apoio pode ter no máximo %d caracteres", position, maxFieldHelpText)
	}
	config := FieldConfig{Required: true, HelpText: helpText}
	if input.Required != nil {
		config.Required = *input.Required
	}
	fieldType := strings.TrimSpace(input.FieldType)
	switch fieldType {
	case FieldShortText, FieldLongText:
		limit, fallback := maxShortTextLength, defaultShortTextLength
		if fieldType == FieldLongText {
			limit, fallback = maxLongTextLength, defaultLongTextLength
		}
		maxLength := fallback
		if input.MaxLength != nil {
			maxLength = *input.MaxLength
		}
		if maxLength < 1 || maxLength > limit {
			return normalizedField{}, invalid("Campo %d: o tamanho máximo do texto deve ficar entre 1 e %d", position, limit)
		}
		config.MaxLength = &maxLength
	case FieldScale:
		if input.Min == nil || input.Max == nil {
			return normalizedField{}, invalid("Campo %d: a escala precisa de valor mínimo e máximo", position)
		}
		minValue, maxValue := *input.Min, *input.Max
		if minValue >= maxValue {
			return normalizedField{}, invalid("Campo %d: o mínimo da escala deve ser menor que o máximo", position)
		}
		if maxValue-minValue > maxScaleRange {
			return normalizedField{}, invalid("Campo %d: a escala pode ter no máximo %d pontos", position, maxScaleRange)
		}
		config.Min, config.Max = &minValue, &maxValue
		config.MinLabel = strings.TrimSpace(input.MinLabel)
		config.MaxLabel = strings.TrimSpace(input.MaxLabel)
		if len(config.MinLabel) > maxChoiceOptionLength || len(config.MaxLabel) > maxChoiceOptionLength {
			return normalizedField{}, invalid("Campo %d: os rótulos da escala podem ter no máximo %d caracteres", position, maxChoiceOptionLength)
		}
	case FieldSingleChoice, FieldMultipleChoice:
		options, err := normalizeOptions(position, input.Options)
		if err != nil {
			return normalizedField{}, err
		}
		config.Options = options
	case FieldBoolean, FieldDate, FieldDatetime:
		// Sem configuração própria.
	case "":
		return normalizedField{}, invalid("Campo %d: selecione o tipo de resposta", position)
	default:
		return normalizedField{}, invalid("Campo %d: tipo de resposta desconhecido", position)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return normalizedField{}, err
	}
	return normalizedField{Label: label, FieldType: fieldType, Config: encoded}, nil
}

func normalizeOptions(position int, raw []string) ([]string, error) {
	options := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, option := range raw {
		trimmed := strings.TrimSpace(option)
		if trimmed == "" {
			return nil, invalid("Campo %d: remova as opções em branco", position)
		}
		if len(trimmed) > maxChoiceOptionLength {
			return nil, invalid("Campo %d: cada opção pode ter no máximo %d caracteres", position, maxChoiceOptionLength)
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			return nil, invalid("Campo %d: a opção \"%s\" está repetida", position, trimmed)
		}
		seen[key] = true
		options = append(options, trimmed)
	}
	if len(options) < minChoiceOptions {
		return nil, invalid("Campo %d: informe pelo menos %d opções", position, minChoiceOptions)
	}
	if len(options) > maxChoiceOptions {
		return nil, invalid("Campo %d: informe no máximo %d opções", position, maxChoiceOptions)
	}
	return options, nil
}

// Acentos comuns em português; evita depender de x/text só para isto.
var asciiFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// fieldCode deriva um código estável e legível a partir da pergunta
// ("Descreva a situação" -> "descreva_a_situacao").
func fieldCode(label string) string {
	lowered := asciiFold.Replace(strings.ToLower(label))
	var builder strings.Builder
	lastUnderscore := true
	for _, r := range lowered {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastUnderscore = false
		case unicode.IsSpace(r), unicode.IsPunct(r), unicode.IsSymbol(r), r == '_':
			if !lastUnderscore {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
		if builder.Len() >= maxFieldCodeLength {
			break
		}
	}
	code := strings.Trim(builder.String(), "_")
	if code == "" || (code[0] >= '0' && code[0] <= '9') {
		code = "campo_" + code
		code = strings.TrimSuffix(code, "_")
	}
	return code
}

func uniqueCode(base string, used map[string]bool) string {
	code := base
	for suffix := 2; used[code]; suffix++ {
		code = fmt.Sprintf("%s_%d", base, suffix)
	}
	used[code] = true
	return code
}

// author resolve a psicóloga autenticada. Toda escrita na biblioteca exige isso.
func (s *Service) author(ctx context.Context) (tenant.Identity, db.GetPsychologistByUserRow, db.Querier, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" {
		return tenant.Identity{}, db.GetPsychologistByUserRow{}, nil, ErrPsychologistRequired
	}
	q := tenant.Queries(ctx, s.q)
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		return tenant.Identity{}, db.GetPsychologistByUserRow{}, nil, ErrPsychologistRequired
	}
	return id, psy, q, nil
}

// CreateTemplate cria a versão 1 de um template da organização da autora.
func (s *Service) CreateTemplate(ctx context.Context, req TemplateRequest) (*TemplateDetail, error) {
	id, psy, q, err := s.author(ctx)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeTemplate(req)
	if err != nil {
		return nil, err
	}
	activityType, err := q.GetActivityTypeByCode(ctx, normalized.TypeCode)
	if err != nil {
		return nil, invalid("Tipo base desconhecido")
	}
	orgID := id.OrgID
	row, err := q.CreateActivityTemplate(ctx, db.CreateActivityTemplateParams{
		TypeID: activityType.ID, OrganizationID: &orgID, AuthorID: psy.ID,
		Title: normalized.Title, Description: normalized.Description,
		Instructions: normalized.Instructions, Version: 1,
	})
	if err != nil {
		return nil, err
	}
	if err := s.insertFields(ctx, q, row.ID, normalized.Fields); err != nil {
		return nil, err
	}
	return s.loadTemplate(ctx, q, id, psy.ID, row.ID)
}

// GetTemplate devolve um template visível à organização, com campos.
func (s *Service) GetTemplate(ctx context.Context, templateID uuid.UUID) (*TemplateDetail, error) {
	id, psy, q, err := s.author(ctx)
	if err != nil {
		return nil, err
	}
	return s.loadTemplate(ctx, q, id, psy.ID, templateID)
}

// UpdateTemplate edita no lugar quando o template nunca foi atribuído; caso
// contrário publica uma nova versão (linha nova com parent_template_id) para
// não quebrar as atividades que pinaram a versão anterior.
func (s *Service) UpdateTemplate(ctx context.Context, templateID uuid.UUID, req TemplateRequest) (*TemplateDetail, error) {
	id, psy, q, err := s.author(ctx)
	if err != nil {
		return nil, err
	}
	current, err := s.loadTemplate(ctx, q, id, psy.ID, templateID)
	if err != nil {
		return nil, err
	}
	if err := ensureEditable(current); err != nil {
		return nil, err
	}
	normalized, err := normalizeTemplate(req)
	if err != nil {
		return nil, err
	}
	activityType, err := q.GetActivityTypeByCode(ctx, normalized.TypeCode)
	if err != nil {
		return nil, invalid("Tipo base desconhecido")
	}
	orgID := id.OrgID

	if current.AssignmentCount == 0 {
		affected, err := q.UpdateActivityTemplateInPlace(ctx, db.UpdateActivityTemplateInPlaceParams{
			TypeID: activityType.ID, Title: normalized.Title,
			Description: normalized.Description, Instructions: normalized.Instructions,
			ID: templateID, OrganizationID: &orgID, AuthorID: psy.ID,
		})
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, ErrTemplateReadOnly
		}
		if err := q.DeleteActivityFields(ctx, templateID); err != nil {
			return nil, err
		}
		if err := s.insertFields(ctx, q, templateID, normalized.Fields); err != nil {
			return nil, err
		}
		return s.loadTemplate(ctx, q, id, psy.ID, templateID)
	}

	row, err := q.CreateActivityTemplate(ctx, db.CreateActivityTemplateParams{
		TypeID: activityType.ID, OrganizationID: &orgID, AuthorID: psy.ID,
		ParentTemplateID: &templateID, Title: normalized.Title,
		Description: normalized.Description, Instructions: normalized.Instructions,
		Version: current.Version + 1,
	})
	if err != nil {
		return nil, err
	}
	if err := s.insertFields(ctx, q, row.ID, normalized.Fields); err != nil {
		return nil, err
	}
	return s.loadTemplate(ctx, q, id, psy.ID, row.ID)
}

// ArchiveTemplate tira o template da biblioteca. Atividades já atribuídas
// continuam válidas porque pinam template_id e template_version.
func (s *Service) ArchiveTemplate(ctx context.Context, templateID uuid.UUID) (*TemplateDetail, error) {
	id, psy, q, err := s.author(ctx)
	if err != nil {
		return nil, err
	}
	current, err := s.loadTemplate(ctx, q, id, psy.ID, templateID)
	if err != nil {
		return nil, err
	}
	if !current.OwnedByMe {
		return nil, ErrTemplateReadOnly
	}
	if current.IsArchived {
		return nil, ErrTemplateArchived
	}
	orgID := id.OrgID
	affected, err := q.ArchiveActivityTemplate(ctx, db.ArchiveActivityTemplateParams{
		ID: templateID, OrganizationID: &orgID, AuthorID: psy.ID,
	})
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, ErrTemplateReadOnly
	}
	return s.loadTemplate(ctx, q, id, psy.ID, templateID)
}

func ensureEditable(t *TemplateDetail) error {
	switch {
	case !t.OwnedByMe:
		return ErrTemplateReadOnly
	case t.IsArchived:
		return ErrTemplateArchived
	case t.Superseded:
		return ErrTemplateSuperseded
	}
	return nil
}

func (s *Service) insertFields(ctx context.Context, q db.Querier, templateID uuid.UUID, fields []normalizedField) error {
	for index, field := range fields {
		if _, err := q.CreateActivityField(ctx, db.CreateActivityFieldParams{
			TemplateID: templateID, Code: field.Code, Label: field.Label,
			FieldType: field.FieldType, Config: field.Config, DisplayOrder: int32(index + 1),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) loadTemplate(ctx context.Context, q db.Querier, id tenant.Identity, psyID, templateID uuid.UUID) (*TemplateDetail, error) {
	orgID := id.OrgID
	row, err := q.GetActivityTemplateDetail(ctx, db.GetActivityTemplateDetailParams{
		ID: templateID, OrganizationID: &orgID,
	})
	if err != nil {
		return nil, ErrTemplateNotFound
	}
	fieldRows, err := q.ListActivityFields(ctx, templateID)
	if err != nil {
		return nil, err
	}
	assignments, err := q.CountAssignmentsByTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}
	fields := make([]TemplateField, 0, len(fieldRows))
	for _, f := range fieldRows {
		config := json.RawMessage(f.Config)
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		fields = append(fields, TemplateField{
			ID: f.ID, Code: f.Code, Label: f.Label, FieldType: f.FieldType,
			DisplayOrder: f.DisplayOrder, Config: config,
		})
	}
	isGlobal := row.OrganizationID == nil
	isAuthor := !isGlobal && row.AuthorID == psyID
	return &TemplateDetail{
		ID: row.ID, Title: row.Title, Type: row.Type,
		Description: row.Description, Instructions: row.Instructions,
		Version: row.Version, IsGlobal: isGlobal, IsArchived: row.IsArchived,
		Superseded: row.Superseded, OwnedByMe: isAuthor,
		Editable:         isAuthor && !row.IsArchived && !row.Superseded,
		ParentTemplateID: row.ParentTemplateID, AssignmentCount: assignments,
		Fields: fields, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}
