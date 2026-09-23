package onboardingstore

import (
	"context"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
	"slices"
)

type GuardedExecutor struct {
	guard        *application.ExecutionGuard
	entitlements entitlement.EntitlementService
	github       application.OnboardingGitHub
	policies     map[string]feature.Key
	next         Executor
}

func NewGuardedExecutor(tenants application.TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas application.QuotaChecker, github application.OnboardingGitHub, policies []application.OnboardingPolicy, next Executor) (*GuardedExecutor, error) {
	if next == nil || github == nil || len(policies) == 0 {
		return nil, flow.ErrInvalid
	}
	guard, err := application.NewExecutionGuard(tenants, grants, permissions, quotas, application.Requirement{Feature: feature.KeyVerifyBasic, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	p := map[string]feature.Key{}
	for _, v := range policies {
		if feature.ValidateKey(v.Feature) != nil {
			return nil, flow.ErrInvalid
		}
		p[v.ID] = v.Feature
	}
	return &GuardedExecutor{guard, grants, github, p, next}, nil
}
func (g *GuardedExecutor) Execute(ctx context.Context, actor authorization.Principal, v flow.State) (string, string, error) {
	if g == nil || g.guard == nil || ctx == nil || v.Validate() != nil || len(v.Repositories) == 0 {
		return "UNKNOWN", "", flow.ErrInvalid
	}
	key, ok := g.policies[v.PolicyID]
	if !ok {
		return "UNKNOWN", "", flow.ErrInvalid
	}
	for _, repo := range v.Repositories {
		if _, err := g.guard.Authorize(ctx, actor, tenant.Scope{OrganizationID: v.OrganizationID, ResourceID: repo.ID}); err != nil {
			return "UNKNOWN", "", err
		}
		required := []feature.Key{key}
		if repo.Private {
			required = append(required, feature.KeyIntegrationGitHubPrivate)
		}
		for _, k := range required {
			ok, err := g.entitlements.Has(ctx, v.OrganizationID, k)
			if err != nil {
				return "UNKNOWN", "", err
			}
			if !ok {
				return "UNKNOWN", "", entitlement.ErrNotEntitled
			}
		}
	}
	rows, err := g.github.Repositories(ctx, actor, v.InstallationID)
	if err != nil {
		return "UNKNOWN", "", err
	}
	for _, repo := range v.Repositories {
		if !slices.Contains(rows, repo) {
			return "UNKNOWN", "", flow.ErrNotReady
		}
	}
	return g.next.Execute(ctx, actor, v)
}
