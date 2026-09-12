package activity

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }
func strPtr(v string) *string {
	return &v
}

func validRequest() TemplateRequest {
	return TemplateRequest{
		Title:        "  Registro de pensamentos ",
		Description:  strPtr("  "),
		Instructions: strPtr(" Sempre que sentir uma emoção intensa. "),
		TypeCode:     "record",
		Fields: []FieldInput{
			{Label: "Descreva a situação", FieldType: FieldLongText, HelpText: "Quando, onde e com quem?"},
			{Label: "Qual foi o pensamento?", FieldType: FieldShortText, MaxLength: intPtr(140)},
			{Label: "Intensidade da emoção", FieldType: FieldScale, Min: intPtr(1), Max: intPtr(10), MinLabel: "leve", MaxLabel: "intensa"},
			{Label: "Qual distorção você reconhece?", FieldType: FieldMultipleChoice, Options: []string{"Catastrofização", " Leitura mental ", "Outra"}},
			{Label: "Conseguiu responder ao pensamento?", FieldType: FieldBoolean, Required: boolPtr(false)},
			{Label: "Quando aconteceu?", FieldType: FieldDatetime},
			{Label: "Data", FieldType: FieldDate},
			{Label: "Escolha uma", FieldType: FieldSingleChoice, Options: []string{"A", "B"}},
		},
	}
}

func decodeConfig(t *testing.T, raw []byte) FieldConfig {
	t.Helper()
	var config FieldConfig
	require.NoError(t, json.Unmarshal(raw, &config))
	return config
}

func TestNormalizeTemplateHappyPath(t *testing.T) {
	got, err := normalizeTemplate(validRequest())
	require.NoError(t, err)

	assert.Equal(t, "Registro de pensamentos", got.Title)
	assert.Nil(t, got.Description, "descrição só com espaços vira nula")
	require.NotNil(t, got.Instructions)
	assert.Equal(t, "Sempre que sentir uma emoção intensa.", *got.Instructions)
	assert.Equal(t, "record", got.TypeCode)
	require.Len(t, got.Fields, 8)

	codes := make([]string, 0, len(got.Fields))
	for _, f := range got.Fields {
		codes = append(codes, f.Code)
	}
	assert.Equal(t, []string{
		"descreva_a_situacao", "qual_foi_o_pensamento", "intensidade_da_emocao",
		"qual_distorcao_voce_reconhece", "conseguiu_responder_ao_pensamento",
		"quando_aconteceu", "data", "escolha_uma",
	}, codes)

	longText := decodeConfig(t, got.Fields[0].Config)
	assert.True(t, longText.Required, "obrigatório por padrão")
	assert.Equal(t, "Quando, onde e com quem?", longText.HelpText)
	require.NotNil(t, longText.MaxLength)
	assert.Equal(t, defaultLongTextLength, *longText.MaxLength)

	shortText := decodeConfig(t, got.Fields[1].Config)
	assert.Equal(t, 140, *shortText.MaxLength)

	scale := decodeConfig(t, got.Fields[2].Config)
	assert.Equal(t, 1, *scale.Min)
	assert.Equal(t, 10, *scale.Max)
	assert.Equal(t, "leve", scale.MinLabel)
	assert.Equal(t, "intensa", scale.MaxLabel)

	choices := decodeConfig(t, got.Fields[3].Config)
	assert.Equal(t, []string{"Catastrofização", "Leitura mental", "Outra"}, choices.Options, "opções são aparadas")

	optional := decodeConfig(t, got.Fields[4].Config)
	assert.False(t, optional.Required)

	plain := decodeConfig(t, got.Fields[5].Config)
	assert.Nil(t, plain.MaxLength)
	assert.Nil(t, plain.Min)
	assert.Empty(t, plain.Options)
}

func TestNormalizeTemplateRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(r *TemplateRequest)
		message string
	}{
		{"título curto", func(r *TemplateRequest) { r.Title = " a " }, "Informe um título"},
		{"título longo", func(r *TemplateRequest) { r.Title = strings.Repeat("x", maxTemplateTitle+1) }, "no máximo 120"},
		{"descrição longa", func(r *TemplateRequest) { r.Description = strPtr(strings.Repeat("x", maxTemplateDescription+1)) }, "descrição"},
		{"sem tipo base", func(r *TemplateRequest) { r.TypeCode = " " }, "tipo base"},
		{"sem campos", func(r *TemplateRequest) { r.Fields = nil }, "pelo menos um campo"},
		{"campos demais", func(r *TemplateRequest) {
			for len(r.Fields) <= maxFieldsPerTemplate {
				r.Fields = append(r.Fields, FieldInput{Label: "x", FieldType: FieldBoolean})
			}
		}, "no máximo 30 campos"},
		{"campo sem pergunta", func(r *TemplateRequest) { r.Fields[1].Label = "  " }, "Campo 2: informe a pergunta"},
		{"campo sem tipo", func(r *TemplateRequest) { r.Fields[2].FieldType = "" }, "Campo 3: selecione o tipo"},
		{"tipo desconhecido", func(r *TemplateRequest) { r.Fields[0].FieldType = "file" }, "Campo 1: tipo de resposta desconhecido"},
		{"texto com tamanho zero", func(r *TemplateRequest) { r.Fields[1].MaxLength = intPtr(0) }, "Campo 2: o tamanho máximo"},
		{"texto longo acima do limite", func(r *TemplateRequest) { r.Fields[0].MaxLength = intPtr(maxLongTextLength + 1) }, "entre 1 e 5000"},
		{"escala sem limites", func(r *TemplateRequest) { r.Fields[2].Max = nil }, "Campo 3: a escala precisa"},
		{"escala invertida", func(r *TemplateRequest) { r.Fields[2].Min = intPtr(10); r.Fields[2].Max = intPtr(1) }, "menor que o máximo"},
		{"escala gigante", func(r *TemplateRequest) { r.Fields[2].Min = intPtr(0); r.Fields[2].Max = intPtr(maxScaleRange + 1) }, "no máximo 100 pontos"},
		{"escolha com uma opção", func(r *TemplateRequest) { r.Fields[7].Options = []string{"A"} }, "pelo menos 2 opções"},
		{"escolha com opção vazia", func(r *TemplateRequest) { r.Fields[3].Options = []string{"A", " "} }, "opções em branco"},
		{"escolha repetida", func(r *TemplateRequest) { r.Fields[3].Options = []string{"Outra", "outra"} }, "está repetida"},
		{"texto de apoio longo", func(r *TemplateRequest) { r.Fields[0].HelpText = strings.Repeat("x", maxFieldHelpText+1) }, "texto de apoio"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			tc.mutate(&req)
			_, err := normalizeTemplate(req)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidTemplate), "deve ser erro de validação")
			assert.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestFieldCodeIsStableAndUnique(t *testing.T) {
	assert.Equal(t, "descreva_a_situacao", fieldCode("Descreva a situação"))
	assert.Equal(t, "como_voce_se_sente_hoje", fieldCode("  Como você se sente hoje?  "))
	assert.Equal(t, "campo_1_pergunta", fieldCode("1. Pergunta"), "não começa com dígito")
	assert.Equal(t, "campo", fieldCode("???"), "sem letras cai no fallback")
	assert.LessOrEqual(t, len(fieldCode(strings.Repeat("palavra ", 20))), maxFieldCodeLength)

	used := map[string]bool{}
	assert.Equal(t, "humor", uniqueCode("humor", used))
	assert.Equal(t, "humor_2", uniqueCode("humor", used))
	assert.Equal(t, "humor_3", uniqueCode("humor", used))
}

func TestEnsureEditable(t *testing.T) {
	assert.NoError(t, ensureEditable(&TemplateDetail{OwnedByMe: true}))
	assert.ErrorIs(t, ensureEditable(&TemplateDetail{OwnedByMe: false}), ErrTemplateReadOnly)
	assert.ErrorIs(t, ensureEditable(&TemplateDetail{IsGlobal: true}), ErrTemplateReadOnly)
	assert.ErrorIs(t, ensureEditable(&TemplateDetail{OwnedByMe: true, IsArchived: true}), ErrTemplateArchived)
	assert.ErrorIs(t, ensureEditable(&TemplateDetail{OwnedByMe: true, Superseded: true}), ErrTemplateSuperseded)
}
