package stripe_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	stripe "safepatch/internal/billing/stripe"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
)

// Deliberately fake credentials; every test uses a local transport.
const apiKey = "sk_test_safePatchFixtureOnly"
const secret = "whsec_safePatchFixtureOnly"

func now() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }

type directory struct {
	forward, reverse map[string]string
	err              error
}

func (d directory) CustomerForOrganization(_ context.Context, provider, organization string) (string, error) {
	if provider != stripe.Provider {
		return "", billing.ErrOwnership
	}
	return d.forward[organization], d.err
}
func (d directory) OrganizationForCustomer(_ context.Context, provider, customer string) (string, error) {
	if provider != stripe.Provider {
		return "", billing.ErrOwnership
	}
	return d.reverse[customer], d.err
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}
func config(t *testing.T) stripe.Config {
	t.Helper()
	features, err := feature.NewBuiltinCatalog("1.0.0", now())
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := edition.NewCatalog(features)
	for _, key := range []edition.Key{edition.Team, edition.Developer} {
		catalog, err = catalog.Publish(edition.EditionVersion{EditionVersionID: string(key) + "-2026-v1", EditionKey: key, CreatedAt: now()}, []edition.EditionFeature{{EditionVersionID: string(key) + "-2026-v1", FeatureID: "feat_0011", Enabled: true}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return stripe.Config{APIKey: apiKey, WebhookSecrets: []string{secret}, Catalog: catalog,
		Offers:     []stripe.Offer{{OfferID: "collaboration_monthly", EditionVersionID: "team-2026-v1", PriceID: "price_team"}, {OfferID: "individual_monthly", EditionVersionID: "developer-2026-v1", PriceID: "price_developer"}},
		Customers:  directory{forward: map[string]string{"org_a": "cus_a", "org_b": "cus_b"}, reverse: map[string]string{"cus_a": "org_a", "cus_b": "org_b"}},
		SuccessURL: "https://app.safepatch.example/billing/success", CancelURL: "https://app.safepatch.example/billing/cancel", PortalReturnURL: "https://app.safepatch.example/billing", Now: now,
		Transport: transport(func(*http.Request) (*http.Response, error) {
			t.Error("unexpected provider request")
			return nil, errors.New("unexpected network")
		}),
	}
}
func adapter(t *testing.T, c stripe.Config) *stripe.StripeBillingAdapter {
	t.Helper()
	a, err := stripe.NewStripeBillingAdapter(c)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func subscriptionBody(status, price string) string {
	return `{"id":"sub_a","object":"subscription","customer":"cus_a","status":"` + status + `","livemode":false,"created":1788782400,"cancel_at_period_end":false,"metadata":{"organization_id":"org_a"},"trial_end":1791374400,"latest_invoice":{"object":"invoice","status":"paid","customer":"cus_a","parent":{"type":"subscription_details","subscription_details":{"subscription":"sub_a"}}},"pause_collection":null,"items":{"object":"list","has_more":false,"data":[{"quantity":1,"current_period_end":1791374400,"price":{"id":"` + price + `","object":"price","type":"recurring"}}]}}`
}
func eventBody(kind, sub string) []byte {
	return []byte(`{"id":"evt_a","object":"event","type":"` + kind + `","api_version":"` + stripe.APIVersion + `","livemode":false,"created":` + strconv.FormatInt(now().Unix(), 10) + `,"data":{"object":` + sub + `}}`)
}
func sign(body []byte, key string, at time.Time) string {
	stamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(stamp + "."))
	mac.Write(body)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
func checkoutResponse() string {
	return `{"id":"cs_test_a","object":"checkout.session","url":"https://checkout.stripe.com/c/pay/cs_test_a#fixture","mode":"subscription","customer":"cus_a","client_reference_id":"org_a","livemode":false}`
}
func portalResponse() string {
	return `{"id":"bps_a","object":"billing_portal.session","url":"https://billing.stripe.com/p/session?secret=fixture","customer":"cus_a","livemode":false}`
}

func TestStripeHTTPWorkflow(t *testing.T) {
	var keys []string
	var mu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, password, ok := r.BasicAuth()
		if !ok || key != apiKey || password != "" || r.Header.Get("Stripe-Version") != stripe.APIVersion {
			t.Error("incorrect authentication/version")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/checkout/sessions":
			if r.Method != http.MethodPost {
				t.Error(r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("mode") != "subscription" || r.Form.Get("customer") != "cus_a" || r.Form.Get("line_items[0][price]") != "price_team" || r.Form.Get("line_items[0][quantity]") != "1" || r.Form.Get("subscription_data[metadata][organization_id]") != "org_a" || r.Form.Get("client_reference_id") != "org_a" || r.Form.Get("success_url") != "https://app.safepatch.example/billing/success" {
				t.Error("incorrect checkout fields")
			}
			mu.Lock()
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			mu.Unlock()
			io.WriteString(w, checkoutResponse())
		case "/v1/billing_portal/sessions":
			if r.Method != http.MethodPost {
				t.Error(r.Method)
			}
			r.ParseForm()
			if r.Form.Get("customer") != "cus_a" || r.Form.Get("return_url") != "https://app.safepatch.example/billing" {
				t.Error("incorrect portal fields")
			}
			mu.Lock()
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			mu.Unlock()
			io.WriteString(w, portalResponse())
		case "/v1/subscriptions/sub_a":
			if r.Method != http.MethodGet {
				t.Error(r.Method)
			}
			io.WriteString(w, subscriptionBody("active", "price_team"))
		default:
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	localURL, _ := url.Parse(server.URL)
	c := config(t)
	c.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.stripe.com" {
			t.Error("unsafe provider origin")
		}
		copy := r.Clone(r.Context())
		target := *r.URL
		target.Scheme = localURL.Scheme
		target.Host = localURL.Host
		copy.URL = &target
		return server.Client().Transport.RoundTrip(copy)
	})
	a := adapter(t, c)
	for range 2 {
		if _, err := a.CreateCheckout(t.Context(), billing.CheckoutRequest{OrganizationID: "org_a", OfferID: "collaboration_monthly", IdempotencyKey: "attempt_1"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CreateCustomerPortal(t.Context(), billing.PortalRequest{OrganizationID: "org_a", IdempotencyKey: "attempt_1"}); err != nil {
		t.Fatal(err)
	}
	s, err := a.ReadSubscription(t.Context(), billing.SubscriptionRequest{OrganizationID: "org_a", ExternalReference: "sub_a"})
	if err != nil || s.State != org.Subscribed || s.EditionVersionID != "team-2026-v1" {
		t.Fatal(s, err)
	}
	if len(keys) != 3 || keys[0] == "" || keys[0] != keys[1] || keys[0] == keys[2] {
		t.Fatal("unsafe retry identity", keys)
	}
}

func TestProviderErrorsAndUnsafeResponsesFailClosed(t *testing.T) {
	for _, mode := range []string{"redirect", "error", "transport", "oversize", "duplicate", "bad_url", "wrong_customer", "missing_mode", "wrong_environment"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := config(t)
			c.Transport = transport(func(*http.Request) (*http.Response, error) {
				calls++
				body := checkoutResponse()
				switch mode {
				case "redirect":
					r := response(302, "")
					r.Header.Set("Location", "https://attacker.example/steal")
					return r, nil
				case "error":
					return response(400, apiKey+secret), nil
				case "transport":
					return nil, errors.New(apiKey + secret)
				case "oversize":
					body = strings.Repeat("x", stripe.MaxPayloadBytes+1)
				case "duplicate":
					body = strings.Replace(body, `"livemode":false`, `"livemode":true,"livemode":false`, 1)
				case "bad_url":
					body = strings.Replace(body, "checkout.stripe.com", "checkout.stripe.com.attacker.example", 1)
				case "wrong_customer":
					body = strings.Replace(body, "cus_a", "cus_b", 1)
				case "missing_mode":
					body = strings.Replace(body, `"livemode":false`, `"livemode":null`, 1)
				case "wrong_environment":
					body = strings.Replace(body, `"livemode":false`, `"livemode":true`, 1)
				}
				return response(200, body), nil
			})
			_, err := adapter(t, c).CreateCheckout(t.Context(), billing.CheckoutRequest{OrganizationID: "org_a", OfferID: "collaboration_monthly", IdempotencyKey: "attempt"})
			if err == nil || calls != 1 || strings.Contains(err.Error(), apiKey) || strings.Contains(err.Error(), secret) {
				t.Fatal("unsafe response", err, calls)
			}
		})
	}
}

func TestSubscriptionNormalizationAndTenantIsolation(t *testing.T) {
	for status, want := range map[string]org.SubscriptionState{"incomplete": org.SubscriptionPending, "trialing": org.Trialing, "active": org.Subscribed, "past_due": org.PastDue, "unpaid": org.PastDue, "paused": org.PastDue, "canceled": org.Canceled, "incomplete_expired": org.Expired} {
		t.Run(status, func(t *testing.T) {
			c := config(t)
			c.Transport = transport(func(*http.Request) (*http.Response, error) {
				return response(200, subscriptionBody(status, "price_team")), nil
			})
			got, err := adapter(t, c).ReadSubscription(t.Context(), billing.SubscriptionRequest{OrganizationID: "org_a", ExternalReference: "sub_a"})
			if err != nil || got.State != want {
				t.Fatal(got, err)
			}
		})
	}
	for _, mode := range []string{"foreign", "metadata", "unknown_state", "unknown_price", "truncated", "quantity", "missing_live", "id", "paused_collection"} {
		t.Run(mode, func(t *testing.T) {
			body := subscriptionBody("active", "price_team")
			organizationID := "org_a"
			switch mode {
			case "foreign":
				organizationID = "org_b"
			case "metadata":
				body = strings.Replace(body, `"organization_id":"org_a"`, `"organization_id":"org_b"`, 1)
			case "unknown_state":
				body = strings.Replace(body, `"status":"active"`, `"status":"UNKNOWN"`, 1)
			case "unknown_price":
				body = strings.Replace(body, "price_team", "price_unmapped", 1)
			case "truncated":
				body = strings.Replace(body, `"has_more":false`, `"has_more":true`, 1)
			case "quantity":
				body = strings.Replace(body, `"quantity":1`, `"quantity":2`, 1)
			case "missing_live":
				body = strings.Replace(body, `"livemode":false`, `"livemode":null`, 1)
			case "id":
				body = strings.Replace(body, "sub_a", "sub_b", 1)
			case "paused_collection":
				body = strings.Replace(body, `"pause_collection":null`, `"pause_collection":{"behavior":"void"}`, 1)
			}
			c := config(t)
			c.Transport = transport(func(*http.Request) (*http.Response, error) { return response(200, body), nil })
			got, err := adapter(t, c).ReadSubscription(t.Context(), billing.SubscriptionRequest{OrganizationID: organizationID, ExternalReference: "sub_a"})
			if mode == "paused_collection" {
				if err != nil || got.State != org.PastDue {
					t.Fatal(got, err)
				}
			} else if err == nil || got != (billing.SubscriptionSnapshot{}) {
				t.Fatal("invalid subscription accepted", got, err)
			}
		})
	}
}

func TestInputAndConfigurationBoundaries(t *testing.T) {
	for _, mode := range []string{"key", "environment", "secret", "url", "bundle", "duplicate_price", "duplicate_offer", "directory"} {
		t.Run(mode, func(t *testing.T) {
			c := config(t)
			switch mode {
			case "key":
				c.APIKey = ""
			case "environment":
				c.LiveMode = true
			case "secret":
				c.WebhookSecrets = nil
			case "url":
				c.SuccessURL = "http://127.0.0.1/"
			case "bundle":
				c.Offers[0].EditionVersionID = "team-2099-v1"
			case "duplicate_price":
				c.Offers[1].PriceID = c.Offers[0].PriceID
			case "duplicate_offer":
				c.Offers[1].OfferID = c.Offers[0].OfferID
			case "directory":
				c.Customers = nil
			}
			if _, err := stripe.NewStripeBillingAdapter(c); !errors.Is(err, billing.ErrInvalidConfiguration) {
				t.Fatal(err)
			}
		})
	}
	a := adapter(t, config(t))
	if _, err := a.ReadSubscription(t.Context(), billing.SubscriptionRequest{OrganizationID: "org_a", ExternalReference: "sub_a/../../customers"}); !errors.Is(err, billing.ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := a.CreateCheckout(t.Context(), billing.CheckoutRequest{OrganizationID: "org_a", OfferID: "price_attacker", IdempotencyKey: "attempt"}); !errors.Is(err, billing.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := a.CreateCustomerPortal(t.Context(), billing.PortalRequest{OrganizationID: "unknown", IdempotencyKey: "attempt"}); !errors.Is(err, billing.ErrOwnership) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.CreateCustomerPortal(ctx, billing.PortalRequest{OrganizationID: "org_a", IdempotencyKey: "attempt"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c := config(t)
	c.Customers = directory{forward: map[string]string{"org_a": "cus_b"}, reverse: map[string]string{"cus_b": "org_b"}}
	if _, err := adapter(t, c).CreateCustomerPortal(t.Context(), billing.PortalRequest{OrganizationID: "org_a", IdempotencyKey: "attempt"}); !errors.Is(err, billing.ErrOwnership) {
		t.Fatal("inconsistent binding", err)
	}
}

type forgedEvent struct{}

func (forgedEvent) Provider() string { return stripe.Provider }
func TestWebhookVerificationAndOpaqueHandles(t *testing.T) {
	a := adapter(t, config(t))
	body := eventBody("customer.subscription.updated", subscriptionBody("active", "price_team"))
	for _, mode := range []string{"valid", "rotated", "tampered", "expired", "future", "wrong_secret", "duplicate_timestamp", "no_signature", "duplicate_json", "wrong_version", "connect", "wrong_environment"} {
		t.Run(mode, func(t *testing.T) {
			payload := append([]byte(nil), body...)
			at := now()
			key := secret
			switch mode {
			case "expired":
				at = at.Add(-6 * time.Minute)
			case "future":
				at = at.Add(6 * time.Minute)
			case "wrong_secret":
				key = "wrong"
			case "duplicate_json":
				payload = []byte(strings.Replace(string(payload), `"id":"evt_a"`, `"id":"evt_bad","id":"evt_a"`, 1))
			case "wrong_version":
				payload = []byte(strings.Replace(string(payload), stripe.APIVersion, "2024-06-20", 1))
			case "connect":
				payload = []byte(strings.Replace(string(payload), `"object":"event"`, `"object":"event","account":"acct_foreign"`, 1))
			case "wrong_environment":
				payload = []byte(strings.Replace(string(payload), `"livemode":false`, `"livemode":true`, 1))
			}
			header := sign(payload, key, at)
			if mode == "tampered" {
				payload = append(payload, ' ')
			}
			if mode == "duplicate_timestamp" {
				header += ",t=" + strconv.FormatInt(at.Unix(), 10)
			}
			if mode == "no_signature" {
				header = ""
			}
			if mode == "rotated" {
				header += ",v1=" + strings.Repeat("00", 32)
			}
			h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: payload, Signature: header})
			if mode != "valid" && mode != "rotated" {
				if err == nil || h != nil {
					t.Fatal("unverified payload accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			payload[0] = '!'
			got, err := a.ProcessVerifiedWebhookEvent(t.Context(), h)
			if err != nil || got.EventID != "evt_a" || got.Subscription.OrganizationID != "org_a" {
				t.Fatal(got, err)
			}
			again, err := a.ProcessVerifiedWebhookEvent(t.Context(), h)
			if err != nil || again != got {
				t.Fatal("non-deterministic retry", err)
			}
			if _, err := adapter(t, config(t)).ProcessVerifiedWebhookEvent(t.Context(), h); !errors.Is(err, billing.ErrUnverifiedEvent) {
				t.Fatal("foreign adapter handle accepted", err)
			}
		})
	}
	for _, handle := range []billing.VerifiedWebhookEvent{nil, forgedEvent{}} {
		if _, err := a.ProcessVerifiedWebhookEvent(t.Context(), handle); !errors.Is(err, billing.ErrUnverifiedEvent) {
			t.Fatal(err)
		}
	}
}

func TestVerifiedSubscriptionChangesPreservePriorValuesAndCatalog(t *testing.T) {
	c := config(t)
	before, _ := c.Catalog.GetVersion("team-2026-v1")
	a := adapter(t, c)
	var snapshots []billing.SubscriptionEvent
	for _, pair := range [][2]string{{"active", "price_team"}, {"active", "price_developer"}, {"canceled", "price_developer"}} {
		body := eventBody("customer.subscription.updated", subscriptionBody(pair[0], pair[1]))
		h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: body, Signature: sign(body, secret, now())})
		if err != nil {
			t.Fatal(err)
		}
		e, err := a.ProcessVerifiedWebhookEvent(t.Context(), h)
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, e)
	}
	if snapshots[0].Subscription.EditionVersionID != "team-2026-v1" || snapshots[1].Subscription.EditionVersionID != "developer-2026-v1" || snapshots[2].Subscription.State != org.Canceled {
		t.Fatal(snapshots)
	}
	after, _ := c.Catalog.GetVersion("team-2026-v1")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("changed immutable edition history")
	}
	// Constructor copies mutable configuration: later SKU/secret edits cannot grant access.
	c.Offers[0].PriceID = "price_attacker"
	c.WebhookSecrets[0] = "changed"
	body := eventBody("customer.subscription.updated", subscriptionBody("active", "price_team"))
	h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: body, Signature: sign(body, secret, now())})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			e, err := a.ProcessVerifiedWebhookEvent(t.Context(), h)
			if err != nil || e.Subscription.EditionVersionID != "team-2026-v1" {
				t.Error(e, err)
			}
		})
	}
	wg.Wait()
}

func TestWebhookUnsupportedOwnershipAndExpiredHandle(t *testing.T) {
	for _, mode := range []string{"unsupported", "foreign", "unknown_price", "deleted_active", "backend"} {
		t.Run(mode, func(t *testing.T) {
			c := config(t)
			body := eventBody("customer.subscription.updated", subscriptionBody("active", "price_team"))
			want := billing.ErrInvalidData
			switch mode {
			case "unsupported":
				body = eventBody("checkout.session.completed", `{}`)
				want = billing.ErrUnsupported
			case "foreign":
				body = []byte(strings.Replace(string(body), `"organization_id":"org_a"`, `"organization_id":"org_b"`, 1))
				want = billing.ErrOwnership
			case "unknown_price":
				body = eventBody("customer.subscription.updated", subscriptionBody("active", "price_unknown"))
				want = billing.ErrUnsupported
			case "deleted_active":
				body = eventBody("customer.subscription.deleted", subscriptionBody("active", "price_team"))
			case "backend":
				c.Customers = directory{err: errors.New("secret backend failure")}
				want = billing.ErrOwnership
			}
			a := adapter(t, c)
			h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: body, Signature: sign(body, secret, now())})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ProcessVerifiedWebhookEvent(t.Context(), h); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
		})
	}
	c := config(t)
	current := now()
	c.Now = func() time.Time { return current }
	a := adapter(t, c)
	body := eventBody("customer.subscription.deleted", subscriptionBody("canceled", "price_team"))
	h, err := a.VerifyWebhook(t.Context(), billing.WebhookDelivery{Payload: body, Signature: sign(body, secret, now())})
	if err != nil {
		t.Fatal(err)
	}
	current = current.Add(6 * time.Minute)
	if _, err := a.ProcessVerifiedWebhookEvent(t.Context(), h); !errors.Is(err, billing.ErrUnverifiedEvent) {
		t.Fatal(err)
	}
}
