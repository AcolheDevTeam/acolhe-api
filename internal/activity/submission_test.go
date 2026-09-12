package activity

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

func field(t *testing.T, code, fieldType string, config FieldConfig, order int32) db.ListActivityFieldsRow {
	t.Helper()
	encoded, err := json.Marshal(config)
	require.NoError(t, err)
	return db.ListActivityFieldsRow{
		ID: uuid.New(), TemplateID: uuid.Nil, Code: code, Label: code,
		FieldType: fieldType, Config: encoded, DisplayOrder: order,
	}
}

func required() FieldConfig { return FieldConfig{Required: true} }

func scaleConfig(min, max int) FieldConfig {
	return FieldConfig{Required: true, Min: &min, Max: &max}
}

func number(value float64) *float64 { return &value }

// formulário com um campo de cada tipo suportado, na ordem do builder.
func everyTypeForm(t *testing.T) []db.ListActivityFieldsRow {
	t.Helper()
	return []db.ListActivityFieldsRow{
		field(t, "resumo", FieldShortText, FieldConfig{Required: true, MaxLength: intPtr(10)}, 1),
		field(t, "relato", FieldLongText, required(), 2),
		field(t, "intensidade", FieldScale, scaleConfig(1, 10), 3),
		field(t, "humor", FieldSingleChoice, FieldConfig{Required: true, Options: []string{"bem", "mal"}}, 4),
		field(t, "sintomas", FieldMultipleChoice, FieldConfig{Required: true, Options: []string{"sono", "apetite"}}, 5),
		field(t, "dormiu", FieldBoolean, required(), 6),
		field(t, "dia", FieldDate, required(), 7),
		field(t, "momento", FieldDatetime, required(), 8),
	}
}

func everyTypeValues() []SubmissionValue {
	return []SubmissionValue{
		{FieldCode: "resumo", Kind: FieldShortText, Text: strPtr("tudo bem")},
		{FieldCode: "relato", Kind: FieldLongText, Text: strPtr("um relato mais longo")},
		{FieldCode: "intensidade", Kind: FieldScale, Number: number(7)},
		{FieldCode: "humor", Kind: FieldSingleChoice, Choice: strPtr("bem")},
		{FieldCode: "sintomas", Kind: FieldMultipleChoice, Choices: []string{"sono"}},
		{FieldCode: "dormiu", Kind: FieldBoolean, Boolean: boolPtr(true)},
		{FieldCode: "dia", Kind: FieldDate, Date: strPtr("2026-09-12")},
		{FieldCode: "momento", Kind: FieldDatetime, Datetime: strPtr("2026-09-12T10:30:00Z")},
	}
}

func TestValidateSubmission_CadaTipoVaiParaSuaColuna(t *testing.T) {
	prepared, err := validateSubmission(everyTypeForm(t), everyTypeValues())
	require.NoError(t, err)
	require.Len(t, prepared, 8)

	byCode := map[string]preparedValue{}
	for _, value := range prepared {
		byCode[value.fieldCode] = value
	}

	// Texto curto, texto longo e escolha única gravam em value_text — é o que a
	// revisão já lê como kind "text".
	assert.Equal(t, "tudo bem", *byCode["resumo"].text)
	assert.Equal(t, "um relato mais longo", *byCode["relato"].text)
	assert.Equal(t, "bem", *byCode["humor"].text)

	assert.Equal(t, float64(7), *byCode["intensidade"].number)
	assert.True(t, *byCode["dormiu"].boolean)

	// Data e data-hora vão para value_datetime; a data vira meia-noite UTC.
	require.NotNil(t, byCode["dia"].datetime)
	assert.Equal(t, "2026-09-12T00:00:00Z", byCode["dia"].datetime.Format("2006-01-02T15:04:05Z"))
	require.NotNil(t, byCode["momento"].datetime)
	assert.Equal(t, "2026-09-12T10:30:00Z", byCode["momento"].datetime.Format("2006-01-02T15:04:05Z"))

	// Múltipla escolha é a única que vai para value_json.
	assert.JSONEq(t, `["sono"]`, string(byCode["sintomas"].jsonValue))

	// Exatamente uma coluna preenchida por campo — o trigger do banco exige isso.
	for _, value := range prepared {
		filled := 0
		for _, isSet := range []bool{
			value.text != nil, value.number != nil, value.boolean != nil,
			value.datetime != nil, value.jsonValue != nil,
		} {
			if isSet {
				filled++
			}
		}
		assert.Equal(t, 1, filled, "campo %q deve preencher exatamente uma coluna", value.fieldCode)
	}
}

