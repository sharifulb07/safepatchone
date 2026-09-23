package billing_test

import (
	"errors"
	"strings"
	"testing"

	"safepatch/internal/domain/billing"
)

func TestRequestsRequireBoundedOrganizationIdentity(t *testing.T) {
	for _, id := range []string{"", "../tenant", "org\nheader", strings.Repeat("x", 129)} {
		for _, request := range []interface{ Validate() error }{
			billing.CheckoutRequest{OrganizationID: id, OfferID: "offer", IdempotencyKey: "attempt"},
			billing.CheckoutRequest{OrganizationID: "org", OfferID: id, IdempotencyKey: "attempt"},
			billing.CheckoutRequest{OrganizationID: "org", OfferID: "offer", IdempotencyKey: id},
			billing.PortalRequest{OrganizationID: id, IdempotencyKey: "attempt"},
			billing.PortalRequest{OrganizationID: "org", IdempotencyKey: id},
			billing.SubscriptionRequest{OrganizationID: id, ExternalReference: "external"},
			billing.SubscriptionRequest{OrganizationID: "org", ExternalReference: id},
		} {
			if !errors.Is(request.Validate(), billing.ErrInvalidRequest) {
				t.Fatal("unsafe billing identity accepted")
			}
		}
	}
	// Opaque provider identities are not constrained to Stripe object prefixes.
	for _, external := range []string{"marketplace_purchase_1", "aws_contract_1", "manual_agreement_1"} {
		if err := (billing.SubscriptionRequest{OrganizationID: "org", ExternalReference: external}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
