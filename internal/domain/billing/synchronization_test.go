package billing_test

import (
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
	"testing"
	"time"
)

func TestDerivedGrantsComeFromLocalBundleAndExpire(t *testing.T) {
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	features, _ := feature.NewBuiltinCatalog("1.0.0", at)
	catalog, _ := edition.NewCatalog(features)
	catalog, err := catalog.Publish(edition.EditionVersion{EditionVersionID: "team-2026-v2", EditionKey: edition.Team, CreatedAt: at}, []edition.EditionFeature{{EditionVersionID: "team-2026-v2", FeatureID: "feat_0011", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	bundle, _ := catalog.GetVersion("team-2026-v2")
	sub := org.Subscription{OrganizationID: "org", SubscriptionID: "sub_internal", Provider: "stripe", ExternalReference: "sub_external", EditionVersionID: "team-2026-v2", State: org.Subscribed, Revision: 1, CreatedAt: at, UpdatedAt: at}
	until := at.Add(time.Hour)
	grants, err := billing.DeriveSubscriptionEntitlements(sub, bundle, features, true, until, at)
	if err != nil || len(grants) != 1 || !grants[0].Enabled || grants[0].SourceReference != "sub_internal" || !grants[0].ExpiresAt.Equal(until) {
		t.Fatal(grants, err)
	}
	for _, state := range []org.SubscriptionState{org.SubscriptionPending, org.PastDue, org.Canceled, org.Expired} {
		changed := sub
		changed.State = state
		got, err := billing.DeriveSubscriptionEntitlements(changed, bundle, features, true, until, at)
		if err != nil || len(got) != 0 {
			t.Fatal(state, got, err)
		}
	}
	for _, paid := range []bool{false, true} {
		expiry := until
		if paid {
			expiry = at
		}
		got, err := billing.DeriveSubscriptionEntitlements(sub, bundle, features, paid, expiry, at)
		if err != nil || len(got) != 0 {
			t.Fatal("unpaid or expired grants", got, err)
		}
	}
	if _, err := billing.DeriveSubscriptionEntitlements(sub, bundle, features, true, time.Time{}, at); err == nil {
		t.Fatal("indefinite paid access")
	}
	altered := sub
	altered.EditionVersionID = "developer-2026-v2"
	if _, err := billing.DeriveSubscriptionEntitlements(altered, bundle, features, true, until, at); err == nil {
		t.Fatal("mismatched bundle")
	}
	// Derivation and cancellation do not mutate historical subscriptions/bundles.
	if sub.State != org.Subscribed || !bundle.Features[0].Enabled || !grants[0].Enabled {
		t.Fatal("plan change corrupted history")
	}
}
