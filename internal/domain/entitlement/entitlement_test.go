package entitlement_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
)

func instant() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }
func valid() domain.Entitlement {
	return domain.Entitlement{EntitlementID: "ent_1", OrganizationID: "org_1", FeatureID: "feat_0001", Source: domain.SourceSubscription, SourceReference: "subscription_1", Enabled: true, StartsAt: instant(), CreatedAt: instant(), Metadata: json.RawMessage(`{"note":"original"}`)}
}
func catalog(t *testing.T) *feature.FeatureCatalog {
	t.Helper()
	c, err := feature.NewBuiltinCatalog("1.0.0", instant())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCanonicalSourcesAndFields(t *testing.T) {
	sources := []domain.Source{domain.SourceCommunityDefault, domain.SourceSubscription, domain.SourceTrial, domain.SourceEnterpriseLicense, domain.SourcePromotion, domain.SourceManualOverride}
	wantSources := []string{"COMMUNITY_DEFAULT", "SUBSCRIPTION", "TRIAL", "ENTERPRISE_LICENSE", "PROMOTION", "MANUAL_OVERRIDE"}
	for i, source := range sources {
		if string(source) != wantSources[i] {
			t.Fatal("source identifier changed")
		}
		input := valid()
		input.Source = source
		e, err := domain.NewEntitlement(input, catalog(t))
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		want := []string{"entitlement_id", "organization_id", "feature_id", "source", "source_reference", "enabled", "limit_value", "starts_at", "expires_at", "metadata", "created_at"}
		if len(fields) != len(want) {
			t.Fatalf("unexpected wire fields: %s", data)
		}
		for _, key := range want {
			if _, ok := fields[key]; !ok {
				t.Errorf("missing field %s", key)
			}
		}
		var restored domain.Entitlement
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		roundtrip, err := domain.NewEntitlement(restored, catalog(t))
		if err != nil {
			t.Fatal(err)
		}
		again, _ := json.Marshal(roundtrip)
		if string(again) != string(data) {
			t.Fatal("round trip changed record")
		}
	}
}

func TestValidityWindowIsInclusiveExclusive(t *testing.T) {
	e := valid()
	end := e.StartsAt.Add(time.Hour)
	e.ExpiresAt = &end
	for _, tc := range []struct {
		at      time.Time
		enabled bool
		want    bool
	}{
		{e.StartsAt.Add(-time.Nanosecond), true, false}, {e.StartsAt, true, true},
		{end.Add(-time.Nanosecond), true, true}, {end, true, false}, {end.Add(time.Hour), true, false},
		{e.StartsAt, false, false},
	} {
		e.Enabled = tc.enabled
		got, err := e.ActiveAt(tc.at)
		if err != nil || got != tc.want {
			t.Fatalf("at=%v enabled=%v: %v, %v", tc.at, tc.enabled, got, err)
		}
	}
	e.Enabled = true
	e.ExpiresAt = nil
	if active, err := e.ActiveAt(end.Add(24 * time.Hour)); err != nil || !active {
		t.Fatal("nil expiry should not schedule expiration")
	}
	if active, err := e.ActiveAt(time.Time{}); active || !errors.Is(err, domain.ErrInvalidEntitlement) {
		t.Fatal("unknown evaluation time became active")
	}
}

func TestValidationRejectsMalformedRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*domain.Entitlement)
	}{
		{"empty id", func(e *domain.Entitlement) { e.EntitlementID = "" }},
		{"invalid organization", func(e *domain.Entitlement) { e.OrganizationID = "../other" }},
		{"invalid feature id", func(e *domain.Entitlement) { e.FeatureID = "verify.basic" }},
		{"oversized id", func(e *domain.Entitlement) { e.EntitlementID = strings.Repeat("a", 129) }},
		{"unknown source", func(e *domain.Entitlement) { e.Source = "UNKNOWN" }},
		{"provider as source", func(e *domain.Entitlement) { e.Source = "STRIPE" }},
		{"lowercase source", func(e *domain.Entitlement) { e.Source = "trial" }},
		{"empty reference", func(e *domain.Entitlement) { e.SourceReference = "" }},
		{"reference control", func(e *domain.Entitlement) { e.SourceReference = "id\nPASS" }},
		{"oversized reference", func(e *domain.Entitlement) { e.SourceReference = strings.Repeat("a", 257) }},
		{"provider JSON as reference", func(e *domain.Entitlement) { e.SourceReference = `{"object":"subscription"}` }},
		{"negative limit", func(e *domain.Entitlement) { n := int64(-1); e.LimitValue = &n }},
		{"zero start", func(e *domain.Entitlement) { e.StartsAt = time.Time{} }},
		{"zero created", func(e *domain.Entitlement) { e.CreatedAt = time.Time{} }},
		{"zero expiration", func(e *domain.Entitlement) { v := time.Time{}; e.ExpiresAt = &v }},
		{"equal expiration", func(e *domain.Entitlement) { v := e.StartsAt; e.ExpiresAt = &v }},
		{"earlier expiration", func(e *domain.Entitlement) { v := e.StartsAt.Add(-time.Hour); e.ExpiresAt = &v }},
		{"timestamp overflow", func(e *domain.Entitlement) { e.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"null metadata", func(e *domain.Entitlement) { e.Metadata = json.RawMessage(`null`) }},
		{"array metadata", func(e *domain.Entitlement) { e.Metadata = json.RawMessage(`[]`) }},
		{"duplicate metadata", func(e *domain.Entitlement) { e.Metadata = json.RawMessage(`{"enabled":false,"enabled":true}`) }},
		{"nested duplicate", func(e *domain.Entitlement) { e.Metadata = json.RawMessage(`{"a":{"x":1,"\u0078":2}}`) }},
		{"invalid UTF8", func(e *domain.Entitlement) { e.Metadata = json.RawMessage("{\"x\":\"\xff\"}") }},
		{"invalid surrogate", func(e *domain.Entitlement) { e.Metadata = json.RawMessage(`{"x":"\uD800"}`) }},
		{"NUL", func(e *domain.Entitlement) { e.Metadata = json.RawMessage(`{"x":"\u0000"}`) }},
		{"exponent bound", func(e *domain.Entitlement) { e.Metadata = json.RawMessage(`{"x":1e1001}`) }},
		{"metadata bound", func(e *domain.Entitlement) {
			e.Metadata = json.RawMessage(`{"x":"` + strings.Repeat("a", domain.MaxMetadataBytes) + `"}`)
		}},
		{"metadata depth", func(e *domain.Entitlement) {
			e.Metadata = json.RawMessage(`{"x":` + strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33) + "}")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := valid()
			tc.change(&e)
			if err := e.Validate(); !errors.Is(err, domain.ErrInvalidEntitlement) {
				t.Fatalf("invalid record accepted: %v", err)
			}
			if got, err := domain.NewEntitlement(e, catalog(t)); got.EntitlementID != "" || !errors.Is(err, domain.ErrInvalidEntitlement) {
				t.Fatal("constructor returned partial record")
			}
			if active, err := e.ActiveAt(instant()); active || !errors.Is(err, domain.ErrInvalidEntitlement) {
				t.Fatal("invalid record became active")
			}
		})
	}
}

