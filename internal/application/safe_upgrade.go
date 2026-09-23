package application

import (
	"context"
	"errors"
	"safepatch/internal/authorization"
	"safepatch/internal/domain"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
	"safepatch/internal/verification/upgrade"
)

var ErrSafeUpgradeUnavailable = errors.New("safe upgrade unavailable or stale")

// UpgradeSource must load a complete, server-owned candidate from pinned source
// revisions, confirm dependency versions/file digests and exclude unrelated edits.
// It must never resolve mutable package tags during verification or execute hooks.
type UpgradeSource interface {
	LoadUpgrade(context.Context, tenant.Scope, string) (upgrade.Candidate, error)
	IsCurrent(context.Context, domain.Change) (bool, error)
}
type SafeUpgradeResult struct {
	Schema            string         `json:"schema"`
	CandidateID       string         `json:"candidate_id"`
	ChangeID          string         `json:"change_id"`
	RunID             string         `json:"run_id"`
	Decision          trust.Decision `json:"decision"`
	EvidenceReference string         `json:"evidence_reference"`
}
type SafeUpgradeService struct {
	guard    *ExecutionGuard
	quota    *entitlement.QuotaService
	source   UpgradeSource
	workflow *ChangeWorkflow
}

func NewSafeUpgradeService(tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, meter *entitlement.QuotaService, source UpgradeSource, workflow *ChangeWorkflow) (*SafeUpgradeService, error) {
	if meter == nil || source == nil || workflow == nil {
		return nil, ErrInvalidGuard
	}
	guard, err := NewExecutionGuard(tenants, grants, permissions, quotas, Requirement{Feature: feature.KeyAutomationSafeUpgrade, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	return &SafeUpgradeService{guard, meter, source, workflow}, nil
}

// Verify records a verification outcome, never approval to merge or deploy. A
// retry of the same candidate/run is rejected before running the verifier again.
func (s *SafeUpgradeService) Verify(ctx context.Context, actor authorization.Principal, scope tenant.Scope, id, run string) (SafeUpgradeResult, error) {
	bad := SafeUpgradeResult{Schema: "safepatch.safe-upgrade-result.v1", Decision: trust.Unknown}
	if s == nil || !upgrade.ValidID(id) || !evidence.ValidIdentifier(run) {
		return bad, ErrSafeUpgradeUnavailable
	}
	if _, err := s.guard.Authorize(ctx, actor, scope); err != nil {
		return bad, err
	}
	c, err := s.source.LoadUpgrade(ctx, scope, id)
	f, validation := c.Fields()
	if err != nil || validation != nil || c.ID() != id || f.Change.OrganizationID != scope.OrganizationID || f.Change.RepositoryID != scope.ResourceID {
		return bad, ErrSafeUpgradeUnavailable
	}
	current := func() error {
		ok, err := s.source.IsCurrent(ctx, f.Change)
		if err != nil || !ok || ctx.Err() != nil {
			return ErrSafeUpgradeUnavailable
		}
		return nil
	}
	if err := current(); err != nil {
		return bad, err
	}
	if _, err := s.guard.authorizeAccess(ctx, actor, scope); err != nil {
		return bad, err
	}
	reservation := "safeupgrade_" + evidence.Digest([]byte(id+"\n"+run))
	if _, err := s.quota.ConsumeOnce(ctx, scope.OrganizationID, limits.LimitSafeUpgradesMonthly, 1, reservation); err != nil {
		return bad, err
	}
	result, err := s.workflow.RunHosted(ctx, actor, f.Change, run)
	if err != nil || result.ChangeID != f.Change.ID() || result.EvidenceReference == "" {
		return bad, ErrSafeUpgradeUnavailable
	}
	switch result.Decision {
	case trust.Pass, trust.Fail, trust.Unknown:
	default:
		return bad, ErrSafeUpgradeUnavailable
	}
	if err := current(); err != nil {
		return bad, err
	}
	if _, err := s.guard.authorizeAccess(ctx, actor, scope); err != nil {
		return bad, err
	}
	return SafeUpgradeResult{Schema: bad.Schema, CandidateID: c.ID(), ChangeID: result.ChangeID, RunID: run, Decision: result.Decision, EvidenceReference: result.EvidenceReference}, nil
}
