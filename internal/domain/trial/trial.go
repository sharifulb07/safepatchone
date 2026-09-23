// Package trial describes organization-owned offers and finite entitlement grants.
// Trial records are commercial sources, never permissions or runtime modes.
package trial

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"time"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/organization"
)

var (
	ErrInvalid     = errors.New("invalid organization trial")
	ErrUnavailable = errors.New("trial offer unavailable")
	ErrUsed        = errors.New("organization trial already used")
	ErrNotFound    = errors.New("organization trial not found")
	versionPattern = regexp.MustCompile(`^([a-z]+)-[1-9][0-9]{3}-v[1-9][0-9]{0,8}$`)
)

const Day = 24 * time.Hour

type Offer struct {
	Edition          edition.Key
	EditionVersionID string
	Days             int
}

// Policy is trusted server configuration. Callers select an offer, not dates or
// a bundle. Business is disabled until both its version and duration are set.
type Policy struct{ offers map[edition.Key]Offer }

func NewPolicy(developerVersion, teamVersion, businessVersion string, businessDays int) (*Policy, error) {
	p := &Policy{offers: map[edition.Key]Offer{
		edition.Developer: {edition.Developer, developerVersion, 14},
		edition.Team:      {edition.Team, teamVersion, 14},
	}}
	if businessVersion != "" || businessDays != 0 {
		p.offers[edition.Business] = Offer{edition.Business, businessVersion, businessDays}
	}
	for _, o := range p.offers {
		if !validOffer(o) {
			return nil, ErrInvalid
		}
	}
	return p, nil
}
func validOffer(o Offer) bool {
	if len(o.EditionVersionID) > 64 {
		return false
	}
	parts := versionPattern.FindStringSubmatch(o.EditionVersionID)
	if len(parts) != 2 || parts[1] != string(o.Edition) {
		return false
	}
	switch o.Edition {
	case edition.Developer, edition.Team:
		return o.Days == 14
	case edition.Business:
		return o.Days >= 1 && o.Days <= 365
	default:
		return false
	}
}
func (p *Policy) Offer(key edition.Key) (Offer, error) {
	if p != nil {
		if o, ok := p.offers[key]; ok {
			return o, nil
		}
	}
	return Offer{}, ErrUnavailable
}

type Trial struct {
	OrganizationID   string      `json:"organization_id"`
	TrialID          string      `json:"trial_id"`
	Edition          edition.Key `json:"edition"`
	EditionVersionID string      `json:"edition_version_id"`
	StartsAt         time.Time   `json:"starts_at"`
	ExpiresAt        time.Time   `json:"expires_at"`
}

func identifier(parts string) string { return fmt.Sprintf("trial_%x", sha256.Sum256([]byte(parts))) }

func New(org string, offer Offer, at time.Time) (Trial, error) {
	if !organization.ValidID(org) || !validOffer(offer) {
		return Trial{}, ErrInvalid
	}
	v := Trial{org, identifier(org), offer.Edition, offer.EditionVersionID, at, at.Add(time.Duration(offer.Days) * Day)}
	if err := v.Validate(); err != nil {
		return Trial{}, err
	}
	return v, nil
}
func (v Trial) Validate() error {
	if !organization.ValidID(v.OrganizationID) || v.TrialID != identifier(v.OrganizationID) ||
		!organization.ValidTime(v.StartsAt) || !organization.ValidTime(v.ExpiresAt) || !v.ExpiresAt.After(v.StartsAt) {
		return ErrInvalid
	}
	d := v.ExpiresAt.Sub(v.StartsAt)
	if d%Day != 0 || !validOffer(Offer{v.Edition, v.EditionVersionID, int(d / Day)}) {
		return ErrInvalid
	}
	return nil
}

// Repository atomically starts at most one lifetime trial per organization and
// publishes its canonical grants. Repeating the same edition returns the original
// record, even after expiry or offer changes; another edition returns ErrUsed.
// Only trusted application composition should receive this persistence port.
type Repository interface {
	Start(context.Context, string, edition.Key) (Trial, error)
	Get(context.Context, string) (Trial, error)
}

func Derive(v Trial, bundle edition.Bundle, catalog *feature.FeatureCatalog) ([]entitlement.Entitlement, error) {
	if v.Validate() != nil || bundle.Version.EditionVersionID != v.EditionVersionID || bundle.Version.EditionKey != v.Edition || bundle.Version.CreatedAt.After(v.StartsAt) || len(bundle.Features) == 0 {
		return nil, ErrInvalid
	}
	c, err := edition.NewCatalog(catalog)
	if err != nil {
		return nil, err
	}
	c, err = c.Publish(bundle.Version, bundle.Features)
	if err != nil {
		return nil, err
	}
	bundle, err = c.GetVersion(v.EditionVersionID)
	if err != nil {
		return nil, err
	}
	grants := make([]entitlement.Entitlement, 0, len(bundle.Features))
	for _, m := range bundle.Features {
		e, err := entitlement.NewEntitlement(entitlement.Entitlement{
			EntitlementID: identifier(v.TrialID + ":" + m.FeatureID), OrganizationID: v.OrganizationID,
			FeatureID: m.FeatureID, Source: entitlement.SourceTrial, SourceReference: v.TrialID,
			Enabled: m.Enabled, LimitValue: m.LimitValue, StartsAt: v.StartsAt, ExpiresAt: &v.ExpiresAt, CreatedAt: v.StartsAt,
		}, catalog)
		if err != nil {
			return nil, err
		}
		grants = append(grants, e)
	}
	return grants, nil
}
