package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/billing"
	"safepatch/internal/tenant"
)

type billingProvider struct {
	calls      int
	err        error
	wrongOwner bool
}

func (p *billingProvider) CreateCheckout(context.Context, billing.CheckoutRequest) (billing.Session, error) {
	p.calls++
	return billing.Session{ExternalReference: "checkout", URL: "https://provider.example/checkout"}, p.err
}
func (p *billingProvider) CreateCustomerPortal(context.Context, billing.PortalRequest) (billing.Session, error) {
	p.calls++
	return billing.Session{ExternalReference: "portal", URL: "https://provider.example/portal"}, p.err
}
func (p *billingProvider) ReadSubscription(_ context.Context, r billing.SubscriptionRequest) (billing.SubscriptionSnapshot, error) {
	p.calls++
	owner := r.OrganizationID
	if p.wrongOwner {
		owner = "foreign"
	}
	return billing.SubscriptionSnapshot{OrganizationID: owner, ExternalReference: r.ExternalReference}, p.err
}
func (*billingProvider) ProcessVerifiedWebhookEvent(context.Context, billing.VerifiedWebhookEvent) (billing.SubscriptionEvent, error) {
	return billing.SubscriptionEvent{}, billing.ErrUnsupported
}

type billingMemberships struct {
	active bool
	err    error
}

func (m billingMemberships) GetMembership(_ context.Context, id string, actor authorization.Principal) (authorization.Membership, error) {
	role := authorization.RoleViewer
	if id == "org_a" {
		role = authorization.RoleOwner
	}
	return authorization.Membership{OrganizationID: id, Principal: actor, Role: role, Active: m.active}, m.err
}

func TestBillingManagementUsesIndependentOrganizationPermission(t *testing.T) {
	for _, operation := range []string{"checkout", "portal", "read"} {
		for _, mode := range []string{"owner", "viewer", "inactive", "membership_error", "foreign_scope", "provider_error", "unsupported", "canceled", "invalid_actor"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				provider := &billingProvider{}
				if mode == "provider_error" {
					provider.err = errors.New("private provider credential")
				}
				if mode == "unsupported" {
					provider.err = billing.ErrUnsupported
				}
				members := billingMemberships{active: mode != "inactive"}
				if mode == "membership_error" {
					members.err = errors.New("private database detail")
				}
				permissions, _ := authorization.NewPermissionService(members)
				tenants := tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					if mode == "foreign_scope" {
						s.OrganizationID = "foreign"
					}
					return s, nil
				})
				service, err := application.NewBillingService(provider, tenants, permissions)
				if err != nil {
					t.Fatal(err)
				}
				organization := "org_a"
				if mode == "viewer" {
					organization = "org_b"
				}
				actor := authorization.Principal{ID: "user", Kind: authorization.User}
				if mode == "invalid_actor" {
					actor.Kind = "LLM"
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "canceled" {
					cancel()
				}
				switch operation {
				case "checkout":
					var result billing.Session
					result, err = service.CreateCheckout(ctx, actor, billing.CheckoutRequest{OrganizationID: organization, OfferID: "offer", IdempotencyKey: "attempt"})
					if err != nil && result != (billing.Session{}) {
						t.Fatal("partial billing result")
					}
				case "portal":
					_, err = service.CreateCustomerPortal(ctx, actor, billing.PortalRequest{OrganizationID: organization, IdempotencyKey: "attempt"})
				case "read":
					_, err = service.ReadSubscription(ctx, actor, billing.SubscriptionRequest{OrganizationID: organization, ExternalReference: "contract"})
				}
				if (err == nil) != (mode == "owner") {
					t.Fatal(err)
				}
				wantCalls := 0
				if mode == "owner" || mode == "provider_error" || mode == "unsupported" {
					wantCalls = 1
				}
				if provider.calls != wantCalls {
					t.Fatal("billing side effect crossed denied boundary", provider.calls)
				}
				if err != nil && strings.Contains(err.Error(), "private") {
					t.Fatal("exposed provider data")
				}
				if mode == "unsupported" && !errors.Is(err, billing.ErrUnsupported) {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestBillingReadRejectsMismatchedProviderOwnership(t *testing.T) {
	provider := &billingProvider{wrongOwner: true}
	service, _ := application.NewBillingService(provider, tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
		return true, nil
	}))
	if _, err := service.ReadSubscription(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, billing.SubscriptionRequest{OrganizationID: "org_a", ExternalReference: "contract"}); !errors.Is(err, billing.ErrOwnership) {
		t.Fatal(err)
	}
	var empty application.BillingService
	if _, err := empty.CreateCustomerPortal(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, billing.PortalRequest{OrganizationID: "org_a", IdempotencyKey: "attempt"}); !errors.Is(err, billing.ErrInvalidRequest) {
		t.Fatal(err)
	}
}
