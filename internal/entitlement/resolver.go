package entitlement

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
)

var (
	ErrInvalidResolver       = errors.New("entitlement resolver is not configured")
	ErrUnknownFeature        = errors.New("unknown entitlement feature")
	ErrInvalidRepositoryData = errors.New("invalid entitlement repository data")
	ErrResolutionBound       = errors.New("entitlement resolution resource bound exceeded")
	ErrNotEntitled           = errors.New("organization is not entitled to the feature")
	ErrLimitUnspecified      = errors.New("entitlement limit is unspecified")
)

const (
	MaxResolutionRecords       = 4096
	MaxResolutionMetadataBytes = 2 * 1024 * 1024
)

// EntitlementService resolves ownership only. PermissionService must separately
// authorize the actor/action/resource; callers must also enforce quota and policy.
type EntitlementService interface {
	Has(context.Context, string, feature.Key) (bool, error)
	Limit(context.Context, string, feature.Key) (int64, error)
	Resolve(context.Context, string, feature.Key) (Resolution, error)
}

type Resolution struct {
	Enabled        bool          `json:"enabled"`
	EffectiveLimit *int64        `json:"effective_limit"`
	Source         domain.Source `json:"source"`
	Expiration     *time.Time    `json:"expiration"`
	Explanation    string        `json:"explanation"`
}

// EntitlementResolver reads canonical records without billing, edition, or actor
// dependencies. The injected clock is read once per Resolve/Has/Limit call.
// No grant cache is retained. Dependencies must support concurrent callers.
type EntitlementResolver struct {
	repository EntitlementRepository
	catalog    *feature.FeatureCatalog
	now        func() time.Time
}

var _ EntitlementService = (*EntitlementResolver)(nil)

func NewEntitlementResolver(repository EntitlementRepository, catalog *feature.FeatureCatalog, now func() time.Time) (*EntitlementResolver, error) {
	if repository == nil || catalog == nil || now == nil {
		return nil, ErrInvalidResolver
	}
	return &EntitlementResolver{repository: repository, catalog: catalog, now: now}, nil
}

func (r *EntitlementResolver) Has(ctx context.Context, organizationID string, key feature.Key) (bool, error) {
	result, err := r.Resolve(ctx, organizationID, key)
	return err == nil && result.Enabled, err
}

// Limit returns only an explicit finite limit. Denial, unspecified limits, and
// resolution failure return zero with an error; null never becomes unlimited.
func (r *EntitlementResolver) Limit(ctx context.Context, organizationID string, key feature.Key) (int64, error) {
	result, err := r.Resolve(ctx, organizationID, key)
	if err != nil {
		return 0, err
	}
	if !result.Enabled {
		return 0, ErrNotEntitled
	}
	if result.EffectiveLimit == nil {
		return 0, ErrLimitUnspecified
	}
	return *result.EffectiveLimit, nil
}

func (r *EntitlementResolver) Resolve(ctx context.Context, organizationID string, key feature.Key) (Resolution, error) {
	if r == nil || r.repository == nil || r.catalog == nil || r.now == nil {
		return failed("not_configured"), ErrInvalidResolver
	}
	if ctx == nil {
		return failed("invalid_request"), ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return failed("cancelled"), err
	}
	return r.ResolveAt(ctx, organizationID, key, r.now())
}

