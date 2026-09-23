package application

import (
	"context"
	"errors"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/composition"
	"safepatch/internal/tenant"
)

var ErrCompositionUnavailable = errors.New("authoritative composition evidence unavailable")

// CompositionSource loads server-owned manifests and current retained evidence.
// Manifest may expose repository IDs only to already admitted organization users;
// Evidence is called only after every component repository has been admitted.
// Implementations must load completed shared-verifier/policy results, never trust
// uploaded PASS claims, and report revoked/deleted proofs as missing.
type CompositionSource interface {
	Manifest(context.Context, string, string) (composition.Manifest, error)
	Evidence(context.Context, composition.Manifest) (composition.Input, error)
}
type CompositionGuard struct {
	access *ExecutionGuard
	source CompositionSource
	now    func() time.Time
}

func NewCompositionGuard(tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, source CompositionSource, now func() time.Time) (*CompositionGuard, error) {
	if source == nil || now == nil {
		return nil, ErrInvalidGuard
	}
	access, err := NewExecutionGuard(tenants, grants, permissions, quotas, Requirement{Feature: feature.KeyProofComposition, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	return &CompositionGuard{access, source, now}, nil
}
func (s *CompositionGuard) Evaluate(ctx context.Context, actor authorization.Principal, org, id string) (composition.Result, error) {
	bad := composition.Result{}
	if s == nil || !evidence.ValidIdentifier(id) {
		return bad, ErrCompositionUnavailable
	}
	if _, err := s.access.Authorize(ctx, actor, tenant.Scope{OrganizationID: org, ResourceID: org}); err != nil {
		return bad, err
	}
	m, err := s.source.Manifest(ctx, org, id)
	if err != nil {
		return bad, ErrCompositionUnavailable
	}
	f, err := m.Fields()
	if err != nil || f.OrganizationID != org || f.ID != id {
		return bad, ErrCompositionUnavailable
	}
	admit := func() error {
		seen := map[string]bool{}
		for _, component := range f.Components {
			if seen[component.RepositoryID] {
				continue
			}
			seen[component.RepositoryID] = true
			if _, err := s.access.authorizeAccess(ctx, actor, tenant.Scope{OrganizationID: org, ResourceID: component.RepositoryID}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := admit(); err != nil {
		return bad, err
	}
	input, err := s.source.Evidence(ctx, m)
	if err != nil {
		return bad, ErrCompositionUnavailable
	}
	result, err := (composition.CompositionGuard{}).Evaluate(ctx, m, input, s.now())
	if err != nil {
		return bad, ErrCompositionUnavailable
	}
	if err := admit(); err != nil {
		return bad, err
	}
	r, _ := result.Fields()
	now := s.now()
	if now.Before(r.EvaluatedAt) || !now.Before(r.ValidUntil) {
		return bad, ErrCompositionUnavailable
	}
	return result, nil
}
