package activity

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

func reviewContext() (context.Context, tenant.Identity, uuid.UUID) {
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	return tenant.WithIdentity(context.Background(), identity), identity, uuid.New()
}

func completeReviewFixture(assignmentID, psyID uuid.UUID) (db.GetActivityReviewMetadataRow, []db.ListActivityReviewValuesRow) {
	responseID := uuid.New()
	submittedAt := time.Now().UTC().Add(-time.Hour)
	metadata := db.GetActivityReviewMetadataRow{
		ID: assignmentID, TemplateID: uuid.New(), TemplateVersion: 2,
		PatientID: uuid.New(), PatientName: "Paciente", AssignerID: psyID,
		Status: "submitted", Title: "Registro", Type: "record",
		ResponseID: responseID.String(), SubmittedAt: &submittedAt,
		SubmissionComplete: true, FieldCount: 5, CreatedAt: submittedAt.Add(-time.Hour),
	}
	textValue := "Situação descrita"
	booleanValue := true
	datetimeValue := submittedAt.Add(-30 * time.Minute)
	values := []db.ListActivityReviewValuesRow{
		{
			FieldID: uuid.New(), FieldCode: "text", Label: "Texto",
			FieldType: "long_text", Config: []byte(`{}`), DisplayOrder: 1,
			ValueText: &textValue,
		},
		{
			FieldID: uuid.New(), FieldCode: "scale", Label: "Escala",
			FieldType: "scale", Config: []byte(`{"min":1,"max":10}`), DisplayOrder: 2,
			ValueNumber: pgtype.Numeric{Int: big.NewInt(7), Valid: true},
		},
		{
			FieldID: uuid.New(), FieldCode: "boolean", Label: "Sim ou não",
			FieldType: "boolean", Config: []byte(`{}`), DisplayOrder: 3,
			ValueBoolean: &booleanValue,
		},
		{
			FieldID: uuid.New(), FieldCode: "datetime", Label: "Quando",
			FieldType: "datetime", Config: []byte(`{}`), DisplayOrder: 4,
			ValueDatetime: &datetimeValue,
		},
		{
			FieldID: uuid.New(), FieldCode: "choices", Label: "Opções",
			FieldType: "multiple_choice", Config: []byte(`{}`), DisplayOrder: 5,
			ValueJson: []byte(`["A","B"]`),
		},
	}
	return metadata, values
}

func TestGetReviewDetailReturnsOrderedTypedValues(t *testing.T) {
	ctx, identity, psyID := reviewContext()
	assignmentID := uuid.New()
	metadata, values := completeReviewFixture(assignmentID, psyID)
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psyID}, nil
		},
		GetActivityReviewFn: func(_ context.Context, arg db.GetActivityReviewMetadataParams) (db.GetActivityReviewMetadataRow, error) {
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			assert.Equal(t, psyID, arg.AssignerID)
			return metadata, nil
		},
		ListActivityValuesFn: func(_ context.Context, arg db.ListActivityReviewValuesParams) ([]db.ListActivityReviewValuesRow, error) {
			assert.Equal(t, assignmentID, arg.AssignmentID)
			return values, nil
		},
	}

	detail, err := NewService(fake).Get(ctx, assignmentID)
	require.NoError(t, err)
	require.NotNil(t, detail.Submission)
	assert.Equal(t, "submitted", detail.State)
	assert.Equal(t, int32(2), detail.TemplateVersion)
	assert.Equal(t, []string{"text", "number", "boolean", "datetime", "json"}, []string{
		detail.Submission.Fields[0].Kind,
		detail.Submission.Fields[1].Kind,
		detail.Submission.Fields[2].Kind,
		detail.Submission.Fields[3].Kind,
		detail.Submission.Fields[4].Kind,
	})
}

func TestGetReviewDetailFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		responseID string
		want       string
	}{
		{name: "awaiting", status: "pending", want: "awaiting_response"},
		{name: "closed", status: "canceled", want: "closed_without_submission"},
		{name: "status without response", status: "submitted", want: "submission_invalid"},
		{name: "incomplete legacy response", status: "submitted", responseID: uuid.NewString(), want: "submission_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, _, psyID := reviewContext()
			fake := &testsupport.FakeQuerier{
				GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
					return db.GetPsychologistByUserRow{ID: psyID}, nil
				},
				GetActivityReviewFn: func(context.Context, db.GetActivityReviewMetadataParams) (db.GetActivityReviewMetadataRow, error) {
					return db.GetActivityReviewMetadataRow{
						ID: uuid.New(), Status: test.status, ResponseID: test.responseID,
					}, nil
				},
			}
			detail, err := NewService(fake).Get(ctx, uuid.New())
			require.NoError(t, err)
			assert.Equal(t, test.want, detail.State)
			assert.Nil(t, detail.Submission)
		})
	}
}

func TestGetReviewDetailHidesOtherOwner(t *testing.T) {
	ctx, _, psyID := reviewContext()
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psyID}, nil
		},
		GetActivityReviewFn: func(context.Context, db.GetActivityReviewMetadataParams) (db.GetActivityReviewMetadataRow, error) {
			return db.GetActivityReviewMetadataRow{}, pgx.ErrNoRows
		},
	}
	_, err := NewService(fake).Get(ctx, uuid.New())
	require.ErrorIs(t, err, ErrAssignmentNotFound)
}

func TestMarkReviewedIsCompleteAndIdempotent(t *testing.T) {
	ctx, _, psyID := reviewContext()
	assignmentID := uuid.New()
	metadata, values := completeReviewFixture(assignmentID, psyID)
	updates := 0
	fake := &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(context.Context, uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psyID}, nil
		},
		GetActivityReviewFn: func(context.Context, db.GetActivityReviewMetadataParams) (db.GetActivityReviewMetadataRow, error) {
			if updates > 0 {
				metadata.Status = "reviewed"
				reviewedAt := time.Now().UTC()
				metadata.ReviewedAt = &reviewedAt
			}
			return metadata, nil
		},
		ListActivityValuesFn: func(context.Context, db.ListActivityReviewValuesParams) ([]db.ListActivityReviewValuesRow, error) {
			return values, nil
		},
		MarkCompleteReviewedFn: func(context.Context, db.MarkCompleteAssignmentReviewedParams) (int64, error) {
			updates++
			return 1, nil
		},
	}
	service := NewService(fake)
	first, err := service.MarkReviewed(ctx, assignmentID)
	require.NoError(t, err)
	assert.Equal(t, "reviewed", first.State)
	second, err := service.MarkReviewed(ctx, assignmentID)
	require.NoError(t, err)
	assert.Equal(t, "reviewed", second.State)
	assert.Equal(t, 1, updates)
}
