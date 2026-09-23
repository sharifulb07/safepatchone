package migrationtest

import (
	"encoding/json"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
)

// Integrates existing seed identities and pinned version references with the
// canonical record. It does not implement assignment or entitlement resolution.
func TestEntitlementModelPreservesPinnedSeedReferences(t *testing.T) {
	fc, err := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range initialBundles(t) {
		catalog, err := edition.NewCatalog(fc)
		if err != nil {
			t.Fatal(err)
		}
		catalog, err = catalog.Publish(bundle.Version, bundle.Features)
		if err != nil {
			t.Fatal(err)
		}
		for _, mapping := range bundle.Features {
			input := domain.Entitlement{EntitlementID: "ent_" + mapping.FeatureID, OrganizationID: "org_1", FeatureID: mapping.FeatureID, Source: domain.SourceSubscription, SourceReference: bundle.Version.EditionVersionID, Enabled: mapping.Enabled, LimitValue: mapping.LimitValue, StartsAt: bundle.Version.CreatedAt, Metadata: json.RawMessage(`{}`), CreatedAt: bundle.Version.CreatedAt}
			e, err := domain.NewEntitlement(input, fc)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(e)
			v := bundle.Version
			v.EditionVersionID = string(v.EditionKey) + "-2027-v2"
			if _, err := catalog.Publish(v, nil); err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(e)
			if string(before) != string(after) || e.SourceReference != bundle.Version.EditionVersionID {
				t.Fatal("new bundle changed existing entitlement record")
			}
		}
	}
}
