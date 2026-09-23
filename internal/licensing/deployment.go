package licensing

import (
	"context"
	"time"

	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

type DeploymentMode string

const (
	Connected DeploymentMode = "connected"
	AirGapped DeploymentMode = "air_gapped"
)

// DeploymentPolicy is operator configuration, not an edition or a feature grant.
// The zero value preserves the standalone signed-license provider contract.
type DeploymentPolicy struct {
	Mode            DeploymentMode `json:"mode"`
	RefreshInterval time.Duration  `json:"refresh_interval"`
	GracePeriod     time.Duration  `json:"grace_period"`
}

func (p DeploymentPolicy) Validate() error {
	switch p.Mode {
	case "", AirGapped:
		if p.RefreshInterval != 0 || p.GracePeriod != 0 {
			return ErrInvalidLicense
		}
	case Connected:
		if p.RefreshInterval < time.Minute || p.RefreshInterval > 24*time.Hour || p.GracePeriod < 0 || p.GracePeriod > 30*24*time.Hour {
			return ErrInvalidLicense
		}
	default:
		return ErrInvalidLicense
	}
	return nil
}

// LicenseAvailability reports connectivity separately from verification results.
// A grace state never converts an UNKNOWN proof or policy decision into PASS.
type LicenseAvailability struct {
	Mode         DeploymentMode `json:"mode"`
	State        string         `json:"state"`
	RefreshDueAt *time.Time     `json:"refresh_due_at,omitempty"`
	ValidUntil   time.Time      `json:"valid_until"`
}

func (p *EnterpriseLicenseEntitlementProvider) Availability(ctx context.Context, org string) (LicenseAvailability, error) {
	v, _, err := p.inspect(ctx, org)
	return v.availability, err
}
func (p *EnterpriseLicenseEntitlementProvider) availability(ctx context.Context, v VerifiedLicense, state LicenseState, now time.Time) (LicenseAvailability, error) {
	d, _ := v.Document()
	a := LicenseAvailability{Mode: p.deployment.Mode, State: "offline", ValidUntil: d.ExpiresAt}
	if p.deployment.Mode == "" {
		return a, nil
	}
	required := []feature.Key{feature.KeyEnterpriseSelfHosted}
	if p.deployment.Mode == AirGapped {
		required = append(required, feature.KeyEnterpriseAirgap)
	}
	for _, key := range required {
		r, err := p.resolveLicense(ctx, v, key, now)
		if err != nil {
			return LicenseAvailability{}, err
		}
		if !r.Enabled {
			return LicenseAvailability{}, entitlement.ErrNotEntitled
		}
	}
	if p.deployment.Mode == AirGapped {
		return a, nil
	}
	// The anchor is persisted. Neither process restart nor a failed attempt may
	// move it forward. Signed expiry remains a hard upper bound on outage grace.
	if state.ConfirmedAt.IsZero() || state.ConfirmedAt.Before(d.IssuedAt) || state.ConfirmedAt.After(now) {
		return LicenseAvailability{}, ErrInvalidLicense
	}
	due := state.ConfirmedAt.Add(p.deployment.RefreshInterval)
	a.RefreshDueAt = &due
	a.State = "current"
	deadline := due.Add(p.deployment.GracePeriod)
	if deadline.Before(a.ValidUntil) {
		a.ValidUntil = deadline
	}
	if !now.Before(a.ValidUntil) {
		return LicenseAvailability{}, ErrLicenseUnavailable
	}
	if !now.Before(due) {
		a.State = "grace"
	}
	return a, nil
}
