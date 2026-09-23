package application

import (
	"context"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/intent"
	"safepatch/internal/spec"
	"safepatch/internal/tenant"
)

// SpecService binds concrete operations to fixed capabilities. Callers cannot
// select intent.basic for analysis or generation. Call from authoritative server
// or worker dispatch after authenticating the actor, including on every retry.
type SpecService struct{ basic, advanced, forge *ExecutionGuard }

func NewSpecService(catalog *feature.FeatureCatalog, tenants TenantResolver, features entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker) (*SpecService, error) {
	s := &SpecService{}
	for _, item := range []struct {
		key feature.Key
		dst **ExecutionGuard
	}{
		{feature.KeyIntentBasic, &s.basic}, {feature.KeyIntentAdvanced, &s.advanced}, {feature.KeyIntentSpecforge, &s.forge},
	} {
		r, err := IntentRequirement(catalog, item.key)
		if err != nil {
			return nil, err
		}
		g, err := NewExecutionGuard(tenants, features, permissions, quotas, r)
		if err != nil {
			return nil, err
		}
		*item.dst = g
	}
	return s, nil
}

func (s *SpecService) Validate(ctx context.Context, actor authorization.Principal, scope tenant.Scope, raw []byte) (spec.Validation, error) {
	failure := spec.Validation{Status: "ERROR", Code: "ADMISSION_DENIED"}
	if s == nil {
		return failure, ErrInvalidGuard
	}
	if _, err := s.basic.Authorize(ctx, actor, scope); err != nil {
		return failure, err
	}
	return (spec.SpecDoctor{}).Validate(ctx, raw)
}

// Analyze includes ambiguity scoring and review-only contract suggestions.
func (s *SpecService) Analyze(ctx context.Context, actor authorization.Principal, scope tenant.Scope, c intent.IntentContract) (spec.Analysis, error) {
	if s == nil {
		return spec.Analysis{}, ErrInvalidGuard
	}
	if _, err := s.advanced.Authorize(ctx, actor, scope); err != nil {
		return spec.Analysis{}, err
	}
	return (spec.SpecDoctor{}).Analyze(ctx, c)
}

func (s *SpecService) Generate(ctx context.Context, actor authorization.Principal, scope tenant.Scope, c intent.IntentContract, bindings []spec.Binding) (spec.Specification, error) {
	if s == nil {
		return spec.Specification{}, ErrInvalidGuard
	}
	if _, err := s.forge.Authorize(ctx, actor, scope); err != nil {
		return spec.Specification{}, err
	}
	return (spec.SpecForge{}).Generate(ctx, c, scope, bindings)
}
