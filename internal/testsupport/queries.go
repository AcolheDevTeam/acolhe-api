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

	HealthCheckFn               func(ctx context.Context) (int32, error)
	GetUserByEmailFn            func(ctx context.Context, email string) (db.GetUserByEmailRow, error)
	GetUserByIDFn               func(ctx context.Context, id uuid.UUID) (db.GetUserByIDRow, error)
	GetPsychologistByUserFn     func(ctx context.Context, userID uuid.UUID) (db.GetPsychologistByUserRow, error)
	GetActiveRelationshipFn     func(ctx context.Context, arg db.GetActiveRelationshipParams) (db.GetActiveRelationshipRow, error)
	GetPatientExportAccessFn    func(ctx context.Context, arg db.GetPatientExportAccessParams) (db.GetPatientExportAccessRow, error)
	GetPatientForPsychFn        func(ctx context.Context, arg db.GetPatientForPsychologistParams) (db.GetPatientForPsychologistRow, error)
	GetPatientPortalContextFn   func(ctx context.Context, userID *uuid.UUID) (db.GetPatientPortalContextRow, error)
	GetReissuableInvitationFn   func(ctx context.Context, arg db.GetReissuableInvitationForPatientParams) (db.GetReissuableInvitationForPatientRow, error)
	ReissuePatientInvitationFn  func(ctx context.Context, arg db.ReissuePatientInvitationParams) (db.ReissuePatientInvitationRow, error)
	CreateLGPDExportRequestFn   func(ctx context.Context, arg db.CreateLGPDExportRequestParams) (db.LgpdExportRequest, error)
	MarkLGPDExportQueueFailedFn func(ctx context.Context, arg db.MarkLGPDExportQueueFailedParams) (int64, error)
	GetActivityReviewFn         func(ctx context.Context, arg db.GetActivityReviewMetadataParams) (db.GetActivityReviewMetadataRow, error)
	ListActivityValuesFn        func(ctx context.Context, arg db.ListActivityReviewValuesParams) ([]db.ListActivityReviewValuesRow, error)
	MarkCompleteReviewedFn      func(ctx context.Context, arg db.MarkCompleteAssignmentReviewedParams) (int64, error)
	CreateSessionFn             func(ctx context.Context, arg db.CreateSessionParams) (db.CreateSessionRow, error)
	CreateClinicalRecordFn      func(ctx context.Context, arg db.CreateClinicalRecordParams) error
	GetSessionsByPatientFn      func(ctx context.Context, arg db.GetSessionsByPatientParams) ([]db.GetSessionsByPatientRow, error)
	CountAppointmentConflFn     func(ctx context.Context, arg db.CountAppointmentConflictsParams) (int64, error)
	CreateAppointmentFn         func(ctx context.Context, arg db.CreateAppointmentParams) (db.CreateAppointmentRow, error)
	GetAppointmentForPsychFn    func(ctx context.Context, arg db.GetAppointmentForPsychologistParams) (db.GetAppointmentForPsychologistRow, error)
	UpdateAppointmentStatusFn   func(ctx context.Context, arg db.UpdateAppointmentStatusParams) (db.UpdateAppointmentStatusRow, error)
	PatientInOrgFn              func(ctx context.Context, arg db.PatientInOrgParams) (bool, error)
	CreateCheckinFn             func(ctx context.Context, arg db.CreateCheckinParams) (db.Checkin, error)
	ListCheckinsFn              func(ctx context.Context, arg db.ListCheckinsByPatientParams) ([]db.Checkin, error)
	CreateDocumentFn            func(ctx context.Context, arg db.CreateDocumentParams) (db.CreateDocumentRow, error)
	ListDocumentsFn             func(ctx context.Context, arg db.ListDocumentsByPatientParams) ([]db.ListDocumentsByPatientRow, error)
	ListPatientsByOrgFn         func(ctx context.Context, organizationID uuid.UUID) ([]db.ListPatientsByOrgRow, error)
	ListPatientsByPsychFn       func(ctx context.Context, arg db.ListPatientsByPsychologistParams) ([]db.ListPatientsByPsychologistRow, error)
	WriteAuditLogFn             func(ctx context.Context, arg db.WriteAuditLogParams) error
}

func (f *FakeQuerier) GetPatientPortalContext(ctx context.Context, userID *uuid.UUID) (db.GetPatientPortalContextRow, error) {
	if f.GetPatientPortalContextFn != nil {
		return f.GetPatientPortalContextFn(ctx, userID)
	}
	panic("GetPatientPortalContext não configurado no FakeQuerier")
}

func (f *FakeQuerier) GetPatientForPsychologist(ctx context.Context, arg db.GetPatientForPsychologistParams) (db.GetPatientForPsychologistRow, error) {
	if f.GetPatientForPsychFn != nil {
		return f.GetPatientForPsychFn(ctx, arg)
	}
	panic("GetPatientForPsychologist não configurado no FakeQuerier")
}

func (f *FakeQuerier) GetReissuableInvitationForPatient(ctx context.Context, arg db.GetReissuableInvitationForPatientParams) (db.GetReissuableInvitationForPatientRow, error) {
	if f.GetReissuableInvitationFn != nil {
		return f.GetReissuableInvitationFn(ctx, arg)
	}
	panic("GetReissuableInvitationForPatient não configurado no FakeQuerier")
}

func (f *FakeQuerier) ReissuePatientInvitation(ctx context.Context, arg db.ReissuePatientInvitationParams) (db.ReissuePatientInvitationRow, error) {
	if f.ReissuePatientInvitationFn != nil {
		return f.ReissuePatientInvitationFn(ctx, arg)
	}
	panic("ReissuePatientInvitation não configurado no FakeQuerier")
}

