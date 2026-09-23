package application

import (
	"context"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/policy"
	"safepatch/internal/tenant"
)

// PolicyService admits central policy validation using the existing catalog,
// permission and quota boundaries. It does not publish or activate a policy.
// Existing required policies must still be evaluated after management revocation.
type PolicyService struct{ manage *ExecutionGuard }

func NewPolicyService(catalog *feature.FeatureCatalog, tenants TenantResolver, features entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker) (*PolicyService, error) {
	if catalog == nil {
		return nil, ErrInvalidGuard
	}
	if _, err := catalog.Get(feature.KeyPolicyCentral); err != nil {
		return nil, err
	}
	guard, err := NewExecutionGuard(tenants, features, permissions, quotas, Requirement{Feature: feature.KeyPolicyCentral, Permission: authorization.KeyPolicyManage})
	if err != nil {
		return nil, err
	}
	return &PolicyService{manage: guard}, nil
}
func (s *PolicyService) CompileCentral(ctx context.Context, actor authorization.Principal, organizationID string, raw []byte) (policy.Policy, error) {
	if s == nil || s.manage == nil {
		return policy.Policy{}, ErrInvalidGuard
	}
	if _, err := s.manage.Authorize(ctx, actor, tenant.Scope{OrganizationID: organizationID, ResourceID: organizationID}); err != nil {
		return policy.Policy{}, err
	}
	p, err := policy.Decode(raw)
	if err != nil {
		return policy.Policy{}, err
	}
	d, _ := p.Document()
	if d.TenantID != organizationID {
		return policy.Policy{}, tenant.ErrInvalidScope
	}
	if err := ctx.Err(); err != nil {
		return policy.Policy{}, err
	}
	return p, nil
}
