package config

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	grants "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

func TestGlobalPatchBundleExtensionAndDataOnlyReassignment(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	fc, _ := feature.NewBuiltinCatalog("1.0.0", now)
	base, err := CompositionEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(base)
	current, err := GlobalPatchEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range current.Editions {
		a, _ := json.Marshal(e.Limits)
		b, _ := json.Marshal(base.Editions[i].Limits)
		if !bytes.Equal(a, b) {
			t.Fatal("quota policy changed")
		}
		if e.Bundle.Version.EditionKey != edition.Enterprise {
			a, _ = json.Marshal(e)
			b, _ = json.Marshal(base.Editions[i])
			if !bytes.Equal(a, b) {
				t.Fatal("unrelated edition changed")
			}
		}
	}
	var changes editionUpdates
	_ = json.Unmarshal(globalPatchBundles, &changes)
	delta := changes.Updates[0]
	delta.BaseVersion = "business-2026-v3"
	delta.Version.EditionKey = edition.Business
	delta.Version.EditionVersionID = "business-2026-v4"
	delta.Features = append([]edition.EditionFeature{}, delta.Features...)
	delta.Features[0].EditionVersionID = delta.Version.EditionVersionID
	changes.Updates = append(changes.Updates, delta)
	raw, _ := json.Marshal(changes)
	future, err := applyEditionUpdatesTo(fc, raw, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range future.Editions {
		repo := matrixRepository{}
		for _, mapping := range e.Bundle.Features {
			r, err := grants.NewEntitlement(grants.Entitlement{EntitlementID: "grant_" + mapping.FeatureID, OrganizationID: "org", FeatureID: mapping.FeatureID, Source: grants.SourceSubscription, SourceReference: e.Bundle.Version.EditionVersionID, Enabled: mapping.Enabled, StartsAt: now, CreatedAt: now}, fc)
			if err != nil {
				t.Fatal(err)
			}
			repo.records = append(repo.records, r)
		}
		resolver, _ := entitlement.NewEntitlementResolver(repo, fc, func() time.Time { return now })
		has, err := resolver.Has(t.Context(), "org", feature.KeyGraphGlobalChange)
		expected := e.Bundle.Version.EditionKey == edition.Business || e.Bundle.Version.EditionKey == edition.Enterprise
		if err != nil || has != expected {
			t.Fatal("configuration-only capability assignment failed", err)
		}
	}
	after, _ := json.Marshal(base)
	if !bytes.Equal(before, after) {
		t.Fatal("base profile mutated")
	}
	again, _ := CompositionEditions(fc)
	after, _ = json.Marshal(again)
	if !bytes.Equal(before, after) {
		t.Fatal("historical profile changed")
	}
}
