package paddle

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
	"strconv"
	"strings"
	"testing"
	"time"
)

const price = "pri_01h04vsc0qhwtsbsxh3422wjs4"
const subID = "sub_01h04vsc0qhwtsbsxh3422wjs4"
const txnID = "txn_01h04vsc0qhwtsbsxh3422wjs4"
const customerID = "ctm_01h04vsc0qhwtsbsxh3422wjs4"
const eventID = "evt_01h04vsc0qhwtsbsxh3422wjs4"
const secret = "pdl_ntfset_fixture_secret_not_real"

func now() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }

type fixtureDirectory struct {
	intent    Intent
	bound     bool
	bindCalls int
	session   billing.Session
}

func (d *fixtureDirectory) CustomerForOrganization(_ context.Context, p, o string) (string, error) {
	if p != Provider || o != "org_a" || !d.bound {
		return "", billing.ErrOwnership
	}
	return customerID, nil
}
func (d *fixtureDirectory) OrganizationForCustomer(_ context.Context, p, c string) (string, error) {
	if p != Provider || c != customerID || !d.bound {
		return "", billing.ErrOwnership
	}
	return "org_a", nil
}
func (d *fixtureDirectory) Lookup(_ context.Context, id string) (Intent, error) {
	if id != d.intent.ID {
		return Intent{}, billing.ErrOwnership
	}
	return d.intent, nil
}
func (d *fixtureDirectory) Bind(_ context.Context, i Intent, c, s string) error {
	if i.ID != d.intent.ID || c != customerID || s != subID {
		return billing.ErrOwnership
	}
	d.bound = true
	d.intent.SubscriptionID = s
	d.bindCalls++
	return nil
}
func (d *fixtureDirectory) Checkout(_ context.Context, r billing.CheckoutRequest, create func(string) (billing.Session, error)) (billing.Session, error) {
	if r.OrganizationID != "org_a" {
		return billing.Session{}, billing.ErrOwnership
	}
	if d.session.URL != "" {
		return d.session, nil
	}
	s, e := create(d.intent.ID)
	if e == nil {
		d.session = s
		d.intent.TransactionID = s.ExternalReference
	}
	return s, e
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: http.Header{}}
}
func items() []item {
	var v []item
	_ = json.Unmarshal([]byte(`[{"quantity":1,"price":{"id":"`+price+`","billing_cycle":{"interval":"month","frequency":1}}}]`), &v)
	return v
}
func transactionFixture() transaction {
	v := transaction{ID: txnID, CustomerID: customerID, SubscriptionID: subID, Status: "completed", CollectionMode: "automatic", Items: items(), CustomData: map[string]json.RawMessage{"safepatch_checkout": json.RawMessage(`"intent"`)}, BillingPeriod: &period{now().Add(-time.Hour), now().AddDate(0, 1, 0)}}
	return v
}
func subscriptionFixture() subscription {
	return subscription{ID: subID, CustomerID: customerID, Status: "active", CollectionMode: "automatic", CreatedAt: now().Add(-time.Hour), Items: items(), CustomData: map[string]json.RawMessage{"safepatch_checkout": json.RawMessage(`"intent"`)}, CurrentBillingPeriod: &period{now().Add(-time.Hour), now().AddDate(0, 1, 0)}}
}
func config(t *testing.T) (Config, *fixtureDirectory) {
	t.Helper()
	features, _ := feature.NewBuiltinCatalog("1.0.0", now())
	catalog, _ := edition.NewCatalog(features)
	catalog, err := catalog.Publish(edition.EditionVersion{EditionVersionID: "team-2026-v1", EditionKey: edition.Team, CreatedAt: now()}, []edition.EditionFeature{{EditionVersionID: "team-2026-v1", FeatureID: "feat_0011", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	d := &fixtureDirectory{intent: Intent{ID: "intent", OrganizationID: "org_a", OfferID: "team", TransactionID: txnID, SubscriptionID: subID}, bound: true}
	c := Config{APIKey: "pdl_sdbx_apikey_fixture_only_not_real", Environment: "sandbox", WebhookSecrets: []string{secret}, CheckoutURL: "https://app.example.test/checkout", Offers: []Offer{{"team", "team-2026-v1", price}}, Catalog: catalog, Customers: d, Checkouts: d, Now: now, Transport: transport(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected provider request")
		return nil, billing.ErrProvider
	})}
	return c, d
}
func adapter(t *testing.T, c Config) *Adapter {
	t.Helper()
	a, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func delivery(body string, at time.Time) billing.WebhookDelivery {
	stamp := strconv.FormatInt(at.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(stamp + ":" + body))
	return billing.WebhookDelivery{Payload: []byte(body), Signature: "ts=" + stamp + ";h1=" + hex.EncodeToString(m.Sum(nil))}
}
func eventBody(kind, id string) string {
	return `{"event_id":"` + eventID + `","event_type":"` + kind + `","occurred_at":"2026-09-27T11:59:59Z","data":{"id":"` + id + `","subscription_id":"` + subID + `"}}`
}
func TestSignatureAuthenticatesExactBodyAndRejectsReplay(t *testing.T) {
	c, _ := config(t)
	a := adapter(t, c)
	raw := eventBody("subscription.updated", subID)
	good := delivery(raw, now())
	if _, err := a.VerifyWebhook(t.Context(), good); err != nil {
		t.Fatal(err)
	}
	for _, d := range []billing.WebhookDelivery{delivery(raw, now().Add(-6*time.Second)), delivery(raw, now().Add(6*time.Second)), {Payload: append([]byte(raw), ' '), Signature: good.Signature}, {Payload: good.Payload, Signature: good.Signature + ";ts=" + strconv.FormatInt(now().Unix(), 10)}, {Payload: good.Payload, Signature: "ts=1;h1=bad"}} {
		if _, e := a.VerifyWebhook(t.Context(), d); !errors.Is(e, billing.ErrUnverifiedEvent) {
			t.Fatal("accepted invalid signature", e)
		}
	}
	if _, e := a.VerifyWebhook(t.Context(), delivery(strings.Replace(raw, `"event_id":`, `"event_id":"duplicate","event_id":`, 1), now())); !errors.Is(e, billing.ErrInvalidData) {
		t.Fatal("duplicate field", e)
	}
	h, _ := a.VerifyWebhook(t.Context(), good)
	other := adapter(t, c)
	if _, e := other.SubscriptionNotification(t.Context(), h); !errors.Is(e, billing.ErrUnverifiedEvent) {
		t.Fatal("foreign handle", e)
	}
}
func TestCheckoutCreatesOnlyConfiguredTransactionAndDoesNotGrantAccess(t *testing.T) {
	c, d := config(t)
	d.bound = false
	calls := 0
	c.Transport = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "sandbox-api.paddle.com" || r.URL.Path != "/transactions" || r.Method != "POST" || r.Header.Get("Paddle-Version") != "1" || r.Header.Get("Authorization") != "Bearer "+c.APIKey {
			t.Fatal("unsafe request", r.URL)
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("body")
		}
		if body["customer_id"] != nil || body["collection_mode"] != "automatic" {
			t.Fatal(body)
		}
		v := transactionFixture()
		v.Status = "draft"
		v.SubscriptionID = ""
		return response(map[string]any{"data": v}), nil
	})
	a := adapter(t, c)
	r := billing.CheckoutRequest{OrganizationID: "org_a", OfferID: "team", IdempotencyKey: "attempt"}
	for range 2 {
		session, e := a.CreateCheckout(t.Context(), r)
		if e != nil || session.URL != "https://app.example.test/checkout?_ptxn="+txnID {
			t.Fatal(session, e)
		}
	}
	if calls != 1 || d.bound || d.bindCalls != 0 {
		t.Fatal("checkout granted ownership or duplicated", calls)
	}
	r.OfferID = "enterprise"
	if _, e := a.CreateCheckout(t.Context(), r); !errors.Is(e, billing.ErrUnsupported) {
		t.Fatal(e)
	}
}
func TestOwnershipRequiresStoredTransactionSubscriptionAndCustomer(t *testing.T) {
	for _, mode := range []string{"valid", "wrong_subscription", "wrong_customer", "unpaid", "wrong_price", "unknown_intent"} {
		t.Run(mode, func(t *testing.T) {
			c, d := config(t)
			d.bound = false
			s := subscriptionFixture()
			txn := transactionFixture()
			switch mode {
			case "wrong_subscription":
				txn.SubscriptionID = "sub_01h04vsc0qhwtsbsxh3422wjs5"
			case "wrong_customer":
				txn.CustomerID = "ctm_01h04vsc0qhwtsbsxh3422wjs5"
			case "unpaid":
				txn.Status = "paid"
			case "wrong_price":
				txn.Items[0].Price.ID = "pri_01h04vsc0qhwtsbsxh3422wjs5"
			case "unknown_intent":
				s.CustomData["safepatch_checkout"] = json.RawMessage(`"forged"`)
			}
			c.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/subscriptions/"+subID {
					return response(map[string]any{"data": s}), nil
				}
				if r.URL.Path == "/transactions/"+txnID {
					return response(map[string]any{"data": txn}), nil
				}
				t.Fatal(r.URL)
				return nil, billing.ErrProvider
			})
			a := adapter(t, c)
			h, e := a.VerifyWebhook(t.Context(), delivery(eventBody("subscription.created", subID), now()))
			if e != nil {
				t.Fatal(e)
			}
			n, e := a.SubscriptionNotification(t.Context(), h)
			if mode == "valid" {
				if e != nil || n.OrganizationID != "org_a" || !d.bound {
					t.Fatal(n, e)
				}
			} else if e == nil || d.bound {
				t.Fatal("forged ownership accepted", n, e)
			}
		})
	}
}
func TestCurrentSubscriptionAndSettlementControlAccess(t *testing.T) {
	for _, mode := range []string{"active", "canceled", "paused", "past_due", "old_payment", "different_price", "no_payment", "trialing", "wrong_owner", "wrong_binding"} {
		t.Run(mode, func(t *testing.T) {
			c, d := config(t)
			s := subscriptionFixture()
			txn := transactionFixture()
			owner := "org_a"
			switch mode {
			case "canceled", "paused", "past_due", "trialing":
				s.Status = mode
			case "old_payment":
				txn.BillingPeriod.EndsAt = now()
			case "different_price":
				txn.Items[0].Price.ID = "pri_01h04vsc0qhwtsbsxh3422wjs5"
			case "wrong_owner":
				owner = "org_b"
			case "wrong_binding":
				d.intent.SubscriptionID = "sub_01h04vsc0qhwtsbsxh3422wjs5"
			}
			c.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/subscriptions/"+subID {
					return response(map[string]any{"data": s}), nil
				}
				if r.URL.Path == "/transactions" {
					rows := []transaction{txn}
					if mode == "no_payment" {
						rows = nil
					}
					return response(map[string]any{"data": rows}), nil
				}
				t.Fatal(r.URL)
				return nil, billing.ErrProvider
			})
			a := adapter(t, c)
			v, e := a.ReadSubscription(t.Context(), billing.SubscriptionRequest{OrganizationID: owner, ExternalReference: subID})
			if mode == "wrong_owner" || mode == "wrong_binding" || mode == "trialing" {
				if e == nil {
					t.Fatal("invalid accepted", v)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if v.PaymentSettled != (mode == "active") {
				t.Fatal("incorrect paid access", v)
			}
			if mode == "canceled" && v.State != org.Canceled {
				t.Fatal(v)
			}
			if (mode == "paused" || mode == "past_due") && v.State != org.PastDue {
				t.Fatal(v)
			}
		})
	}
}
func TestConfigurationAndProviderFailuresFailClosed(t *testing.T) {
	for _, edit := range []func(*Config){func(c *Config) { c.Environment = "" }, func(c *Config) { c.Environment = "production" }, func(c *Config) { c.CheckoutURL = "https://evil.example/checkout?redirect=bad" }, func(c *Config) { c.Offers = append(c.Offers, c.Offers[0]) }, func(c *Config) { c.WebhookSecrets = nil }} {
		c, _ := config(t)
		edit(&c)
		if _, e := New(c); e == nil {
			t.Fatal("bad config accepted")
		}
	}
	for _, status := range []int{302, 401, 429, 500} {
		c, _ := config(t)
		c.Transport = transport(func(*http.Request) (*http.Response, error) {
			r := response(map[string]string{"secret": "must not leak"})
			r.StatusCode = status
			return r, nil
		})
		_, e := adapter(t, c).ReadSubscription(t.Context(), billing.SubscriptionRequest{OrganizationID: "org_a", ExternalReference: subID})
		if !errors.Is(e, billing.ErrProvider) {
			t.Fatal(e)
		}
	}
}
func TestPortalRejectsForeignCustomerAndRedirect(t *testing.T) {
	for _, mode := range []string{"valid", "wrong_customer", "wrong_host"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := config(t)
			customer := customerID
			target := "https://sandbox-customer-portal.paddle.com/cpl_01h04vsc0qhwtsbsxh3422wjs4?token=fixture&action=overview"
			if mode == "wrong_customer" {
				customer = "ctm_01h04vsc0qhwtsbsxh3422wjs5"
			}
			if mode == "wrong_host" {
				target = "https://evil.example/"
			}
			c.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/customers/"+customerID+"/portal-sessions" {
					t.Fatal(r.URL)
				}
				return response(map[string]any{"data": map[string]any{"id": "cpls_01h04vsc0qhwtsbsxh3422wjs4", "customer_id": customer, "urls": map[string]any{"general": map[string]string{"overview": target}}}}), nil
			})
			v, e := adapter(t, c).CreateCustomerPortal(t.Context(), billing.PortalRequest{OrganizationID: "org_a", IdempotencyKey: "attempt"})
			if mode == "valid" {
				if e != nil || v.URL != "https://sandbox-customer-portal.paddle.com/cpl_01h04vsc0qhwtsbsxh3422wjs4" || strings.Contains(v.URL, "token") {
					t.Fatal(v, e)
				}
			} else if e == nil {
				t.Fatal("unsafe portal accepted")
			}
		})
	}
}

