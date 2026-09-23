// Package httpapi contains HTTP adapters for shared application guards.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

// RequestReader extracts credentials and requested scope from a transport. It
// does not authenticate or establish ownership; the guard always does both.
// Feature/permission keys are bound by route composition, never read from HTTP.
type RequestReader func(*http.Request) (application.Request, error)

type EnforcementConfig struct {
	Authenticator application.Authenticator
	Tenants       application.TenantResolver
	Entitlements  entitlement.EntitlementService
	Permissions   authorization.PermissionChecker
	Quotas        application.QuotaChecker
	Permission    authorization.Key
	Request       RequestReader
}

type EntitlementMiddleware struct{ config EnforcementConfig }

func NewEntitlementMiddleware(config EnforcementConfig) (*EntitlementMiddleware, error) {
	if config.Authenticator == nil || config.Tenants == nil || config.Entitlements == nil || config.Permissions == nil || config.Quotas == nil || config.Request == nil || authorization.ValidateKey(config.Permission) != nil {
		return nil, application.ErrInvalidGuard
	}
	return &EntitlementMiddleware{config: config}, nil
}

// RequireEntitlement binds a capability to a route. Direct HTTP requests go
// through authentication, tenant resolution, ownership, permission and quota,
// regardless of frontend visibility or prior CLI checks. No admissions cache.
func (m *EntitlementMiddleware) RequireEntitlement(key feature.Key) (func(http.Handler) http.Handler, error) {
	if m == nil {
		return nil, application.ErrInvalidGuard
	}
	c := m.config
	if c.Request == nil {
		return nil, application.ErrInvalidGuard
	}
	// The middleware uses Authorize so a handler that has written its response
	// cannot be followed by a second error response from post-operation checks.
	guard, err := application.NewGuardedOperation(c.Authenticator, c.Tenants, c.Entitlements, c.Permissions, c.Quotas, application.Requirement{Feature: key, Permission: c.Permission}, func(context.Context, application.Execution) error { return nil })
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if next == nil {
				writeFailure(w, application.ErrInvalidGuard, key)
				return
			}
			request, err := c.Request(r)
			if err != nil {
				writeFailure(w, application.ErrInvalidOperationRequest, key)
				return
			}
			execution, err := guard.Authorize(r.Context(), request)
			if err != nil {
				writeFailure(w, err, key)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), executionContextKey{}, execution)))
		})
	}, nil
}

type executionContextKey struct{}

// ExecutionFromContext returns request-scoped trusted guard output. Do not
// persist it as a worker admission token. Missing output is not authorization.
func ExecutionFromContext(ctx context.Context) (application.Execution, bool) {
	if ctx == nil {
		return application.Execution{}, false
	}
	execution, ok := ctx.Value(executionContextKey{}).(application.Execution)
	return execution, ok
}

func writeFailure(w http.ResponseWriter, err error, key feature.Key) {
	status, code, message := http.StatusServiceUnavailable, "ENFORCEMENT_UNAVAILABLE", "request access could not be established"
	var stage *application.StageError
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
	case errors.Is(err, entitlement.ErrNotEntitled):
		status, code, message = http.StatusForbidden, "FEATURE_NOT_ENTITLED", "feature is disabled in the effective entitlement bundle"
	case errors.Is(err, application.ErrPermissionDenied):
		status, code, message = http.StatusForbidden, "PERMISSION_DENIED", "actor lacks the required permission"
	case errors.Is(err, tenant.ErrInvalidScope):
		status, code, message = http.StatusForbidden, "TENANT_ACCESS_DENIED", "requested resource is not accessible"
	case errors.Is(err, application.ErrInvalidOperationRequest):
		status, code, message = http.StatusBadRequest, "INVALID_REQUEST", "invalid request credentials or scope"
	case errors.Is(err, entitlement.ErrQuotaExceeded), errors.Is(err, entitlement.ErrQuotaDisabled), errors.Is(err, entitlement.ErrQuotaBound):
		status, code, message = http.StatusTooManyRequests, "QUOTA_DENIED", "quota admission denied"
	case errors.As(err, &stage) && stage.Stage == "authentication":
		status, code, message = http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "valid authentication is required"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error struct {
			Code    string      `json:"code"`
			Message string      `json:"message"`
			Feature feature.Key `json:"feature"`
		} `json:"error"`
	}{Error: struct {
		Code    string      `json:"code"`
		Message string      `json:"message"`
		Feature feature.Key `json:"feature"`
	}{code, message, key}})
}
