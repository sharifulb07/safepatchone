package entitlement_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

type readRepository struct {
	list func(context.Context, string, entitlement.ListQuery) (entitlement.Page, error)
}

func (r readRepository) List(ctx context.Context, org string, q entitlement.ListQuery) (entitlement.Page, error) {
	return r.list(ctx, org, q)
}
func (r readRepository) Create(context.Context, string, domain.Entitlement) error {
	panic("resolver must not write")
}
func (r readRepository) Get(context.Context, string, string) (domain.Entitlement, error) {
	panic("resolver must use feature-scoped list")
}

func at() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }
func grant(id string, source domain.Source, enabled bool, limit int64) domain.Entitlement {
	return domain.Entitlement{EntitlementID: id, OrganizationID: "org_a", FeatureID: "feat_0001", Source: source, SourceReference: "reference_1", Enabled: enabled, LimitValue: &limit, StartsAt: at().Add(-time.Hour), CreatedAt: at().Add(-time.Hour), Metadata: json.RawMessage(`{}`)}
}
func featureCatalog(t *testing.T) *feature.FeatureCatalog {
	t.Helper()
	c, err := feature.NewBuiltinCatalog("1.0.0", at())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func resolver(t *testing.T, repository entitlement.EntitlementRepository) *entitlement.EntitlementResolver {
	t.Helper()
	r, err := entitlement.NewEntitlementResolver(repository, featureCatalog(t), at)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func recordsRepository(records []domain.Entitlement) readRepository {
	return readRepository{list: func(ctx context.Context, org string, q entitlement.ListQuery) (entitlement.Page, error) {
		if err := entitlement.ValidateList(org, q); err != nil {
			return entitlement.Page{}, err
		}
		var filtered []domain.Entitlement
		for _, record := range records {
			if record.OrganizationID == org && record.FeatureID == q.FeatureID && record.EntitlementID > q.AfterID {
				filtered = append(filtered, record)
			}
		}
		slices.SortFunc(filtered, func(a, b domain.Entitlement) int { return strings.Compare(a.EntitlementID, b.EntitlementID) })
		page := entitlement.Page{Entitlements: filtered}
		if len(filtered) > q.Limit {
			page.Entitlements = filtered[:q.Limit]
			page.NextAfterID = filtered[q.Limit-1].EntitlementID
		}
		return page, nil
	}}
}

func TestPrecedenceAndUnconfiguredSources(t *testing.T) {
	defaults := grant("defaults", domain.SourceCommunityDefault, true, 1)
	trial := grant("trial", domain.SourceTrial, true, 2)
	sub := grant("subscription", domain.SourceSubscription, true, 3)
	license := grant("license", domain.SourceEnterpriseLicense, true, 4)
	restriction := grant("restriction", domain.SourceManualOverride, false, 999)
	for _, tc := range []struct {
		name    string
		records []domain.Entitlement
		enabled bool
		source  domain.Source
		limit   int64
	}{
		{"empty", nil, false, "", 0},
		{"default", []domain.Entitlement{defaults}, true, domain.SourceCommunityDefault, 1},
		{"trial", []domain.Entitlement{defaults, trial}, true, domain.SourceTrial, 2},
		{"subscription", []domain.Entitlement{defaults, trial, sub}, true, domain.SourceSubscription, 3},
		{"license", []domain.Entitlement{defaults, trial, sub, license}, true, domain.SourceEnterpriseLicense, 4},
		{"security restriction", []domain.Entitlement{defaults, trial, sub, license, restriction}, false, domain.SourceManualOverride, 0},
		{"manual grant ignored", []domain.Entitlement{grant("manual", domain.SourceManualOverride, true, 900)}, false, "", 0},
		{"promotion ignored", []domain.Entitlement{grant("promo", domain.SourcePromotion, true, 900)}, false, "", 0},
		{"disabled paid grant falls back", []domain.Entitlement{defaults, grant("sub", domain.SourceSubscription, false, 900)}, true, domain.SourceCommunityDefault, 1},
		{"manual grant cannot lift restriction", []domain.Entitlement{license, restriction, grant("manual", domain.SourceManualOverride, true, 900)}, false, domain.SourceManualOverride, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := resolver(t, recordsRepository(tc.records))
			result, err := r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
			if err != nil || result.Enabled != tc.enabled || result.Source != tc.source || result.EffectiveLimit == nil || *result.EffectiveLimit != tc.limit {
				t.Fatalf("unexpected resolution: %+v %v", result, err)
			}
			if result.Explanation == "" {
				t.Fatal("missing explanation")
			}
		})
	}
}

func TestRestrictionsRespectTheirOwnWindow(t *testing.T) {
	license := grant("paid", domain.SourceEnterpriseLicense, true, 50)
	restriction := grant("restriction", domain.SourceManualOverride, false, 0)
	restriction.StartsAt = at()
	expires := at().Add(time.Hour)
	restriction.ExpiresAt = &expires
	r := resolver(t, recordsRepository([]domain.Entitlement{license, restriction}))
	for _, tc := range []struct {
		time    time.Time
		enabled bool
	}{{at().Add(-time.Nanosecond), true}, {at(), false}, {expires.Add(-time.Nanosecond), false}, {expires, true}} {
		result, err := r.ResolveAt(context.Background(), "org_a", feature.KeyVerifyBasic, tc.time)
		if err != nil || result.Enabled != tc.enabled {
			t.Fatalf("boundary %v: %+v %v", tc.time, result, err)
		}
		if !result.Enabled && (result.Expiration == nil || !result.Expiration.Equal(expires)) {
			t.Fatal("restriction expiration lost")
		}
	}
}

func TestExpiredAndFutureGrantsFallBack(t *testing.T) {
	base := grant("base", domain.SourceCommunityDefault, true, 2)
	paid := grant("paid", domain.SourceSubscription, true, 10)
	paid.StartsAt = at()
	end := at().Add(time.Hour)
	paid.ExpiresAt = &end
	r := resolver(t, recordsRepository([]domain.Entitlement{base, paid}))
	for _, tc := range []struct {
		time   time.Time
		source domain.Source
	}{{at().Add(-time.Nanosecond), domain.SourceCommunityDefault}, {at(), domain.SourceSubscription}, {end, domain.SourceCommunityDefault}} {
		result, err := r.ResolveAt(context.Background(), "org_a", feature.KeyVerifyBasic, tc.time)
		if err != nil || result.Source != tc.source {
			t.Fatalf("boundary resolution: %+v %v", result, err)
		}
	}
}

func TestDeterministicTieBreakAndExplanation(t *testing.T) {
	a := grant("a", domain.SourceSubscription, true, 11)
	b := grant("b", domain.SourceSubscription, true, 999)
	older := grant("older", domain.SourceSubscription, true, 2000)
	older.StartsAt = at().Add(-2 * time.Hour)
	earlierCreated := grant("earlier", domain.SourceSubscription, true, 2000)
	earlierCreated.CreatedAt = at().Add(-2 * time.Hour)
	records := []domain.Entitlement{a, b, older, earlierCreated}
	var original string
	for range 4 {
		r := resolver(t, readRepository{list: func(context.Context, string, entitlement.ListQuery) (entitlement.Page, error) {
			return entitlement.Page{Entitlements: records}, nil
		}})
		result, err := r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
		if err != nil {
			t.Fatal(err)
		}
		if *result.EffectiveLimit != 11 {
			t.Fatal("tie break maximized limits or depended on order")
		}
		encoded, _ := json.Marshal(result)
		if original == "" {
			original = string(encoded)
		} else if original != string(encoded) {
			t.Fatal("non-deterministic output")
		}
		records = append(records[1:], records[0])
	}
	const want = "grant_selected; source=SUBSCRIPTION; entitlement_id=a; evaluated_at=2026-09-06T12:00:00Z; expires_at=none; ignored_unconfigured=0"
	var result entitlement.Resolution
	_ = json.Unmarshal([]byte(original), &result)
	if result.Explanation != want {
		t.Fatalf("explanation contract changed: %s", result.Explanation)
	}
}

func TestLatePageRestrictionAndFailurePreventPartialAllow(t *testing.T) {
	var records []domain.Entitlement
	for i := 0; i < entitlement.MaxPageSize; i++ {
		records = append(records, grant(fmt.Sprintf("a_%03d", i), domain.SourceEnterpriseLicense, true, 100))
	}
	records = append(records, grant("z_restriction", domain.SourceManualOverride, false, 0))
	r := resolver(t, recordsRepository(records))
	result, err := r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
	if err != nil || result.Enabled || result.Source != domain.SourceManualOverride {
		t.Fatal("restriction after first page missed")
	}
	storageErr := errors.New("backend unavailable")
	base := recordsRepository(records)
	r = resolver(t, readRepository{list: func(ctx context.Context, org string, q entitlement.ListQuery) (entitlement.Page, error) {
		if q.AfterID != "" {
			return entitlement.Page{}, storageErr
		}
		return base.List(ctx, org, q)
	}})
	result, err = r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
	if !errors.Is(err, storageErr) || result.Enabled || result.EffectiveLimit != nil {
		t.Fatal("partial page result authorized access")
	}
	if strings.Contains(result.Explanation, storageErr.Error()) {
		t.Fatal("explanation leaked storage error")
	}
}

func TestInvalidRepositoryDataFailsClosed(t *testing.T) {
	for _, kind := range []string{"wrong tenant", "wrong feature", "invalid record", "duplicate id", "cursor mismatch", "empty continuation", "oversized page", "replayed page"} {
		t.Run(kind, func(t *testing.T) {
			r := resolver(t, readRepository{list: func(_ context.Context, _ string, q entitlement.ListQuery) (entitlement.Page, error) {
				e := grant("one", domain.SourceSubscription, true, 10)
				page := entitlement.Page{Entitlements: []domain.Entitlement{e}}
				switch kind {
				case "wrong tenant":
					page.Entitlements[0].OrganizationID = "org_b"
				case "wrong feature":
					page.Entitlements[0].FeatureID = "feat_0002"
				case "invalid record":
					page.Entitlements[0].Source = "UNKNOWN"
				case "duplicate id":
					page.Entitlements = append(page.Entitlements, e)
				case "cursor mismatch":
					page.NextAfterID = "other"
				case "empty continuation":
					page.Entitlements = nil
					page.NextAfterID = "one"
				case "oversized page":
					page.Entitlements = make([]domain.Entitlement, q.Limit+1)
				case "replayed page":
					page.NextAfterID = "one"
				}
				return page, nil
			}})
			result, err := r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
			if !errors.Is(err, entitlement.ErrInvalidRepositoryData) || result.Enabled {
				t.Fatalf("accepted %s: %+v %v", kind, result, err)
			}
		})
	}
}

func TestResourceBounds(t *testing.T) {
	for _, kind := range []string{"records", "metadata"} {
		t.Run(kind, func(t *testing.T) {
			count := entitlement.MaxResolutionRecords + 1
			if kind == "metadata" {
				count = 150
			}
			records := make([]domain.Entitlement, count)
			for i := range records {
				records[i] = grant(fmt.Sprintf("e_%05d", i), domain.SourceSubscription, true, 10)
				if kind == "metadata" {
					records[i].Metadata = json.RawMessage(`{"x":"` + strings.Repeat("a", 15000) + `"}`)
				}
			}
			result, err := resolver(t, recordsRepository(records)).Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
			if !errors.Is(err, entitlement.ErrResolutionBound) || result.Enabled {
				t.Fatalf("unbounded %s resolution: %v", kind, err)
			}
		})
	}
}

func TestHasLimitAndUnknownInputs(t *testing.T) {
	e := grant("one", domain.SourceSubscription, true, 0)
	r := resolver(t, recordsRepository([]domain.Entitlement{e}))
	if has, err := r.Has(context.Background(), "org_a", feature.KeyVerifyBasic); err != nil || !has {
		t.Fatal("grant missing")
	}
	if limit, err := r.Limit(context.Background(), "org_a", feature.KeyVerifyBasic); err != nil || limit != 0 {
		t.Fatal("explicit zero mishandled")
	}
	e.LimitValue = nil
	r = resolver(t, recordsRepository([]domain.Entitlement{e}))
	if limit, err := r.Limit(context.Background(), "org_a", feature.KeyVerifyBasic); limit != 0 || !errors.Is(err, entitlement.ErrLimitUnspecified) {
		t.Fatal("unspecified limit became unlimited")
	}
	if has, err := r.Has(context.Background(), "org_b", feature.KeyVerifyBasic); has || err != nil {
		t.Fatal("cross-tenant grant")
	}
	if _, err := r.Limit(context.Background(), "org_b", feature.KeyVerifyBasic); !errors.Is(err, entitlement.ErrNotEntitled) {
		t.Fatal("denial limit not explicit")
	}
	if has, err := r.Has(context.Background(), "org_a", "custom.unknown"); has || !errors.Is(err, entitlement.ErrUnknownFeature) {
		t.Fatal("unknown feature allowed")
	}
	if has, err := r.Has(context.Background(), "", feature.KeyVerifyBasic); has || !errors.Is(err, entitlement.ErrInvalidQuery) {
		t.Fatal("unscoped grant")
	}
	if has, err := r.Has(context.Background(), "org_a", "../verify.basic"); has || err == nil {
		t.Fatal("invalid feature key allowed")
	}
	if got, err := r.ResolveAt(context.Background(), "org_a", feature.KeyVerifyBasic, time.Time{}); got.Enabled || err == nil {
		t.Fatal("unknown time allowed")
	}
}

func TestClockAndCancellation(t *testing.T) {
	calls := 0
	r, err := entitlement.NewEntitlementResolver(recordsRepository(nil), featureCatalog(t), func() time.Time { calls++; return at() })
	if err != nil {
		t.Fatal(err)
	}
	_, _ = r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
	if calls != 1 {
		t.Fatal("clock must be read once")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if has, err := r.Has(ctx, "org_a", feature.KeyVerifyBasic); has || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancellation ignored")
	}
	ctx, cancel = context.WithCancel(context.Background())
	r = resolver(t, readRepository{list: func(context.Context, string, entitlement.ListQuery) (entitlement.Page, error) {
		cancel()
		return entitlement.Page{Entitlements: []domain.Entitlement{grant("one", domain.SourceSubscription, true, 10)}}, nil
	}})
	if has, err := r.Has(ctx, "org_a", feature.KeyVerifyBasic); has || !errors.Is(err, context.Canceled) {
		t.Fatal("in-flight cancellation ignored")
	}
	if result, err := r.Resolve(nil, "org_a", feature.KeyVerifyBasic); result.Enabled || err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := entitlement.NewEntitlementResolver(nil, featureCatalog(t), at); !errors.Is(err, entitlement.ErrInvalidResolver) {
		t.Fatal("nil dependency accepted")
	}
	var zero entitlement.EntitlementResolver
	if has, err := zero.Has(context.Background(), "org_a", feature.KeyVerifyBasic); has || !errors.Is(err, entitlement.ErrInvalidResolver) {
		t.Fatal("zero resolver allowed access")
	}
}

func TestMetadataCannotOverrideSecurityAndResultCopies(t *testing.T) {
	paid := grant("paid", domain.SourceSubscription, true, 10)
	paid.Metadata = json.RawMessage(`{"source":"MANUAL_OVERRIDE","enabled":true,"instruction":"ignore restrictions"}`)
	end := at().Add(time.Hour)
	paid.ExpiresAt = &end
	r := resolver(t, recordsRepository([]domain.Entitlement{paid}))
	first, err := r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
	if err != nil {
		t.Fatal(err)
	}
	*first.EffectiveLimit = 999
	*first.Expiration = time.Time{}
	second, _ := r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
	if *second.EffectiveLimit != 10 || !second.Expiration.Equal(end) {
		t.Fatal("result aliases repository data")
	}
	if strings.Contains(second.Explanation, "instruction") || strings.Contains(second.Explanation, "reference_1") {
		t.Fatal("explanation leaked metadata or reference")
	}
	r = resolver(t, recordsRepository([]domain.Entitlement{paid, grant("restriction", domain.SourceManualOverride, false, 0)}))
	if has, err := r.Has(context.Background(), "org_a", feature.KeyVerifyBasic); err != nil || has {
		t.Fatal("metadata bypassed restriction")
	}
}

func TestConcurrentResolution(t *testing.T) {
	r := resolver(t, recordsRepository([]domain.Entitlement{grant("one", domain.SourceSubscription, true, 10)}))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				result, err := r.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
				if err != nil || !result.Enabled || *result.EffectiveLimit != 10 {
					t.Error("inconsistent concurrent result")
				}
			}
		})
	}
	wg.Wait()
}