func (f *FakeQuerier) PatientInOrg(ctx context.Context, arg db.PatientInOrgParams) (bool, error) {
	if f.PatientInOrgFn != nil {
		return f.PatientInOrgFn(ctx, arg)
	}
	panic("PatientInOrg não configurado no FakeQuerier")
}

func (f *FakeQuerier) CreateCheckin(ctx context.Context, arg db.CreateCheckinParams) (db.Checkin, error) {
	if f.CreateCheckinFn != nil {
		return f.CreateCheckinFn(ctx, arg)
	}
	panic("CreateCheckin não configurado no FakeQuerier")
}

func (f *FakeQuerier) ListCheckinsByPatient(ctx context.Context, arg db.ListCheckinsByPatientParams) ([]db.Checkin, error) {
	if f.ListCheckinsFn != nil {
		return f.ListCheckinsFn(ctx, arg)
	}
	panic("ListCheckinsByPatient não configurado no FakeQuerier")
}

func (f *FakeQuerier) CreateDocument(ctx context.Context, arg db.CreateDocumentParams) (db.CreateDocumentRow, error) {
	if f.CreateDocumentFn != nil {
		return f.CreateDocumentFn(ctx, arg)
	}
	panic("CreateDocument não configurado no FakeQuerier")
}

func (f *FakeQuerier) ListDocumentsByPatient(ctx context.Context, arg db.ListDocumentsByPatientParams) ([]db.ListDocumentsByPatientRow, error) {
	if f.ListDocumentsFn != nil {
		return f.ListDocumentsFn(ctx, arg)
	}
	panic("ListDocumentsByPatient não configurado no FakeQuerier")
}

func (f *FakeQuerier) GetPatientExportAccess(ctx context.Context, arg db.GetPatientExportAccessParams) (db.GetPatientExportAccessRow, error) {
	if f.GetPatientExportAccessFn != nil {
		return f.GetPatientExportAccessFn(ctx, arg)
	}
	panic("GetPatientExportAccess não configurado no FakeQuerier")
}

func (f *FakeQuerier) CreateLGPDExportRequest(ctx context.Context, arg db.CreateLGPDExportRequestParams) (db.LgpdExportRequest, error) {
	if f.CreateLGPDExportRequestFn != nil {
		return f.CreateLGPDExportRequestFn(ctx, arg)
	}
	panic("CreateLGPDExportRequest não configurado no FakeQuerier")
}

func (f *FakeQuerier) MarkLGPDExportQueueFailed(ctx context.Context, arg db.MarkLGPDExportQueueFailedParams) (int64, error) {
	if f.MarkLGPDExportQueueFailedFn != nil {
		return f.MarkLGPDExportQueueFailedFn(ctx, arg)
	}
	panic("MarkLGPDExportQueueFailed não configurado no FakeQuerier")
}

func (f *FakeQuerier) GetActivityReviewMetadata(ctx context.Context, arg db.GetActivityReviewMetadataParams) (db.GetActivityReviewMetadataRow, error) {
	if f.GetActivityReviewFn != nil {
		return f.GetActivityReviewFn(ctx, arg)
	}
	panic("GetActivityReviewMetadata não configurado no FakeQuerier")
}

func (f *FakeQuerier) ListActivityReviewValues(ctx context.Context, arg db.ListActivityReviewValuesParams) ([]db.ListActivityReviewValuesRow, error) {
	if f.ListActivityValuesFn != nil {
		return f.ListActivityValuesFn(ctx, arg)
	}
	panic("ListActivityReviewValues não configurado no FakeQuerier")
}

func (f *FakeQuerier) MarkCompleteAssignmentReviewed(ctx context.Context, arg db.MarkCompleteAssignmentReviewedParams) (int64, error) {
	if f.MarkCompleteReviewedFn != nil {
		return f.MarkCompleteReviewedFn(ctx, arg)
	}
	panic("MarkCompleteAssignmentReviewed não configurado no FakeQuerier")
}

func (f *FakeQuerier) CreateClinicalRecord(ctx context.Context, arg db.CreateClinicalRecordParams) error {
	if f.CreateClinicalRecordFn != nil {
		return f.CreateClinicalRecordFn(ctx, arg)
	}
	return nil
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

func (f *FakeQuerier) GetAppointmentForPsychologist(ctx context.Context, arg db.GetAppointmentForPsychologistParams) (db.GetAppointmentForPsychologistRow, error) {
	if f.GetAppointmentForPsychFn != nil {
		return f.GetAppointmentForPsychFn(ctx, arg)
	}
	panic("GetAppointmentForPsychologist não configurado no FakeQuerier")
}

func (f *FakeQuerier) UpdateAppointmentStatus(ctx context.Context, arg db.UpdateAppointmentStatusParams) (db.UpdateAppointmentStatusRow, error) {
	if f.UpdateAppointmentStatusFn != nil {
		return f.UpdateAppointmentStatusFn(ctx, arg)
	}
	panic("UpdateAppointmentStatus não configurado no FakeQuerier")
}

func (f *FakeQuerier) ListPatientsByOrg(ctx context.Context, organizationID uuid.UUID) ([]db.ListPatientsByOrgRow, error) {
	if f.ListPatientsByOrgFn != nil {
		return f.ListPatientsByOrgFn(ctx, organizationID)
	}
	panic("ListPatientsByOrg não configurado no FakeQuerier")
}

func (f *FakeQuerier) ListPatientsByPsychologist(ctx context.Context, arg db.ListPatientsByPsychologistParams) ([]db.ListPatientsByPsychologistRow, error) {
	if f.ListPatientsByPsychFn != nil {
		return f.ListPatientsByPsychFn(ctx, arg)
	}
	panic("ListPatientsByPsychologist não configurado no FakeQuerier")
}