type integrationStore struct {
	a       *Adapter
	applied map[string]bool
	state   billing.SubscriptionSnapshot
	writes  int
}

func (s *integrationStore) Accept(_ context.Context, n billing.Notification) error {
	return n.Validate()
}
func (s *integrationStore) Reconcile(ctx context.Context, id string, r billing.SubscriptionReader) (bool, error) {
	if s.applied[id] {
		return true, nil
	}
	v, e := r.ReadSubscription(ctx, billing.SubscriptionRequest{OrganizationID: "org_a", ExternalReference: subID})
	if e == nil {
		s.applied[id] = true
		s.state = v
		s.writes++
	}
	return false, e
}
func TestDuplicateAndOlderEventsReconcileCurrentState(t *testing.T) {
	c, _ := config(t)
	current := subscriptionFixture()
	txn := transactionFixture()
	c.Transport = transport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/subscriptions/" + subID:
			return response(map[string]any{"data": current}), nil
		case "/transactions/" + txnID:
			return response(map[string]any{"data": txn}), nil
		case "/transactions":
			return response(map[string]any{"data": []transaction{txn}}), nil
		}
		t.Fatal(r.URL)
		return nil, billing.ErrProvider
	})
	a := adapter(t, c)
	store := &integrationStore{a: a, applied: map[string]bool{}}
	integration, _ := NewIntegration(a, store)
	body := eventBody("subscription.activated", subID)
	for range 2 {
		if e := integration.Handle(t.Context(), delivery(body, now())); e != nil {
			t.Fatal(e)
		}
	}
	if store.writes != 1 || !store.state.PaymentSettled {
		t.Fatal(store)
	}
	current.Status = "canceled"
	old := strings.ReplaceAll(strings.Replace(body, eventID, "evt_01h04vsc0qhwtsbsxh3422wjs5", 1), "2026-09-27T11:59:59Z", "2026-09-26T11:59:59Z")
	if e := integration.Handle(t.Context(), delivery(old, now())); e != nil {
		t.Fatal(e)
	}
	if store.state.State != org.Canceled || store.state.PaymentSettled {
		t.Fatal("stale event restored access", store.state)
	}
}
