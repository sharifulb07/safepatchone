package application

import (
	"context"
	"errors"

	"safepatch/internal/authorization"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

var (
	ErrWorkloadNotFound = errors.New("workload not found")
	ErrInvalidWorkload  = errors.New("invalid stored workload")
)

// Workload comes from trusted storage written after enqueue authentication.
// It contains no entitlement, permission, quota-pass or client approval claims.
type Workload struct {
	ID     string
	Kind   string
	Actor  authorization.Principal
	Target tenant.Scope
}

// WorkloadRepository must read current tenant-scoped durable records. Other
// tenants' IDs return ErrWorkloadNotFound. Queue input supplies only org and ID;
// it cannot supply the actor, resource owner or operation kind. Honor contexts.
type WorkloadRepository interface {
	Get(context.Context, string, string) (Workload, error)
}

type WorkerOperation struct {
	repository WorkloadRepository
	access     *ExecutionGuard
	kind       string
	operation  Operation
}

func NewWorkerOperation(repository WorkloadRepository, tenants TenantResolver, entitlements entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, kind string, requirement Requirement, operation Operation) (*WorkerOperation, error) {
	if repository == nil || !tenant.ValidID(kind) || operation == nil {
		return nil, ErrInvalidGuard
	}
	access, err := NewExecutionGuard(tenants, entitlements, permissions, quotas, requirement)
	if err != nil {
		return nil, err
	}
	return &WorkerOperation{repository: repository, access: access, kind: kind, operation: operation}, nil
}

// Start rechecks authoritative access on EVERY dispatch/retry before expensive
// work. Never use enqueue-time CLI/API decisions as a worker admission token.
// This is not a scheduler, deduplication lease or production-approval engine.
func (w *WorkerOperation) Start(ctx context.Context, org, id string) error {
	if w == nil || w.repository == nil || w.access == nil || w.operation == nil {
		return ErrInvalidGuard
	}
	if ctx == nil || !tenant.ValidID(org) || !tenant.ValidID(id) {
		return ErrInvalidWorkload
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	job, err := w.repository.Get(ctx, org, id)
	if err == nil && (job.ID != id || job.Kind != w.kind || job.Target.OrganizationID != org || job.Target.Validate() != nil || job.Actor.Validate() != nil) {
		err = ErrInvalidWorkload
	}
	if err := stageResult(ctx, "workload lookup", err); err != nil {
		return err
	}
	execution, err := w.access.Authorize(ctx, job.Actor, job.Target)
	if err != nil {
		return err
	}
	return stageResult(ctx, "operation", w.operation(ctx, execution))
}
