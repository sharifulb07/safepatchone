// Package stripe implements SafePatch's billing port using Stripe's REST API.
// Credentials, prices, HTTP and signature formats remain outside core code.
package stripe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const (
	Provider = "stripe"
	// Pin the tested snapshot-event/REST shape; upgrade deliberately with fixtures.
	APIVersion         = "2025-06-30.basil"
	MaxPayloadBytes    = 256 * 1024
	SignatureTolerance = 5 * time.Minute
)

var credentialPattern = regexp.MustCompile(`^(sk|rk)_(test|live)_[a-zA-Z0-9]{8,256}$`)

type Offer struct{ OfferID, EditionVersionID, PriceID string }
type Config struct {
	APIKey                                 string   `json:"-"`
	WebhookSecrets                         []string `json:"-"` // Current and rotating previous endpoint secrets.
	LiveMode                               bool
	Offers                                 []Offer
	Catalog                                *edition.Catalog
	Customers                              billing.CustomerDirectory
	SuccessURL, CancelURL, PortalReturnURL string            // Fixed trusted application URLs.
	Transport                              http.RoundTripper // Trusted composition/test seam, never a request parameter.
	Now                                    func() time.Time
}
type StripeBillingAdapter struct {
	key                              string
	secrets                          []string
	live                             bool
	offers                           map[string]Offer
	prices                           map[string]Offer
	customers                        billing.CustomerDirectory
	successURL, cancelURL, portalURL string
	client                           *http.Client
	now                              func() time.Time
}

var _ billing.BillingProvider = (*StripeBillingAdapter)(nil)
var _ billing.WebhookVerifier = (*StripeBillingAdapter)(nil)

