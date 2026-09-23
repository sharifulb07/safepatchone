package application

import (
	"context"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/billing"
	"safepatch/internal/tenant"
)

// BillingService accepts principals from trusted authentication. Billing
// management requires membership and billing.manage, not a premium feature:
// organizations must be able to purchase capabilities they do not yet own.
type BillingService struct {
	provider    billing.BillingProvider
	tenants     TenantResolver
	permissions authorization.PermissionChecker
}

func NewBillingService(provider billing.BillingProvider, tenants TenantResolver, permissions authorization.PermissionChecker) (*BillingService, error) {
	if provider == nil || tenants == nil || permissions == nil {
		return nil, billing.ErrInvalidConfiguration
	}
	return &BillingService{provider, tenants, permissions}, nil
}
func (s *BillingService) authorize(ctx context.Context, actor authorization.Principal, org string) error {
	if s == nil || s.provider == nil || s.tenants == nil || s.permissions == nil || ctx == nil || actor.Validate() != nil || !tenant.ValidID(org) {
		return billing.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target := tenant.Scope{OrganizationID: org, ResourceID: org}
	scope, err := s.tenants.Resolve(ctx, actor, target)
	if err == nil && scope != target {
		err = tenant.ErrInvalidScope
	}
	if err = stageResult(ctx, "tenant", err); err != nil {
		return err
	}
	allowed, err := s.permissions.Allowed(ctx, actor, org, authorization.KeyBillingManage)
	if err == nil && !allowed {
		err = ErrPermissionDenied
	}
	return stageResult(ctx, "permission", err)
}
func (s *BillingService) CreateCheckout(ctx context.Context, actor authorization.Principal, r billing.CheckoutRequest) (billing.Session, error) {
	if err := r.Validate(); err != nil {
		return billing.Session{}, err
	}
	if err := s.authorize(ctx, actor, r.OrganizationID); err != nil {
		return billing.Session{}, err
	}
	v, err := s.provider.CreateCheckout(ctx, r)
	if err = stageResult(ctx, "billing", err); err != nil {
		return billing.Session{}, err
	}
	return v, nil
}
func (s *BillingService) ReadSubscription(ctx context.Context, actor authorization.Principal, r billing.SubscriptionRequest) (billing.SubscriptionSnapshot, error) {
	if err := r.Validate(); err != nil {
		return billing.SubscriptionSnapshot{}, err
	}
	if err := s.authorize(ctx, actor, r.OrganizationID); err != nil {
		return billing.SubscriptionSnapshot{}, err
	}
	v, err := s.provider.ReadSubscription(ctx, r)
	if err == nil && (v.OrganizationID != r.OrganizationID || v.ExternalReference != r.ExternalReference) {
		err = billing.ErrOwnership
	}
	if err = stageResult(ctx, "billing", err); err != nil {
		return billing.SubscriptionSnapshot{}, err
	}
	return v, nil
}
func (s *BillingService) CreateCustomerPortal(ctx context.Context, actor authorization.Principal, r billing.PortalRequest) (billing.Session, error) {
	if err := r.Validate(); err != nil {
		return billing.Session{}, err
	}
	if err := s.authorize(ctx, actor, r.OrganizationID); err != nil {
		return billing.Session{}, err
	}
	v, err := s.provider.CreateCustomerPortal(ctx, r)
	if err = stageResult(ctx, "billing", err); err != nil {
		return billing.Session{}, err
	}
	return v, nil
}
