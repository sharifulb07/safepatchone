package edition_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
)

func registered() time.Time { return time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC) }
func catalog(t *testing.T) *edition.Catalog {
	t.Helper()
	features, err := feature.NewBuiltinCatalog("1.0.0", registered())
	if err != nil {
		t.Fatal(err)
	}
	c, err := edition.NewCatalog(features)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func version(id string, key edition.Key) edition.EditionVersion {
	return edition.EditionVersion{EditionVersionID: id, EditionKey: key, CreatedAt: registered()}
}
func mapping(id string, enabled bool, limit *int64, config string) edition.EditionFeature {
	return edition.EditionFeature{EditionVersionID: id, FeatureID: "feat_0001", Enabled: enabled, LimitValue: limit, ConfigurationJSON: json.RawMessage(config)}
}

func TestExactlyFivePermanentEditions(t *testing.T) {
	want := []edition.Edition{{edition.Community, "Community"}, {edition.Developer, "Developer"}, {edition.Team, "Team"}, {edition.Business, "Business"}, {edition.Enterprise, "Enterprise"}}
	got := edition.Editions()
	if len(got) != len(want) {
		t.Fatal("wrong edition count")
	}
	for i, expected := range want {
		if got[i] != expected {
			t.Fatalf("unexpected edition: %+v", got[i])
		}
		if actual, err := edition.Lookup(expected.Key); err != nil || actual != expected {
			t.Fatal("lookup mismatch")
		}
	}
	got[0].Name = "changed"
	if edition.Editions()[0].Name != "Community" {
		t.Fatal("mutable edition definitions")
	}
	for _, key := range []edition.Key{"", "Team", "premium", "team-2026-v1", "../enterprise"} {
		if _, err := edition.Lookup(key); !errors.Is(err, edition.ErrInvalidEdition) {
			t.Fatalf("accepted unknown edition %q", key)
		}
	}
}

func TestGrandfatheringAcrossAllEditions(t *testing.T) {
	for _, offering := range edition.Editions() {
		t.Run(string(offering.Key), func(t *testing.T) {
			oldID, newID := string(offering.Key)+"-2026-v1", string(offering.Key)+"-2027-v2"
			limit := int64(25)
			input := []edition.EditionFeature{mapping(oldID, true, &limit, `{"mode":"original"}`)}
			initial := catalog(t)
			old, err := initial.Publish(version(oldID, offering.Key), input)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := old.GetVersion(oldID)
			baseline, _ := json.Marshal(before)
			limit = 999
			input[0].ConfigurationJSON[2] = 'X'
			zero := int64(0)
			newer, err := old.Publish(version(newID, offering.Key), []edition.EditionFeature{mapping(newID, false, &zero, `{"mode":"revised"}`)})
			if err != nil {
				t.Fatal(err)
			}
			// A grandfathered customer's pinned ID still resolves byte-identically.
			after, _ := newer.GetVersion(oldID)
			actual, _ := json.Marshal(after)
			if string(actual) != string(baseline) {
				t.Fatal("historical bundle changed")
			}
			for _, history := range []*edition.Catalog{old, newer} {
				if replaced, err := history.Publish(version(oldID, offering.Key), nil); replaced != nil || !errors.Is(err, edition.ErrVersionExists) {
					t.Fatal("historical overwrite allowed")
				}
			}
			if _, err := old.GetVersion(newID); !errors.Is(err, edition.ErrVersionNotFound) {
				t.Fatal("publication mutated old snapshot")
			}
			if _, err := initial.GetVersion(oldID); !errors.Is(err, edition.ErrVersionNotFound) {
				t.Fatal("publication mutated empty snapshot")
			}
			newMapping, err := newer.GetFeature(newID, "feat_0001")
			if err != nil || newMapping.Enabled || newMapping.LimitValue == nil || *newMapping.LimitValue != 0 {
				t.Fatal("explicit disabled/zero mapping lost")
			}
			versions, _ := newer.Versions(offering.Key)
			if len(versions) != 2 || versions[0].EditionVersionID != oldID {
				t.Fatal("wrong version list")
			}
		})
	}
}

func TestCopiesAndConcurrentReads(t *testing.T) {
	limit := int64(5)
	c, err := catalog(t).Publish(version("team-2026-v1", edition.Team), []edition.EditionFeature{mapping("team-2026-v1", true, &limit, `{"nested":{"value":1}}`)})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				m, err := c.GetFeature("team-2026-v1", "feat_0001")
				if err != nil || *m.LimitValue != 5 || string(m.ConfigurationJSON) != `{"nested":{"value":1}}` {
					t.Error("corrupt snapshot")
					return
				}
				*m.LimitValue = 999
				m.ConfigurationJSON[0] = '!'
				b, _ := c.GetVersion("team-2026-v1")
				b.Features[0].Enabled = false
			}
		})
	}
	wg.Wait()
}

