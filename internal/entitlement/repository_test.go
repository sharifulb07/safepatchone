package entitlement_test

import (
	"errors"
	"testing"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/entitlement"
)

func record() domain.Entitlement {
	return domain.Entitlement{EntitlementID: "ent_1", OrganizationID: "org_a", FeatureID: "feat_0001", Source: domain.SourceCommunityDefault, SourceReference: "community-2026-v1", Enabled: true, StartsAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)}
}

func TestCreateRequiresMatchingOrganizationScope(t *testing.T) {
	if err := entitlement.ValidateCreate("org_a", record()); err != nil {
		t.Fatal(err)
	}
	if err := entitlement.ValidateCreate("org_b", record()); !errors.Is(err, entitlement.ErrOrganizationMismatch) {
		t.Fatal("cross-organization creation accepted")
	}
	if err := entitlement.ValidateCreate("", record()); !errors.Is(err, entitlement.ErrInvalidQuery) {
		t.Fatal("unscoped creation accepted")
	}
	e := record()
	e.Source = "UNKNOWN"
	if err := entitlement.ValidateCreate("org_a", e); !errors.Is(err, domain.ErrInvalidEntitlement) {
		t.Fatal("invalid grant accepted")
	}
}

func TestGetRequiresBoundedOrganizationAndRecordIDs(t *testing.T) {
	for _, tc := range []struct{ org, id string }{{"", "ent_1"}, {"org_a", ""}, {"../org_b", "ent_1"}, {"org_a", "ent_1 OR 1=1"}} {
		if err := entitlement.ValidateGet(tc.org, tc.id); !errors.Is(err, entitlement.ErrInvalidQuery) {
			t.Fatalf("invalid get accepted: %+v", tc)
		}
	}
	if err := entitlement.ValidateGet("org_a", "ent_1"); err != nil {
		t.Fatal(err)
	}
}

func TestListRequiresExplicitScopeAndBoundedPagination(t *testing.T) {
	for _, query := range []entitlement.ListQuery{{Limit: 0}, {Limit: -1}, {Limit: 201}, {Limit: 1, FeatureID: "../feature"}, {Limit: 1, AfterID: "1;DROP TABLE entitlements"}} {
		if err := entitlement.ValidateList("org_a", query); !errors.Is(err, entitlement.ErrInvalidQuery) {
			t.Fatalf("invalid query accepted: %+v", query)
		}
	}
	if err := entitlement.ValidateList("", entitlement.ListQuery{Limit: 1}); !errors.Is(err, entitlement.ErrInvalidQuery) {
		t.Fatal("unscoped enumeration accepted")
	}
	for _, query := range []entitlement.ListQuery{{Limit: 1}, {Limit: entitlement.MaxPageSize, FeatureID: "feat_0001", AfterID: "ent_001"}} {
		if err := entitlement.ValidateList("org_a", query); err != nil {
			t.Fatal(err)
		}
	}
}
