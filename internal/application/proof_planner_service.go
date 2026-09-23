package application

import (
	"context"
	"math"
	"slices"
	"time"

	"safepatch/internal/authorization"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/proof"
	"safepatch/internal/tenant"
)

// ProofProviderSource reports real technical readiness for this pinned subject.
// Implementations are trusted, context-aware adapters. Registration, an enabled
// grant or an LLM suggestion alone must not be reported as AVAILABLE. No provider
// execution occurs here. Repository/provider errors are redacted at this boundary.
type ProofProviderSource interface {
	Check(context.Context, tenant.Scope, evidence.Subject, proof.Provider) (proof.Availability, error)
}

// Bindings are deployment configuration, never request fields. Features includes
// every prerequisite (including organization intelligence/advanced provider keys).
// Independent provider-specific permission is checked after all grants pass.
type ProofProviderBinding struct {
	ProviderID string
	Features   []feature.Key
	Permission authorization.Key
}
type ProofPlanningRequest struct {
	Subject evidence.Subject
	Target  proof.Level
}
type ProofPlannerService struct {
	planner     *proof.ProofPlanner
	access      *ExecutionGuard
	features    entitlement.EntitlementService
	permissions authorization.PermissionChecker
	source      ProofProviderSource
	budgets     entitlement.QuotaStore
	bindings    map[string]ProofProviderBinding
	now         func() time.Time
}