func TestValidateSubmission_ConjuntoDeCamposExato(t *testing.T) {
	form := everyTypeForm(t)

	t.Run("campo faltando", func(t *testing.T) {
		_, err := validateSubmission(form, everyTypeValues()[:7])
		require.ErrorIs(t, err, ErrInvalidSubmission)
		assert.Contains(t, err.Error(), "8 perguntas")
	})

	t.Run("campo a mais", func(t *testing.T) {
		extra := append(everyTypeValues(), SubmissionValue{
			FieldCode: "invasor", Kind: FieldShortText, Text: strPtr("x"),
		})
		_, err := validateSubmission(form, extra)
		require.ErrorIs(t, err, ErrInvalidSubmission)
	})

	t.Run("campo desconhecido no lugar de um válido", func(t *testing.T) {
		values := everyTypeValues()
		values[2].FieldCode = "nao_existe"
		_, err := validateSubmission(form, values)
		require.ErrorIs(t, err, ErrInvalidSubmission)
		assert.Contains(t, err.Error(), "nao_existe")
	})

	t.Run("campo duplicado", func(t *testing.T) {
		values := everyTypeValues()
		values[1] = values[0]
		_, err := validateSubmission(form, values)
		require.ErrorIs(t, err, ErrInvalidSubmission)
	})

	t.Run("fora de ordem", func(t *testing.T) {
		values := everyTypeValues()
		values[0], values[1] = values[1], values[0]
		_, err := validateSubmission(form, values)
		require.ErrorIs(t, err, ErrInvalidSubmission)
	})
}

func TestValidateSubmission_TipoDeclaradoPrecisaBater(t *testing.T) {
	form := everyTypeForm(t)
	values := everyTypeValues()
	values[2].Kind = FieldShortText // escala anunciada como texto
	_, err := validateSubmission(form, values)
	require.ErrorIs(t, err, ErrInvalidSubmission)
	assert.Contains(t, err.Error(), "intensidade")
}

func TestValidateSubmission_RestricoesDeConfig(t *testing.T) {
	form := everyTypeForm(t)

	casos := []struct {
		nome   string
		ajuste func(values []SubmissionValue)
		trecho string
	}{
		{"obrigatório em branco", func(v []SubmissionValue) { v[1].Text = strPtr("   ") }, "relato"},
		{"texto acima do maxLength", func(v []SubmissionValue) { v[0].Text = strPtr("excede o limite de dez") }, "10 caracteres"},
		{"escala abaixo do mínimo", func(v []SubmissionValue) { v[2].Number = number(0) }, "no mínimo 1"},
		{"escala acima do máximo", func(v []SubmissionValue) { v[2].Number = number(11) }, "no máximo 10"},
		{"escala fracionada", func(v []SubmissionValue) { v[2].Number = number(7.5) }, "número inteiro"},
		{"opção única inexistente", func(v []SubmissionValue) { v[3].Choice = strPtr("mais ou menos") }, "não existe"},
		{"opção múltipla inexistente", func(v []SubmissionValue) { v[4].Choices = []string{"sono", "outra"} }, "não existe"},
		{"opção múltipla repetida", func(v []SubmissionValue) { v[4].Choices = []string{"sono", "sono"} }, "duas vezes"},
		{"data em formato errado", func(v []SubmissionValue) { v[6].Date = strPtr("12/09/2026") }, "AAAA-MM-DD"},
		{"data-hora em formato errado", func(v []SubmissionValue) { v[7].Datetime = strPtr("ontem") }, "formato inválido"},
		{"booleano ausente", func(v []SubmissionValue) { v[5].Boolean = nil }, "dormiu"},
		{"múltipla escolha vazia", func(v []SubmissionValue) { v[4].Choices = nil }, "sintomas"},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			values := everyTypeValues()
			caso.ajuste(values)
			_, err := validateSubmission(form, values)
			require.ErrorIs(t, err, ErrInvalidSubmission)
			assert.Contains(t, err.Error(), caso.trecho)
		})
	}
}

func TestValidateSubmission_CampoOpcionalEmBrancoTambemNaoPassa(t *testing.T) {
	// O banco exige exatamente um valor por campo, então nem opcional pode vir
	// vazio. A mensagem precisa explicar isso em vez de dizer "obrigatório".
	form := []db.ListActivityFieldsRow{
		field(t, "observacao", FieldLongText, FieldConfig{Required: false}, 1),
	}
	_, err := validateSubmission(form, []SubmissionValue{
		{FieldCode: "observacao", Kind: FieldLongText, Text: strPtr("")},
	})
	require.ErrorIs(t, err, ErrInvalidSubmission)
	assert.Contains(t, err.Error(), "ficou em branco")
}

func TestValidateSubmission_EscalaSemFaixaConfigurada(t *testing.T) {
	form := []db.ListActivityFieldsRow{
		field(t, "nota", FieldScale, FieldConfig{Required: true}, 1),
	}
	prepared, err := validateSubmission(form, []SubmissionValue{
		{FieldCode: "nota", Kind: FieldScale, Number: number(42)},
	})
	require.NoError(t, err)
	assert.Equal(t, float64(42), *prepared[0].number)
}

func TestValidateSubmission_ConfigVaziaNaoQuebra(t *testing.T) {
	form := []db.ListActivityFieldsRow{
		{ID: uuid.New(), Code: "livre", Label: "livre", FieldType: FieldShortText, DisplayOrder: 1},
	}
	prepared, err := validateSubmission(form, []SubmissionValue{
		{FieldCode: "livre", Kind: FieldShortText, Text: strPtr("ok")},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", *prepared[0].text)
}
