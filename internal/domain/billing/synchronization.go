package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"safepatch/internal/domain/edition"
	grants "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
)

// Notification is trusted ingress output, not a public client command. Its
// identity is persisted before fetching current provider state under an org lock.
type Notification struct {
	Provider, EventID, OrganizationID, ExternalReference string
	OccurredAt                                           time.Time
}

func (n Notification) Validate() error {
	if !org.ValidID(n.Provider) || !org.ValidID(n.EventID) || !org.ValidID(n.OrganizationID) || !org.ValidID(n.ExternalReference) || !org.ValidTime(n.OccurredAt) {
		return ErrInvalidData
	}
	return nil
}

type SubscriptionReader interface {
	ReadSubscription(context.Context, SubscriptionRequest) (SubscriptionSnapshot, error)
}
type SubscriptionStore interface {
	Accept(context.Context, Notification) error
	Reconcile(context.Context, string, SubscriptionReader) (bool, error) // true means already applied
}

func StableID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return "bill_" + hex.EncodeToString(h.Sum(nil))
}

// DeriveSubscriptionEntitlements uses only SafePatch's validated bundle and
// synchronized domain state. No provider feature names or Stripe API calls occur.
func DeriveSubscriptionEntitlements(s org.Subscription, bundle edition.Bundle, catalog *feature.FeatureCatalog, paid bool, until, at time.Time) ([]grants.Entitlement, error) {
	if s.Validate() != nil || !org.ValidTime(at) || bundle.Version.EditionVersionID != s.EditionVersionID {
		return nil, ErrInvalidData
	}
	c, err := edition.NewCatalog(catalog)
	if err != nil {
		return nil, err
	}
	c, err = c.Publish(bundle.Version, bundle.Features)
	if err != nil {
		return nil, err
	}
	bundle, err = c.GetVersion(s.EditionVersionID)
	if err != nil {
		return nil, err
	}
	result := []grants.Entitlement{}
	if (s.State != org.Subscribed && s.State != org.Trialing) || (s.State == org.Subscribed && !paid) {
		return result, nil
	}
	if !org.ValidTime(until) {
		return nil, ErrInvalidData
	}
	if !until.After(at) {
		return result, nil
	}
	for _, m := range bundle.Features {
		e, err := grants.NewEntitlement(grants.Entitlement{EntitlementID: StableID(s.SubscriptionID, m.FeatureID), OrganizationID: s.OrganizationID, FeatureID: m.FeatureID, Source: grants.SourceSubscription, SourceReference: s.SubscriptionID, Enabled: m.Enabled, LimitValue: m.LimitValue, StartsAt: at, ExpiresAt: &until, CreatedAt: s.UpdatedAt}, catalog)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, nil
}
