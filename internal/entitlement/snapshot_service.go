package entitlement

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
)

var (
	ErrInvalidSnapshotService = errors.New("invalid entitlement snapshot service configuration")
	ErrSnapshotChanged        = errors.New("entitlement sources changed during snapshot evaluation")
)

const MaxCachedOrganizations = 1024

// SnapshotService implements the same ownership contract as EntitlementResolver.
// It never serves stale-on-error results, including for low-value operations.
// Cache entries are private, bounded, and scoped to this immutable catalog.
type SnapshotService struct {
	source     SnapshotSource
	catalog    *feature.FeatureCatalog
	now        func() time.Time
	ttl        time.Duration
	capacity   int
	mu         sync.Mutex
	cache      map[string]*snapshotEntry
	generation *cacheGeneration
}

type cacheGeneration struct{ marker byte }
type snapshotEntry struct {
	snapshot    domain.EffectiveEntitlementSnapshot
	resolutions map[feature.Key]Resolution
}

var _ EntitlementService = (*SnapshotService)(nil)

func NewSnapshotService(source SnapshotSource, catalog *feature.FeatureCatalog, now func() time.Time, ttl time.Duration, capacity int) (*SnapshotService, error) {
	if source == nil || catalog == nil || catalog.Len() == 0 || now == nil || ttl <= 0 || ttl > domain.MaxSnapshotTTL || capacity < 1 || capacity > MaxCachedOrganizations {
		return nil, ErrInvalidSnapshotService
	}
	return &SnapshotService{source: source, catalog: catalog, now: now, ttl: ttl, capacity: capacity, cache: make(map[string]*snapshotEntry), generation: &cacheGeneration{}}, nil
}

func (s *SnapshotService) configured() bool {
	return s != nil && s.source != nil && s.catalog != nil && s.now != nil && s.cache != nil
}

func (s *SnapshotService) Snapshot(ctx context.Context, org string) (domain.EffectiveEntitlementSnapshot, error) {
	entry, err := s.get(ctx, org)
	if err != nil {
		return domain.EffectiveEntitlementSnapshot{}, err
	}
	return entry.snapshot.Clone(), nil
}

func (s *SnapshotService) Resolve(ctx context.Context, org string, key feature.Key) (Resolution, error) {
	if !s.configured() {
		return failed("not_configured"), ErrInvalidSnapshotService
	}
	if err := feature.ValidateKey(key); err != nil {
		return failed("invalid_feature_key"), err
	}
	if _, err := s.catalog.Get(key); err != nil {
		return failed("unknown_feature"), ErrUnknownFeature
	}
	entry, err := s.get(ctx, org)
	if err != nil {
		return failed("snapshot_unavailable"), err
	}
	result := entry.resolutions[key]
	if result.EffectiveLimit != nil {
		n := *result.EffectiveLimit
		result.EffectiveLimit = &n
	}
	if result.Expiration != nil {
		at := *result.Expiration
		result.Expiration = &at
	}
	return result, nil
}

func (s *SnapshotService) Has(ctx context.Context, org string, key feature.Key) (bool, error) {
	result, err := s.Resolve(ctx, org, key)
	return err == nil && result.Enabled, err
}

