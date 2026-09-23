package stripe

import (
	"context"
	"encoding/json"
	"safepatch/internal/domain/billing"
	"time"
)

// SubscriptionNotification extracts only verified routing identity. Historic
// prices/statuses are not applied: reconciliation retrieves the current object.
func (a *StripeBillingAdapter) SubscriptionNotification(ctx context.Context, h billing.VerifiedWebhookEvent) (billing.Notification, error) {
	fail := billing.Notification{}
	if err := a.check(ctx); err != nil {
		return fail, err
	}
	v, ok := h.(*verifiedEvent)
	if !ok || v == nil || v.adapter != a || !a.fresh(v.signedAt) {
		return fail, billing.ErrUnverifiedEvent
	}
	var object struct {
		ID, Object, Customer string
		Metadata             map[string]string
		Parent               struct {
			Type                string
			SubscriptionDetails struct{ Subscription string } `json:"subscription_details"`
		}
	}
	if json.Unmarshal(v.event.Data.Object, &object) != nil {
		return fail, billing.ErrInvalidData
	}
	reference := ""
	switch v.event.Type {
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted", "customer.subscription.paused", "customer.subscription.resumed":
		if object.Object != "subscription" {
			return fail, billing.ErrInvalidData
		}
		reference = object.ID
	case "invoice.paid", "invoice.payment_failed", "invoice.payment_action_required":
		if object.Object != "invoice" || object.Parent.Type != "subscription_details" {
			return fail, billing.ErrUnsupportedEvent
		}
		reference = object.Parent.SubscriptionDetails.Subscription
	default:
		return fail, billing.ErrUnsupportedEvent
	}
	if !providerID(object.Customer, "cus_") || !providerID(reference, "sub_") {
		return fail, billing.ErrInvalidData
	}
	owner, err := a.customers.OrganizationForCustomer(ctx, Provider, object.Customer)
	if err != nil {
		return fail, billing.ErrOwnership
	}
	customer, err := a.customer(ctx, owner)
	if err != nil || customer != object.Customer {
		return fail, billing.ErrOwnership
	}
	if object.Object == "subscription" && object.Metadata["organization_id"] != owner {
		return fail, billing.ErrOwnership
	}
	n := billing.Notification{Provider: Provider, EventID: v.event.ID, OrganizationID: owner, ExternalReference: reference, OccurredAt: time.Unix(v.event.Created, 0).UTC()}
	return n, n.Validate()
}

type SubscriptionIntegration struct {
	adapter *StripeBillingAdapter
	store   billing.SubscriptionStore
}

func NewSubscriptionIntegration(a *StripeBillingAdapter, store billing.SubscriptionStore) (*SubscriptionIntegration, error) {
	if a == nil || store == nil {
		return nil, billing.ErrInvalidConfiguration
	}
	return &SubscriptionIntegration{a, store}, nil
}
func (s *SubscriptionIntegration) Handle(ctx context.Context, d billing.WebhookDelivery) error {
	if s == nil || s.adapter == nil || s.store == nil {
		return billing.ErrInvalidConfiguration
	}
	h, err := s.adapter.VerifyWebhook(ctx, d)
	if err != nil {
		return err
	}
	n, err := s.adapter.SubscriptionNotification(ctx, h)
	if err != nil {
		return err
	}
	if err = s.store.Accept(ctx, n); err != nil {
		return err
	}
	_, err = s.store.Reconcile(ctx, n.EventID, s.adapter)
	return err
}

// Retry uses a durably accepted notification, never a deserialized verification handle.
func (s *SubscriptionIntegration) Retry(ctx context.Context, eventID string) error {
	if s == nil || s.store == nil || s.adapter == nil {
		return billing.ErrInvalidConfiguration
	}
	_, err := s.store.Reconcile(ctx, eventID, s.adapter)
	return err
}
