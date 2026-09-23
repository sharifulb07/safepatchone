package entitlement

import (
	"context"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"slices"
	"time"
)

// ResolveRecords reuses the resolver over a complete, transactionally consistent
// local input. It retains no cache and cannot make a provider/network call.
func ResolveRecords(ctx context.Context, organizationID string, key feature.Key, records []domain.Entitlement, catalog *feature.FeatureCatalog, at time.Time) (Resolution, error) {
	if ctx == nil || !domain.ValidID(organizationID) || len(records) > MaxResolutionRecords {
		return Resolution{}, ErrInvalidRepositoryData
	}
	repo := frozenSnapshotRepository{org: organizationID, records: map[string][]domain.Entitlement{}}
	seen := map[string]bool{}
	total := 0
	for _, r := range records {
		if r.OrganizationID != organizationID || seen[r.EntitlementID] {
			return Resolution{}, ErrInvalidRepositoryData
		}
		seen[r.EntitlementID] = true
		valid, err := domain.NewEntitlement(r, catalog)
		if err != nil {
			return Resolution{}, err
		}
		total += len(valid.Metadata)
		if total > MaxResolutionMetadataBytes {
			return Resolution{}, ErrResolutionBound
		}
		repo.records[r.FeatureID] = append(repo.records[r.FeatureID], valid)
	}
	for _, group := range repo.records {
		slices.SortFunc(group, func(a, b domain.Entitlement) int {
			if a.EntitlementID < b.EntitlementID {
				return -1
			}
			if a.EntitlementID > b.EntitlementID {
				return 1
			}
			return 0
		})
	}
	resolver, err := NewEntitlementResolver(repo, catalog, func() time.Time { return at })
	if err != nil {
		return Resolution{}, err
	}
	return resolver.Resolve(ctx, organizationID, key)
}