func TestInvalidBundlesFailAtomically(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*edition.EditionVersion, *[]edition.EditionFeature)
	}{
		{"unknown edition", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) { v.EditionKey = "premium" }},
		{"wrong prefix", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) { v.EditionVersionID = "business-2026-v1" }},
		{"software version", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) { v.EditionVersionID = "1.5.2" }},
		{"channel", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) { v.EditionVersionID = "stable" }},
		{"zero revision", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) { v.EditionVersionID = "team-2026-v0" }},
		{"padded revision", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) { v.EditionVersionID = "team-2026-v01" }},
		{"oversized version", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) {
			v.EditionVersionID = strings.Repeat("a", 65)
		}},
		{"zero time", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) { v.CreatedAt = time.Time{} }},
		{"incompatible time", func(v *edition.EditionVersion, _ *[]edition.EditionFeature) {
			v.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		}},
		{"wrong mapping version", func(_ *edition.EditionVersion, m *[]edition.EditionFeature) {
			(*m)[0].EditionVersionID = "enterprise-2026-v1"
		}},
		{"unknown feature", func(_ *edition.EditionVersion, m *[]edition.EditionFeature) { (*m)[0].FeatureID = "unknown" }},
		{"negative limit", func(_ *edition.EditionVersion, m *[]edition.EditionFeature) { n := int64(-1); (*m)[0].LimitValue = &n }},
		{"duplicate mapping", func(_ *edition.EditionVersion, m *[]edition.EditionFeature) { *m = append(*m, (*m)[0]) }},
		{"too many mappings", func(_ *edition.EditionVersion, m *[]edition.EditionFeature) {
			*m = make([]edition.EditionFeature, feature.MaxFeatures+1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := catalog(t)
			v := version("team-2026-v1", edition.Team)
			m := []edition.EditionFeature{mapping(v.EditionVersionID, true, nil, `{}`)}
			tc.mutate(&v, &m)
			if next, err := c.Publish(v, m); next != nil || !errors.Is(err, edition.ErrInvalidEdition) {
				t.Fatalf("invalid bundle accepted: %v", err)
			}
			if versions, _ := c.Versions(edition.Team); len(versions) != 0 {
				t.Fatal("partial publication")
			}
		})
	}
}

func TestConfigurationUnicodeAndNumericBounds(t *testing.T) {
	for _, raw := range []string{`{"x":"\uD800"}`, `{"x":"\uDC00"}`, `{"x":"\uD800\u0041"}`, `{"x":1e1001}`, `{"x":1e-1001}`} {
		if next, err := catalog(t).Publish(version("team-2026-v1", edition.Team), []edition.EditionFeature{mapping("team-2026-v1", true, nil, raw)}); next != nil || !errors.Is(err, edition.ErrInvalidEdition) {
			t.Fatalf("accepted unsafe JSON: %s", raw)
		}
	}
	for _, raw := range []string{`{"x":"\uD83D\uDE00"}`, `{"x":"\\uD800"}`, `{"x":1e1000}`, `{"x":1e-1000}`} {
		if _, err := catalog(t).Publish(version("team-2026-v1", edition.Team), []edition.EditionFeature{mapping("team-2026-v1", true, nil, raw)}); err != nil {
			t.Fatalf("rejected valid JSON: %s: %v", raw, err)
		}
	}
}

func TestAggregateConfigurationBound(t *testing.T) {
	var definitions []feature.Feature
	var mappings []edition.EditionFeature
	for i := range 80 {
		id := fmt.Sprintf("custom_%d", i)
		definitions = append(definitions, feature.Feature{FeatureID: id, Key: feature.Key(fmt.Sprintf("custom.f%d", i)), Name: "Custom", Description: "Custom capability", Category: feature.CategoryPlatform, Status: feature.StatusPlanned, IntroducedVersion: "1.0.0", DefaultLimitType: feature.LimitNone, CreatedAt: registered()})
		m := mapping("team-2026-v1", false, nil, `{"x":"`+strings.Repeat("a", 15000)+`"}`)
		m.FeatureID = id
		mappings = append(mappings, m)
	}
	fc, err := feature.NewFeatureCatalog(definitions)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := edition.NewCatalog(fc)
	if next, err := c.Publish(version("team-2026-v1", edition.Team), mappings); next != nil || !errors.Is(err, edition.ErrInvalidEdition) {
		t.Fatal("aggregate configuration bound not enforced")
	}
}

