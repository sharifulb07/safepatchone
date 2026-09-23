package entitlement_test

import (
	"encoding/json"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/trial"
	entitlementservice "safepatch/internal/entitlement"
	"testing"
	"time"
)

func TestTrialUsesCanonicalResolverAndPreservesOtherSources(t *testing.T) {
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	f, _ := feature.NewBuiltinCatalog("1.0.0", at)
	p, _ := trial.NewPolicy("developer-2026-v2", "team-2026-v2", "", 0)
	o, _ := p.Offer(edition.Team)
	v, _ := trial.New("org", o, at)
	limit := int64(5)
	b := edition.Bundle{Version: edition.EditionVersion{EditionVersionID: o.EditionVersionID, EditionKey: o.Edition, CreatedAt: at}, Features: []edition.EditionFeature{{EditionVersionID: o.EditionVersionID, FeatureID: "feat_0011", Enabled: true, LimitValue: &limit}}}
	grants, err := trial.Derive(v, b, f)
	if err != nil {
		t.Fatal(err)
	}
	before := grants[0].Clone()
	community, _ := entitlement.NewEntitlement(entitlement.Entitlement{EntitlementID: "community", OrganizationID: "org", FeatureID: "feat_0001", Source: entitlement.SourceCommunityDefault, SourceReference: "community", Enabled: true, StartsAt: at, CreatedAt: at}, f)
	for _, when := range []time.Time{at.Add(-time.Microsecond), at, v.ExpiresAt.Add(-time.Microsecond), v.ExpiresAt, v.ExpiresAt.Add(time.Hour)} {
		r, err := entitlementservice.ResolveRecords(t.Context(), "org", feature.KeyProofPlanner, grants, f, when)
		want := !when.Before(at) && when.Before(v.ExpiresAt)
		if err != nil || r.Enabled != want {
			t.Fatal(when, r, err)
		}
		if want && (r.Source != entitlement.SourceTrial || r.EffectiveLimit == nil || *r.EffectiveLimit != 5 || !r.Expiration.Equal(v.ExpiresAt)) {
			t.Fatal(r)
		}
	}
	for _, source := range []entitlement.Source{entitlement.SourceSubscription, entitlement.SourceEnterpriseLicense} {
		paid := before.Clone()
		paid.EntitlementID = "paid"
		paid.Source = source
		end := v.ExpiresAt.Add(trial.Day)
		paid.ExpiresAt = &end
		r, err := entitlementservice.ResolveRecords(t.Context(), "org", feature.KeyProofPlanner, append(grants, paid, community), f, v.ExpiresAt)
		if err != nil || !r.Enabled || r.Source != source {
			t.Fatal(r, err)
		}
	}
	r, err := entitlementservice.ResolveRecords(t.Context(), "org", feature.KeyVerifyBasic, append(grants, community), f, v.ExpiresAt)
	if err != nil || !r.Enabled || r.Source != entitlement.SourceCommunityDefault {
		t.Fatal(r, err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(grants[0])
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("expiration or paid transition mutated trial history")
	}
	b.Features[0].Enabled = false
	limit = 999
	if !grants[0].Enabled || *grants[0].LimitValue != 5 {
		t.Fatal("grant aliases configuration")
	}
	v.OrganizationID = "foreign"
	if _, err := trial.Derive(v, b, f); err == nil {
		t.Fatal("accepted foreign ownership")
	}
}
