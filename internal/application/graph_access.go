package application

import (
	"context"
	"slices"
	"time"

	"safepatch/internal/authorization"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/graph"
	"safepatch/internal/tenant"
)

type GraphView string

const (
	GraphLocal           GraphView = "LOCAL"
	GraphOrganization    GraphView = "ORGANIZATION"
	GraphCrossRepository GraphView = "CROSS_REPOSITORY"
)

// GraphAccess creates a scope only after resolving every requested repository,
// checking current grants and independent actor permission, and reading current
// retention policy. The authenticated principal and view come from trusted
// composition. Call for every operation/worker start; scopes are not leases.
type GraphAccess struct {
	tenants     TenantResolver
	features    entitlement.EntitlementService
	permissions authorization.PermissionChecker
	limits      entitlement.QuotaStore
	now         func() time.Time
}

func NewGraphAccess(tenants TenantResolver, features entitlement.EntitlementService, permissions authorization.PermissionChecker, limits entitlement.QuotaStore, now func() time.Time) (*GraphAccess, error) {
	if tenants == nil || features == nil || permissions == nil || limits == nil || now == nil {
		return nil, ErrInvalidGuard
	}
	return &GraphAccess{tenants, features, permissions, limits, now}, nil
}
func (a *GraphAccess) Authorize(ctx context.Context, actor authorization.Principal, org string, repositories []string, view GraphView, write bool) (graph.Scope, error) {
	denied := graph.Scope{}
	if a == nil || a.tenants == nil || a.features == nil || a.permissions == nil || a.limits == nil || a.now == nil || ctx == nil || actor.Validate() != nil {
		return denied, ErrInvalidOperationRequest
	}
	if err := ctx.Err(); err != nil {
		return denied, err
	}
	// Do not retain a caller-owned slice across admission stages.
	scope := graph.Scope{TenantID: org, RepositoryIDs: slices.Clone(repositories), AsOf: a.now().UTC().Truncate(time.Microsecond)}
	if scope.Validate() != nil {
		return denied, ErrInvalidOperationRequest
	}
	keys := []feature.Key{feature.KeyGraphLocal}
	switch view {
	case GraphLocal:
		if len(repositories) != 1 {
			return denied, ErrInvalidOperationRequest
		}
	case GraphOrganization:
		keys = append(keys, feature.KeyGraphOrganization)
		if len(repositories) > 1 {
			keys = append(keys, feature.KeyGraphCrossRepo)
		}
	case GraphCrossRepository:
		keys = append(keys, feature.KeyGraphOrganization, feature.KeyGraphCrossRepo)
	default:
		return denied, ErrInvalidOperationRequest
	}
	for _, repo := range scope.RepositoryIDs {
		target := tenant.Scope{OrganizationID: org, ResourceID: repo}
		resolved, err := a.tenants.Resolve(ctx, actor, target)
		if err == nil && resolved != target {
			err = tenant.ErrInvalidScope
		}
		if err := stageResult(ctx, "graph tenant resolution", err); err != nil {
			return denied, err
		}
	}
	for _, key := range keys {
		if err := stageResult(ctx, "graph entitlement", entitlement.RequireEntitlement(ctx, a.features, org, key)); err != nil {
			return denied, err
		}
	}
	permission := authorization.KeyProofView
	if write {
		permission = authorization.KeyProofExecute
	}
	allowed, err := a.permissions.Allowed(ctx, actor, org, permission)
	if err == nil && !allowed {
		err = ErrPermissionDenied
	}
	if err := stageResult(ctx, "graph permission", err); err != nil {
		return denied, err
	}
	var limit domain.EntitlementLimit
	err = a.limits.WithinOrganization(ctx, org, func(tx entitlement.QuotaTransaction) error {
		var err error
		limit, err = tx.Limit(domain.LimitRetentionDays)
		return err
	})
	if err := stageResult(ctx, "graph retention", err); err != nil {
		return denied, err
	}
	if limit.Validate() != nil || limit.Key != domain.LimitRetentionDays {
		return denied, domain.ErrInvalidLimit
	}
	switch limit.State {
	case domain.Disabled:
		return denied, entitlement.ErrNotEntitled
	case domain.Integer:
		if limit.Value == 0 {
			return denied, entitlement.ErrNotEntitled
		}
		// Compare calendar seconds before conversion; arbitrary int64 policies
		// cannot overflow time.Duration or int, even on 32-bit hosts.
		floor := time.Time{}
		if limit.Value <= (scope.AsOf.Unix()-floor.Unix())/86400 {
			scope.NotBefore = scope.AsOf.AddDate(0, 0, -int(limit.Value))
		}
	case domain.Unlimited:
	}
	return scope, nil
}
