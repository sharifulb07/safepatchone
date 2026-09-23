package stripe_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	stripe "safepatch/internal/billing/stripe"
	configuration "safepatch/internal/config"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
)

// Application-flow double only; PostgreSQL commit/rollback/receipt semantics
// are separately exercised in billingstore and native migration fixtures.
type syncStore struct {
	mu      sync.Mutex
	pending map[string]billing.Notification
	applied map[string]bool
	value   billing.SubscriptionSnapshot
	failure bool
}

func (s *syncStore) Accept(_ context.Context, n billing.Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.pending[n.EventID]; ok && old != n {
		return billing.ErrOwnership
	}
	s.pending[n.EventID] = n
	return nil
}
func (s *syncStore) Reconcile(ctx context.Context, id string, r billing.SubscriptionReader) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.applied[id] {
		return true, nil
	}
	if s.failure {
		return false, billing.ErrProvider
	}
	n := s.pending[id]
	v, err := r.ReadSubscription(ctx, billing.SubscriptionRequest{OrganizationID: n.OrganizationID, ExternalReference: n.ExternalReference})
	if err != nil {
		return false, err
	}
	s.value = v
	s.applied[id] = true
	return false, nil
}
func TestSubscriptionIntegrationRetriesDuplicatesAndOutOfOrderEvents(t *testing.T) {
	c := config(t)
	current := subscriptionBody("active", "price_team")
	calls := 0
	c.Transport = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("expand[]") != "latest_invoice" {
			t.Error("invoice not expanded")
		}
		return response(200, current), nil
	})
	a := adapter(t, c)
	store := &syncStore{pending: map[string]billing.Notification{}, applied: map[string]bool{}, failure: true}
	integration, err := stripe.NewSubscriptionIntegration(a, store)
	if err != nil {
		t.Fatal(err)
	}
	old := eventBody("customer.subscription.updated", subscriptionBody("active", "price_team"))
	delivery := billing.WebhookDelivery{Payload: old, Signature: sign(old, secret, now())}
	if err := integration.Handle(t.Context(), delivery); err == nil || len(store.pending) != 1 || len(store.applied) != 0 {
		t.Fatal("failed work lost its receipt", err)
	}
	store.failure = false
	if err := integration.Retry(t.Context(), "evt_a"); err != nil {
		t.Fatal(err)
	}
	if !store.value.PaymentSettled || store.value.AccessUntil.IsZero() {
		t.Fatal("missing paid boundary")
	}
	if err := integration.Handle(t.Context(), delivery); err != nil || calls != 1 {
		t.Fatal("duplicate performed work", err, calls)
	}
	var concurrent sync.WaitGroup
	for range 8 {
		concurrent.Go(func() {
			if err := integration.Handle(t.Context(), delivery); err != nil {
				t.Error(err)
			}
		})
	}
	concurrent.Wait()
	if calls != 1 {
		t.Fatal("concurrent duplicates fetched provider", calls)
	}
	// The delayed payload says Team, but the current Stripe object says Developer.
	current = subscriptionBody("active", "price_developer")
	old = []byte(strings.Replace(string(old), "evt_a", "evt_delayed", 1))
	delivery = billing.WebhookDelivery{Payload: old, Signature: sign(old, secret, now())}
	if err := integration.Handle(t.Context(), delivery); err != nil || store.value.EditionVersionID != "developer-2026-v1" {
		t.Fatal("stale event restored old plan", store.value, err)
	}
	current = subscriptionBody("canceled", "price_developer")
	deleted := eventBody("customer.subscription.deleted", subscriptionBody("canceled", "price_developer"))
	deleted = []byte(strings.Replace(string(deleted), "evt_a", "evt_deleted", 1))
	if err := integration.Handle(t.Context(), billing.WebhookDelivery{Payload: deleted, Signature: sign(deleted, secret, now())}); err != nil || store.value.State != "CANCELED" {
		t.Fatal(store.value, err)
	}
}
func TestNotificationRoutingDoesNotTrustHistoricalPriceOrEntitlementEvents(t *testing.T) {
	a := adapter(t, config(t))
	for _, kind := range []string{"customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted", "customer.subscription.paused", "customer.subscription.resumed", "invoice.paid", "invoice.payment_failed", "invoice.payment_action_required"} {
		body := subscriptionBody("UNKNOWN", "price_retired")
		if strings.HasPrefix(kind, "invoice.") {
			body = `{"id":"in_a","object":"invoice","customer":"cus_a","parent":{"type":"subscription_details","subscription_details":{"subscription":"sub_a"}}}`
		}
		raw := eventBody(kind, body)
		h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: raw, Signature: sign(raw, secret, now())})
		if err != nil {
			t.Fatal(err)
		}
		n, err := a.SubscriptionNotification(t.Context(), h)
		if err != nil || n.OrganizationID != "org_a" || n.ExternalReference != "sub_a" {
			t.Fatal(kind, n, err)
		}
	}
	raw := eventBody("entitlements.active_entitlement_summary.updated", `{}`)
	h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: raw, Signature: sign(raw, secret, now())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SubscriptionNotification(t.Context(), h); !errors.Is(err, billing.ErrUnsupportedEvent) {
		t.Fatal("Stripe entitlements interpreted", err)
	}
	if _, err := a.SubscriptionNotification(t.Context(), forgedEvent{}); !errors.Is(err, billing.ErrUnverifiedEvent) {
		t.Fatal(err)
	}
}
func TestConfiguredSubscriptionProductsUseOwnPublishedBundles(t *testing.T) {
	features, _ := feature.NewBuiltinCatalog("1.0.0", now())
	matrix, err := configuration.V02Editions(features)
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := edition.NewCatalog(features)
	for _, e := range matrix.Editions {
		catalog, err = catalog.Publish(e.Bundle.Version, e.Bundle.Features)
		if err != nil {
			t.Fatal(err)
		}
	}
	products, err := stripe.SubscriptionProducts(catalog, "price_dev", "price_team", "price_business")
	if err != nil || len(products) != 3 {
		t.Fatal(products, err)
	}
	names := []string{"SafePatch Developer", "SafePatch Team", "SafePatch Business"}
	for i, p := range products {
		if p.Name != names[i] {
			t.Fatal(p)
		}
	}
	if len(stripe.ProductOffers(products)) != 3 {
		t.Fatal("missing offers")
	}
	if _, err := stripe.SubscriptionProducts(catalog, "price_same", "price_same", "price_other"); err == nil {
		t.Fatal("ambiguous price mapping")
	}
}
func TestActiveSubscriptionRequiresOwnSettledInvoiceAndFinitePeriod(t *testing.T) {
	for _, mode := range []string{"open_invoice", "foreign_invoice", "missing_period", "missing_trial_end"} {
		t.Run(mode, func(t *testing.T) {
			body := subscriptionBody("active", "price_team")
			switch mode {
			case "open_invoice":
				body = strings.Replace(body, `"status":"paid"`, `"status":"open"`, 1)
			case "foreign_invoice":
				body = strings.Replace(body, `"subscription":"sub_a"`, `"subscription":"sub_other"`, 1)
			case "missing_period":
				body = strings.Replace(body, `"current_period_end":1791374400`, `"current_period_end":0`, 1)
			case "missing_trial_end":
				body = strings.Replace(subscriptionBody("trialing", "price_team"), `"trial_end":1791374400`, `"trial_end":0`, 1)
			}
			c := config(t)
			c.Transport = transport(func(*http.Request) (*http.Response, error) { return response(200, body), nil })
			v, err := adapter(t, c).ReadSubscription(t.Context(), billing.SubscriptionRequest{OrganizationID: "org_a", ExternalReference: "sub_a"})
			if mode == "missing_period" || mode == "missing_trial_end" {
				if err == nil {
					t.Fatal("unbounded grant")
				}
			} else if err != nil || v.PaymentSettled {
				t.Fatal(v, err)
			}
		})
	}
}
