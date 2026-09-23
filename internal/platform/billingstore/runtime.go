package billingstore

import (
	"context"
	"database/sql"
	"time"

	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/editionstore"
	"safepatch/internal/platform/organizationstore"
)

func (s *Postgres) bundle(ctx context.Context, tx *sql.Tx, id string) (edition.Bundle, error) {
	return editionstore.Load(ctx, tx, id, s.features)
}

// RuntimeEntitlements uses PostgreSQL domain state and sealed SafePatch bundles.
// It has no BillingProvider field and deliberately has no cross-request cache.
type RuntimeEntitlements struct {
	store    *Postgres
	fallback string
}

var _ entitlement.EntitlementService = (*RuntimeEntitlements)(nil)

// NewOrganizationRuntime enables local entitlement reads without configuring any
// billing provider. It uses the same resolver and storage as subscription sync.
func NewOrganizationRuntime(db *sql.DB, features *feature.FeatureCatalog, fallbackVersion string, now func() time.Time) (*RuntimeEntitlements, error) {
	if db == nil || features == nil || now == nil {
		return nil, billing.ErrInvalidConfiguration
	}
	return NewRuntimeEntitlements(&Postgres{db: db, features: features, now: now}, fallbackVersion)
}

func NewRuntimeEntitlements(store *Postgres, fallbackVersion string) (*RuntimeEntitlements, error) {
	if store == nil || !org.ValidID(fallbackVersion) {
		return nil, billing.ErrInvalidConfiguration
	}
	return &RuntimeEntitlements{store, fallbackVersion}, nil
}
func (r *RuntimeEntitlements) Has(ctx context.Context, id string, key feature.Key) (bool, error) {
	v, err := r.Resolve(ctx, id, key)
	return err == nil && v.Enabled, err
}
func (r *RuntimeEntitlements) Limit(ctx context.Context, id string, key feature.Key) (int64, error) {
	v, err := r.Resolve(ctx, id, key)
	if err != nil {
		return 0, err
	}
	if !v.Enabled {
		return 0, entitlement.ErrNotEntitled
	}
	if v.EffectiveLimit == nil {
		return 0, entitlement.ErrLimitUnspecified
	}
	return *v.EffectiveLimit, nil
}
func (r *RuntimeEntitlements) Resolve(ctx context.Context, id string, key feature.Key) (entitlement.Resolution, error) {
	if r == nil || r.store == nil {
		return entitlement.Resolution{}, billing.ErrInvalidConfiguration
	}
	records, err := r.Load(ctx, id)
	if err != nil {
		return entitlement.Resolution{}, err
	}
	return entitlement.ResolveRecords(ctx, id, key, records, r.store.features, r.store.now().UTC().Truncate(time.Microsecond))
}

// Load freezes all sources in one repeatable-read transaction. Historical
// SUBSCRIPTION rows are not replayed as grants: only current synchronized state
// can produce subscription entitlements. Manual/license/restriction sources stay.
func (r *RuntimeEntitlements) Load(ctx context.Context, id string) ([]domain.Entitlement, error) {
	if r == nil || r.store == nil {
		return nil, billing.ErrInvalidConfiguration
	}
	s := r.store
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if !org.ValidID(id) {
		return nil, billing.ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM organizations WHERE organization_id=$1`, id).Scan(&state); err != nil {
		return nil, err
	}
	if state != "ACTIVE" {
		return nil, billing.ErrOwnership
	}
	fallback, err := s.bundle(ctx, tx, r.fallback)
	if err != nil {
		return nil, err
	}
	if fallback.Version.EditionKey != edition.Community {
		return nil, billing.ErrInvalidConfiguration
	}
	records := []domain.Entitlement{}
	for _, m := range fallback.Features {
		e, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: billing.StableID("default", r.fallback, m.FeatureID), OrganizationID: id, FeatureID: m.FeatureID, Source: domain.SourceCommunityDefault, SourceReference: r.fallback, Enabled: m.Enabled, LimitValue: m.LimitValue, StartsAt: fallback.Version.CreatedAt, CreatedAt: fallback.Version.CreatedAt}, s.features)
		if err != nil {
			return nil, err
		}
		records = append(records, e)
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.entitlement_id,e.canonical_record,e.feature_id,e.source,e.source_reference FROM entitlements e WHERE e.organization_id=$1 AND e.source<>'SUBSCRIPTION' ORDER BY e.entitlement_id COLLATE "C" LIMIT 4097`, id)
	if err != nil {
		return nil, err
	}
	bytes := 0
	for rows.Next() {
		var eid, raw, fid, source, ref string
		if err = rows.Scan(&eid, &raw, &fid, &source, &ref); err != nil {
			rows.Close()
			return nil, err
		}
		bytes += len(raw)
		if len(records) >= entitlement.MaxResolutionRecords || bytes > entitlement.MaxResolutionMetadataBytes {
			rows.Close()
			return nil, entitlement.ErrResolutionBound
		}
		e, err := organizationstore.DecodeEntitlement(s.features, raw, id, eid, fid, source, ref)
		if err != nil || e.Source == domain.SourceSubscription {
			rows.Close()
			return nil, billing.ErrInvalidData
		}
		records = append(records, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// A joined state revision mismatch is corruption, not a stale-success fallback.
	rows, err = tx.QueryContext(ctx, `SELECT s.organization_id,s.subscription_id,s.edition_version_id,s.provider,s.external_reference,s.state,s.revision,s.created_at,s.updated_at,b.revision,b.access_until,b.payment_settled
 FROM subscriptions s JOIN billing_subscription_state b ON b.organization_id=s.organization_id AND b.subscription_id=s.subscription_id
 WHERE s.organization_id=$1 AND s.state IN ('ACTIVE','TRIALING') LIMIT 2`, id)
	if err != nil {
		return nil, err
	}
	var sub org.Subscription
	var revision int64
	var until sql.NullTime
	var paid bool
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			rows.Close()
			return nil, billing.ErrInvalidData
		}
		err = rows.Scan(&sub.OrganizationID, &sub.SubscriptionID, &sub.EditionVersionID, &sub.Provider, &sub.ExternalReference, &sub.State, &sub.Revision, &sub.CreatedAt, &sub.UpdatedAt, &revision, &until, &paid)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if sub.Validate() != nil || sub.OrganizationID != id || revision != sub.Revision || !until.Valid {
			rows.Close()
			return nil, billing.ErrInvalidData
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if count == 1 {
		bundle, err := s.bundle(ctx, tx, sub.EditionVersionID)
		if err != nil {
			return nil, err
		}
		derived, err := billing.DeriveSubscriptionEntitlements(sub, bundle, s.features, paid, until.Time, s.now().UTC().Truncate(time.Microsecond))
		if err != nil {
			return nil, err
		}
		records = append(records, derived...)
	}
	if len(records) > entitlement.MaxResolutionRecords {
		return nil, entitlement.ErrResolutionBound
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return records, nil
}
