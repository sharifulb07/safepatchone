package config

import (
	"bytes"
	"encoding/json"
	"safepatch/internal/domain/edition"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"testing"
	"time"
)

func TestSafeUpgradeConfigurationLimitsAndImmutableHistory(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	base, err := GlobalPatchEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(base)
	matrix, err := SafeUpgradeEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[edition.Key]int64{edition.Developer: 10, edition.Team: 100, edition.Business: 1000, edition.Enterprise: 0}
	for i, e := range matrix.Editions {
		found := false
		for _, l := range e.Limits {
			if l.Key == limits.LimitSafeUpgradesMonthly {
				found = true
				state := limits.Integer
				if e.Bundle.Version.EditionKey == edition.Enterprise {
					state = limits.Unlimited
				}
				if l.Value != expected[e.Bundle.Version.EditionKey] || l.State != state {
					t.Fatal("wrong upgrade policy", l)
				}
			}
		}
		if found != (e.Bundle.Version.EditionKey != edition.Community) {
			t.Fatal("incorrect allocation")
		}
		for j, l := range base.Editions[i].Limits {
			if e.Limits[j] != l {
				t.Fatal("inherited limit changed")
			}
		}
	}
	// Change only data to assign a custom Enterprise cap. Domain/service untouched.
	var updates editionUpdates
	_ = json.Unmarshal(safeUpgradeBundles, &updates)
	updates.Updates[3].Limits[0].State = limits.Integer
	updates.Updates[3].Limits[0].Value = 27
	raw, _ := json.Marshal(updates)
	custom, err := applyEditionUpdatesTo(fc, raw, base)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, e := range custom.Editions {
		for _, l := range e.Limits {
			if e.Bundle.Version.EditionKey == edition.Enterprise && l.Key == limits.LimitSafeUpgradesMonthly {
				matched = l.Value == 27 && l.State == limits.Integer
			}
		}
	}
	if !matched {
		t.Fatal("custom limit not applied")
	}
	after, _ := json.Marshal(base)
	if !bytes.Equal(before, after) {
		t.Fatal("historical bundle mutated")
	}
	for _, bad := range []limits.EntitlementLimit{{Key: limits.LimitSafeUpgradesMonthly, State: limits.Integer, Value: -1}, {Key: "unknown", State: limits.Unlimited}} {
		updates.Updates[0].Limits = []limits.EntitlementLimit{bad}
		raw, _ := json.Marshal(updates)
		if _, err := applyEditionUpdatesTo(fc, raw, base); err == nil {
			t.Fatal("invalid limit")
		}
	}
	updates.Updates[0].Limits = []limits.EntitlementLimit{{Key: limits.LimitSafeUpgradesMonthly, State: limits.Unlimited}, {Key: limits.LimitSafeUpgradesMonthly, State: limits.Disabled}}
	raw, _ = json.Marshal(updates)
	if _, err := applyEditionUpdatesTo(fc, raw, base); err == nil {
		t.Fatal("duplicate limit")
	}
}
