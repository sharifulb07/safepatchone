package application

import (
	"context"
	"errors"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/quorum"
	"safepatch/internal/tenant"
)

var ErrQuorumUnavailable = errors.New("authoritative quorum evidence unavailable")

// QuorumSource loads operator-owned policy and the complete relevant retained
// evidence set for an admitted repository/change/run. Implementations must bind
// the run to the repository and enforce graph/storage read access and retention.
// Evidence source families and completeness must never come from scanner JSON.
type QuorumSource interface {
	Load(context.Context, tenant.Scope, string, string, string) (quorum.QuorumPolicy, quorum.Input, error)
}
type EvidenceQuorumService struct {
	access *ExecutionGuard
	source QuorumSource
	now    func() time.Time
}

func NewEvidenceQuorumService(tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, source QuorumSource, now func() time.Time) (*EvidenceQuorumService, error) {
	if source == nil || now == nil {
		return nil, ErrInvalidGuard
	}
	access, err := NewExecutionGuard(tenants, grants, permissions, quotas, Requirement{Feature: feature.KeyProofQuorum, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	return &EvidenceQuorumService{access, source, now}, nil
}

func (s *EvidenceQuorumService) Evaluate(ctx context.Context, actor authorization.Principal, scope tenant.Scope, policyID, changeID, runID string) (quorum.QuorumResult, error) {
	bad := quorum.QuorumResult{}
	if s == nil {
		return bad, ErrInvalidGuard
	}
	for _, id := range []string{policyID, changeID, runID} {
		if !evidence.ValidIdentifier(id) {
			return bad, quorum.ErrInvalid
		}
	}
	if _, err := s.access.Authorize(ctx, actor, scope); err != nil {
		return bad, err
	}
	p, in, err := s.source.Load(ctx, scope, policyID, changeID, runID)
	if err != nil {
		return bad, ErrQuorumUnavailable
	}
	f, err := p.Fields()
	if err != nil || f.ID != policyID || in.TenantID != scope.OrganizationID || in.RepositoryID != scope.ResourceID || in.ChangeID != changeID || in.RunID != runID {
		return bad, ErrQuorumUnavailable
	}
	in.AsOf = s.now()
	result, err := (quorum.EvidenceQuorum{}).Evaluate(ctx, p, in)
	if err != nil {
		return bad, ErrQuorumUnavailable
	}
	if _, err := s.access.authorizeAccess(ctx, actor, scope); err != nil {
		return bad, err
	}
	return result, nil
}
