package migrationtest

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

// Test adapter only: integration covers seed -> canonical record -> resolver,
// not a database adapter or production authorization engine.
type seedGrantReader struct{ records []domain.Entitlement }

func (r *seedGrantReader) Create(context.Context, string, domain.Entitlement) error {
	panic("unexpected write")
}
func (r *seedGrantReader) Get(context.Context, string, string) (domain.Entitlement, error) {
	panic("unexpected unfiltered read")
}
func (r *seedGrantReader) List(ctx context.Context, org string, q entitlement.ListQuery) (entitlement.Page, error) {
	if err := ctx.Err(); err != nil {
		return entitlement.Page{}, err
	}
	if err := entitlement.ValidateList(org, q); err != nil {
		return entitlement.Page{}, err
	}
	var matches []domain.Entitlement
	for _, record := range r.records {
		if record.OrganizationID == org && record.FeatureID == q.FeatureID && record.EntitlementID > q.AfterID {
			matches = append(matches, record.Clone())
		}
	}
	slices.SortFunc(matches, func(a, b domain.Entitlement) int { return strings.Compare(a.EntitlementID, b.EntitlementID) })
	page := entitlement.Page{Entitlements: matches}
	if len(matches) > q.Limit {
		page.Entitlements = matches[:q.Limit]
		page.NextAfterID = matches[q.Limit-1].EntitlementID
	}
	return page, nil
}

// Caller-composition example only. The resolver provides ownership; the actor
// permission check must still succeed. No production permission service exists.
type testPermissionService struct {
	allowed bool
	calls   int
}

func (p *testPermissionService) Allowed() bool { p.calls++; return p.allowed }
func protectedExample(ctx context.Context, service entitlement.EntitlementService, permissions *testPermissionService, key feature.Key) (bool, error) {
	has, err := service.Has(ctx, "org_customer", key)
	if err != nil || !has {
		return false, err
	}
	return permissions.Allowed(), nil
}

func TestSeededEntitlementsAndSeparatePermissionBoundary(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fc, err := feature.NewBuiltinCatalog("1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range initialBundles(t) {
		t.Run(string(bundle.Version.EditionKey), func(t *testing.T) {
			reader := &seedGrantReader{}
			expected := map[string]bool{}
			for _, mapping := range bundle.Features {
				record, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: "ent_" + mapping.FeatureID, OrganizationID: "org_customer", FeatureID: mapping.FeatureID, Source: domain.SourceSubscription, SourceReference: bundle.Version.EditionVersionID, Enabled: mapping.Enabled, LimitValue: mapping.LimitValue, StartsAt: bundle.Version.CreatedAt, CreatedAt: bundle.Version.CreatedAt}, fc)
				if err != nil {
					t.Fatal(err)
				}
				reader.records = append(reader.records, record)
				expected[mapping.FeatureID] = mapping.Enabled
			}
			service, err := entitlement.NewEntitlementResolver(reader, fc, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(reader.records)
			for _, definition := range fc.List() {
				has, err := service.Has(context.Background(), "org_customer", definition.Key)
				if err != nil || has != expected[definition.FeatureID] {
					t.Fatalf("incorrect seed boundary %s: %v %v", definition.Key, has, err)
				}
				if other, err := service.Has(context.Background(), "other_org", definition.Key); err != nil || other {
					t.Fatal("grant crossed tenant boundary")
				}
				permissions := &testPermissionService{allowed: false}
				allowed, err := protectedExample(context.Background(), service, permissions, definition.Key)
				if err != nil || allowed {
					t.Fatal("ownership bypassed caller permission check")
				}
				if has && permissions.calls != 1 {
					t.Fatal("permission not consulted after entitlement passed")
				}
				permissions = &testPermissionService{allowed: true}
				allowed, err = protectedExample(context.Background(), service, permissions, definition.Key)
				if err != nil || allowed != has {
					t.Fatal("caller permission substituted for feature ownership")
				}
			}
			after, _ := json.Marshal(reader.records)
			if string(before) != string(after) {
				t.Fatal("resolution mutated stored records")
			}
			// An authoritative repository snapshot can change after a plan change;
			// the resolver must reread, without mutating the old pinned snapshot.
			original := reader.records
			reader.records = nil
			if has, err := service.Has(context.Background(), "org_customer", feature.KeyVerifyBasic); err != nil || has {
				t.Fatal("stale grant retained after repository change")
			}
			old, _ := json.Marshal(original)
			if string(old) != string(before) {
				t.Fatal("plan change corrupted historical data")
			}
		})
	}
}
