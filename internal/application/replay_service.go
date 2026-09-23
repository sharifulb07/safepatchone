package application

import (
	"context"
	"errors"
	"time"

	"safepatch/internal/authorization"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/replay"
	"safepatch/internal/tenant"
)

// ReplayStore persists only redacted datasets. Claim is durable and atomic;
// retries must never execute a claimed run again. Results remain immutable.
type ReplayStore interface {
	Put(context.Context, replay.ReplayDataset) error
	Get(context.Context, replay.Scope, string) (replay.ReplayDataset, error)
	Claim(context.Context, replay.Scope, string, string) error
	Finish(context.Context, replay.Scope, string, replay.Result) error
}
type ReplayService struct {
	access   *ExecutionGuard
	read     *ExecutionGuard
	store    ReplayStore
	quotas   *entitlement.QuotaService
	limits   entitlement.QuotaStore
	redactor *replay.Redactor
	engine   *replay.Engine
	now      func() time.Time
}

func NewReplayService(tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, admission QuotaChecker, quotaStore entitlement.QuotaStore, store ReplayStore, redactor *replay.Redactor, engine *replay.Engine, now func() time.Time) (*ReplayService, error) {
	if quotaStore == nil || store == nil || redactor.Digest() == "" || engine == nil || now == nil {
		return nil, ErrInvalidGuard
	}
	access, err := NewExecutionGuard(tenants, grants, permissions, admission, Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute})
	if err != nil {
		return nil, err
	}
	read, err := NewExecutionGuard(tenants, grants, permissions, admission, Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayView})
	if err != nil {
		return nil, err
	}
	quotas, err := entitlement.NewQuotaService(quotaStore, now)
	if err != nil {
		return nil, err
	}
	return &ReplayService{access, read, store, quotas, quotaStore, redactor, engine, now}, nil
}
func (s *ReplayService) Capture(ctx context.Context, actor authorization.Principal, scope replay.Scope, id string, days int64, records []replay.Record) (replay.ReplayDataset, error) {
	if s == nil || days < 1 || days > 366 {
		return replay.ReplayDataset{}, replay.ErrInvalid
	}
	if _, err := s.access.Authorize(ctx, actor, scope.Tenant()); err != nil {
		return replay.ReplayDataset{}, err
	}
	if err := s.quotas.Check(ctx, scope.OrganizationID, limits.LimitDatasetRetentionDays, days); err != nil {
		return replay.ReplayDataset{}, err
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	d, err := replay.NewDataset(scope, id, now, now.Add(time.Duration(days)*24*time.Hour), records, s.redactor)
	if err != nil {
		return replay.ReplayDataset{}, err
	}
	if err := s.store.Put(ctx, d); err != nil {
		return replay.ReplayDataset{}, replay.ErrUnavailable
	}
	return d, nil
}
func (s *ReplayService) dataset(ctx context.Context, scope replay.Scope, id string) (replay.ReplayDataset, error) {
	if !tenant.ValidID(id) {
		return replay.ReplayDataset{}, replay.ErrInvalid
	}
	d, err := s.store.Get(ctx, scope, id)
	if err != nil {
		return replay.ReplayDataset{}, replay.ErrUnavailable
	}
	f, err := d.Fields()
	if err != nil || f.Scope != scope || f.ID != id || f.RedactionDigest != s.redactor.Digest() {
		return replay.ReplayDataset{}, replay.ErrUnavailable
	}
	now := s.now().UTC()
	if now.Before(f.CreatedAt) || !now.Before(f.ExpiresAt) {
		return replay.ReplayDataset{}, replay.ErrDenied
	}
	err = s.limits.WithinOrganization(ctx, scope.OrganizationID, func(tx entitlement.QuotaTransaction) error {
		limit, err := tx.Limit(limits.LimitDatasetRetentionDays)
		if err != nil {
			return err
		}
		if limit.Validate() != nil || limit.Key != limits.LimitDatasetRetentionDays {
			return replay.ErrUnavailable
		}
		if limit.State == limits.Disabled || limit.State == limits.Integer && (limit.Value == 0 || limit.Value <= 366 && !now.Before(f.CreatedAt.Add(time.Duration(limit.Value)*24*time.Hour))) {
			return replay.ErrDenied
		}
		return nil
	})
	if err != nil {
		return replay.ReplayDataset{}, err
	}
	return d, nil
}
func (s *ReplayService) Read(ctx context.Context, actor authorization.Principal, scope replay.Scope, id string) (replay.ReplayDataset, error) {
	if s == nil {
		return replay.ReplayDataset{}, replay.ErrInvalid
	}
	if _, err := s.read.Authorize(ctx, actor, scope.Tenant()); err != nil {
		return replay.ReplayDataset{}, err
	}
	return s.dataset(ctx, scope, id)
}

func (s *ReplayService) Run(ctx context.Context, actor authorization.Principal, scope replay.Scope, datasetID, runID string) (result replay.Result, err error) {
	result.Decision = replay.Unknown
	if s == nil || !tenant.ValidID(runID) || len(runID) > 100 {
		return result, replay.ErrInvalid
	}
	if _, err = s.access.Authorize(ctx, actor, scope.Tenant()); err != nil {
		return result, err
	}
	d, err := s.dataset(ctx, scope, datasetID)
	if err != nil {
		return result, err
	}
	// Claim before reserving: repeated IDs cannot double-dispatch during retries
	// or concurrent calls. Quota denial leaves an inert UNKNOWN run, not a retry.
	if err = s.store.Claim(ctx, scope, runID, d.Digest()); err != nil {
		return result, replay.ErrDenied
	}
	identity := evidence.Digest([]byte(scope.OrganizationID + "\n" + scope.RepositoryID + "\n" + runID))
	capacityID, monthlyID := "replay_capacity_"+identity, "replay_monthly_"+identity
	capacity, err := s.quotas.Reserve(ctx, scope.OrganizationID, limits.LimitReplayConcurrency, 1, capacityID)
	if err != nil {
		return result, err
	}
	if capacity.State != entitlement.Reserved {
		return result, replay.ErrDenied
	}
	stopped := true
	defer func() {
		if !stopped {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, releaseErr := s.quotas.Release(cleanup, scope.OrganizationID, capacityID); releaseErr != nil {
			result.Decision = replay.Unknown
			err = replay.ErrUnavailable
		}
	}()
	monthly, err := s.quotas.Reserve(ctx, scope.OrganizationID, limits.LimitReplayRequestsMonthly, 1, monthlyID)
	if err != nil {
		return result, err
	}
	if monthly.State != entitlement.Reserved {
		return result, replay.ErrDenied
	}
	if _, err = s.quotas.Commit(ctx, scope.OrganizationID, monthlyID); err != nil {
		return result, err
	}
	if _, err = s.access.authorizeAccess(ctx, actor, scope.Tenant()); err != nil {
		return result, err
	}
	// Expiry and retention are checked again after potentially blocking admission.
	if _, err = s.dataset(ctx, scope, datasetID); err != nil {
		return result, err
	}
	f, _ := d.Fields()
	deadline := s.now().Add(5 * time.Minute)
	if f.ExpiresAt.Before(deadline) {
		deadline = f.ExpiresAt
	}
	work, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	stopped = false
	result, err = s.engine.Run(work, d, runID)
	stopped = !errors.Is(err, replay.ErrUnsafe)
	if err != nil {
		return replay.Result{DatasetDigest: d.Digest(), Decision: replay.Unknown}, err
	}
	if _, checkErr := s.dataset(ctx, scope, datasetID); checkErr != nil {
		return replay.Result{DatasetDigest: d.Digest(), Decision: replay.Unknown}, checkErr
	}
	if err = s.store.Finish(ctx, scope, runID, result); err != nil {
		return replay.Result{DatasetDigest: d.Digest(), Decision: replay.Unknown}, replay.ErrUnavailable
	}
	return result, nil
}
