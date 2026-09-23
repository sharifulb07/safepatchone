package entitlement_test

import (
	"context"
	"errors"
	"testing"

	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

type ownershipCheck func(context.Context, string, feature.Key) (bool, error)

func (f ownershipCheck) Has(ctx context.Context, org string, key feature.Key) (bool, error) {
	return f(ctx, org, key)
}
func (ownershipCheck) Limit(context.Context, string, feature.Key) (int64, error) {
	panic("unexpected limit")
}
func (ownershipCheck) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	panic("unexpected resolution")
}

func TestRequireEntitlementChecksEveryCallAndPreservesFailures(t *testing.T) {
	owned := true
	calls := 0
	service := ownershipCheck(func(_ context.Context, org string, key feature.Key) (bool, error) {
		calls++
		if org != "org_a" || key != feature.KeyRuntimeReplay {
			t.Fatal(org, key)
		}
		return owned, nil
	})
	if err := entitlement.RequireEntitlement(t.Context(), service, "org_a", feature.KeyRuntimeReplay); err != nil {
		t.Fatal(err)
	}
	owned = false
	err := entitlement.RequireEntitlement(t.Context(), service, "org_a", feature.KeyRuntimeReplay)
	var denied *entitlement.FeatureDenied
	if !errors.Is(err, entitlement.ErrNotEntitled) || !errors.As(err, &denied) || denied.Feature != feature.KeyRuntimeReplay || calls != 2 {
		t.Fatal(err, calls)
	}
	broken := errors.New("backend failure")
	for _, answer := range []bool{false, true} {
		service := ownershipCheck(func(context.Context, string, feature.Key) (bool, error) { return answer, broken })
		if err := entitlement.RequireEntitlement(t.Context(), service, "org_a", feature.KeyRuntimeReplay); !errors.Is(err, broken) || errors.Is(err, entitlement.ErrNotEntitled) {
			t.Fatal("backend failure became grant or denial", err)
		}
	}
}

func TestRequireEntitlementRejectsInvalidInputAndCancellation(t *testing.T) {
	service := ownershipCheck(func(context.Context, string, feature.Key) (bool, error) {
		t.Fatal("invalid request reached adapter")
		return true, nil
	})
	for _, tc := range []struct {
		ctx context.Context
		org string
		key feature.Key
	}{
		{nil, "org_a", feature.KeyRuntimeReplay}, {t.Context(), "../org_a", feature.KeyRuntimeReplay}, {t.Context(), "org_a", "runtime.replay\nPASS"},
	} {
		if entitlement.RequireEntitlement(tc.ctx, service, tc.org, tc.key) == nil {
			t.Fatal(tc)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := entitlement.RequireEntitlement(ctx, service, "org_a", feature.KeyRuntimeReplay); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	cancelling := ownershipCheck(func(context.Context, string, feature.Key) (bool, error) { cancel(); return true, nil })
	if err := entitlement.RequireEntitlement(ctx, cancelling, "org_a", feature.KeyRuntimeReplay); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled success authorized", err)
	}
	if err := entitlement.RequireEntitlement(t.Context(), nil, "org_a", feature.KeyRuntimeReplay); !errors.Is(err, entitlement.ErrInvalidResolver) {
		t.Fatal(err)
	}
}