// ResolveAt exposes the explicit evaluation instant for deterministic callers.
// Expiration is the selected record's expiry, not a safe cache TTL: new records,
// future starts, or revocation can change the result before that instant.
func (r *EntitlementResolver) ResolveAt(ctx context.Context, organizationID string, key feature.Key, at time.Time) (Resolution, error) {
	if r == nil || r.repository == nil || r.catalog == nil {
		return failed("not_configured"), ErrInvalidResolver
	}
	if ctx == nil || !domain.ValidID(organizationID) || at.IsZero() || at.UTC().Year() < 1 || at.UTC().Year() > 9999 {
		return failed("invalid_request"), ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return failed("cancelled"), err
	}
	if err := feature.ValidateKey(key); err != nil {
		return failed("invalid_feature_key"), err
	}
	definition, err := r.catalog.Get(key)
	if err != nil {
		return failed("unknown_feature"), ErrUnknownFeature
	}
	at = at.UTC()
	query := ListQuery{FeatureID: definition.FeatureID, Limit: MaxPageSize}
	var winner *candidate
	count, metadataBytes, ignored := 0, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return failed("cancelled"), err
		}
		page, err := r.repository.List(ctx, organizationID, query)
		if err != nil {
			return failed("repository_error"), fmt.Errorf("read entitlement records: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return failed("cancelled"), err
		}
		if len(page.Entitlements) > query.Limit {
			return failed("invalid_repository_data"), ErrInvalidRepositoryData
		}
		count += len(page.Entitlements)
		if count > MaxResolutionRecords {
			return failed("resource_bound"), ErrResolutionBound
		}
		// Sort our own slice to make selection independent of page-local ordering
		// without modifying adapter-owned data. Cursor traversal must still progress.
		records := slices.Clone(page.Entitlements)
		slices.SortFunc(records, func(a, b domain.Entitlement) int { return strings.Compare(a.EntitlementID, b.EntitlementID) })
		last := query.AfterID
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return failed("cancelled"), err
			}
			if record.OrganizationID != organizationID || record.FeatureID != definition.FeatureID || record.EntitlementID <= last {
				return failed("invalid_repository_data"), ErrInvalidRepositoryData
			}
			metadataBytes += len(record.Metadata)
			if metadataBytes > MaxResolutionMetadataBytes {
				return failed("resource_bound"), ErrResolutionBound
			}
			if err := record.Validate(); err != nil {
				return failed("invalid_repository_data"), ErrInvalidRepositoryData
			}
			last = record.EntitlementID
			// ActiveAt includes Enabled and would incorrectly discard restrictions.
			if at.Before(record.StartsAt) || (record.ExpiresAt != nil && !at.Before(*record.ExpiresAt)) {
				continue
			}
			rank := sourceRank(record)
			if rank == 0 {
				if record.Source == domain.SourcePromotion || record.Source == domain.SourceManualOverride {
					ignored++
				}
				continue
			}
			current := newCandidate(record, rank)
			if winner == nil || current.before(*winner) {
				winner = &current
			}
		}
		if page.NextAfterID == "" {
			break
		}
		if len(records) == 0 || page.NextAfterID != last || page.NextAfterID <= query.AfterID {
			return failed("invalid_repository_data"), ErrInvalidRepositoryData
		}
		query.AfterID = page.NextAfterID
	}
	if err := ctx.Err(); err != nil {
		return failed("cancelled"), err
	}
	if winner == nil {
		zero := int64(0)
		return Resolution{EffectiveLimit: &zero, Explanation: fmt.Sprintf("no_active_entitlement; evaluated_at=%s; ignored_unconfigured=%d", at.Format(time.RFC3339Nano), ignored)}, nil
	}
	code := "grant_selected"
	if !winner.enabled {
		code = "security_restriction"
		zero := int64(0)
		winner.limit = &zero
	}
	expires := "none"
	if winner.expiration != nil {
		expires = winner.expiration.Format(time.RFC3339Nano)
	}
	return Resolution{Enabled: winner.enabled, EffectiveLimit: winner.limit, Source: winner.source, Expiration: winner.expiration,
		Explanation: fmt.Sprintf("%s; source=%s; entitlement_id=%s; evaluated_at=%s; expires_at=%s; ignored_unconfigured=%d", code, winner.source, winner.id, at.Format(time.RFC3339Nano), expires, ignored)}, nil
}

func failed(reason string) Resolution { return Resolution{Explanation: "resolution_failed: " + reason} }

func sourceRank(record domain.Entitlement) int {
	if record.Source == domain.SourceManualOverride && !record.Enabled {
		return 5
	}
	if !record.Enabled {
		return 0
	}
	switch record.Source {
	case domain.SourceEnterpriseLicense:
		return 4
	case domain.SourceSubscription:
		return 3
	case domain.SourceTrial:
		return 2
	case domain.SourceCommunityDefault:
		return 1
	default:
		return 0 // Promotions and positive manual overrides have no grant policy.
	}
}

type candidate struct {
	rank                int
	id                  string
	source              domain.Source
	enabled             bool
	limit               *int64
	expiration          *time.Time
	startsAt, createdAt time.Time
}

func newCandidate(record domain.Entitlement, rank int) candidate {
	c := candidate{rank: rank, id: record.EntitlementID, source: record.Source, enabled: record.Enabled, startsAt: record.StartsAt, createdAt: record.CreatedAt}
	if record.LimitValue != nil {
		n := *record.LimitValue
		c.limit = &n
	}
	if record.ExpiresAt != nil {
		at := record.ExpiresAt.UTC()
		c.expiration = &at
	}
	return c
}

// Within a tier: later start, then later creation, then lexical smaller ID.
// Limits are selected from one record, never summed or silently maximized.
func (c candidate) before(other candidate) bool {
	if c.rank != other.rank {
		return c.rank > other.rank
	}
	if !c.startsAt.Equal(other.startsAt) {
		return c.startsAt.After(other.startsAt)
	}
	if !c.createdAt.Equal(other.createdAt) {
		return c.createdAt.After(other.createdAt)
	}
	return c.id < other.id
}
