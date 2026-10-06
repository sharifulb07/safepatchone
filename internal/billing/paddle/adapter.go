// Package paddle implements Paddle Billing (not Paddle Classic).
package paddle

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/jsonobject"
	org "safepatch/internal/domain/organization"
)

const Provider = "paddle"
const MaxPayloadBytes = 256 * 1024
const maxAPIResponseBytes = 2 * 1024 * 1024
const SignatureTolerance = 5 * time.Second

type Offer struct{ OfferID, EditionVersionID, PriceID string }
type Intent struct{ ID, OrganizationID, OfferID, TransactionID, SubscriptionID string }

// CheckoutDirectory records server-created transactions before their URLs are
// returned. Custom data and customer email alone never establish ownership.
type CheckoutDirectory interface {
	Checkout(context.Context, billing.CheckoutRequest, func(string) (billing.Session, error)) (billing.Session, error)
	Lookup(context.Context, string) (Intent, error)
	Bind(context.Context, Intent, string, string) error
}
type Config struct {
	APIKey         string   `json:"-"`
	WebhookSecrets []string `json:"-"`
	Environment    string
	CheckoutURL    string
	Offers         []Offer
	Catalog        *edition.Catalog
	Customers      billing.CustomerDirectory
	Checkouts      CheckoutDirectory
	Transport      http.RoundTripper
	Now            func() time.Time
}
type Adapter struct {
	key, base, checkoutURL string
	secrets                []string
	offers, prices         map[string]Offer
	customers              billing.CustomerDirectory
	checkouts              CheckoutDirectory
	client                 *http.Client
	now                    func() time.Time
}

var _ billing.BillingProvider = (*Adapter)(nil)
var idPattern = regexp.MustCompile(`^[a-z]{3,4}_[a-z0-9]{26}$`)

func validID(s, prefix string) bool { return strings.HasPrefix(s, prefix) && idPattern.MatchString(s) }
func New(c Config) (*Adapter, error) {
	base, keyPrefix := "https://sandbox-api.paddle.com", "pdl_sdbx_apikey_"
	if c.Environment == "production" {
		base, keyPrefix = "https://api.paddle.com", "pdl_live_apikey_"
	} else if c.Environment != "sandbox" {
		return nil, billing.ErrInvalidConfiguration
	}
	u, err := url.Parse(c.CheckoutURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/checkout" || (u.Port() != "" && u.Port() != "443") || strings.ContainsAny(c.CheckoutURL, "\\\r\n\t") || !strings.HasPrefix(c.APIKey, keyPrefix) || len(c.APIKey) < len(keyPrefix)+16 || len(c.APIKey) > 512 || strings.ContainsAny(c.APIKey, " \r\n\t") || c.Customers == nil || c.Checkouts == nil || c.Catalog == nil || len(c.Offers) == 0 || len(c.Offers) > 256 || len(c.WebhookSecrets) < 1 || len(c.WebhookSecrets) > 3 {
		return nil, billing.ErrInvalidConfiguration
	}
	for _, s := range c.WebhookSecrets {
		if len(s) < 16 || len(s) > 256 || strings.ContainsAny(s, " \r\n\t") {
			return nil, billing.ErrInvalidConfiguration
		}
	}
	a := &Adapter{key: c.APIKey, base: base, checkoutURL: c.CheckoutURL, secrets: append([]string(nil), c.WebhookSecrets...), offers: map[string]Offer{}, prices: map[string]Offer{}, customers: c.Customers, checkouts: c.Checkouts, now: c.Now}
	for _, o := range c.Offers {
		bundle, e := c.Catalog.GetVersion(o.EditionVersionID)
		if e != nil || bundle.Version.EditionKey == edition.Community || !org.ValidID(o.OfferID) || !validID(o.PriceID, "pri_") {
			return nil, billing.ErrInvalidConfiguration
		}
		if _, ok := a.offers[o.OfferID]; ok {
			return nil, billing.ErrInvalidConfiguration
		}
		if _, ok := a.prices[o.PriceID]; ok {
			return nil, billing.ErrInvalidConfiguration
		}
		a.offers[o.OfferID], a.prices[o.PriceID] = o, o
	}
	if a.now == nil {
		a.now = time.Now
	}
	transport := c.Transport
	if transport == nil {
		transport = &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 10, MaxConnsPerHost: 20, ForceAttemptHTTP2: true}
	}
	a.client = &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return a, nil
}
func (a *Adapter) request(ctx context.Context, method, path string, body any, out any) error {
	if ctx == nil || a == nil {
		return billing.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return billing.ErrInvalidRequest
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, a.base+path, strings.NewReader(string(raw)))
	if err != nil {
		return billing.ErrInvalidRequest
	}
	r.Header.Set("Authorization", "Bearer "+a.key)
	r.Header.Set("Paddle-Version", "1")
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	res, err := a.client.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return billing.ErrProvider
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return billing.ErrProvider
	}
	raw, err = io.ReadAll(io.LimitReader(res.Body, maxAPIResponseBytes+1))
	if err != nil {
		return billing.ErrProvider
	}
	raw, err = jsonobject.Normalize(raw, maxAPIResponseBytes)
	if err != nil || json.Unmarshal(raw, out) != nil {
		return billing.ErrInvalidData
	}
	return ctx.Err()
}