func NewProofPlannerService(planner *proof.ProofPlanner, catalog *feature.FeatureCatalog, tenants TenantResolver, features entitlement.EntitlementService, permissions authorization.PermissionChecker, planningQuota QuotaChecker, budgets entitlement.QuotaStore, source ProofProviderSource, bindings []ProofProviderBinding, now func() time.Time) (*ProofPlannerService, error) {
	if planner == nil || planner.ConfigurationHash() == "" || catalog == nil || budgets == nil || source == nil || now == nil || len(bindings) != len(planner.Providers()) {
		return nil, ErrInvalidGuard
	}
	if _, err := catalog.Get(feature.KeyProofPlanner); err != nil {
		return nil, err
	}
	access, err := NewExecutionGuard(tenants, features, permissions, planningQuota, Requirement{Feature: feature.KeyProofPlanner, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	s := &ProofPlannerService{planner: planner, access: access, features: features, permissions: permissions, source: source, budgets: budgets, bindings: map[string]ProofProviderBinding{}, now: now}
	providers := map[string]bool{}
	resources := map[string]bool{}
	for _, p := range planner.Providers() {
		providers[p.ID] = true
		for _, c := range p.Costs {
			// Only monthly consumables are summed for a plan. Reusable capacity and
			// retention are separate dispatch/storage policies, not cumulative spend.
			kind, err := domain.LimitKey(c.Resource).Accounting()
			if err != nil || kind != domain.Monthly {
				return nil, ErrInvalidGuard
			}
			resources[c.Resource] = true
		}
	}
	if len(resources) > proof.MaxResources {
		return nil, ErrInvalidGuard
	}
	for _, b := range bindings {
		if !providers[b.ProviderID] || s.bindings[b.ProviderID].ProviderID != "" || len(b.Features) == 0 || len(b.Features) > 32 || authorization.ValidateKey(b.Permission) != nil {
			return nil, ErrInvalidGuard
		}
		b.Features = slices.Clone(b.Features)
		slices.Sort(b.Features)
		for i, k := range b.Features {
			if i > 0 && b.Features[i-1] == k {
				return nil, ErrInvalidGuard
			}
			if _, err := catalog.Get(k); err != nil {
				return nil, err
			}
		}
		s.bindings[b.ProviderID] = b
	}
	return s, nil
}

// Plan rechecks ownership, grants, permission, readiness and current budget on
// every call. Planning has its own quota hook; execution is NOT reserved here.
// Workers must recheck actual provider admission and reserve applicable quotas
// through existing execution guards/QuotaService immediately before dispatch.
func (s *ProofPlannerService) Plan(ctx context.Context, actor authorization.Principal, scope tenant.Scope, r ProofPlanningRequest) (proof.Plan, error) {
	if s == nil || s.access == nil {
		return proof.Plan{}, ErrInvalidGuard
	}
	if ctx == nil || r.Subject.Validate() != nil || r.Target.Rank() < 0 {
		return proof.Plan{}, proof.ErrInvalidRequest
	}
	if _, err := s.access.Authorize(ctx, actor, scope); err != nil {
		return proof.Plan{}, err
	}
	if r.Subject.RepositoryID != scope.ResourceID {
		return proof.Plan{}, tenant.ErrInvalidScope
	}
	request := proof.Request{TenantID: scope.OrganizationID, Subject: r.Subject, Target: r.Target, Providers: []proof.ProviderState{}}
	resources := map[domain.LimitKey]bool{}
	for _, p := range s.planner.ProvidersFor(r.Target) {
		b := s.bindings[p.ID]
		state := proof.Available
		for _, key := range b.Features {
			has, err := s.features.Has(ctx, scope.OrganizationID, key)
			if err := stageResult(ctx, "proof provider entitlement", err); err != nil {
				return proof.Plan{}, err
			}
			if !has {
				state = proof.Disabled
				break
			}
		}
		if state == proof.Available {
			allowed, err := s.permissions.Allowed(ctx, actor, scope.OrganizationID, b.Permission)
			if err := stageResult(ctx, "proof provider permission", err); err != nil {
				return proof.Plan{}, err
			}
			if !allowed {
				state = proof.Disabled
			}
		}
		if state == proof.Available {
			var err error
			probe := p
			probe.Costs = slices.Clone(p.Costs)
			state, err = s.source.Check(ctx, scope, r.Subject, probe)
			if err == nil && !state.Valid() {
				err = proof.ErrInvalidRequest
			}
			if err := stageResult(ctx, "proof provider readiness", err); err != nil {
				return proof.Plan{}, err
			}
		}
		request.Providers = append(request.Providers, proof.ProviderState{ID: p.ID, Availability: state})
		if state == proof.Available {
			for _, c := range p.Costs {
				resources[domain.LimitKey(c.Resource)] = true
			}
		}
	}
	var err error
	request.Budgets, err = s.remaining(ctx, scope.OrganizationID, resources)
	if err := stageResult(ctx, "proof planning budget", err); err != nil {
		return proof.Plan{}, err
	}
	return s.planner.Plan(ctx, request)
}

// Read-only advisory snapshot in the same organization-serialized quota store.
// Reserved units count against the plan as well as already committed units.
func (s *ProofPlannerService) remaining(ctx context.Context, org string, resources map[domain.LimitKey]bool) ([]proof.Budget, error) {
	out := []proof.Budget{}
	if len(resources) == 0 {
		return out, nil
	}
	now := s.now().UTC()
	if now.IsZero() || now.Year() < 1 || now.Year() > 9999 {
		return nil, entitlement.ErrInvalidQuota
	}
	period := now.Format("2006-01")
	keys := make([]domain.LimitKey, 0, len(resources))
	for k := range resources {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	err := s.budgets.WithinOrganization(ctx, org, func(tx entitlement.QuotaTransaction) error {
		for _, k := range keys {
			limit, err := tx.Limit(k)
			if err != nil {
				return err
			}
			if limit.Key != k || limit.Validate() != nil {
				return entitlement.ErrQuotaData
			}
			if limit.State == domain.Disabled {
				out = append(out, proof.Budget{Resource: string(k), Remaining: 0})
				continue
			}
			usage, err := tx.Usage(k, period)
			if err != nil {
				return err
			}
			if usage.Reserved < 0 || usage.Committed < 0 || usage.Reserved > math.MaxInt64-usage.Committed {
				return entitlement.ErrQuotaData
			}
			maximum := int64(math.MaxInt64)
			if limit.State == domain.Integer {
				maximum = limit.Value
			}
			remaining := int64(0)
			used := usage.Reserved + usage.Committed
			if used < maximum {
				remaining = maximum - used
			}
			out = append(out, proof.Budget{Resource: string(k), Remaining: remaining})
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
