package entitlement

import (
	"context"
	"fmt"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
)

// FeatureDenied is ownership denial, never actor authorization or readiness.
// Its feature is validated before construction. Backend errors are not denials.
type FeatureDenied struct{ Feature feature.Key }

func (e *FeatureDenied) Error() string { return fmt.Sprintf("FEATURE_NOT_ENTITLED: %s", e.Feature) }
func (e *FeatureDenied) Unwrap() error { return ErrNotEntitled }

// RequireEntitlement checks current ownership every time; no prior client,
// frontend, queue or helper decision is cached. Services must read authoritative
// state or validate snapshot revisions on every Has call, with no stale fallback.
// Authentication, tenant ownership, permissions and quotas are separate checks.
func RequireEntitlement(ctx context.Context, service EntitlementService, org string, key feature.Key) error {
	if ctx == nil || !domain.ValidID(org) {
		return ErrInvalidQuery
	}
	if err := feature.ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if service == nil {
		return ErrInvalidResolver
	}
	owned, err := service.Has(ctx, org, key)
	if cancelled := ctx.Err(); cancelled != nil {
		return cancelled
	}
	if err != nil {
		return err
	}
	if !owned {
		return &FeatureDenied{Feature: key}
	}
	return nil
}