type item struct {
	Quantity int `json:"quantity"`
	Price    struct {
		ID           string `json:"id"`
		BillingCycle *struct {
			Interval  string `json:"interval"`
			Frequency int    `json:"frequency"`
		} `json:"billing_cycle"`
	} `json:"price"`
}
type period struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}
type transaction struct {
	ID             string                     `json:"id"`
	Status         string                     `json:"status"`
	CustomerID     string                     `json:"customer_id"`
	SubscriptionID string                     `json:"subscription_id"`
	CollectionMode string                     `json:"collection_mode"`
	CustomData     map[string]json.RawMessage `json:"custom_data"`
	Items          []item                     `json:"items"`
	BillingPeriod  *period                    `json:"billing_period"`
	Checkout       struct {
		URL string `json:"url"`
	} `json:"checkout"`
}
type subscription struct {
	ID                   string                     `json:"id"`
	Status               string                     `json:"status"`
	CustomerID           string                     `json:"customer_id"`
	CollectionMode       string                     `json:"collection_mode"`
	CreatedAt            time.Time                  `json:"created_at"`
	CustomData           map[string]json.RawMessage `json:"custom_data"`
	Items                []item                     `json:"items"`
	CurrentBillingPeriod *period                    `json:"current_billing_period"`
	ScheduledChange      *struct {
		Action string `json:"action"`
	} `json:"scheduled_change"`
}

