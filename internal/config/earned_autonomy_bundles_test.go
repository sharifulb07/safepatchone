package config

import (
	"bytes"
	"encoding/json"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"testing"
	"time"
)

func TestEarnedAutonomyAllocationAndConfigurationOnlyReassignment(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Now().UTC())
	base, err := SafeUpgradeEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(base)
	matrix, err := EarnedAutonomyEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	if len(matrix.Editions) != 5 {
		t.Fatal("edition count")
	}
	for _, e := range matrix.Editions {
		found := false
		for _, f := range e.Bundle.Features {
			if f.FeatureID == "feat_0045" {
				found = f.Enabled && f.LimitValue != nil && *f.LimitValue == 32
			}
		}
		if found != (e.Bundle.Version.EditionKey == edition.Enterprise) {
			t.Fatal("wrong initial allocation")
		}
	}
	var updates editionUpdates
	_ = json.Unmarshal(earnedAutonomyBundles, &updates)
	u := &updates.Updates[0]
	u.BaseVersion = "business-2026-v4"
	u.Version.EditionKey = edition.Business
	u.Version.EditionVersionID = "business-2026-v5"
	for i := range u.Features {
		u.Features[i].EditionVersionID = u.Version.EditionVersionID
	}
	limited := int64(2)
	u.Features[0].LimitValue = &limited
	raw, _ := json.Marshal(updates)
	custom, err := applyEditionUpdatesTo(fc, raw, base)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range custom.Editions {
		for _, f := range e.Bundle.Features {
			if e.Bundle.Version.EditionKey == edition.Business && f.FeatureID == "feat_0045" {
				found = f.Enabled && *f.LimitValue == 2
			}
		}
	}
	if !found {
		t.Fatal("configuration-only reassignment failed")
	}
	after, _ := json.Marshal(base)
	if !bytes.Equal(before, after) {
		t.Fatal("historical bundles changed")
	}
}
