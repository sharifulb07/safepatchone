// Package licensestore persists trusted installation metadata separately from
// untrusted signed license bytes. Installation is an operator-only operation.
package licensestore

import (
	"context"
	"database/sql"
	"errors"

	"safepatch/internal/domain/entitlement"
	"safepatch/internal/licensing"
)

type Postgres struct {
	db  *sql.DB
	org string
}

var _ licensing.LicenseStateStore = (*Postgres)(nil)

func NewPostgres(db *sql.DB, org string) (*Postgres, error) {
	if db == nil || !entitlement.ValidID(org) {
		return nil, licensing.ErrInvalidLicense
	}
	return &Postgres{db, org}, nil
}
func (s *Postgres) check(ctx context.Context, org string) error {
	if s == nil || s.db == nil || ctx == nil || org != s.org {
		return licensing.ErrInvalidLicense
	}
	return ctx.Err()
}
func (s *Postgres) LoadLicense(ctx context.Context, org string) (licensing.LicenseState, error) {
	var v licensing.LicenseState
	if err := s.check(ctx, org); err != nil {
		return v, err
	}
	var revision int64
	var confirmed sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT revision,revoked,envelope,confirmed_at,refresh_denied FROM license_installations WHERE organization_id=$1`, org).Scan(&revision, &v.Revoked, &v.Envelope, &confirmed, &v.RefreshDenied)
	if errors.Is(err, sql.ErrNoRows) {
		return v, licensing.ErrLicenseNotInstalled
	}
	if err != nil {
		return licensing.LicenseState{}, licensing.ErrLicenseUnavailable
	}
	if revision <= 0 || len(v.Envelope) == 0 || len(v.Envelope) > licensing.MaxLicenseBytes {
		return licensing.LicenseState{}, licensing.ErrInvalidLicense
	}
	v.Revision = uint64(revision)
	if confirmed.Valid {
		v.ConfirmedAt = confirmed.Time.UTC()
	}
	return v, nil
}
func (s *Postgres) CompareAndSwapLicense(ctx context.Context, org string, expected uint64, next licensing.LicenseState) error {
	if err := s.check(ctx, org); err != nil {
		return err
	}
	if expected >= 1<<63-1 || next.Revision != expected+1 || len(next.Envelope) == 0 || len(next.Envelope) > licensing.MaxLicenseBytes || !next.ConfirmedAt.IsZero() && (next.ConfirmedAt.Year() < 1 || next.ConfirmedAt.Year() > 9999) {
		return licensing.ErrInvalidLicense
	}
	var confirmed any
	if !next.ConfirmedAt.IsZero() {
		confirmed = next.ConfirmedAt.UTC()
	}
	var result sql.Result
	var err error
	if expected == 0 {
		result, err = s.db.ExecContext(ctx, `INSERT INTO license_installations(organization_id,revision,revoked,envelope,confirmed_at,refresh_denied) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(organization_id) DO NOTHING`, org, int64(next.Revision), next.Revoked, next.Envelope, confirmed, next.RefreshDenied)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE license_installations SET revision=$2,revoked=$3,envelope=$4,confirmed_at=$5,refresh_denied=$6 WHERE organization_id=$1 AND revision=$7`, org, int64(next.Revision), next.Revoked, next.Envelope, confirmed, next.RefreshDenied, int64(expected))
	}
	if err != nil {
		return licensing.ErrLicenseUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil {
		return licensing.ErrLicenseUnavailable
	}
	if count != 1 {
		return licensing.ErrLicenseConflict
	}
	return nil
}
