package feature_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/domain/feature"
)

func validFeature(id string, key feature.Key, dependencies ...feature.Key) feature.Feature {
	return feature.Feature{
		FeatureID: id, Key: key, Name: "Capability", Description: "Capability description.",
		Category: feature.CategoryVerify, Status: feature.StatusPlanned,
		IntroducedVersion: "1.0.0", DefaultLimitType: feature.LimitNone,
		RequiresFeatures: dependencies, CreatedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
	}
}

func TestCatalogLookupOrderingAndIsolation(t *testing.T) {
	input := []feature.Feature{
		validFeature("3", "proof.custom", "verify.security", "verify.basic"),
		validFeature("2", "verify.security", "verify.basic"),
		validFeature("1", "verify.basic"),
	}
	catalog, err := feature.NewFeatureCatalog(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Name = "mutated input"
	input[0].RequiresFeatures[0] = "unknown"
	if catalog.Len() != 3 {
		t.Fatal("wrong size")
	}
	list := catalog.List()
	if list[0].Key != "proof.custom" || list[1].Key != "verify.basic" || list[2].Key != "verify.security" {
		t.Fatal("nondeterministic order")
	}
	if list[0].RequiresFeatures[0] != "verify.basic" {
		t.Fatal("dependencies not sorted/copied")
	}
	list[0].Name = "mutated list"
	list[0].RequiresFeatures[0] = "unknown"
	got, err := catalog.GetByID("3")
	if err != nil || got.Name != "Capability" || got.RequiresFeatures[0] != "verify.basic" {
		t.Fatalf("snapshot changed: %+v, %v", got, err)
	}
	got.RequiresFeatures[0] = "unknown"
	again, _ := catalog.Get("proof.custom")
	if again.RequiresFeatures[0] != "verify.basic" {
		t.Fatal("lookup leaked internal slice")
	}
	for _, key := range []feature.Key{"unknown", "Team", "../verify.basic", ""} {
		if f, err := catalog.Get(key); !errors.Is(err, feature.ErrFeatureNotFound) || f.FeatureID != "" {
			t.Fatalf("unknown key returned data: %+v %v", f, err)
		}
	}
	if _, err := catalog.GetByID("missing"); !errors.Is(err, feature.ErrFeatureNotFound) {
		t.Fatal(err)
	}
}

func TestCatalogRejectsInvalidGraphsAtomically(t *testing.T) {
	for _, tc := range []struct {
		name     string
		features []feature.Feature
		want     error
	}{
		{"invalid entry", []feature.Feature{validFeature("1", "verify.basic"), {}}, feature.ErrInvalidFeature},
		{"duplicate key", []feature.Feature{validFeature("1", "verify.basic"), validFeature("2", "verify.basic")}, feature.ErrDuplicateFeature},
		{"duplicate id", []feature.Feature{validFeature("1", "verify.basic"), validFeature("1", "verify.api")}, feature.ErrDuplicateFeature},
		{"unknown dependency", []feature.Feature{validFeature("1", "verify.basic", "verify.api")}, feature.ErrUnknownDependency},
		{"self dependency", []feature.Feature{validFeature("1", "verify.basic", "verify.basic")}, feature.ErrInvalidFeature},
		{"duplicate dependency", []feature.Feature{validFeature("1", "verify.basic", "verify.api", "verify.api")}, feature.ErrInvalidFeature},
		{"cycle", []feature.Feature{validFeature("1", "verify.basic", "verify.api"), validFeature("2", "verify.api", "proof.planner"), validFeature("3", "proof.planner", "verify.basic")}, feature.ErrDependencyCycle},
		{"size bound", make([]feature.Feature, feature.MaxFeatures+1), feature.ErrInvalidFeature},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := feature.NewFeatureCatalog(tc.features)
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("got %v, %v; want nil, %v", got, err, tc.want)
			}
		})
	}
}

func TestZeroAndEmptyCatalog(t *testing.T) {
	var zero feature.FeatureCatalog
	empty, err := feature.NewFeatureCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, catalog := range []*feature.FeatureCatalog{&zero, empty} {
		if catalog.Len() != 0 || catalog.List() == nil {
			t.Fatal("expected empty snapshot")
		}
		if _, err := catalog.Get("verify.basic"); !errors.Is(err, feature.ErrFeatureNotFound) {
			t.Fatal(err)
		}
	}
}

func TestDeepDependencyChainIsBoundedAndIterative(t *testing.T) {
	features := make([]feature.Feature, feature.MaxFeatures)
	for i := range features {
		features[i] = validFeature(fmt.Sprintf("id_%d", i), feature.Key(fmt.Sprintf("custom.f%d", i)))
		if i > 0 {
			features[i].RequiresFeatures = []feature.Key{features[i-1].Key}
		}
	}
	catalog, err := feature.NewFeatureCatalog(features)
	if err != nil || catalog.Len() != feature.MaxFeatures {
		t.Fatalf("valid deep chain rejected: %v", err)
	}
}

func TestConcurrentReadersCannotMutateCatalog(t *testing.T) {
	catalog, err := feature.NewFeatureCatalog([]feature.Feature{validFeature("1", "verify.basic"), validFeature("2", "proof.planner", "verify.basic")})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				f, err := catalog.Get("proof.planner")
				if err != nil || f.RequiresFeatures[0] != "verify.basic" {
					t.Errorf("corrupted lookup: %v", err)
					return
				}
				f.RequiresFeatures[0] = "changed"
				catalog.List()[0].RequiresFeatures[0] = "changed"
			}
		})
	}
	wg.Wait()
}

func TestJSONRoundTripAndDeprecationPreserveIdentity(t *testing.T) {
	f := validFeature("permanent_1", "verify.basic")
	f.CreatedAt = f.CreatedAt.In(time.FixedZone("local", 6*60*60))
	original, err := feature.NewFeatureCatalog([]feature.Feature{f})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := original.Get(f.Key)
	if before.CreatedAt.Location() != time.UTC {
		t.Fatal("timestamp not normalized")
	}
	f.Status = feature.StatusDeprecated
	f.DeprecatedVersion = "2.0.0"
	updated, err := feature.NewFeatureCatalog([]feature.Feature{f})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(updated.List())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"feature_id", "key", "name", "description", "category", "status", "introduced_version", "deprecated_version", "default_limit_type", "requires_features", "created_at"} {
		if !strings.Contains(string(data), `"`+field+`":`) {
			t.Errorf("missing JSON field %s", field)
		}
	}
	var decoded []feature.Feature
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	restored, err := feature.NewFeatureCatalog(decoded)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := restored.GetByID(before.FeatureID)
	if after.Key != before.Key || after.IntroducedVersion != before.IntroducedVersion || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatal("lifecycle metadata lost identity")
	}
	unchanged, _ := original.Get(f.Key)
	if unchanged.Status != feature.StatusPlanned || unchanged.DeprecatedVersion != "" {
		t.Fatal("original snapshot changed")
	}
}
