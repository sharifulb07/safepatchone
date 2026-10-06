package config

import (
	"encoding/json"
	"net/url"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"testing"
	"time"
)

func TestPublishedPricesAreConfigurationOnlyAndValidated(t *testing.T) {
	price := PublishedPrice{Amount: "29.00", Currency: "USD", Interval: "month"}
	p, err := PublicPricing(PricingOptions{Prices: map[edition.Key]PublishedPrice{edition.Developer: price}})
	if err != nil || p.Editions[1].PriceLabel != "USD 29.00" || p.Editions[1].PriceNote != "Per organization / month" {
		t.Fatal(p, err)
	}
	baseline, _ := PublicPricing(PricingOptions{})
	want, _ := json.Marshal(baseline.Editions[1].Features)
	got, _ := json.Marshal(p.Editions[1].Features)
	if string(want) != string(got) {
		t.Fatal("price changed capabilities")
	}
	for _, bad := range []PublishedPrice{{"0", "USD", "month"}, {"-1", "USD", "month"}, {"1", "usd", "month"}, {"1", "USD", "forever"}, {"1<script>", "USD", "month"}} {
		if _, err := PublicPricing(PricingOptions{Prices: map[edition.Key]PublishedPrice{edition.Developer: bad}}); err == nil {
			t.Fatal("invalid price accepted", bad)
		}
	}
	if _, err := PublicPricing(PricingOptions{Prices: map[edition.Key]PublishedPrice{edition.Community: price}}); err == nil {
		t.Fatal("Community price overridden")
	}
}

func TestPricingUsesCanonicalEditionsFeaturesAndTrialPolicy(t *testing.T) {
	p, err := PublicPricing(PricingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := feature.NewBuiltinCatalog("0.2.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	matrix, _ := V02Editions(f)
	if len(p.Editions) != 5 || p.Release != matrix.Release {
		t.Fatal("wrong catalog")
	}
	labels := []string{"Download", "Start Trial", "Start Trial", "Choose plan", "Contact Sales"}
	for i, e := range edition.Editions() {
		v := p.Editions[i]
		b := matrix.Editions[i].Bundle
		if v.Key != e.Key || v.Name != e.Name || v.EditionVersionID != b.Version.EditionVersionID || v.Action.Label != labels[i] {
			t.Fatal(v)
		}
		if i == 1 || i == 2 {
			if v.Action.Href != "/onboarding?edition="+string(e.Key) {
				t.Fatal("trial onboarding destination missing")
			}
			if v.TrialDays == nil || *v.TrialDays != 14 {
				t.Fatal("trial policy drift")
			}
		} else if v.TrialDays != nil {
			t.Fatal("invented trial offer")
		}
		count := 0
		for _, m := range b.Features {
			if !m.Enabled {
				continue
			}
			count++
			definition, _ := f.GetByID(m.FeatureID)
			var meta struct {
				Availability string `json:"availability"`
			}
			_ = json.Unmarshal(m.ConfigurationJSON, &meta)
			found := false
			for _, shown := range v.Features {
				if shown.Key == definition.Key {
					found = true
					if shown.Name != definition.Name || shown.Description != definition.Description || shown.Category != definition.Category || shown.Availability != meta.Availability {
						t.Fatal("frontend feature drift", shown)
					}
				}
			}
			if !found {
				t.Fatal("missing enabled feature", definition.Key)
			}
		}
		if count != len(v.Features) {
			t.Fatal("extra features")
		}
	}
}
func TestPricingDestinationsAndBusinessConfiguration(t *testing.T) {
	opts := PricingOptions{DownloadURL: "https://download.example/releases", TrialURL: "https://app.example/signup?campaign=pricing&edition=untrusted", SalesURL: "mailto:sales@example.com", BusinessTrialDays: 30}
	p, err := PublicPricing(opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Editions[0].Action.Href != opts.DownloadURL || p.Editions[4].Action.Href != opts.SalesURL {
		t.Fatal("lost destination")
	}
	for _, e := range p.Editions[1:4] {
		u, err := url.Parse(e.Action.Href)
		if err != nil || u.Query().Get("edition") != string(e.Key) || u.Query().Get("campaign") != "pricing" {
			t.Fatal(e.Action)
		}
	}
	if *p.Editions[3].TrialDays != 30 {
		t.Fatal("business duration drift")
	}
	opts.BusinessTrialDays = 0
	p, err = PublicPricing(opts)
	if err != nil || p.Editions[3].TrialDays != nil || p.Editions[3].Action.Label != "Choose plan" {
		t.Fatal("disabled trial still offered", err)
	}
	u, _ := url.Parse(p.Editions[3].Action.Href)
	if u.Query().Get("edition") != "business" {
		t.Fatal("paid Business onboarding unreachable")
	}
}
func TestPricingRejectsUnsafeDestinationsAndInvalidPolicies(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,bad", "//attacker.example", "http://example.com", "https://user:secret@example.com", "https://example.com\r\nHeader:bad", "https://example.com\\evil", " https://example.com", "mailto:a@example.com?body=bad"} {
		for _, field := range []int{0, 1, 2} {
			opts := PricingOptions{}
			switch field {
			case 0:
				opts.DownloadURL = raw
			case 1:
				opts.TrialURL = raw
			case 2:
				opts.SalesURL = raw
			}
			if _, err := PublicPricing(opts); err == nil {
				t.Fatal("unsafe destination", field, raw)
			}
		}
	}
	for _, days := range []int{-1, 366} {
		if _, err := PublicPricing(PricingOptions{BusinessTrialDays: days}); err == nil {
			t.Fatal("invalid business duration")
		}
	}
}
func TestPricingChangesDoNotMutateEditionOrEntitlementConfiguration(t *testing.T) {
	before, _ := PublicPricing(PricingOptions{})
	want, _ := json.Marshal(before)
	changed, _ := PublicPricing(PricingOptions{BusinessTrialDays: 45})
	changed.Editions[0].Name = "poison"
	changed.Editions[0].Features[0].Name = "poison"
	after, _ := PublicPricing(PricingOptions{})
	got, _ := json.Marshal(after)
	if string(got) != string(want) {
		t.Fatal("public configuration mutated canonical data")
	}
}
