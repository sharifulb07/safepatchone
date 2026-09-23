package application

import (
	"context"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/intent/guard"
	"safepatch/internal/tenant"
)

// IntentGuardService admits deterministic evaluation only. It does not perform
// advanced inference, generate specifications or manage organization templates.
type IntentGuardService struct{ access *ExecutionGuard }

func NewIntentGuardService(catalog *feature.FeatureCatalog, tenants TenantResolver, features entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker) (*IntentGuardService, error) {
	r, err := IntentRequirement(catalog, feature.KeyIntentBasic)
	if err != nil {
		return nil, err
	}
	g, err := NewExecutionGuard(tenants, features, permissions, quotas, r)
	if err != nil {
		return nil, err
	}
	return &IntentGuardService{access: g}, nil
}

func (s *IntentGuardService) Evaluate(ctx context.Context, actor authorization.Principal, scope tenant.Scope, in guard.Input) (guard.Result, error) {
	failure := guard.Result{IntentCompliance: guard.Assessment{Status: "ERROR", Code: "ADMISSION_DENIED"}, ScopeDrift: guard.ScopeResult{Assessment: guard.Assessment{Status: "ERROR", Code: "ADMISSION_DENIED"}}}
	if s == nil {
		return failure, ErrInvalidGuard
	}
	if _, err := s.access.Authorize(ctx, actor, scope); err != nil {
		return failure, err
	}
	if in.Identity.TenantID != scope.OrganizationID || in.Identity.RepositoryID != scope.ResourceID {
		return failure, tenant.ErrInvalidScope
	}
	return (guard.IntentGuard{}).Evaluate(ctx, in)
}
