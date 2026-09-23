// Package trialstore atomically persists organization trials and canonical grants.
package trialstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/organization"
	"safepatch/internal/domain/trial"
	"safepatch/internal/platform/editionstore"
)

type Postgres struct {
	db       *sql.DB
	policy   *trial.Policy
	features *feature.FeatureCatalog
	now      func() time.Time
}

var _ trial.Repository = (*Postgres)(nil)

func NewPostgres(db *sql.DB, policy *trial.Policy, features *feature.FeatureCatalog, now func() time.Time) (*Postgres, error) {
	if db == nil || policy == nil || features == nil || now == nil {
		return nil, trial.ErrInvalid
	}
	if _, err := policy.Offer(edition.Developer); err != nil {
		return nil, trial.ErrInvalid
	}
	return &Postgres{db, policy, features, now}, nil
}
func (s *Postgres) check(ctx context.Context, org string) error {
	if s == nil || s.db == nil || s.policy == nil || s.features == nil || s.now == nil || ctx == nil || !organization.ValidID(org) {
		return trial.ErrInvalid
	}
	return ctx.Err()
}

const selectTrial = `SELECT t.organization_id,t.trial_id,t.edition_key,t.edition_version_id,t.starts_at,t.expires_at FROM organization_trials t WHERE t.organization_id=$1`

func decode(row *sql.Row, org string) (trial.Trial, error) {
	var v trial.Trial
	err := row.Scan(&v.OrganizationID, &v.TrialID, &v.Edition, &v.EditionVersionID, &v.StartsAt, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return trial.Trial{}, trial.ErrNotFound
	}
	if err != nil {
		return trial.Trial{}, err
	}
	v.StartsAt = v.StartsAt.UTC()
	v.ExpiresAt = v.ExpiresAt.UTC()
	if v.Validate() != nil || v.OrganizationID != org {
		return trial.Trial{}, trial.ErrInvalid
	}
	return v, nil
}
func (s *Postgres) Get(ctx context.Context, org string) (trial.Trial, error) {
	if err := s.check(ctx, org); err != nil {
		return trial.Trial{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return decode(s.db.QueryRowContext(ctx, selectTrial+` AND EXISTS (SELECT 1 FROM organizations o WHERE o.organization_id=t.organization_id AND o.state='ACTIVE')`, org), org)
}

func (s *Postgres) Start(ctx context.Context, org string, key edition.Key) (trial.Trial, error) {
	if err := s.check(ctx, org); err != nil {
		return trial.Trial{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return trial.Trial{}, err
	}
	defer tx.Rollback()
	// Every starter locks the same organization before checking eligibility. The
	// primary key is a second guard against retries on another process/connection.
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM organizations WHERE organization_id=$1 FOR UPDATE`, org).Scan(&state); err != nil {
		return trial.Trial{}, err
	}
	if state != "ACTIVE" {
		return trial.Trial{}, trial.ErrUnavailable
	}
	prior, err := decode(tx.QueryRowContext(ctx, selectTrial, org), org)
	if err == nil {
		if prior.Edition != key {
			return trial.Trial{}, trial.ErrUsed
		}
		if err = tx.Commit(); err != nil {
			return trial.Trial{}, err
		}
		return prior, nil
	}
	if !errors.Is(err, trial.ErrNotFound) {
		return trial.Trial{}, err
	}
	// Legacy TRIAL grants also consume eligibility; migration never rewrites them.
	var used bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM entitlements WHERE organization_id=$1 AND source='TRIAL')`, org).Scan(&used); err != nil {
		return trial.Trial{}, err
	}
	if used {
		return trial.Trial{}, trial.ErrUsed
	}
	offer, err := s.policy.Offer(key)
	if err != nil {
		return trial.Trial{}, err
	}
	bundle, err := editionstore.Load(ctx, tx, offer.EditionVersionID, s.features)
	if err != nil {
		return trial.Trial{}, err
	}
	v, err := trial.New(org, offer, s.now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return trial.Trial{}, err
	}
	grants, err := trial.Derive(v, bundle, s.features)
	if err != nil {
		return trial.Trial{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_trials (organization_id,trial_id,edition_key,edition_version_id,starts_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6)`, org, v.TrialID, string(v.Edition), v.EditionVersionID, v.StartsAt, v.ExpiresAt); err != nil {
		return trial.Trial{}, err
	}
	for _, e := range grants {
		raw, err := json.Marshal(e)
		if err != nil || len(raw) > 32768 {
			return trial.Trial{}, trial.ErrInvalid
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO entitlements (organization_id,entitlement_id,feature_id,source,source_reference,canonical_record) VALUES ($1,$2,$3,$4,$5,$6)`, org, e.EntitlementID, e.FeatureID, string(e.Source), e.SourceReference, string(raw)); err != nil {
			return trial.Trial{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_trial_grants (organization_id,trial_id,entitlement_id) VALUES ($1,$2,$3)`, org, v.TrialID, e.EntitlementID); err != nil {
			return trial.Trial{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return trial.Trial{}, err
	}
	return v, nil
}
