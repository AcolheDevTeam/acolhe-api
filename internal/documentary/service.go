package documentary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/joycesilva/acolhe-api/internal/db/generated"
	"github.com/joycesilva/acolhe-api/internal/tenant"
)

const MaxContentBytes = 200000

var (
	ErrForbidden = errors.New("registro documental disponível somente à psicóloga autora")
	ErrNotFound  = errors.New("caderno ou paciente não encontrado")
	ErrReadOnly  = errors.New("paciente ou vínculo clínico inativo: caderno disponível somente para leitura")
	ErrConflict  = errors.New("este caderno foi atualizado em outra aba; consulte a versão mais recente antes de salvar")
	ErrInput     = errors.New("dados inválidos: confira a categoria, a revisão e o limite de 200.000 bytes do texto")
)
var Categories = []string{"hypothesis", "technical_observation", "planning", "transcription", "other"}

type Service struct {
	pool *pgxpool.Pool
	keys *Keyring
}

func NewService(pool *pgxpool.Pool, keys *Keyring) *Service { return &Service{pool, keys} }

type Notebook struct {
	ID        uuid.UUID `json:"id"`
	PatientID uuid.UUID `json:"patientId"`
	Category  string    `json:"category"`
	Content   string    `json:"content"`
	Revision  int       `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type Patient struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"fullName"`
	Writable bool      `json:"writable"`
}
type PatientNotebooks struct {
	Patient Patient    `json:"patient"`
	Items   []Notebook `json:"items"`
}
type Version struct {
	ID           uuid.UUID `json:"id"`
	Revision     int       `json:"revision"`
	CreatedAt    time.Time `json:"createdAt"`
	RestoredFrom *int      `json:"restoredFrom"`
	Content      *string   `json:"content,omitempty"`
}
type Page[T any] struct {
	Items      []T `json:"items"`
	TotalCount int `json:"totalCount"`
	TotalPages int `json:"totalPages"`
	Page       int `json:"page"`
	PageSize   int `json:"pageSize"`
}

func pageOf[T any](items []T, total, page, size int) Page[T] {
	return Page[T]{items, total, (total + size - 1) / size, page, size}
}

type scope struct {
	tx          pgx.Tx
	org, author uuid.UUID
}

func (s *Service) begin(ctx context.Context, readOnly bool) (*scope, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id.Role != "psychologist" || id.OrgID == uuid.Nil {
		return nil, ErrForbidden
	}
	if s.keys == nil || s.pool == nil {
		return nil, ErrCrypto
	}
	options := pgx.TxOptions{}
	if readOnly {
		options = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	}
	tx, err := s.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	q := db.New(tx)
	if err = tenant.ApplyRLSContext(ctx, tx, q, id); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	psy, err := q.GetPsychologistByUser(ctx, id.UserID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, ErrForbidden
	}
	return &scope{tx, id.OrgID, psy.ID}, nil
}
func aad(org, author uuid.UUID, n Notebook, revision int) []byte {
	b, _ := json.Marshal([]any{"documentary-v1", org, author, n.PatientID, n.ID, n.Category, revision})
	return b
}
func (sc *scope) patient(ctx context.Context, id uuid.UUID) (Patient, error) {
	p := Patient{ID: id}
	err := sc.tx.QueryRow(ctx, `SELECT p.full_name, p.status='active' AND p.deleted_at IS NULL AND has_active_clinical_relationship(p.id,$2)
 FROM patient_profile p WHERE p.id=$1 AND p.organization_id=$3 AND
 (EXISTS(SELECT 1 FROM patient_relationship r WHERE r.patient_id=p.id AND r.psychologist_id=$2)
 OR EXISTS(SELECT 1 FROM documentary_record d WHERE d.patient_id=p.id AND d.author_id=$2 AND d.organization_id=$3))`, id, sc.author, sc.org).Scan(&p.FullName, &p.Writable)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}
