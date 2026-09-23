package application

import (
	"context"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/sir"
	"safepatch/internal/tenant"
	"safepatch/internal/verification"
	"safepatch/internal/verification/languagepack"
)

// LanguagePackRequirement maps concrete technical operations to existing group
// entitlements. Verifier execution retains its own capability admission mapping.
func LanguagePackRequirement(catalog *feature.FeatureCatalog, capability languagepack.Capability) (Requirement, error) {
	if catalog == nil {
		return Requirement{}, ErrInvalidGuard
	}
	var key feature.Key
	switch capability {
	case languagepack.Detect:
		key = feature.KeyVerifyBasic
	case languagepack.NormalizeSIR:
		key = feature.KeyLanguageAdditionalPacks
	default:
		return Requirement{}, ErrInvalidGuard
	}
	if _, err := catalog.Get(key); err != nil {
		return Requirement{}, err
	}
	return Requirement{Feature: key, Permission: authorization.KeyProofExecute}, nil
}

type LanguagePackService struct {
	registry          *languagepack.Registry
	detect, normalize *ExecutionGuard
}

func NewLanguagePackService(registry *languagepack.Registry, catalog *feature.FeatureCatalog, tenants TenantResolver, features entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker) (*LanguagePackService, error) {
	if registry == nil {
		return nil, ErrInvalidGuard
	}
	s := &LanguagePackService{registry: registry}
	for _, item := range []struct {
		capability  languagepack.Capability
		destination **ExecutionGuard
	}{{languagepack.Detect, &s.detect}, {languagepack.NormalizeSIR, &s.normalize}} {
		r, err := LanguagePackRequirement(catalog, item.capability)
		if err != nil {
			return nil, err
		}
		g, err := NewExecutionGuard(tenants, features, permissions, quotas, r)
		if err != nil {
			return nil, err
		}
		*item.destination = g
	}
	return s, nil
}
func (s *LanguagePackService) Detect(ctx context.Context, actor authorization.Principal, scope tenant.Scope, packID string, target verification.Target) (languagepack.Detection, error) {
	failure := languagepack.Detection{State: verification.DetectionUnknown, Files: []string{}}
	if s == nil || s.registry == nil {
		return failure, ErrInvalidGuard
	}
	if _, err := s.detect.Authorize(ctx, actor, scope); err != nil {
		return failure, err
	}
	if target.Subject.RepositoryID != scope.ResourceID {
		return failure, tenant.ErrInvalidScope
	}
	return s.registry.Detect(ctx, packID, target)
}
func (s *LanguagePackService) Normalize(ctx context.Context, actor authorization.Principal, scope tenant.Scope, packID string, input sir.Extraction) (sir.Representation, error) {
	if s == nil || s.registry == nil {
		return sir.Representation{}, ErrInvalidGuard
	}
	if _, err := s.normalize.Authorize(ctx, actor, scope); err != nil {
		return sir.Representation{}, err
	}
	if input.TenantID != scope.OrganizationID || input.Subject.RepositoryID != scope.ResourceID {
		return sir.Representation{}, tenant.ErrInvalidScope
	}
	return s.registry.Normalize(ctx, packID, input)
}
