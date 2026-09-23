package application

import (
	"context"
	"slices"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/replay"
	behavior "safepatch/internal/runtime"
)

// BehaviorDatasetReader must enforce current source access and retention.
// ReplayService implements this contract; do not inject a raw storage adapter.
type BehaviorDatasetReader interface {
	Read(context.Context, authorization.Principal, replay.Scope, string) (replay.ReplayDataset, error)
}
type BehaviorService struct {
	describe, manage, evaluate *ExecutionGuard
	reader                     BehaviorDatasetReader
	metrics                    []behavior.Metric
	now                        func() time.Time
}

// Metrics are trusted privacy-approved configuration, not request or LLM input.
// Admission quotas are supplied by the same organization quota policy as other
// operations. This service contains no edition selection or paid default limits.
func NewBehaviorService(tenants TenantResolver, features entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, reader BehaviorDatasetReader, metrics []behavior.Metric, now func() time.Time) (*BehaviorService, error) {
	if reader == nil || now == nil || behavior.ValidateBehaviorMetrics(metrics) != nil {
		return nil, ErrInvalidGuard
	}
	describe, err := NewExecutionGuard(tenants, features, permissions, quotas, Requirement{Feature: feature.KeyRuntimeBehaviorDNA, Permission: authorization.KeyReplayView})
	if err != nil {
		return nil, err
	}
	manage, err := NewExecutionGuard(tenants, features, permissions, quotas, Requirement{Feature: feature.KeyBusinessInvariants, Permission: authorization.KeyPolicyManage})
	if err != nil {
		return nil, err
	}
	evaluate, err := NewExecutionGuard(tenants, features, permissions, quotas, Requirement{Feature: feature.KeyBusinessInvariants, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	return &BehaviorService{describe: describe, manage: manage, evaluate: evaluate, reader: reader, metrics: slices.Clone(metrics), now: now}, nil
}

func (s *BehaviorService) Describe(ctx context.Context, actor authorization.Principal, scope replay.Scope, datasetID string) (behavior.BehaviorDNA, error) {
	if s == nil {
		return behavior.BehaviorDNA{}, ErrInvalidGuard
	}
	if _, err := s.describe.Authorize(ctx, actor, scope.Tenant()); err != nil {
		return behavior.BehaviorDNA{}, err
	}
	d, err := s.reader.Read(ctx, actor, scope, datasetID)
	if err != nil {
		return behavior.BehaviorDNA{}, replay.ErrUnavailable
	}
	f, err := d.Fields()
	now := s.now()
	if err != nil || f.Scope != scope || f.ID != datasetID || now.Before(f.CreatedAt) || !now.Before(f.ExpiresAt) {
		return behavior.BehaviorDNA{}, replay.ErrDenied
	}
	return behavior.BuildBehaviorDNA(ctx, d, s.metrics)
}
func (s *BehaviorService) CompileInvariants(ctx context.Context, actor authorization.Principal, scope replay.Scope, raw []byte) (behavior.InvariantPolicy, error) {
	if s == nil {
		return behavior.InvariantPolicy{}, ErrInvalidGuard
	}
	if _, err := s.manage.Authorize(ctx, actor, scope.Tenant()); err != nil {
		return behavior.InvariantPolicy{}, err
	}
	p, err := behavior.DecodeInvariantPolicy(raw)
	if err != nil {
		return behavior.InvariantPolicy{}, err
	}
	f, _ := p.Fields()
	if f.Scope != scope {
		return behavior.InvariantPolicy{}, replay.ErrDenied
	}
	return p, nil
}
func (s *BehaviorService) Evaluate(ctx context.Context, actor authorization.Principal, scope replay.Scope, d behavior.BehaviorDNA, p behavior.InvariantPolicy) (behavior.InvariantResult, error) {
	denied := behavior.InvariantResult{Schema: "safepatch.invariant-result.v1", Decision: behavior.InvariantUnknown, Checks: []behavior.InvariantCheck{}}
	if s == nil {
		return denied, ErrInvalidGuard
	}
	if _, err := s.evaluate.Authorize(ctx, actor, scope.Tenant()); err != nil {
		return denied, err
	}
	f, err := d.Fields()
	if err != nil || f.Scope != scope {
		return denied, replay.ErrDenied
	}
	if err := s.verifySource(ctx, actor, scope, d); err != nil {
		return denied, err
	}
	return (behavior.BusinessInvariantEngine{}).Evaluate(ctx, d, p, s.now())
}

// Re-derive through the admitted source reader so copied or modified artifacts
// cannot bypass a source retention downgrade or claim authentic observations.
func (s *BehaviorService) verifySource(ctx context.Context, actor authorization.Principal, scope replay.Scope, d behavior.BehaviorDNA) error {
	f, err := d.Fields()
	if err != nil || f.Scope != scope {
		return replay.ErrDenied
	}
	source, err := s.reader.Read(ctx, actor, scope, f.DatasetID)
	if err != nil {
		return replay.ErrUnavailable
	}
	fields, err := source.Fields()
	now := s.now()
	if err != nil || fields.Scope != scope || fields.ID != f.DatasetID || now.Before(fields.CreatedAt) || !now.Before(fields.ExpiresAt) {
		return replay.ErrDenied
	}
	derived, err := behavior.BuildBehaviorDNA(ctx, source, s.metrics)
	if err != nil || derived.Digest() != d.Digest() {
		return replay.ErrDenied
	}
	return nil
}
func (s *BehaviorService) Compare(ctx context.Context, actor authorization.Principal, scope replay.Scope, a, b behavior.BehaviorDNA) (replay.Decision, error) {
	if s == nil {
		return replay.Unknown, ErrInvalidGuard
	}
	if _, err := s.describe.Authorize(ctx, actor, scope.Tenant()); err != nil {
		return replay.Unknown, err
	}
	now := s.now()
	for _, d := range []behavior.BehaviorDNA{a, b} {
		f, err := d.Fields()
		if err != nil || f.Scope != scope || now.Before(f.CreatedAt) || !now.Before(f.ExpiresAt) {
			return replay.Unknown, replay.ErrDenied
		}
		if err := s.verifySource(ctx, actor, scope, d); err != nil {
			return replay.Unknown, err
		}
	}
	return behavior.CompareBehavior(ctx, a, b)
}