func (s *SnapshotService) Limit(ctx context.Context, org string, key feature.Key) (int64, error) {
	result, err := s.Resolve(ctx, org, key)
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

// Invalidate evicts local derived data and fences in-flight builds/reads. Call
// AFTER a committed source change; the durable revision remains authoritative
// across processes and missed/duplicate/out-of-order event delivery. This method
// does not mutate grants or make a future source writer revision-safe by itself.
func (s *SnapshotService) Invalidate(ctx context.Context, org string, change SnapshotChange) error {
	if !s.configured() {
		return ErrInvalidSnapshotService
	}
	if ctx == nil || !domain.ValidID(org) || !validSnapshotChange(change) {
		return ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, org)
	s.generation = &cacheGeneration{}
	return nil
}

func (s *SnapshotService) get(ctx context.Context, org string) (*snapshotEntry, error) {
	if !s.configured() {
		return nil, ErrInvalidSnapshotService
	}
	if ctx == nil || !domain.ValidID(org) {
		return nil, ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	state, err := s.current(ctx, org)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	cached := s.cache[org]
	s.mu.Unlock()
	if cached != nil && s.now().Before(cached.snapshot.GeneratedAt) {
		return nil, domain.ErrExpiredSnapshot // Do not rebuild into a known clock rollback.
	}
	if cached != nil && cached.snapshot.State() == state {
		err := cached.snapshot.ValidateAt(org, s.now())
		if err == nil {
			return s.finish(ctx, org, cached, generation, false)
		}
		if !errors.Is(err, domain.ErrExpiredSnapshot) {
			return nil, err
		}
	}
	generated := s.now().UTC()
	if generated.IsZero() || generated.Year() < 1 || generated.Year() > 9999 {
		return nil, domain.ErrInvalidSnapshot
	}
	input, err := s.source.Load(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("load entitlement snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.State != state {
		return nil, ErrSnapshotChanged
	}
	entry, err := s.build(ctx, input, generated)
	if err != nil {
		return nil, err
	}
	latest, err := s.current(ctx, org)
	if err != nil {
		return nil, err
	}
	if latest != state {
		return nil, ErrSnapshotChanged
	}
	return s.finish(ctx, org, entry, generation, true)
}

func (s *SnapshotService) current(ctx context.Context, org string) (domain.SnapshotState, error) {
	state, err := s.source.CurrentState(ctx, org)
	if err != nil {
		return domain.SnapshotState{}, fmt.Errorf("read entitlement source versions: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return domain.SnapshotState{}, err
	}
	if state.OrganizationID != org || state.Validate() != nil {
		return domain.SnapshotState{}, ErrInvalidRepositoryData
	}
	return state, nil
}

func (s *SnapshotService) finish(ctx context.Context, org string, entry *snapshotEntry, generation *cacheGeneration, store bool) (*snapshotEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if generation != s.generation {
		return nil, ErrSnapshotChanged
	}
	if err := entry.snapshot.ValidateAt(org, s.now()); err != nil {
		return nil, err
	}
	if store {
		if _, exists := s.cache[org]; !exists && len(s.cache) >= s.capacity {
			// Evict the earliest deadline, with a stable tenant-ID tie-break.
			victim := ""
			for id, other := range s.cache {
				if victim == "" || other.snapshot.ValidUntil.Before(s.cache[victim].snapshot.ValidUntil) || (other.snapshot.ValidUntil.Equal(s.cache[victim].snapshot.ValidUntil) && id < victim) {
					victim = id
				}
			}
			delete(s.cache, victim)
		}
		s.cache[org] = entry
	}
	return entry, nil
}

func (s *SnapshotService) build(ctx context.Context, input SnapshotInput, generated time.Time) (*snapshotEntry, error) {
	if len(input.Entitlements) > MaxResolutionRecords {
		return nil, ErrResolutionBound
	}
	frozen := frozenSnapshotRepository{org: input.State.OrganizationID, records: make(map[string][]domain.Entitlement)}
	validUntil := generated.Add(s.ttl)
	seen, metadataBytes := make(map[string]bool), 0
	for _, record := range input.Entitlements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if record.OrganizationID != frozen.org || seen[record.EntitlementID] {
			return nil, ErrInvalidRepositoryData
		}
		metadataBytes += len(record.Metadata)
		if metadataBytes > MaxResolutionMetadataBytes {
			return nil, ErrResolutionBound
		}
		if record.Validate() != nil {
			return nil, ErrInvalidRepositoryData
		}
		if _, err := s.catalog.GetByID(record.FeatureID); err != nil {
			return nil, ErrInvalidRepositoryData
		}
		seen[record.EntitlementID] = true
		frozen.records[record.FeatureID] = append(frozen.records[record.FeatureID], record.Clone())
		// Include ALL scheduled transitions, not just the winning grant's expiry:
		// a future manual restriction must invalidate a currently paid allow.
		if record.StartsAt.After(generated) && record.StartsAt.Before(validUntil) {
			validUntil = record.StartsAt.UTC()
		}
		if record.ExpiresAt != nil && record.ExpiresAt.After(generated) && record.ExpiresAt.Before(validUntil) {
			validUntil = record.ExpiresAt.UTC()
		}
	}
	for _, records := range frozen.records {
		slices.SortFunc(records, func(a, b domain.Entitlement) int { return strings.Compare(a.EntitlementID, b.EntitlementID) })
	}
	resolver, err := NewEntitlementResolver(frozen, s.catalog, s.now)
	if err != nil {
		return nil, err
	}
	state := input.State
	snapshot := domain.EffectiveEntitlementSnapshot{OrganizationID: state.OrganizationID, Edition: state.Edition, EditionVersion: state.EditionVersion, SourceVersions: state.SourceVersions, GeneratedAt: generated, ValidUntil: validUntil, Features: make(map[feature.Key]bool), Limits: make(map[feature.Key]*int64)}
	entry := &snapshotEntry{resolutions: make(map[feature.Key]Resolution)}
	for _, definition := range s.catalog.List() {
		result, err := resolver.ResolveAt(ctx, frozen.org, definition.Key, generated)
		if err != nil {
			return nil, err
		}
		entry.resolutions[definition.Key] = result
		snapshot.Features[definition.Key], snapshot.Limits[definition.Key] = result.Enabled, result.EffectiveLimit
	}
	snapshot.ContentHash, err = snapshot.ComputeHash()
	if err != nil {
		return nil, err
	}
	entry.snapshot = snapshot
	return entry, nil
}

// The resolver's existing precedence is reused over one frozen input. Database
// reads happen only in SnapshotSource, never once per feature or page here.
type frozenSnapshotRepository struct {
	org     string
	records map[string][]domain.Entitlement
}

func (r frozenSnapshotRepository) Create(context.Context, string, domain.Entitlement) error {
	return ErrInvalidQuery
}
func (r frozenSnapshotRepository) Get(context.Context, string, string) (domain.Entitlement, error) {
	return domain.Entitlement{}, ErrInvalidQuery
}
func (r frozenSnapshotRepository) List(ctx context.Context, org string, q ListQuery) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if org != r.org || q.FeatureID == "" || ValidateList(org, q) != nil {
		return Page{}, ErrInvalidQuery
	}
	records := r.records[q.FeatureID]
	start := 0
	for start < len(records) && records[start].EntitlementID <= q.AfterID {
		start++
	}
	end := min(start+q.Limit, len(records))
	page := Page{Entitlements: records[start:end]}
	if end < len(records) {
		page.NextAfterID = records[end-1].EntitlementID
	}
	return page, nil
}
