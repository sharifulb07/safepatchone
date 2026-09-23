package stripe

import (
	"context"
	"net/url"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/organization"
)

// CreateOrganizationCustomer is a trusted provisioning primitive. Its caller
// must authorize billing.manage and persist the account-scoped binding before
// checkout. It creates no subscription or entitlement.
func (a *StripeBillingAdapter) CreateOrganizationCustomer(ctx context.Context, org string) (string, error) {
	if !organization.ValidID(org) {
		return "", billing.ErrInvalidRequest
	}
	if err := a.check(ctx); err != nil {
		return "", err
	}
	var result struct {
		ID       string `json:"id"`
		LiveMode *bool  `json:"livemode"`
	}
	err := a.request(ctx, "POST", "customers", url.Values{"metadata[organization_id]": {org}}, billing.StableID("customer", org), &result)
	if err != nil {
		return "", err
	}
	if !providerID(result.ID, "cus_") || result.LiveMode == nil || *result.LiveMode != a.live {
		return "", billing.ErrInvalidData
	}
	return result.ID, nil
}
