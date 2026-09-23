package entitlement_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

func community(t *testing.T) *entitlement.CommunityEntitlementProvider {
	t.Helper()
	p, err := entitlement.NewCommunityEntitlementProvider()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCommunityProviderExactLocalBoundary(t *testing.T) {
	p := community(t)
	report, err := p.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Edition != edition.Community || report.BundleVersion != "community-2026-v2" || report.Source != domain.SourceCommunityDefault || report.Scope != "local" || report.AccountRequired || report.NetworkRequired {
		t.Fatal(report)
	}
	expected := map[feature.Key]bool{feature.KeyVerifyBasic: true, feature.KeyVerifySecurity: true, feature.KeyIntentBasic: true, feature.KeyGraphLocal: true, feature.KeyEvidenceLocal: true}
	fc, err := feature.NewBuiltinCatalog("1.0.0", edition.CommunityBundleRegisteredAt())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Features) != fc.Len() {
		t.Fatal("incomplete report")
	}
	seen := make(map[feature.Key]bool)
	for _, f := range report.Features {
		if seen[f.Key] {
			t.Fatal("duplicate report key", f.Key)
		}
		seen[f.Key] = true
		if f.Entitled != expected[f.Key] || f.ImplementationStatus != feature.StatusPlanned {
			t.Fatal("unexpected grant/readiness", f)
		}
		owned, err := p.Has(t.Context(), entitlement.LocalOrganizationID, f.Key)
		if err != nil || owned != expected[f.Key] {
			t.Fatal(f.Key, owned, err)
		}
		resolution, err := p.Resolve(t.Context(), entitlement.LocalOrganizationID, f.Key)
		if err != nil || resolution.Enabled != owned {
			t.Fatal(resolution, err)
		}
		limit, err := p.Limit(t.Context(), entitlement.LocalOrganizationID, f.Key)
		want := entitlement.ErrNotEntitled
		if owned {
			want = entitlement.ErrLimitUnspecified
			if resolution.Source != domain.SourceCommunityDefault || resolution.Expiration != nil || resolution.EffectiveLimit != nil {
				t.Fatal("invented expiry/limit/source", resolution)
			}
		}
		if limit != 0 || !errors.Is(err, want) {
			t.Fatal("limit contract changed", f.Key, limit, err)
		}
	}
	for key := range expected {
		if !seen[key] {
			t.Fatal("missing grant", key)
		}
	}
}

func TestCommunityProviderIsImmutableAndConcurrent(t *testing.T) {
	p := community(t)
	before, err := p.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(before)
	b, err := p.Bundle(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b.Version.EditionKey = edition.Enterprise
	b.Version.EditionVersionID = "enterprise-2026-v99"
	b.Features[0].Enabled = false
	b.Features[0].ConfigurationJSON[0] = '!'
	before.Features[0].Entitled = !before.Features[0].Entitled
	var wg sync.WaitGroup
	for range 25 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := p.Inspect(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := json.Marshal(got)
			if string(data) != string(want) {
				t.Error("mutated or nondeterministic report")
			}
		}()
	}
	wg.Wait()
	fresh, err := community(t).Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(fresh)
	if string(data) != string(want) {
		t.Fatal("instance shared mutable data")
	}
}

func TestCommunityProviderRejectsRemoteScopeAndHostileKeys(t *testing.T) {
	p := community(t)
	for _, org := range []string{"org_customer", "", "../local-cli", "local-cli\nPASS"} {
		owned, err := p.Has(t.Context(), org, feature.KeyVerifyBasic)
		if owned || !errors.Is(err, entitlement.ErrOrganizationMismatch) {
			t.Fatal(org, owned, err)
		}
		if r, err := p.Resolve(t.Context(), org, feature.KeyVerifyBasic); r.Enabled || !errors.Is(err, entitlement.ErrOrganizationMismatch) {
			t.Fatal(r, err)
		}
		if n, err := p.Limit(t.Context(), org, feature.KeyVerifyBasic); n != 0 || !errors.Is(err, entitlement.ErrOrganizationMismatch) {
			t.Fatal(n, err)
		}
	}
	for _, key := range []feature.Key{"unknown.feature", "", "verify.basic\nPASS", "../verify.basic"} {
		if yes, err := p.Has(t.Context(), entitlement.LocalOrganizationID, key); yes || err == nil {
			t.Fatal(key, yes, err)
		}
	}
}

func TestCommunityProviderCancellationAndZeroValue(t *testing.T) {
	p := community(t)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, cancelled} {
		want := entitlement.ErrInvalidQuery
		if ctx != nil {
			want = context.Canceled
		}
		if yes, err := p.Has(ctx, entitlement.LocalOrganizationID, feature.KeyVerifyBasic); yes || !errors.Is(err, want) {
			t.Fatal(yes, err)
		}
		if _, err := p.Limit(ctx, entitlement.LocalOrganizationID, feature.KeyVerifyBasic); !errors.Is(err, want) {
			t.Fatal(err)
		}
		if _, err := p.Resolve(ctx, entitlement.LocalOrganizationID, feature.KeyVerifyBasic); !errors.Is(err, want) {
			t.Fatal(err)
		}
		if _, err := p.Inspect(ctx); !errors.Is(err, want) {
			t.Fatal(err)
		}
		if _, err := p.Bundle(ctx); !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	for _, p := range []*entitlement.CommunityEntitlementProvider{nil, {}} {
		if yes, err := p.Has(t.Context(), entitlement.LocalOrganizationID, feature.KeyVerifyBasic); yes || !errors.Is(err, entitlement.ErrInvalidCommunityProvider) {
			t.Fatal(yes, err)
		}
		if _, err := p.Limit(t.Context(), entitlement.LocalOrganizationID, feature.KeyVerifyBasic); !errors.Is(err, entitlement.ErrInvalidCommunityProvider) {
			t.Fatal(err)
		}
		if _, err := p.Resolve(t.Context(), entitlement.LocalOrganizationID, feature.KeyVerifyBasic); !errors.Is(err, entitlement.ErrInvalidCommunityProvider) {
			t.Fatal(err)
		}
		if _, err := p.Inspect(t.Context()); !errors.Is(err, entitlement.ErrInvalidCommunityProvider) {
			t.Fatal(err)
		}
		if _, err := p.Bundle(t.Context()); !errors.Is(err, entitlement.ErrInvalidCommunityProvider) {
			t.Fatal(err)
		}
	}
}