func intentID(data map[string]json.RawMessage) string {
	var id string
	_ = json.Unmarshal(data["safepatch_checkout"], &id)
	if !org.ValidID(id) {
		return ""
	}
	return id
}
func (a *Adapter) offer(items []item) (Offer, error) {
	if len(items) != 1 || items[0].Quantity != 1 || items[0].Price.BillingCycle == nil {
		return Offer{}, billing.ErrUnsupported
	}
	o, ok := a.prices[items[0].Price.ID]
	if !ok {
		return Offer{}, billing.ErrUnsupported
	}
	return o, nil
}
func (a *Adapter) CreateCheckout(ctx context.Context, r billing.CheckoutRequest) (billing.Session, error) {
	if r.Validate() != nil {
		return billing.Session{}, billing.ErrInvalidRequest
	}
	o, ok := a.offers[r.OfferID]
	if !ok {
		return billing.Session{}, billing.ErrUnsupported
	}
	return a.checkouts.Checkout(ctx, r, func(id string) (billing.Session, error) {
		var res struct {
			Data transaction `json:"data"`
		}
		body := map[string]any{"items": []any{map[string]any{"price_id": o.PriceID, "quantity": 1}}, "collection_mode": "automatic", "custom_data": map[string]string{"safepatch_checkout": id}, "checkout": map[string]string{"url": a.checkoutURL}}
		if err := a.request(ctx, "POST", "/transactions", body, &res); err != nil {
			return billing.Session{}, err
		}
		t := res.Data
		got, e := a.offer(t.Items)
		if !validID(t.ID, "txn_") || (t.Status != "draft" && t.Status != "ready") || t.CollectionMode != "automatic" || intentID(t.CustomData) != id || e != nil || got != o {
			return billing.Session{}, billing.ErrInvalidData
		}
		// Construct the known-origin link instead of trusting a provider redirect URL.
		return billing.Session{ExternalReference: t.ID, URL: a.checkoutURL + "?_ptxn=" + t.ID}, nil
	})
}
func (a *Adapter) getSubscription(ctx context.Context, id string) (subscription, error) {
	var r struct {
		Data subscription `json:"data"`
	}
	if !validID(id, "sub_") {
		return r.Data, billing.ErrInvalidData
	}
	if err := a.request(ctx, "GET", "/subscriptions/"+id, nil, &r); err != nil {
		return r.Data, err
	}
	if r.Data.ID != id {
		return r.Data, billing.ErrInvalidData
	}
	return r.Data, nil
}

