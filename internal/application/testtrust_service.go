package application

import (
	"context"
	"errors"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/proof/testtrust"
	"safepatch/internal/tenant"
)

// TestTrustSource loads trusted, revision-pinned extraction and coverage. It must
// authorize storage scope and bind coverage provenance to that exact revision.
// Uploaded scanner JSON alone is not an authoritative implementation.
type TestTrustSource interface {
	Load(context.Context, tenant.Scope, string) (testtrust.Snapshot, error)
}
type TestTrustService struct {
	access *ExecutionGuard
	source TestTrustSource
}

var ErrTestTrustUnavailable = errors.New("TestTrust source unavailable")

// Basic static TestTrust uses the shared basic verification capability. No
// advanced mutation executor is registered by this service.
func NewTestTrustService(tenants TenantResolver, features entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, source TestTrustSource) (*TestTrustService, error) {
	if source == nil {
		return nil, ErrInvalidGuard
	}
	guard, err := NewExecutionGuard(tenants, features, permissions, quotas, Requirement{Feature: feature.KeyVerifyBasic, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	return &TestTrustService{guard, source}, nil
}
func (s *TestTrustService) Compare(ctx context.Context, actor authorization.Principal, scope tenant.Scope, baseRevision, headRevision string) (testtrust.Result, error) {
	if s == nil {
		return testtrust.Unavailable(), ErrInvalidGuard
	}
	if !tenant.ValidID(baseRevision) || !tenant.ValidID(headRevision) {
		return testtrust.Unavailable(), testtrust.ErrInvalid
	}
	if _, err := s.access.Authorize(ctx, actor, scope); err != nil {
		return testtrust.Unavailable(), err
	}
	snapshots := []testtrust.Snapshot{}
	for _, revision := range []string{baseRevision, headRevision} {
		snapshot, err := s.source.Load(ctx, scope, revision)
		if err != nil {
			return testtrust.Unavailable(), ErrTestTrustUnavailable
		}
		f, err := snapshot.Fields()
		if err != nil || f.OrganizationID != scope.OrganizationID || f.Subject.RepositoryID != scope.ResourceID || f.Subject.Revision != revision {
			return testtrust.Unavailable(), ErrTestTrustUnavailable
		}
		snapshots = append(snapshots, snapshot)
	}
	return (testtrust.Engine{}).Compare(ctx, snapshots[0], snapshots[1])
}