func (s *Service) Patients(ctx context.Context, page, size int) (Page[Patient], error) {
	sc, err := s.begin(ctx, true)
	if err != nil {
		return Page[Patient]{}, err
	}
	defer func() { _ = sc.tx.Rollback(ctx) }()
	filter := ` FROM patient_profile p WHERE p.organization_id=$1 AND EXISTS(SELECT 1 FROM documentary_record d WHERE d.patient_id=p.id AND d.organization_id=$1 AND d.author_id=$2)`
	var total int
	if err = sc.tx.QueryRow(ctx, `SELECT count(*)`+filter, sc.org, sc.author).Scan(&total); err != nil {
		return Page[Patient]{}, err
	}
	rows, err := sc.tx.Query(ctx, `SELECT p.id,p.full_name,p.status='active' AND p.deleted_at IS NULL AND has_active_clinical_relationship(p.id,$2)`+filter+` ORDER BY p.full_name,p.id LIMIT $3 OFFSET $4`, sc.org, sc.author, size, (page-1)*size)
	if err != nil {
		return Page[Patient]{}, err
	}
	defer rows.Close()
	items := []Patient{}
	for rows.Next() {
		var p Patient
		if err = rows.Scan(&p.ID, &p.FullName, &p.Writable); err != nil {
			return Page[Patient]{}, err
		}
		items = append(items, p)
	}
	return pageOf(items, total, page, size), rows.Err()
}
func (s *Service) Notebooks(ctx context.Context, patient uuid.UUID) (PatientNotebooks, error) {
	sc, err := s.begin(ctx, true)
	if err != nil {
		return PatientNotebooks{}, err
	}
	defer func() { _ = sc.tx.Rollback(ctx) }()
	p, err := sc.patient(ctx, patient)
	if err != nil {
		return PatientNotebooks{}, err
	}
	out := PatientNotebooks{p, []Notebook{}}
	rows, err := sc.tx.Query(ctx, `SELECT id,patient_id,category,revision,updated_at,content_encrypted FROM documentary_record WHERE organization_id=$1 AND author_id=$2 AND patient_id=$3 ORDER BY category,id`, sc.org, sc.author, patient)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var n Notebook
		var encrypted []byte
		if err = rows.Scan(&n.ID, &n.PatientID, &n.Category, &n.Revision, &n.UpdatedAt, &encrypted); err != nil {
			return out, err
		}
		n.Content, err = s.keys.Decrypt(encrypted, aad(sc.org, sc.author, n, n.Revision))
		if err != nil {
			return PatientNotebooks{}, err
		}
		out.Items = append(out.Items, n)
	}
	return out, rows.Err()
}
func (sc *scope) notebook(ctx context.Context, id uuid.UUID) (Notebook, error) {
	row, err := db.New(sc.tx).GetDocumentaryNotebook(ctx, db.GetDocumentaryNotebookParams{ID: id, OrganizationID: sc.org, AuthorID: sc.author})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return Notebook{ID: row.ID, PatientID: row.PatientID, Category: row.Category, Revision: int(row.Revision), UpdatedAt: row.UpdatedAt}, err
}

func (s *Service) History(ctx context.Context, id uuid.UUID, page, size int) (Page[Version], error) {
	sc, err := s.begin(ctx, true)
	if err != nil {
		return Page[Version]{}, err
	}
	defer func() { _ = sc.tx.Rollback(ctx) }()
	if _, err = sc.notebook(ctx, id); err != nil {
		return Page[Version]{}, err
	}
	var total int
	err = sc.tx.QueryRow(ctx, `SELECT count(*) FROM documentary_record_version WHERE record_id=$1 AND organization_id=$2 AND author_id=$3`, id, sc.org, sc.author).Scan(&total)
	if err != nil {
		return Page[Version]{}, err
	}
	rows, err := sc.tx.Query(ctx, `SELECT id,revision,created_at,restored_from FROM documentary_record_version WHERE record_id=$1 AND organization_id=$2 AND author_id=$3 ORDER BY revision DESC,id LIMIT $4 OFFSET $5`, id, sc.org, sc.author, size, (page-1)*size)
	if err != nil {
		return Page[Version]{}, err
	}
	defer rows.Close()
	items := []Version{}
	for rows.Next() {
		var v Version
		if err = rows.Scan(&v.ID, &v.Revision, &v.CreatedAt, &v.RestoredFrom); err != nil {
			return Page[Version]{}, err
		}
		items = append(items, v)
	}
	return pageOf(items, total, page, size), rows.Err()
}
func (s *Service) version(ctx context.Context, sc *scope, n Notebook, revision int) (Version, error) {
	var v Version
	var encrypted []byte
	err := sc.tx.QueryRow(ctx, `SELECT id,revision,created_at,restored_from,content_encrypted FROM documentary_record_version WHERE record_id=$1 AND organization_id=$2 AND author_id=$3 AND revision=$4`, n.ID, sc.org, sc.author, revision).Scan(&v.ID, &v.Revision, &v.CreatedAt, &v.RestoredFrom, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	text, err := s.keys.Decrypt(encrypted, aad(sc.org, sc.author, n, v.Revision))
	v.Content = &text
	return v, err
}
func (s *Service) GetVersion(ctx context.Context, id uuid.UUID, revision int) (Version, error) {
	sc, err := s.begin(ctx, true)
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = sc.tx.Rollback(ctx) }()
	n, err := sc.notebook(ctx, id)
	if err != nil {
		return Version{}, err
	}
	return s.version(ctx, sc, n, revision)
}
func validCategory(category string) bool {
	for _, c := range Categories {
		if c == category {
			return true
		}
	}
	return false
}