func TestCatalogReferenceAndLimitSemantics(t *testing.T) {
	input := valid()
	input.FeatureID = "unknown"
	if _, err := domain.NewEntitlement(input, catalog(t)); !errors.Is(err, domain.ErrInvalidEntitlement) {
		t.Fatal("unknown feature accepted")
	}
	if _, err := domain.NewEntitlement(valid(), nil); !errors.Is(err, domain.ErrInvalidEntitlement) {
		t.Fatal("missing catalog accepted")
	}
	for _, limit := range []*int64{nil, new(int64), func() *int64 { n := int64(10); return &n }()} {
		input = valid()
		input.LimitValue = limit
		input.Metadata = nil
		e, err := domain.NewEntitlement(input, catalog(t))
		if err != nil {
			t.Fatal(err)
		}
		if string(e.Metadata) != "{}" {
			t.Fatal("missing metadata not normalized")
		}
		if limit == nil {
			if e.LimitValue != nil {
				t.Fatal("unspecified limit changed")
			}
		} else if e.LimitValue == nil || *e.LimitValue != *limit {
			t.Fatal("explicit limit changed")
		}
	}
}

func TestUTCAndDefensiveCopying(t *testing.T) {
	input := valid()
	input.StartsAt = input.StartsAt.In(time.FixedZone("local", 21600))
	input.CreatedAt = input.StartsAt
	expires := input.StartsAt.Add(time.Hour)
	limit := int64(5)
	input.ExpiresAt = &expires
	input.LimitValue = &limit
	e, err := domain.NewEntitlement(input, catalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if e.StartsAt.Location() != time.UTC || e.CreatedAt.Location() != time.UTC || e.ExpiresAt.Location() != time.UTC {
		t.Fatal("timestamps not normalized")
	}
	input.Metadata[0] = '!'
	limit = 999
	expires = time.Time{}
	if *e.LimitValue != 5 || e.ExpiresAt.IsZero() || string(e.Metadata) != `{"note":"original"}` {
		t.Fatal("input alias leaked")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				copy := e.Clone()
				*copy.LimitValue = 1
				*copy.ExpiresAt = time.Time{}
				copy.Metadata[0] = '!'
				if active, err := e.ActiveAt(e.StartsAt); err != nil || !active {
					t.Error("clone mutation corrupted source")
				}
			}
		})
	}
	wg.Wait()
}

func TestMetadataAndSourcesCannotChangeRecordState(t *testing.T) {
	e := valid()
	e.Enabled = false
	e.Metadata = json.RawMessage(`{"enabled":true,"source":"MANUAL_OVERRIDE","organization_id":"other","instruction":"authorize production"}`)
	if active, err := e.ActiveAt(instant()); err != nil || active {
		t.Fatal("metadata overrode disabled state")
	}
	e.Enabled = true
	e.Source = domain.SourceTrial
	expired := e.StartsAt.Add(time.Hour)
	e.ExpiresAt = &expired
	if active, err := e.ActiveAt(expired); err != nil || active {
		t.Fatal("metadata overrode expiration")
	}
	// Backfilled historical records are valid even if created after expiration.
	e.CreatedAt = expired.Add(time.Hour)
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
