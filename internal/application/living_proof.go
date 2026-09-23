package application

import (
	"context"
	"errors"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/living"
	"safepatch/internal/tenant"
)

var ErrLivingProofUnavailable = errors.New("authoritative living proof unavailable")

// LivingProofSource loads the server-owned dependency manifest and a consistent,
// bounded current observation snapshot. It must check repository ownership and
// evidence retention, and must not accept client/LLM claims as verification output.
// Revoked/deleted dependencies must be unavailable, not returned as old digests.
type LivingProofSource interface {
	LoadLivingProof(context.Context, tenant.Scope, string) (living.ProofDependencyGraph, living.Snapshot, error)
}
type LivingProofArchive interface {
	Save(context.Context, living.ProofDependencyGraph, living.LivingProof) (evidence.OriginalReference, error)
}
type LivingProofService struct {
	guard   *ExecutionGuard
	source  LivingProofSource
	archive LivingProofArchive
	now     func() time.Time
}

func NewLivingProofService(tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, source LivingProofSource, archive LivingProofArchive, now func() time.Time) (*LivingProofService, error) {
	if source == nil || archive == nil || now == nil {
		return nil, ErrInvalidGuard
	}
	guard, err := NewExecutionGuard(tenants, grants, permissions, quotas, Requirement{Feature: feature.KeyProofLiving, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	return &LivingProofService{guard, source, archive, now}, nil
}

// Reevaluate rechecks current admission for every invocation, including workers.
// The returned re-verification IDs are a plan, never a job or deployment approval.
func (s *LivingProofService) Reevaluate(ctx context.Context, actor authorization.Principal, scope tenant.Scope, id string) (living.LivingProof, evidence.OriginalReference, error) {
	bad, ref := living.LivingProof{}, evidence.OriginalReference{}
	if s == nil || !evidence.ValidIdentifier(id) {
		return bad, ref, ErrLivingProofUnavailable
	}
	if _, err := s.guard.Authorize(ctx, actor, scope); err != nil {
		return bad, ref, err
	}
	g, snapshot, err := s.source.LoadLivingProof(ctx, scope, id)
	if err != nil {
		return bad, ref, ErrLivingProofUnavailable
	}
	f, err := g.Fields()
	if err != nil || f.ID != id || f.Change.OrganizationID != scope.OrganizationID || f.Change.RepositoryID != scope.ResourceID {
		return bad, ref, ErrLivingProofUnavailable
	}
	p, err := living.Evaluate(ctx, g, snapshot, s.now())
	if err != nil {
		return bad, ref, ErrLivingProofUnavailable
	}
	if _, err := s.guard.authorizeAccess(ctx, actor, scope); err != nil {
		return bad, ref, err
	}
	ref, err = s.archive.Save(ctx, g, p)
	raw, _ := p.MarshalJSON()
	if err != nil || ctx.Err() != nil || ref.Validate() != nil || ref.SHA256 != p.Digest() || ref.Bytes != int64(len(raw)) {
		return bad, evidence.OriginalReference{}, ErrLivingProofUnavailable
	}
	if _, err := s.guard.authorizeAccess(ctx, actor, scope); err != nil {
		return bad, evidence.OriginalReference{}, err
	}
	// Check after slow storage AND the last access read. Clock rollback must not
	// make a future-dated evaluation appear current either.
	fields, _ := p.Fields()
	now := s.now()
	if now.Before(fields.EvaluatedAt) || !now.Before(fields.ValidUntil) {
		return bad, evidence.OriginalReference{}, ErrLivingProofUnavailable
	}
	return p, ref, nil
}
