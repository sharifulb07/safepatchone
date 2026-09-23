// Package billing defines provider-neutral, organization-owned billing ports.
// Billing observations are not entitlements, permissions, or proof of payment.
package billing

import (
	"context"
	"errors"
	"time"

	"safepatch/internal/domain/organization"
)

var (
	ErrInvalidRequest       = errors.New("invalid billing request")
	ErrInvalidConfiguration = errors.New("invalid billing configuration")
	ErrProvider             = errors.New("billing provider failed")
	ErrInvalidData          = errors.New("invalid billing provider data")
	ErrOwnership            = errors.New("billing ownership not established")
	ErrUnverifiedEvent      = errors.New("billing event verification failed")
	ErrUnsupported          = errors.New("billing operation or event unsupported")
	ErrUnsupportedEvent     = errors.New("billing event type unsupported")
)

type CheckoutRequest struct {
	OrganizationID string
	OfferID        string // Trusted configuration maps this to a pinned bundle and provider SKU.
	IdempotencyKey string // Stable per checkout attempt, reused for retries only.
}

func (r CheckoutRequest) Validate() error {
	if !organization.ValidID(r.OrganizationID) || !organization.ValidID(r.OfferID) || !organization.ValidID(r.IdempotencyKey) {
		return ErrInvalidRequest
	}
	return nil
}

type SubscriptionRequest struct{ OrganizationID, ExternalReference string }

func (r SubscriptionRequest) Validate() error {
	if !organization.ValidID(r.OrganizationID) || !organization.ValidID(r.ExternalReference) {
		return ErrInvalidRequest
	}
	return nil
}

type PortalRequest struct{ OrganizationID, IdempotencyKey string }

func (r PortalRequest) Validate() error {
	if !organization.ValidID(r.OrganizationID) || !organization.ValidID(r.IdempotencyKey) {
		return ErrInvalidRequest
	}
	return nil
}

type Session struct{ ExternalReference, URL string }

// SubscriptionSnapshot is an external observation, not a revisioned stored
// organization.Subscription. A trusted reconciler assigns internal identity and
// performs CAS updates; provider timestamps are not database revisions.
type SubscriptionSnapshot struct {
	OrganizationID    string
	Provider          string
	ExternalReference string
	EditionVersionID  string
	State             organization.SubscriptionState
	CreatedAt         time.Time
	CancelAtPeriodEnd bool
	AccessUntil       time.Time // Billing period/trial boundary; never an indefinite grant.
	PaymentSettled    bool
}
type SubscriptionEvent struct {
	Provider     string
	EventID      string
	OccurredAt   time.Time
	Subscription SubscriptionSnapshot
}

// VerifiedWebhookEvent is an opaque adapter-issued handle. Provider() alone is
// NOT proof: each adapter must reject foreign, forged, or expired handles.
type VerifiedWebhookEvent interface{ Provider() string }
type WebhookDelivery struct {
	Payload   []byte
	Signature string
}
type WebhookVerifier interface {
	VerifyWebhook(context.Context, WebhookDelivery) (VerifiedWebhookEvent, error)
}

// BillingProvider contains no vendor DTOs or repository/user subscriptions.
// Implementations may return ErrUnsupported for unavailable hosted flows (for
// example manual contracts). Calls require a trusted billing application boundary.
// ProcessVerifiedWebhookEvent normalizes a verified delivery without mutating
// subscriptions or grants. Consumers must durably deduplicate (provider,event_id)
// and reconcile current state before applying observations received out of order.
type BillingProvider interface {
	CreateCheckout(context.Context, CheckoutRequest) (Session, error)
	ReadSubscription(context.Context, SubscriptionRequest) (SubscriptionSnapshot, error)
	CreateCustomerPortal(context.Context, PortalRequest) (Session, error)
	ProcessVerifiedWebhookEvent(context.Context, VerifiedWebhookEvent) (SubscriptionEvent, error)
}

// CustomerDirectory is trusted provisioning data, not request metadata. Customer
// bindings must be unique within a provider account/environment, immutable, and
// scoped to an organization. Missing/ambiguous mappings must return an error.
// Inject a directory scoped to the same account and environment as the adapter.
type CustomerDirectory interface {
	CustomerForOrganization(context.Context, string, string) (string, error)
	OrganizationForCustomer(context.Context, string, string) (string, error)
}
