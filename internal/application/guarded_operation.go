package application

import (
	"context"
	"errors"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

// Authentication adapters validate credential authenticity, expiry, revocation,
// and intended audience. A Principal's shape alone is not authentication.
type Authenticator interface {
	Authenticate(context.Context, string) (authorization.Principal, error)
}

// Resolve must establish current actor membership and resource ownership within
// the requested organization using trusted storage. It must reject cross-tenant
// resources and may not infer scope from a caller-controlled resource ID alone.
type TenantResolver interface {
	Resolve(context.Context, authorization.Principal, tenant.Scope) (tenant.Scope, error)
}

type Requirement struct {
	Feature    feature.Key
	Permission authorization.Key
}
type Execution struct {
	Actor       authorization.Principal
	Scope       tenant.Scope
	Requirement Requirement
}

// Check must reserve applicable additive quotas atomically, or explicitly allow
// an unmetered operation. It is never optional. This application admission hook
// differs from QuotaService.Check, which is advisory: adapters must call Reserve
// for metered work and carry the reservation into Commit/Release settlement.
type QuotaChecker interface {
	Check(context.Context, Execution) error
}
type Operation func(context.Context, Execution) error

// Request contains no role, permission key, feature key, or authenticated actor.
// Credentials are passed only to authentication, never to later stages.
type Request struct {
	Credential string
	Target     tenant.Scope
}

var (
	ErrInvalidGuard            = errors.New("invalid guarded operation configuration")
	ErrInvalidOperationRequest = errors.New("invalid operation request")
	ErrEntitlementDenied       = entitlement.ErrNotEntitled
	ErrPermissionDenied        = errors.New("actor lacks the required permission")
)

// StageError keeps transport-facing messages stable without exposing raw
// credentials, membership records, or untrusted adapter error strings. Internal
// callers can use errors.Is/As for the underlying cause; do not serialize it.
type StageError struct {
	Stage string
	cause error
}

func (e *StageError) Error() string { return "request " + e.Stage + " failed" }
func (e *StageError) Unwrap() error { return e.cause }

// GuardedOperation binds trusted requirements and behavior at composition time.
// It is not a production execution lease: authorization/policy may need rechecks
// for queued or long-running work. Permission alone is not production approval;
// production dispatch still requires trusted policy and approval enforcement.
type GuardedOperation struct {
	auth      Authenticator
	access    *ExecutionGuard
	operation Operation
}

func NewGuardedOperation(auth Authenticator, tenants TenantResolver, entitlements entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, requirement Requirement, operation Operation) (*GuardedOperation, error) {
	if auth == nil || operation == nil {
		return nil, ErrInvalidGuard
	}
	access, err := NewExecutionGuard(tenants, entitlements, permissions, quotas, requirement)
	if err != nil {
		return nil, err
	}
	return &GuardedOperation{auth: auth, access: access, operation: operation}, nil
}

func (g *GuardedOperation) Execute(ctx context.Context, request Request) error {
	if g == nil || g.operation == nil {
		return ErrInvalidGuard
	}
	execution, err := g.Authorize(ctx, request)
	if err != nil {
		return err
	}
	return stageResult(ctx, "operation", g.operation(ctx, execution))
}

// Authorize lets HTTP adapters check access before writing a response. Returned
// execution metadata is request-scoped and is never a durable authorization lease.
func (g *GuardedOperation) Authorize(ctx context.Context, request Request) (Execution, error) {
	if g == nil || g.auth == nil || g.access == nil {
		return Execution{}, ErrInvalidGuard
	}
	if ctx == nil || len(request.Credential) == 0 || len(request.Credential) > 16384 || request.Target.Validate() != nil {
		return Execution{}, ErrInvalidOperationRequest
	}
	if err := ctx.Err(); err != nil {
		return Execution{}, err
	}
	actor, err := g.auth.Authenticate(ctx, request.Credential)
	if err == nil {
		err = actor.Validate()
	}
	if err = stageResult(ctx, "authentication", err); err != nil {
		return Execution{}, err
	}
	return g.access.Authorize(ctx, actor, request.Target)
}

func stageResult(ctx context.Context, stage string, err error) error {
	if cancelled := ctx.Err(); cancelled != nil {
		err = cancelled
	}
	if err != nil {
		return &StageError{Stage: stage, cause: err}
	}
	return nil
}
