package application

import (
	"context"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

// ExecutionGuard handles the shared checks after authenticating an actor. Only
// trusted authentication or stored job provenance may supply Principal. Syntax
// alone is not authentication. Every call resolves current tenant membership,
// ownership, entitlements, permissions and quota; nothing caches admission.
type ExecutionGuard struct {
	tenants      TenantResolver
	entitlements entitlement.EntitlementService
	permissions  authorization.PermissionChecker
	quotas       QuotaChecker
	requirement  Requirement
}

func NewExecutionGuard(tenants TenantResolver, entitlements entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, requirement Requirement) (*ExecutionGuard, error) {
	if tenants == nil || entitlements == nil || permissions == nil || quotas == nil || feature.ValidateKey(requirement.Feature) != nil || authorization.ValidateKey(requirement.Permission) != nil {
		return nil, ErrInvalidGuard
	}
	return &ExecutionGuard{tenants: tenants, entitlements: entitlements, permissions: permissions, quotas: quotas, requirement: requirement}, nil
}

func (g *ExecutionGuard) Authorize(ctx context.Context, actor authorization.Principal, target tenant.Scope) (Execution, error) {
	execution, err := g.authorizeAccess(ctx, actor, target)
	if err != nil {
		return Execution{}, err
	}
	if err := stageResult(ctx, "quota", g.quotas.Check(ctx, execution)); err != nil {
		return Execution{}, err
	}
	return execution, nil
}

// authorizeAccess refreshes ownership and permission without reserving a second
// quota for an operation already admitted by Authorize.
func (g *ExecutionGuard) authorizeAccess(ctx context.Context, actor authorization.Principal, target tenant.Scope) (Execution, error) {
	if g == nil || g.tenants == nil || g.entitlements == nil || g.permissions == nil || g.quotas == nil {
		return Execution{}, ErrInvalidGuard
	}
	if ctx == nil || actor.Validate() != nil || target.Validate() != nil {
		return Execution{}, ErrInvalidOperationRequest
	}
	if err := ctx.Err(); err != nil {
		return Execution{}, err
	}
	scope, err := g.tenants.Resolve(ctx, actor, target)
	if err == nil && (scope.Validate() != nil || scope != target) {
		err = tenant.ErrInvalidScope
	}
	if err := stageResult(ctx, "tenant resolution", err); err != nil {
		return Execution{}, err
	}
	if err := stageResult(ctx, "entitlement", entitlement.RequireEntitlement(ctx, g.entitlements, scope.OrganizationID, g.requirement.Feature)); err != nil {
		return Execution{}, err
	}
	allowed, err := g.permissions.Allowed(ctx, actor, scope.OrganizationID, g.requirement.Permission)
	if err == nil && !allowed {
		err = ErrPermissionDenied
	}
	if err := stageResult(ctx, "permission", err); err != nil {
		return Execution{}, err
	}
	execution := Execution{Actor: actor, Scope: scope, Requirement: g.requirement}
	return execution, nil
}
