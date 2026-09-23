package migrationtest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

// Test fixture for an atomic input; this is not a production persistence adapter.
type seededSnapshotSource struct {
	state   domain.SnapshotState
	records []domain.Entitlement
}

func (s *seededSnapshotSource) CurrentState(ctx context.Context, org string) (domain.SnapshotState, error) {
	state := s.state
	state.OrganizationID = org
	return state, ctx.Err()
}
func (s *seededSnapshotSource) Load(ctx context.Context, org string) (entitlement.SnapshotInput, error) {
	state, err := s.CurrentState(ctx, org)
	input := entitlement.SnapshotInput{State: state}
	for _, record := range s.records {
		if record.OrganizationID == org {
			input.Entitlements = append(input.Entitlements, record.Clone())
		}
	}
	return input, err
}

func TestSeedBundlesThroughSnapshotAndPermissionBoundary(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fc, err := feature.NewBuiltinCatalog("1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range initialBundles(t) {
		t.Run(string(bundle.Version.EditionKey), func(t *testing.T) {
			source := &seededSnapshotSource{state: domain.SnapshotState{OrganizationID: "org_customer", Edition: bundle.Version.EditionKey, EditionVersion: bundle.Version.EditionVersionID, SourceVersions: domain.SourceVersions{Subscription: 1, Trial: 1, License: 1, ManualOverride: 1, EditionConfiguration: 1, CommunityDefault: 1, Promotion: 1}}}
			expected := map[string]bool{}
			for _, mapping := range bundle.Features {
				record, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: "ent_" + mapping.FeatureID, OrganizationID: "org_customer", FeatureID: mapping.FeatureID, Source: domain.SourceSubscription, SourceReference: bundle.Version.EditionVersionID, Enabled: mapping.Enabled, LimitValue: mapping.LimitValue, StartsAt: bundle.Version.CreatedAt, CreatedAt: bundle.Version.CreatedAt}, fc)
				if err != nil {
					t.Fatal(err)
				}
				source.records = append(source.records, record)
				expected[mapping.FeatureID] = mapping.Enabled
			}
			service, err := entitlement.NewSnapshotService(source, fc, func() time.Time { return now }, 30*time.Second, 2)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(source.records)
			old, err := service.Snapshot(context.Background(), "org_customer")
			if err != nil {
				t.Fatal(err)
			}
			for _, definition := range fc.List() {
				ok, err := service.Has(context.Background(), "org_customer", definition.Key)
				if err != nil || ok != expected[definition.FeatureID] {
					t.Fatal("incorrect edition boundary", definition.Key, ok, err)
				}
				if other, err := service.Has(context.Background(), "other_org", definition.Key); err != nil || other {
					t.Fatal("tenant boundary crossed", err)
				}
				permissions := &testPermissionService{}
				allowed, err := protectedExample(context.Background(), service, permissions, definition.Key)
				if err != nil || allowed || (ok && permissions.calls != 1) {
					t.Fatal("permission bypassed", err)
				}
				permissions.allowed = true
				allowed, err = protectedExample(context.Background(), service, permissions, definition.Key)
				if err != nil || allowed != ok {
					t.Fatal("permission substituted for entitlement", err)
				}
			}
			historical := source.records
			source.records = nil
			source.state.SourceVersions.Subscription++
			fresh, err := service.Snapshot(context.Background(), "org_customer")
			if err != nil || fresh.Features[feature.KeyVerifyBasic] || !old.Features[feature.KeyVerifyBasic] {
				t.Fatal("plan change retained grant or corrupted old snapshot", err)
			}
			after, _ := json.Marshal(historical)
			if string(before) != string(after) {
				t.Fatal("historical records overwritten")
			}
		})
	}
}
