package application

import (
	"context"
	"errors"

	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

// RequireFeature is CLI preflight only. It never substitutes for cloud API or
// worker checks, PermissionService, quota or implementation readiness.
func RequireFeature(ctx context.Context, service entitlement.EntitlementService, org string, key feature.Key) error {
	err := entitlement.RequireEntitlement(ctx, service, org, key)
	if err == nil {
		return nil
	}
	result := &CLIError{Code: CodeEntitlementUnavailable, Message: "feature entitlement could not be resolved", cause: err}
	if feature.ValidateKey(key) == nil {
		result.Feature = key
	}
	switch {
	case errors.Is(err, entitlement.ErrNotEntitled):
		result.Code, result.Message = CodeFeatureNotEntitled, "feature is disabled in the effective entitlement bundle"
	case errors.Is(err, entitlement.ErrUnknownFeature):
		result.Code, result.Message = CodeUnknownFeature, "feature is not registered"
	case errors.Is(err, entitlement.ErrInvalidQuery), feature.ValidateKey(key) != nil:
		result.Code, result.Message = CodeInvalidUsage, "invalid feature enforcement request"
	}
	return result
}
