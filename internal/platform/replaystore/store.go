// Package replaystore persists redacted artifacts and transactional run metadata.
package replaystore

import (
	"context"
	"database/sql"
	"encoding/json"

	"safepatch/internal/evidence"
	"safepatch/internal/replay"
	"safepatch/internal/tenant"
)

type Store struct {
	db       *sql.DB
	objects  evidence.OriginalStore
	redactor *replay.Redactor
}

func New(db *sql.DB, objects evidence.OriginalStore, redactor *replay.Redactor) (*Store, error) {
	if db == nil || objects == nil || redactor.Digest() == "" {
		return nil, replay.ErrInvalid
	}
	return &Store{db, objects, redactor}, nil
}
func archiveScope(s replay.Scope, id string) evidence.ArchiveScope {
	return evidence.ArchiveScope{OrganizationID: s.OrganizationID, Subject: evidence.Subject{RepositoryID: s.RepositoryID, Revision: id}}
}
func (s *Store) Put(ctx context.Context, d replay.ReplayDataset) error {
	f, err := d.Fields()
	if s == nil || ctx == nil || err != nil || f.RedactionDigest != s.redactor.Digest() {
		return replay.ErrInvalid
	}
	raw, err := d.MarshalJSON()
	if err != nil {
		return err
	}
	ref, err := s.objects.Put(ctx, archiveScope(f.Scope, f.ID), raw)
	if err != nil || ref.Validate() != nil || ref.SHA256 != d.Digest() || ref.Bytes != int64(len(raw)) {
		return replay.ErrUnavailable
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO replay_datasets(organization_id,repository_id,dataset_id,digest,bytes,redaction_digest,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, f.Scope.OrganizationID, f.Scope.RepositoryID, f.ID, ref.SHA256, ref.Bytes, f.RedactionDigest, f.CreatedAt, f.ExpiresAt)
	if err != nil {
		return replay.ErrUnavailable
	}
	return nil
}
func (s *Store) Get(ctx context.Context, scope replay.Scope, id string) (replay.ReplayDataset, error) {
	if s == nil || ctx == nil || scope.Validate() != nil || !tenant.ValidID(id) {
		return replay.ReplayDataset{}, replay.ErrInvalid
	}
	var ref evidence.OriginalReference
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT digest,bytes,redaction_digest FROM replay_datasets WHERE organization_id=$1 AND repository_id=$2 AND dataset_id=$3 AND expires_at>clock_timestamp()`, scope.OrganizationID, scope.RepositoryID, id).Scan(&ref.SHA256, &ref.Bytes, &digest)
	if err != nil || ref.Validate() != nil || ref.Bytes > replay.MaxDatasetBytes || digest != s.redactor.Digest() {
		return replay.ReplayDataset{}, replay.ErrUnavailable
	}
	raw, err := s.objects.Get(ctx, archiveScope(scope, id), ref)
	if err != nil || int64(len(raw)) != ref.Bytes || evidence.Digest(raw) != ref.SHA256 {
		return replay.ReplayDataset{}, replay.ErrUnavailable
	}
	d, err := replay.DecodeDataset(raw, s.redactor)
	if err != nil {
		return d, err
	}
	f, _ := d.Fields()
	if f.Scope != scope || f.ID != id {
		return replay.ReplayDataset{}, replay.ErrUnavailable
	}
	return d, nil
}
func (s *Store) Claim(ctx context.Context, scope replay.Scope, id, digest string) error {
	if s == nil || ctx == nil || scope.Validate() != nil || !tenant.ValidID(id) || (evidence.OriginalReference{SHA256: digest}).Validate() != nil {
		return replay.ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO replay_runs(organization_id,repository_id,run_id,dataset_digest,decision) VALUES($1,$2,$3,$4,'UNKNOWN')`, scope.OrganizationID, scope.RepositoryID, id, digest)
	if err != nil {
		return replay.ErrDenied
	}
	return nil
}
func (s *Store) Finish(ctx context.Context, scope replay.Scope, id string, r replay.Result) error {
	if s == nil || ctx == nil || scope.Validate() != nil || !tenant.ValidID(id) || (r.Decision != replay.Unknown && r.Decision != replay.Match && r.Decision != replay.Different) {
		return replay.ErrInvalid
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 64*1024 {
		return replay.ErrInvalid
	}
	res, err := s.db.ExecContext(ctx, `UPDATE replay_runs SET decision=$5,result=$6 WHERE organization_id=$1 AND repository_id=$2 AND run_id=$3 AND dataset_digest=$4 AND result IS NULL`, scope.OrganizationID, scope.RepositoryID, id, r.DatasetDigest, string(r.Decision), string(raw))
	if err != nil {
		return replay.ErrUnavailable
	}
	count, err := res.RowsAffected()
	if err != nil || count != 1 {
		return replay.ErrDenied
	}
	return nil
}
