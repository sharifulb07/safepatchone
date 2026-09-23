package stripe

import (
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
)

type SubscriptionProduct struct{ Name, OfferID, EditionVersionID, PriceID string }

// SubscriptionProducts is merchandising configuration, not feature admission.
// Real recurring Stripe price IDs are provisioned externally; no fake live IDs.
func SubscriptionProducts(catalog *edition.Catalog, developerPrice, teamPrice, businessPrice string) ([]SubscriptionProduct, error) {
	products := []SubscriptionProduct{{"SafePatch Developer", "developer_subscription", "developer-2026-v2", developerPrice}, {"SafePatch Team", "team_subscription", "team-2026-v2", teamPrice}, {"SafePatch Business", "business_subscription", "business-2026-v2", businessPrice}}
	seen := map[string]bool{}
	for _, p := range products {
		if !providerID(p.PriceID, "price_") || seen[p.PriceID] {
			return nil, billing.ErrInvalidConfiguration
		}
		seen[p.PriceID] = true
		if _, err := catalog.GetVersion(p.EditionVersionID); err != nil {
			return nil, billing.ErrInvalidConfiguration
		}
	}
	return products, nil
}
func ProductOffers(products []SubscriptionProduct) []Offer {
	result := make([]Offer, 0, len(products))
	for _, p := range products {
		result = append(result, Offer{p.OfferID, p.EditionVersionID, p.PriceID})
	}
	return result
}