func NewStripeBillingAdapter(c Config) (*StripeBillingAdapter, error) {
	if !credentialPattern.MatchString(c.APIKey) || strings.Contains(c.APIKey, "_live_") != c.LiveMode || c.Catalog == nil || c.Customers == nil || len(c.Offers) == 0 || len(c.Offers) > 256 || len(c.WebhookSecrets) < 1 || len(c.WebhookSecrets) > 3 {
		return nil, billing.ErrInvalidConfiguration
	}
	for _, secret := range c.WebhookSecrets {
		if !strings.HasPrefix(secret, "whsec_") || len(secret) < 16 || len(secret) > 256 || strings.ContainsAny(secret, "\r\n\t ") {
			return nil, billing.ErrInvalidConfiguration
		}
	}
	for _, raw := range []string{c.SuccessURL, c.CancelURL, c.PortalReturnURL} {
		if !safeURL(raw, "") {
			return nil, billing.ErrInvalidConfiguration
		}
	}
	a := &StripeBillingAdapter{key: c.APIKey, secrets: append([]string(nil), c.WebhookSecrets...), live: c.LiveMode, offers: map[string]Offer{}, prices: map[string]Offer{}, customers: c.Customers, successURL: c.SuccessURL, cancelURL: c.CancelURL, portalURL: c.PortalReturnURL, now: c.Now}
	for _, o := range c.Offers {
		if !org.ValidID(o.OfferID) || !providerID(o.PriceID, "price_") {
			return nil, billing.ErrInvalidConfiguration
		}
		if _, err := c.Catalog.GetVersion(o.EditionVersionID); err != nil {
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
		// Explicit transport avoids environment-controlled credential-bearing proxies.
		transport = &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 10, MaxConnsPerHost: 20, ForceAttemptHTTP2: true}
	}
	a.client = &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return a, nil
}
func providerID(id, prefix string) bool {
	return org.ValidID(id) && strings.HasPrefix(id, prefix) && len(id) > len(prefix)
}
func safeURL(raw, host string) bool {
	if len(raw) == 0 || len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\t\\") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && (u.Fragment == "" || host == "checkout.stripe.com") && u.Opaque == "" && (u.Port() == "" || u.Port() == "443") && (host == "" || u.Host == host)
}
func (a *StripeBillingAdapter) check(ctx context.Context) error {
	if a == nil || a.client == nil || ctx == nil {
		return billing.ErrInvalidRequest
	}
	return ctx.Err()
}
func (a *StripeBillingAdapter) customer(ctx context.Context, organizationID string) (string, error) {
	id, err := a.customers.CustomerForOrganization(ctx, Provider, organizationID)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || !providerID(id, "cus_") {
		return "", billing.ErrOwnership
	}
	owner, err := a.customers.OrganizationForCustomer(ctx, Provider, id)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || owner != organizationID {
		return "", billing.ErrOwnership
	}
	return id, nil
}
func requestKey(operation, organizationID, key string) string {
	// Scope keys to operation and tenant without leaking customer identifiers.
	h := sha256.Sum256([]byte(operation + "\x00" + organizationID + "\x00" + key))
	return "safepatch_" + hex.EncodeToString(h[:])
}
func (a *StripeBillingAdapter) request(ctx context.Context, method, path string, values url.Values, key string, out any) error {
	if err := a.check(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.stripe.com/v1/"+path, strings.NewReader(values.Encode()))
	if err != nil {
		return billing.ErrInvalidRequest
	}
	req.SetBasicAuth(a.key, "")
	req.Header.Set("Stripe-Version", APIVersion)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := a.client.Do(req)
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
	raw, err := io.ReadAll(io.LimitReader(res.Body, MaxPayloadBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return billing.ErrProvider
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(raw) == 0 || len(raw) > MaxPayloadBytes {
		return billing.ErrInvalidData
	}
	checked, err := jsonobject.Normalize(raw, MaxPayloadBytes)
	if err != nil || json.Unmarshal(checked, out) != nil {
		return billing.ErrInvalidData
	}
	return nil
}
func (a *StripeBillingAdapter) CreateCheckout(ctx context.Context, r billing.CheckoutRequest) (billing.Session, error) {
	if err := a.check(ctx); err != nil {
		return billing.Session{}, err
	}
	if err := r.Validate(); err != nil {
		return billing.Session{}, err
	}
	o, ok := a.offers[r.OfferID]
	if !ok {
		return billing.Session{}, billing.ErrUnsupported
	}
	customer, err := a.customer(ctx, r.OrganizationID)
	if err != nil {
		return billing.Session{}, err
	}
	v := url.Values{"mode": {"subscription"}, "customer": {customer}, "client_reference_id": {r.OrganizationID}, "success_url": {a.successURL}, "cancel_url": {a.cancelURL}, "line_items[0][price]": {o.PriceID}, "line_items[0][quantity]": {"1"}, "subscription_data[metadata][organization_id]": {r.OrganizationID}, "metadata[organization_id]": {r.OrganizationID}}
	var response struct {
		ID, Object, URL, Mode, Customer string
		LiveMode                        *bool  `json:"livemode"`
		ClientReferenceID               string `json:"client_reference_id"`
	}
	if err := a.request(ctx, http.MethodPost, "checkout/sessions", v, requestKey("checkout", r.OrganizationID, r.IdempotencyKey), &response); err != nil {
		return billing.Session{}, err
	}
	if !providerID(response.ID, "cs_") || response.Object != "checkout.session" || response.Mode != "subscription" || response.Customer != customer || response.ClientReferenceID != r.OrganizationID || response.LiveMode == nil || *response.LiveMode != a.live || !safeURL(response.URL, "checkout.stripe.com") {
		return billing.Session{}, billing.ErrInvalidData
	}
	return billing.Session{ExternalReference: response.ID, URL: response.URL}, nil
}
func (a *StripeBillingAdapter) CreateCustomerPortal(ctx context.Context, r billing.PortalRequest) (billing.Session, error) {
	if err := a.check(ctx); err != nil {
		return billing.Session{}, err
	}
	if err := r.Validate(); err != nil {
		return billing.Session{}, err
	}
	customer, err := a.customer(ctx, r.OrganizationID)
	if err != nil {
		return billing.Session{}, err
	}
	var response struct {
		ID, Object, URL, Customer string
		LiveMode                  *bool `json:"livemode"`
	}
	if err := a.request(ctx, http.MethodPost, "billing_portal/sessions", url.Values{"customer": {customer}, "return_url": {a.portalURL}}, requestKey("portal", r.OrganizationID, r.IdempotencyKey), &response); err != nil {
		return billing.Session{}, err
	}
	if !providerID(response.ID, "bps_") || response.Object != "billing_portal.session" || response.Customer != customer || response.LiveMode == nil || *response.LiveMode != a.live || !safeURL(response.URL, "billing.stripe.com") {
		return billing.Session{}, billing.ErrInvalidData
	}
	return billing.Session{ExternalReference: response.ID, URL: response.URL}, nil
}

type subscription struct {
	ID, Object, Customer, Status string
	LiveMode                     *bool `json:"livemode"`
	Created                      int64
	TrialEnd                     int64           `json:"trial_end"`
	LatestInvoice                json.RawMessage `json:"latest_invoice"`
	CancelAtPeriodEnd            *bool           `json:"cancel_at_period_end"`
	Metadata                     map[string]string
	PauseCollection              json.RawMessage `json:"pause_collection"`
	Items                        struct {
		Object  string
		HasMore *bool `json:"has_more"`
		Data    []struct {
			Quantity  int64
			PeriodEnd int64 `json:"current_period_end"`
			Price     struct{ ID, Object, Type string }
		}
	}
}

func (a *StripeBillingAdapter) normalize(ctx context.Context, s subscription, organizationID string) (billing.SubscriptionSnapshot, error) {
	fail := billing.SubscriptionSnapshot{}
	if !org.ValidID(organizationID) || s.Metadata["organization_id"] != organizationID {
		return fail, billing.ErrOwnership
	}
	customer, err := a.customer(ctx, organizationID)
	if err != nil {
		return fail, err
	}
	if s.Customer != customer {
		return fail, billing.ErrOwnership
	}
	if !providerID(s.ID, "sub_") || s.Object != "subscription" || s.LiveMode == nil || *s.LiveMode != a.live || s.CancelAtPeriodEnd == nil || s.Created <= 0 || s.Created > a.now().Add(SignatureTolerance).Unix() || s.Items.Object != "list" || s.Items.HasMore == nil || *s.Items.HasMore || len(s.Items.Data) != 1 {
		return fail, billing.ErrInvalidData
	}
	item := s.Items.Data[0]
	o, ok := a.prices[item.Price.ID]
	if !ok || item.Price.Object != "price" || item.Price.Type != "recurring" || item.Quantity != 1 {
		return fail, billing.ErrUnsupported
	}
	var state org.SubscriptionState
	switch s.Status {
	case "incomplete":
		state = org.SubscriptionPending
	case "trialing":
		state = org.Trialing
	case "active":
		state = org.Subscribed
	case "past_due", "unpaid", "paused":
		state = org.PastDue
	case "canceled":
		state = org.Canceled
	case "incomplete_expired":
		state = org.Expired
	default:
		return fail, billing.ErrInvalidData
	}
	// Paused collection can coexist with active status. Never normalize it as paid.
	if len(s.PauseCollection) > 0 && string(s.PauseCollection) != "null" && (state == org.Subscribed || state == org.Trialing) {
		state = org.PastDue
	}
	var until time.Time
	end := item.PeriodEnd
	if state == org.Trialing && (end == 0 || s.TrialEnd < end) {
		end = s.TrialEnd
	}
	if state == org.Subscribed || state == org.Trialing {
		if end <= s.Created || end > 253402300799 {
			return fail, billing.ErrInvalidData
		}
		until = time.Unix(end, 0).UTC()
	}
	var invoice struct {
		Object, Status, Customer string
		Parent                   struct {
			Type                string
			SubscriptionDetails struct{ Subscription string } `json:"subscription_details"`
		}
	}
	paid := json.Unmarshal(s.LatestInvoice, &invoice) == nil && invoice.Object == "invoice" && invoice.Status == "paid" && invoice.Customer == s.Customer && invoice.Parent.Type == "subscription_details" && invoice.Parent.SubscriptionDetails.Subscription == s.ID
	return billing.SubscriptionSnapshot{OrganizationID: organizationID, Provider: Provider, ExternalReference: s.ID, EditionVersionID: o.EditionVersionID, State: state, CreatedAt: time.Unix(s.Created, 0).UTC(), CancelAtPeriodEnd: *s.CancelAtPeriodEnd, AccessUntil: until, PaymentSettled: paid}, nil
}
func (a *StripeBillingAdapter) ReadSubscription(ctx context.Context, r billing.SubscriptionRequest) (billing.SubscriptionSnapshot, error) {
	if err := a.check(ctx); err != nil {
		return billing.SubscriptionSnapshot{}, err
	}
	if r.Validate() != nil || !providerID(r.ExternalReference, "sub_") {
		return billing.SubscriptionSnapshot{}, billing.ErrInvalidRequest
	}
	if _, err := a.customer(ctx, r.OrganizationID); err != nil {
		return billing.SubscriptionSnapshot{}, err
	}
	var response subscription
	if err := a.request(ctx, http.MethodGet, "subscriptions/"+r.ExternalReference+"?expand%5B%5D=latest_invoice", nil, "", &response); err != nil {
		return billing.SubscriptionSnapshot{}, err
	}
	if response.ID != r.ExternalReference {
		return billing.SubscriptionSnapshot{}, billing.ErrInvalidData
	}
	return a.normalize(ctx, response, r.OrganizationID)
}
