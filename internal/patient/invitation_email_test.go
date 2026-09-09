package patient_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/mailer"
	"github.com/joycesilva/acolhe-api/internal/patient"
	"github.com/joycesilva/acolhe-api/internal/tenant"
	"github.com/joycesilva/acolhe-api/internal/testsupport"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
	err  error
}

func (m *fakeMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

// reissueFixture monta um fake com convite pendente reemissível para o paciente.
func reissueFixture(t *testing.T, identity tenant.Identity, psychologistID, patientID uuid.UUID) *testsupport.FakeQuerier {
	t.Helper()
	expiresAt := time.Date(2026, time.September, 16, 18, 0, 0, 0, time.UTC)
	return &testsupport.FakeQuerier{
		GetPsychologistByUserFn: func(_ context.Context, _ uuid.UUID) (db.GetPsychologistByUserRow, error) {
			return db.GetPsychologistByUserRow{ID: psychologistID, FullName: "Dra. Ana Lima"}, nil
		},
		GetReissuableInvitationFn: func(_ context.Context, _ db.GetReissuableInvitationForPatientParams) (db.GetReissuableInvitationForPatientRow, error) {
			return db.GetReissuableInvitationForPatientRow{ID: uuid.New(), Email: "mariana@example.test"}, nil
		},
		ReissuePatientInvitationFn: func(_ context.Context, arg db.ReissuePatientInvitationParams) (db.ReissuePatientInvitationRow, error) {
			return db.ReissuePatientInvitationRow{ID: arg.ID, ExpiresAt: expiresAt}, nil
		},
		GetPatientForPsychFn: func(_ context.Context, arg db.GetPatientForPsychologistParams) (db.GetPatientForPsychologistRow, error) {
			assert.Equal(t, patientID, arg.ID)
			assert.Equal(t, identity.OrgID, arg.OrganizationID)
			return db.GetPatientForPsychologistRow{ID: patientID, FullName: "Mariana Costa Silva", Status: "onboarding", RelationshipStatus: "pending"}, nil
		},
	}
}

func TestReissueInvitationSendsEmailWithLinkAndStatus(t *testing.T) {
	identity := tenant.Identity{UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist"}
	psychologistID, patientID := uuid.New(), uuid.New()
	mail := &fakeMailer{}

	svc := patient.NewService(reissueFixture(t, identity, psychologistID, patientID), nil,
		patient.WithInvitationMailer(mail, "https://app.acolhe.test/"))
	invitation, err := svc.ReissueInvitation(tenant.WithIdentity(context.Background(), identity), patientID)
	require.NoError(t, err)

	assert.Equal(t, patient.DeliverySent, invitation.DeliveryStatus)
	require.Len(t, mail.sent, 1)
	msg := mail.sent[0]
	assert.Equal(t, "mariana@example.test", msg.To)
	assert.Equal(t, "Convite para o Acolhe · Dra. Ana Lima", msg.Subject)
	assert.Contains(t, msg.Body, "Olá, Mariana.")
	assert.Contains(t, msg.Body, "Dra. Ana Lima convidou você")
	assert.Contains(t, msg.Body, "https://app.acolhe.test/invite/"+invitation.Token, "link usa a origem do frontend sem barra duplicada")
	assert.Contains(t, msg.Body, "16/09/2026 às 15:00 (horário de Brasília)", "validade convertida para o fuso local")
	assert.NotContains(t, msg.Body, "https://app.acolhe.test//", "sem barra dupla")
}

func TestReissueInvitationKeepsTokenWhenEmailFails(t *testing.T) {
	identity := tenant.Identity{UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist"}
	psychologistID, patientID := uuid.New(), uuid.New()
	mail := &fakeMailer{err: errors.New("provedor indisponível")}

	svc := patient.NewService(reissueFixture(t, identity, psychologistID, patientID), nil,
		patient.WithInvitationMailer(mail, "https://app.acolhe.test"))
	invitation, err := svc.ReissueInvitation(tenant.WithIdentity(context.Background(), identity), patientID)
	require.NoError(t, err, "falha de e-mail não pode falhar a operação")
	assert.Equal(t, patient.DeliveryFailed, invitation.DeliveryStatus)
	assert.Len(t, invitation.Token, 43, "token continua disponível para o link copiável")
	assert.Empty(t, mail.sent)
}

func TestReissueInvitationWithoutMailerReportsDisabled(t *testing.T) {
	identity := tenant.Identity{UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist"}
	psychologistID, patientID := uuid.New(), uuid.New()

	svc := patient.NewService(reissueFixture(t, identity, psychologistID, patientID), nil)
	invitation, err := svc.ReissueInvitation(tenant.WithIdentity(context.Background(), identity), patientID)
	require.NoError(t, err)
	assert.Equal(t, patient.DeliveryDisabled, invitation.DeliveryStatus)
	assert.Len(t, invitation.Token, 43)
}

func TestInvitationLinkEscapesTokenAndTrimsSlash(t *testing.T) {
	assert.Equal(t, "https://x.test/invite/abc_-123", patient.InvitationLink("https://x.test/", "abc_-123"))
}
