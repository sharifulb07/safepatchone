package migrationtest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
)

// Read the exact JSON executed by the migration, avoiding a second seed source
// that could disagree with production data. This is test tooling, not a loader.
func initialBundles(t *testing.T) []edition.Bundle {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root(t), "migrations", "000002_initial_edition_bundles.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "$bundles$")
	if len(parts) != 3 {
		t.Fatal("expected one canonical bundle document")
	}
	var bundles []edition.Bundle
	decoder := json.NewDecoder(strings.NewReader(parts[1]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundles); err != nil {
		t.Fatal(err)
	}
	return bundles
}

func initialCatalog(t *testing.T) (*edition.Catalog, *feature.FeatureCatalog) {
	t.Helper()
	fc, err := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	c, err := edition.NewCatalog(fc)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range initialBundles(t) {
		c, err = c.Publish(bundle.Version, bundle.Features)
		if err != nil {
			t.Fatal(err)
		}
	}
	return c, fc
}

func TestInitialBundlesMatchRequestedBoundaries(t *testing.T) {
	// Independent acceptance contract: these are the user's incremental groups.
	groups := []struct {
		key   edition.Key
		added []feature.Key
		count int
	}{
		{edition.Community, []feature.Key{"verify.basic", "verify.security", "intent.basic", "graph.local"}, 4},
		{edition.Developer, []feature.Key{"intent.advanced", "proof.planner", "proof.cloud_history", "integration.github.private", "language.additional_packs"}, 9},
		{edition.Team, []feature.Key{"platform.organizations", "policy.central", "graph.organization", "proof.passport", "proof.shared", "audit.organization", "verify.pr_enforcement"}, 16},
		{edition.Business, []feature.Key{"graph.cross_repo", "testtrust", "runtime.replay", "runtime.behaviordna", "business.invariants", "incident.antibodies", "verify.composition"}, 23},
		{edition.Enterprise, []feature.Key{"agent.mcp_gateway", "agent.safelease", "agent.temporalguard", "proof.living", "enterprise.sso", "enterprise.audit", "enterprise.self_hosted", "enterprise.airgap", "enterprise.lts"}, 32},
	}
	c, fc := initialCatalog(t)
	if len(initialBundles(t)) != 5 {
		t.Fatal("expected exactly five bundles")
	}
	expected := map[feature.Key]bool{}
	for _, group := range groups {
		for _, key := range group.added {
			if expected[key] {
				t.Fatalf("duplicate acceptance key %s", key)
			}
			expected[key] = true
		}
		versionID := string(group.key) + "-2026-v1"
		bundle, err := c.GetVersion(versionID)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Version.EditionKey != group.key || len(bundle.Features) != group.count {
			t.Fatalf("incorrect bundle %s", versionID)
		}
		if !bundle.Version.CreatedAt.Equal(time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)) {
			t.Fatal("seed time must be deterministic")
		}
		for _, definition := range fc.List() {
			mapping, err := c.GetFeature(versionID, definition.FeatureID)
			if expected[definition.Key] {
				if err != nil || !mapping.Enabled {
					t.Errorf("%s missing requested %s: %v", versionID, definition.Key, err)
				}
				if mapping.LimitValue != nil || string(mapping.ConfigurationJSON) != "{}" {
					t.Errorf("invented quota or policy for %s/%s", versionID, definition.Key)
				}
			} else if !errors.Is(err, edition.ErrMappingNotFound) {
				t.Errorf("%s unexpectedly maps %s", versionID, definition.Key)
			}
			if definition.Status != feature.StatusPlanned {
				t.Errorf("seed marked %s implemented", definition.Key)
			}
		}
	}
}

func TestSeedHistorySurvivesNewBundlesAndReturnedValueChanges(t *testing.T) {
	c, _ := initialCatalog(t)
	for _, original := range initialBundles(t) {
		pinned := original.Version.EditionVersionID
		before, _ := c.GetVersion(pinned)
		snapshot, _ := json.Marshal(before)
		before.Features[0].Enabled = false
		before.Features[0].ConfigurationJSON[0] = '!'
		v := original.Version
		v.EditionVersionID = string(v.EditionKey) + "-2027-v2"
		next, err := c.Publish(v, nil)
		if err != nil {
			t.Fatal(err)
		}
		after, _ := next.GetVersion(pinned)
		actual, _ := json.Marshal(after)
		if string(actual) != string(snapshot) {
			t.Fatalf("seed history changed: %s", pinned)
		}
		if replacement, err := next.Publish(original.Version, nil); replacement != nil || !errors.Is(err, edition.ErrVersionExists) {
			t.Fatal("seed overwrite allowed")
		}
		if _, err := c.GetVersion(v.EditionVersionID); !errors.Is(err, edition.ErrVersionNotFound) {
			t.Fatal("old history mutated")
		}
	}
}

func TestMalformedSeedMappingsCannotPublish(t *testing.T) {
	for _, mutation := range []string{"unknown feature", "duplicate feature", "wrong version", "negative limit", "invalid configuration"} {
		t.Run(mutation, func(t *testing.T) {
			b := initialBundles(t)[0]
			b.Features = slices.Clone(b.Features)
			switch mutation {
			case "unknown feature":
				b.Features[0].FeatureID = "unregistered"
			case "duplicate feature":
				b.Features = append(b.Features, b.Features[0])
			case "wrong version":
				b.Features[0].EditionVersionID = "other-2026-v1"
			case "negative limit":
				n := int64(-1)
				b.Features[0].LimitValue = &n
			case "invalid configuration":
				b.Features[0].ConfigurationJSON = json.RawMessage(`{"x":1,"x":2}`)
			}
			fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
			c, _ := edition.NewCatalog(fc)
			if next, err := c.Publish(b.Version, b.Features); next != nil || !errors.Is(err, edition.ErrInvalidEdition) {
				t.Fatalf("malformed seed accepted: %v", err)
			}
		})
	}
}