func TestConfigurationValidation(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `true`, `{"a":1} {}`, `{"a":1,"a":2}`, `{"a":{"x":1,"\u0078":2}}`, `{"a":"\u0000"}`, "{\"a\":\"\xff\"}", `{"a":`, `{"x":` + strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33) + "}", `{"x":"` + strings.Repeat("a", edition.MaxConfigurationBytes) + `"}`} {
		if next, err := catalog(t).Publish(version("team-2026-v1", edition.Team), []edition.EditionFeature{mapping("team-2026-v1", true, nil, raw)}); next != nil || !errors.Is(err, edition.ErrInvalidEdition) {
			t.Fatalf("accepted configuration %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"", `{}`, ` { "large":9007199254740993,"nested":[true,null,{"n":1}] } `} {
		c, err := catalog(t).Publish(version("team-2026-v1", edition.Team), []edition.EditionFeature{mapping("team-2026-v1", true, nil, raw)})
		if err != nil {
			t.Fatal(err)
		}
		m, _ := c.GetFeature("team-2026-v1", "feat_0001")
		if m.LimitValue != nil || len(m.ConfigurationJSON) == 0 {
			t.Fatal("unspecified limit or empty configuration changed")
		}
	}
}

func TestEnabledPrerequisitesRequired(t *testing.T) {
	base := feature.Feature{FeatureID: "base", Key: "verify.basic", Name: "Base", Description: "Base capability", Category: feature.CategoryVerify, Status: feature.StatusPlanned, IntroducedVersion: "1.0.0", DefaultLimitType: feature.LimitNone, CreatedAt: registered()}
	dependent := base
	dependent.FeatureID = "dependent"
	dependent.Key = "proof.planner"
	dependent.RequiresFeatures = []feature.Key{base.Key}
	fc, err := feature.NewFeatureCatalog([]feature.Feature{base, dependent})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := edition.NewCatalog(fc)
	for _, enabled := range []bool{false, true} {
		m := []edition.EditionFeature{{EditionVersionID: "team-2026-v1", FeatureID: "dependent", Enabled: true}, {EditionVersionID: "team-2026-v1", FeatureID: "base", Enabled: enabled}}
		_, err := c.Publish(version("team-2026-v1", edition.Team), m)
		if enabled && err != nil || !enabled && !errors.Is(err, edition.ErrInvalidEdition) {
			t.Fatalf("prerequisite enabled=%t: %v", enabled, err)
		}
	}
}

func TestNoImplicitLatestOrGrant(t *testing.T) {
	c, err := catalog(t).Publish(version("enterprise-2026-v1", edition.Enterprise), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetFeature("enterprise-2026-v1", "feat_0001"); !errors.Is(err, edition.ErrMappingNotFound) {
		t.Fatal("edition name synthesized mapping")
	}
	if _, err := c.GetVersion("enterprise"); !errors.Is(err, edition.ErrVersionNotFound) {
		t.Fatal("unpinned lookup silently selected version")
	}
	if _, err := c.Versions("unknown"); !errors.Is(err, edition.ErrInvalidEdition) {
		t.Fatal("invalid edition accepted")
	}
	if _, err := edition.NewCatalog(nil); !errors.Is(err, edition.ErrInvalidEdition) {
		t.Fatal("nil catalog accepted")
	}
	var zero edition.Catalog
	if _, err := zero.Publish(version("team-2026-v1", edition.Team), nil); !errors.Is(err, edition.ErrInvalidEdition) {
		t.Fatal("uninitialized publication succeeded")
	}
}

func TestBundleJSONRoundTrip(t *testing.T) {
	limit := int64(0)
	c, err := catalog(t).Publish(version("team-2026-v1", edition.Team), []edition.EditionFeature{mapping("team-2026-v1", true, &limit, `{"mode":"fixed"}`)})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.GetVersion("team-2026-v1")
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var restored edition.Bundle
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	next, err := catalog(t).Publish(restored.Version, restored.Features)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := next.GetVersion("team-2026-v1")
	raw2, _ := json.Marshal(b2)
	if string(raw) != string(raw2) {
		t.Fatal("round trip changed bundle")
	}
}

func FuzzEditionConfiguration(f *testing.F) {
	for _, raw := range []string{`{}`, `null`, `{"a":1,"a":2}`, `{"x":[1,2]}`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		c, err := catalog(t).Publish(version("team-2026-v1", edition.Team), []edition.EditionFeature{mapping("team-2026-v1", true, nil, raw)})
		if err == nil {
			b, _ := c.GetVersion("team-2026-v1")
			if _, err := json.Marshal(b); err != nil {
				t.Fatal(fmt.Errorf("accepted unencodable bundle: %w", err))
			}
		}
	})
}
