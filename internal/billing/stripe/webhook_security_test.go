package stripe_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	stripe "safepatch/internal/billing/stripe"
	"safepatch/internal/domain/billing"
)

func TestWebhookRotationLimitsAndDelayedDelivery(t *testing.T) {
	c := config(t)
	c.WebhookSecrets = []string{"whsec_newFixtureSecret", secret}
	a := adapter(t, c)
	body := eventBody("customer.subscription.created", subscriptionBody("active", "price_team"))
	for _, key := range c.WebhookSecrets {
		h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: body, Signature: sign(body, key, now())})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.ProcessVerifiedWebhookEvent(t.Context(), h); err != nil {
			t.Fatal(err)
		}
	}
	// Old event creation is permitted when Stripe signs a new delivery now.
	old := []byte(strings.Replace(string(body), `"created":1788782400`, `"created":1788696000`, 1))
	if _, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: old, Signature: sign(old, secret, now())}); err != nil {
		t.Fatal("delayed delivery rejected", err)
	}
	for _, payload := range [][]byte{nil, []byte(strings.Repeat("x", stripe.MaxPayloadBytes+1))} {
		if _, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: payload, Signature: sign(payload, secret, now())}); !errors.Is(err, billing.ErrUnverifiedEvent) {
			t.Fatal(err)
		}
	}
	for _, header := range []string{"t=not-a-time,v1=" + strings.Repeat("00", 32), "t=1,t=2,v1=ab", sign(body, secret, now()) + strings.Repeat(",v1="+strings.Repeat("00", 32), 9), strings.Repeat("x", 4097)} {
		if _, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: body, Signature: header}); !errors.Is(err, billing.ErrUnverifiedEvent) {
			t.Fatal(err)
		}
	}
	// A fresh delivery signed under a retired key must no longer verify.
	c.WebhookSecrets = []string{"whsec_newFixtureSecret"}
	if _, err := adapter(t, c).VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: body, Signature: sign(body, secret, now().Add(time.Second))}); !errors.Is(err, billing.ErrUnverifiedEvent) {
		t.Fatal(err)
	}
}

func FuzzWebhookIngress(f *testing.F) {
	f.Add([]byte(`{}`), "t=1,v1=00")
	f.Add([]byte(`{"id":"evt_test","id":"evt_other"}`), "")
	f.Fuzz(func(t *testing.T, payload []byte, signature string) {
		if len(payload) > stripe.MaxPayloadBytes+1 || len(signature) > 4097 {
			t.Skip()
		}
		a := adapter(t, config(t))
		h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: payload, Signature: signature})
		if err != nil && h != nil {
			t.Fatal("failure returned verification authority")
		}
		// Exercise authenticated but malformed provider JSON too.
		h, err = a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: payload, Signature: sign(payload, secret, now())})
		if err == nil {
			_, _ = a.ProcessVerifiedWebhookEvent(t.Context(), h)
		}
	})
}

func TestCancellationRetainsTerminalStateWithCollectionPause(t *testing.T) {
	a := adapter(t, config(t))
	sub := strings.Replace(subscriptionBody("canceled", "price_team"), `"pause_collection":null`, `"pause_collection":{"behavior":"void"}`, 1)
	body := eventBody("customer.subscription.deleted", sub)
	h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: body, Signature: sign(body, secret, now())})
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.ProcessVerifiedWebhookEvent(t.Context(), h)
	if err != nil || e.Subscription.State != "CANCELED" {
		t.Fatal(e, err)
	}
}
