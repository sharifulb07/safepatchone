package feature_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/domain/feature"
)

func TestFeatureValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*feature.Feature)
	}{
		{"empty id", func(f *feature.Feature) { f.FeatureID = "" }},
		{"path id", func(f *feature.Feature) { f.FeatureID = "../outside" }},
		{"oversized id", func(f *feature.Feature) { f.FeatureID = strings.Repeat("a", 129) }},
		{"empty key", func(f *feature.Feature) { f.Key = "" }},
		{"empty name", func(f *feature.Feature) { f.Name = "" }},
		{"name newline", func(f *feature.Feature) { f.Name = "name\nPASS" }},
		{"oversized name", func(f *feature.Feature) { f.Name = strings.Repeat("a", 129) }},
		{"invalid UTF8", func(f *feature.Feature) { f.Name = "\xff" }},
		{"empty description", func(f *feature.Feature) { f.Description = "" }},
		{"control description", func(f *feature.Feature) { f.Description = "description\x00" }},
		{"oversized description", func(f *feature.Feature) { f.Description = strings.Repeat("a", 4097) }},
		{"whitespace", func(f *feature.Feature) { f.Description = " padded " }},
		{"unknown category", func(f *feature.Feature) { f.Category = "UNKNOWN" }},
		{"unknown status", func(f *feature.Feature) { f.Status = "UNKNOWN" }},
		{"empty status", func(f *feature.Feature) { f.Status = "" }},
		{"unknown limit", func(f *feature.Feature) { f.DefaultLimitType = "UNLIMITED" }},
		{"empty version", func(f *feature.Feature) { f.IntroducedVersion = "" }},
		{"edition as version", func(f *feature.Feature) { f.IntroducedVersion = "Team" }},
		{"channel as version", func(f *feature.Feature) { f.IntroducedVersion = "stable" }},
		{"unmarked deprecation", func(f *feature.Feature) { f.DeprecatedVersion = "2.0.0" }},
		{"missing deprecation", func(f *feature.Feature) { f.Status = feature.StatusDeprecated }},
		{"earlier deprecation", func(f *feature.Feature) { f.Status = feature.StatusDeprecated; f.DeprecatedVersion = "0.9.0" }},
		{"zero time", func(f *feature.Feature) { f.CreatedAt = time.Time{} }},
		{"unserializable time", func(f *feature.Feature) { f.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"invalid dependency", func(f *feature.Feature) { f.RequiresFeatures = []feature.Key{"../outside"} }},
		{"too many dependencies", func(f *feature.Feature) { f.RequiresFeatures = make([]feature.Key, feature.MaxDependencies+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := validFeature("id", "verify.basic")
			tc.mutate(&f)
			if err := f.Validate(); !errors.Is(err, feature.ErrInvalidFeature) {
				t.Fatalf("invalid value accepted: %v", err)
			}
		})
	}
}

func TestCategoriesStatusesAndLimitTypes(t *testing.T) {
	for _, category := range []feature.Category{feature.CategoryVerify, feature.CategoryIntent, feature.CategoryGraph, feature.CategoryProof, feature.CategoryRuntime, feature.CategoryAgent, feature.CategorySecurity, feature.CategoryEnterprise, feature.CategoryPlatform} {
		for _, status := range []feature.Status{feature.StatusPlanned, feature.StatusActive, feature.StatusDeprecated} {
			for _, limit := range []feature.LimitType{feature.LimitNone, feature.LimitCount, feature.LimitDuration, feature.LimitBytes, feature.LimitCredits} {
				f := validFeature("id", "custom.capability")
				f.Category, f.Status, f.DefaultLimitType = category, status, limit
				if status == feature.StatusDeprecated {
					f.DeprecatedVersion = "1.0.0"
				}
				if err := f.Validate(); err != nil {
					t.Fatalf("valid metadata rejected: %v", err)
				}
			}
		}
	}
}

func TestEditionTokensAndHostileKeysAreRejected(t *testing.T) {
	for _, key := range []string{"community", "verify.community", "developer.verify", "verify.team", "proof.team_passport", "business.custom", "enterprise.custom", "enterprise.sso.extra", "Enterprise.sso", "../verify", "verify..basic", "verify.basic/../../etc", "verify.basic;whoami", "verify.básic", "verify.basic\nPASS", "verify._basic", " verify.basic", strings.Repeat("a", 129)} {
		if err := feature.ValidateKey(feature.Key(key)); !errors.Is(err, feature.ErrInvalidFeature) {
			t.Errorf("accepted hostile or edition key %q", key)
		}
	}
	for _, key := range []feature.Key{"testtrust", "graph.cross_repo", "custom.capability", "business.invariants", "enterprise.sso", "enterprise.audit", "enterprise.self_hosted", "enterprise.airgap", "enterprise.lts"} {
		if err := feature.ValidateKey(key); err != nil {
			t.Errorf("required key rejected: %s: %v", key, err)
		}
	}
}

func FuzzValidateKey(f *testing.F) {
	for _, seed := range []string{"verify.basic", "enterprise.sso", "business.invariants", "enterprise.custom", "../outside", "\x00", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		key := feature.Key(raw)
		if err := feature.ValidateKey(key); err == nil {
			catalog, err := feature.NewFeatureCatalog([]feature.Feature{validFeature("id", key)})
			if err != nil {
				t.Fatal(err)
			}
			got, err := catalog.Get(key)
			if err != nil || got.Key != key {
				t.Fatal("key did not survive catalog round trip")
			}
		}
	})
}
