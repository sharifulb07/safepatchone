package config

import (
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/trial"
)

// Pricing is public presentation data, never effective organization access.
type Pricing struct {
	Release  string           `json:"release"`
	Editions []PricingEdition `json:"editions"`
}
type PricingEdition struct {
	Key              edition.Key      `json:"key"`
	Name             string           `json:"name"`
	Description      string           `json:"description"`
	EditionVersionID string           `json:"edition_version_id"`
	PriceLabel       string           `json:"price_label"`
	PriceNote        string           `json:"price_note"`
	TrialDays        *int             `json:"trial_days"`
	TrialNote        string           `json:"trial_note"`
	Action           PricingAction    `json:"action"`
	Features         []PricingFeature `json:"features"`
}
type PricingAction struct {
	Label              string `json:"label"`
	Href               string `json:"href"`
	UnavailableMessage string `json:"unavailable_message"`
}
type PricingFeature struct {
	Key          feature.Key      `json:"key"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Category     feature.Category `json:"category"`
	Availability string           `json:"availability"`
	LimitValue   *int64           `json:"limit_value"`
}

// Destinations are trusted deployment configuration. TrialURL points at an
// authenticated organization-selection flow, not a mutating GET endpoint.
type PricingOptions struct {
	DownloadURL       string
	TrialURL          string
	SalesURL          string
	BusinessTrialDays int
}

var ErrInvalidPricing = errors.New("invalid pricing configuration")

func validDestination(raw string, email bool) bool {
	if raw == "" {
		return true
	}
	if len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t\\") || strings.TrimSpace(raw) != raw {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return false
	}
	if email && u.Scheme == "mailto" {
		address, err := mail.ParseAddress(u.Opaque)
		return err == nil && address.Address == u.Opaque && u.Host == "" && u.RawQuery == "" && !strings.ContainsAny(u.Opaque, " <>%?#")
	}
	return u.Scheme == "https" && u.Hostname() != "" && u.Opaque == ""
}

// PublicPricing joins sealed edition mappings with the canonical feature catalog.
// Marketing copy and action routing live here, not in a second frontend plan map.
func PublicPricing(options PricingOptions) (Pricing, error) {
	if !validDestination(options.DownloadURL, false) || !validDestination(options.TrialURL, false) || !validDestination(options.SalesURL, true) {
		return Pricing{}, ErrInvalidPricing
	}
	if options.TrialURL == "" {
		options.TrialURL = "/onboarding"
	}
	features, err := feature.NewBuiltinCatalog("0.2.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return Pricing{}, err
	}
	matrix, err := V02Editions(features)
	if err != nil {
		return Pricing{}, err
	}
	versions := map[edition.Key]string{}
	entries := map[edition.Key]EditionConfiguration{}
	for _, entry := range matrix.Editions {
		versions[entry.Bundle.Version.EditionKey] = entry.Bundle.Version.EditionVersionID
		entries[entry.Bundle.Version.EditionKey] = entry
	}
	businessVersion := ""
	if options.BusinessTrialDays != 0 {
		businessVersion = versions[edition.Business]
	}
	policy, err := trial.NewPolicy(versions[edition.Developer], versions[edition.Team], businessVersion, options.BusinessTrialDays)
	if err != nil {
		return Pricing{}, ErrInvalidPricing
	}
	type copy struct{ description, price, note, label, destination, unavailable string }
	presentation := map[edition.Key]copy{
		edition.Community:  {"Start with the foundations of safer software changes.", "Free", "Community edition", "Download", options.DownloadURL, "A public download is not available yet. Check back for a published release."},
		edition.Developer:  {"Build confidence in the changes you ship.", "Pricing to come", "Paid pricing is not published yet.", "Start Trial", options.TrialURL, "Trial signup is not available on this site yet. Starting a trial requires an authenticated organization owner."},
		edition.Team:       {"Bring shared proof and policy to your team.", "Pricing to come", "Paid pricing is not published yet.", "Start Trial", options.TrialURL, "Trial signup is not available on this site yet. Starting a trial requires an authenticated organization owner."},
		edition.Business:   {"Connect assurance across your organization.", "Pricing to come", "Paid pricing is not published yet.", "Start Trial", options.TrialURL, "Business trial availability and duration must be confirmed before signup."},
		edition.Enterprise: {"Match assurance to your operating requirements.", "Custom", "Contract-managed pricing", "Contact Sales", options.SalesURL, "A sales contact has not been published yet. Check back for contact details."},
	}
	result := Pricing{Release: matrix.Release, Editions: []PricingEdition{}}
	for _, ed := range edition.Editions() {
		entry, ok := entries[ed.Key]
		if !ok {
			return Pricing{}, ErrInvalidPricing
		}
		c := presentation[ed.Key]
		v := PricingEdition{Key: ed.Key, Name: ed.Name, Description: c.description, EditionVersionID: versions[ed.Key], PriceLabel: c.price, PriceNote: c.note, Action: PricingAction{Label: c.label, Href: c.destination, UnavailableMessage: c.unavailable}, Features: []PricingFeature{}}
		if offer, e := policy.Offer(ed.Key); e == nil {
			days := offer.Days
			v.TrialDays = &days
			v.TrialNote = "Organization trial"
			if v.Action.Href != "" {
				u, _ := url.Parse(v.Action.Href)
				q := u.Query()
				q.Set("edition", string(ed.Key))
				u.RawQuery = q.Encode()
				v.Action.Href = u.String()
			}
		} else if c.label == "Start Trial" {
			v.Action.Href = ""
			v.TrialNote = "Trial duration confirmed before signup"
		}
		for _, mapping := range entry.Bundle.Features {
			if !mapping.Enabled {
				continue
			}
			f, err := features.GetByID(mapping.FeatureID)
			if err != nil {
				return Pricing{}, err
			}
			var metadata struct {
				Availability string `json:"availability"`
			}
			if json.Unmarshal(mapping.ConfigurationJSON, &metadata) != nil || (metadata.Availability != "SDK" && metadata.Availability != "FUTURE") {
				return Pricing{}, ErrInvalidPricing
			}
			v.Features = append(v.Features, PricingFeature{f.Key, f.Name, f.Description, f.Category, metadata.Availability, mapping.LimitValue})
		}
		result.Editions = append(result.Editions, v)
	}
	return result, nil
}