// establish proves a subscription originated from a transaction saved by this
// server. A forged/custom browser checkout cannot satisfy the transaction link.
func (a *Adapter) establish(ctx context.Context, s subscription) (Intent, error) {
	i, err := a.checkouts.Lookup(ctx, intentID(s.CustomData))
	if err != nil {
		return i, err
	}
	if !validID(i.TransactionID, "txn_") || !validID(s.CustomerID, "ctm_") {
		return i, billing.ErrOwnership
	}
	var r struct {
		Data transaction `json:"data"`
	}
	if err = a.request(ctx, "GET", "/transactions/"+i.TransactionID, nil, &r); err != nil {
		return i, err
	}
	t := r.Data
	o, e := a.offer(t.Items)
	if t.ID != i.TransactionID || t.SubscriptionID != s.ID || t.CustomerID != s.CustomerID || intentID(t.CustomData) != i.ID || t.CollectionMode != "automatic" || t.Status != "completed" || e != nil || o.OfferID != i.OfferID {
		return i, billing.ErrOwnership
	}
	if err = a.checkouts.Bind(ctx, i, s.CustomerID, s.ID); err != nil {
		return i, err
	}
	return i, nil
}
func (a *Adapter) ReadSubscription(ctx context.Context, r billing.SubscriptionRequest) (billing.SubscriptionSnapshot, error) {
	if r.Validate() != nil {
		return billing.SubscriptionSnapshot{}, billing.ErrInvalidRequest
	}
	s, err := a.getSubscription(ctx, r.ExternalReference)
	if err != nil {
		return billing.SubscriptionSnapshot{}, err
	}
	return a.snapshot(ctx, s, r.OrganizationID)
}
func (a *Adapter) snapshot(ctx context.Context, s subscription, owner string) (billing.SubscriptionSnapshot, error) {
	fail := billing.SubscriptionSnapshot{}
	customer, err := a.customers.CustomerForOrganization(ctx, Provider, owner)
	if err != nil || customer != s.CustomerID {
		return fail, billing.ErrOwnership
	}
	reverse, err := a.customers.OrganizationForCustomer(ctx, Provider, customer)
	if err != nil || reverse != owner {
		return fail, billing.ErrOwnership
	}
	i, err := a.checkouts.Lookup(ctx, intentID(s.CustomData))
	if err != nil || i.OrganizationID != owner || i.SubscriptionID != s.ID {
		return fail, billing.ErrOwnership
	}
	o, err := a.offer(s.Items)
	if err != nil {
		return fail, err
	}
	if s.CollectionMode != "automatic" || !org.ValidTime(s.CreatedAt) || s.CreatedAt.After(a.now().Add(SignatureTolerance)) {
		return fail, billing.ErrInvalidData
	}
	state := org.PastDue
	switch s.Status {
	case "active":
		state = org.Subscribed
	case "past_due", "paused":
	case "canceled":
		state = org.Canceled
	case "trialing":
		return fail, billing.ErrUnsupported
	default:
		return fail, billing.ErrInvalidData
	}
	v := billing.SubscriptionSnapshot{OrganizationID: owner, Provider: Provider, ExternalReference: s.ID, EditionVersionID: o.EditionVersionID, State: state, CreatedAt: s.CreatedAt, CancelAtPeriodEnd: s.ScheduledChange != nil && s.ScheduledChange.Action == "cancel"}
	if state == org.Subscribed {
		p := s.CurrentBillingPeriod
		if p == nil || !org.ValidTime(p.EndsAt) || !p.EndsAt.After(p.StartsAt) || !p.EndsAt.After(s.CreatedAt) {
			return fail, billing.ErrInvalidData
		}
		v.AccessUntil = p.EndsAt
		var res struct {
			Data []transaction `json:"data"`
		}
		path := "/transactions?subscription_id=" + s.ID + "&status=completed&per_page=30&order_by=created_at%5BDESC%5D"
		if err = a.request(ctx, "GET", path, nil, &res); err != nil {
			return fail, err
		}
		// An unpaid subscription update cannot reuse a previous plan's settlement.
		for _, t := range res.Data {
			paid, e := a.offer(t.Items)
			if e == nil && paid == o && t.Status == "completed" && t.SubscriptionID == s.ID && t.CustomerID == customer && t.CollectionMode == "automatic" && t.BillingPeriod != nil && !t.BillingPeriod.StartsAt.After(p.StartsAt) && !t.BillingPeriod.EndsAt.Before(p.EndsAt) {
				v.PaymentSettled = true
				break
			}
		}
	}
	return v, nil
}
func (a *Adapter) CreateCustomerPortal(ctx context.Context, r billing.PortalRequest) (billing.Session, error) {
	if r.Validate() != nil {
		return billing.Session{}, billing.ErrInvalidRequest
	}
	customer, err := a.customers.CustomerForOrganization(ctx, Provider, r.OrganizationID)
	if err != nil || !validID(customer, "ctm_") {
		return billing.Session{}, billing.ErrOwnership
	}
	owner, err := a.customers.OrganizationForCustomer(ctx, Provider, customer)
	if err != nil || owner != r.OrganizationID {
		return billing.Session{}, billing.ErrOwnership
	}
	var res struct {
		Data struct {
			ID         string `json:"id"`
			CustomerID string `json:"customer_id"`
			URLs       struct {
				General struct {
					Overview string `json:"overview"`
				} `json:"general"`
			} `json:"urls"`
		} `json:"data"`
	}
	if err = a.request(ctx, "POST", "/customers/"+customer+"/portal-sessions", map[string]any{}, &res); err != nil {
		return billing.Session{}, err
	}
	u, e := url.Parse(res.Data.URLs.General.Overview)
	host := "customer-portal.paddle.com"
	if strings.Contains(a.base, "sandbox") {
		host = "sandbox-customer-portal.paddle.com"
	}
	if e != nil || u.Scheme != "https" || u.Host != host || u.User != nil || u.Fragment != "" || !validID(strings.TrimPrefix(u.Path, "/"), "cpl_") || res.Data.CustomerID != customer || !validID(res.Data.ID, "cpls_") {
		return billing.Session{}, billing.ErrInvalidData
	}
	// A GitHub session proves the organization actor, not the billing email typed
	// into Paddle. Never hand out a customer-wide auto-login token based on that
	// email. Open the merchant portal without credentials; Paddle verifies the
	// billing email itself before displaying history or management controls.
	portal := url.URL{Scheme: "https", Host: host, Path: u.Path}
	return billing.Session{ExternalReference: res.Data.ID, URL: portal.String()}, nil
}