// Save serializes even first saves, rechecks clinical authorization and commits before returning.
// restoredFrom is resolved inside this same transaction, after authorization.
func (s *Service) Save(ctx context.Context, patient uuid.UUID, category, content string, expected int, restoredFrom *int) (Notebook, error) {
	if !validCategory(category) || expected < 0 || len(content) > MaxContentBytes || !utf8.ValidString(content) {
		return Notebook{}, ErrInput
	}
	sc, err := s.begin(ctx, false)
	if err != nil {
		return Notebook{}, err
	}
	defer func() { _ = sc.tx.Rollback(ctx) }()
	_, err = sc.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("documentary/%s/%s/%s/%s", sc.org, sc.author, patient, category))
	if err != nil {
		return Notebook{}, err
	}
	// Locks prevent deactivation/consent changes during a successful write.
	rows, err := sc.tx.Query(ctx, `SELECT p.id FROM patient_profile p WHERE p.id=$1 AND p.organization_id=$2 FOR SHARE`, patient, sc.org)
	if err != nil {
		return Notebook{}, err
	}
	rows.Close()
	rows, err = sc.tx.Query(ctx, `SELECT r.id FROM patient_relationship r JOIN patient_profile p ON p.id=r.patient_id WHERE r.patient_id=$1 AND r.psychologist_id=$2 AND p.organization_id=$3 FOR SHARE OF r`, patient, sc.author, sc.org)
	if err != nil {
		return Notebook{}, err
	}
	rows.Close()
	rows, err = sc.tx.Query(ctx, `SELECT c.id FROM consent c JOIN consent_document consent_doc ON consent_doc.id=c.document_id JOIN patient_relationship r ON r.consent_id=c.id JOIN patient_profile p ON p.id=r.patient_id WHERE r.patient_id=$1 AND r.psychologist_id=$2 AND p.organization_id=$3 FOR SHARE OF c,consent_doc`, patient, sc.author, sc.org)
	if err != nil {
		return Notebook{}, err
	}
	rows.Close()
	p, err := sc.patient(ctx, patient)
	if err != nil {
		return Notebook{}, err
	}
	if !p.Writable {
		return Notebook{}, ErrReadOnly
	}
	n := Notebook{PatientID: patient, Category: category}
	var encrypted []byte
	err = sc.tx.QueryRow(ctx, `SELECT id,revision,updated_at,content_encrypted FROM documentary_record WHERE organization_id=$1 AND author_id=$2 AND patient_id=$3 AND category=$4 FOR UPDATE`, sc.org, sc.author, patient, category).Scan(&n.ID, &n.Revision, &n.UpdatedAt, &encrypted)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Notebook{}, err
	}
	if exists {
		n.Content, err = s.keys.Decrypt(encrypted, aad(sc.org, sc.author, n, n.Revision))
		if err != nil {
			return Notebook{}, err
		}
	}
	if restoredFrom != nil {
		if !exists {
			return Notebook{}, ErrNotFound
		}
		v, e := s.version(ctx, sc, n, *restoredFrom)
		if e != nil {
			return Notebook{}, e
		}
		content = *v.Content
	}
	// Equivalent retries are idempotent, including a response lost after commit.
	if exists && n.Content == content {
		return n, nil
	}
	if n.Revision != expected {
		return Notebook{}, ErrConflict
	}
	if !exists {
		n.ID = uuid.New()
	}
	n.Revision++
	n.Content = content
	encrypted, err = s.keys.Encrypt(content, aad(sc.org, sc.author, n, n.Revision))
	if err != nil {
		return Notebook{}, err
	}
	if exists {
		err = sc.tx.QueryRow(ctx, `UPDATE documentary_record SET content_encrypted=$1,revision=$2,updated_at=now() WHERE id=$3 AND organization_id=$4 AND author_id=$5 AND revision=$6 RETURNING updated_at`, encrypted, n.Revision, n.ID, sc.org, sc.author, expected).Scan(&n.UpdatedAt)
	} else {
		err = sc.tx.QueryRow(ctx, `INSERT INTO documentary_record(id,organization_id,author_id,patient_id,category,content_encrypted,revision) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING updated_at`, n.ID, sc.org, sc.author, patient, category, encrypted, n.Revision).Scan(&n.UpdatedAt)
	}
	if err != nil {
		return Notebook{}, err
	}
	// Independent nonce for the immutable snapshot.
	snapshot, err := s.keys.Encrypt(content, aad(sc.org, sc.author, n, n.Revision))
	if err != nil {
		return Notebook{}, err
	}
	_, err = sc.tx.Exec(ctx, `INSERT INTO documentary_record_version(record_id,organization_id,author_id,revision,content_encrypted,restored_from) VALUES($1,$2,$3,$4,$5,$6)`, n.ID, sc.org, sc.author, n.Revision, snapshot, restoredFrom)
	if err != nil {
		return Notebook{}, err
	}
	if err = sc.tx.Commit(ctx); err != nil {
		return Notebook{}, err
	}
	return n, nil
}
