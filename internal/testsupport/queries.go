// Package testsupport oferece dublês para os testes de handler (httptest), sem
// exigir um banco real. FakeQuerier embute db.Querier, então só os métodos que
// um teste exercita precisam ser configurados — os demais entram em panic se
// chamados, denunciando expectativas erradas.
//
// Os testes de service (regras + isolamento multi-tenant) usam Postgres real via
// testcontainers — ver internal/session/service_test.go.
package testsupport

import (
	"context"

	"github.com/google/uuid"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
)

// FakeQuerier implementa db.Querier via embedding; cada *Fn sobrescreve um método.
type FakeQuerier struct {
	db.Querier

	HealthCheckFn           func(ctx context.Context) (int32, error)
	GetUserByEmailFn        func(ctx context.Context, email string) (db.GetUserByEmailRow, error)
	GetUserByIDFn           func(ctx context.Context, id uuid.UUID) (db.GetUserByIDRow, error)
	GetPsychologistByUserFn func(ctx context.Context, userID uuid.UUID) (db.GetPsychologistByUserRow, error)
	GetActiveRelationshipFn func(ctx context.Context, arg db.GetActiveRelationshipParams) (db.GetActiveRelationshipRow, error)
	CreateSessionFn         func(ctx context.Context, arg db.CreateSessionParams) (db.CreateSessionRow, error)
	GetSessionsByPatientFn  func(ctx context.Context, arg db.GetSessionsByPatientParams) ([]db.GetSessionsByPatientRow, error)
	CountAppointmentConflFn func(ctx context.Context, arg db.CountAppointmentConflictsParams) (int64, error)
	CreateAppointmentFn     func(ctx context.Context, arg db.CreateAppointmentParams) (db.CreateAppointmentRow, error)
	ListPatientsByOrgFn     func(ctx context.Context, organizationID uuid.UUID) ([]db.ListPatientsByOrgRow, error)
	WriteAuditLogFn         func(ctx context.Context, arg db.WriteAuditLogParams) error
}

// WriteAuditLog é no-op por padrão (o middleware Audit o chama em goroutine).
func (f *FakeQuerier) WriteAuditLog(ctx context.Context, arg db.WriteAuditLogParams) error {
	if f.WriteAuditLogFn != nil {
		return f.WriteAuditLogFn(ctx, arg)
	}
	return nil
}

func (f *FakeQuerier) HealthCheck(ctx context.Context) (int32, error) {
	if f.HealthCheckFn != nil {
		return f.HealthCheckFn(ctx)
	}
	return 1, nil
}

func (f *FakeQuerier) GetUserByEmail(ctx context.Context, email string) (db.GetUserByEmailRow, error) {
	if f.GetUserByEmailFn != nil {
		return f.GetUserByEmailFn(ctx, email)
	}
	panic("GetUserByEmail não configurado no FakeQuerier")
}

func (f *FakeQuerier) GetUserByID(ctx context.Context, id uuid.UUID) (db.GetUserByIDRow, error) {
	if f.GetUserByIDFn != nil {
		return f.GetUserByIDFn(ctx, id)
	}
	panic("GetUserByID não configurado no FakeQuerier")
}

func (f *FakeQuerier) GetPsychologistByUser(ctx context.Context, userID uuid.UUID) (db.GetPsychologistByUserRow, error) {
	if f.GetPsychologistByUserFn != nil {
		return f.GetPsychologistByUserFn(ctx, userID)
	}
	panic("GetPsychologistByUser não configurado no FakeQuerier")
}

func (f *FakeQuerier) GetActiveRelationship(ctx context.Context, arg db.GetActiveRelationshipParams) (db.GetActiveRelationshipRow, error) {
	if f.GetActiveRelationshipFn != nil {
		return f.GetActiveRelationshipFn(ctx, arg)
	}
	panic("GetActiveRelationship não configurado no FakeQuerier")
}

func (f *FakeQuerier) CreateSession(ctx context.Context, arg db.CreateSessionParams) (db.CreateSessionRow, error) {
	if f.CreateSessionFn != nil {
		return f.CreateSessionFn(ctx, arg)
	}
	panic("CreateSession não configurado no FakeQuerier")
}

func (f *FakeQuerier) GetSessionsByPatient(ctx context.Context, arg db.GetSessionsByPatientParams) ([]db.GetSessionsByPatientRow, error) {
	if f.GetSessionsByPatientFn != nil {
		return f.GetSessionsByPatientFn(ctx, arg)
	}
	panic("GetSessionsByPatient não configurado no FakeQuerier")
}

func (f *FakeQuerier) CountAppointmentConflicts(ctx context.Context, arg db.CountAppointmentConflictsParams) (int64, error) {
	if f.CountAppointmentConflFn != nil {
		return f.CountAppointmentConflFn(ctx, arg)
	}
	panic("CountAppointmentConflicts não configurado no FakeQuerier")
}

func (f *FakeQuerier) CreateAppointment(ctx context.Context, arg db.CreateAppointmentParams) (db.CreateAppointmentRow, error) {
	if f.CreateAppointmentFn != nil {
		return f.CreateAppointmentFn(ctx, arg)
	}
	panic("CreateAppointment não configurado no FakeQuerier")
}

func (f *FakeQuerier) ListPatientsByOrg(ctx context.Context, organizationID uuid.UUID) ([]db.ListPatientsByOrgRow, error) {
	if f.ListPatientsByOrgFn != nil {
		return f.ListPatientsByOrgFn(ctx, organizationID)
	}
	panic("ListPatientsByOrg não configurado no FakeQuerier")
}
