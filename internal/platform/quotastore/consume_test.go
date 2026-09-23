package quotastore

import (
	"context"
	"errors"
	"fmt"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/entitlement"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonthlyConsumeOnceConcurrencyAndPlanTransitions(t *testing.T) {
	key := domain.LimitSafeUpgradesMonthly
	store, s := setup(t, key, domain.Integer, 2)
	var success atomic.Int64
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ConsumeOnce(t.Context(), "org_a", key, 1, "same")
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, entitlement.ErrQuotaConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 || usage(t, store, "org_a", key, "2026-09").Committed != 1 {
		t.Fatal("duplicate execution admission")
	}
	if _, err := s.Release(t.Context(), "org_a", "same"); !errors.Is(err, entitlement.ErrQuotaConflict) {
		t.Fatal("monthly refund", err)
	}
	_ = store.SetLimit(t.Context(), "org_a", domain.EntitlementLimit{Key: key, State: domain.Disabled})
	if _, err := s.ConsumeOnce(t.Context(), "org_a", key, 1, "after_downgrade"); !errors.Is(err, entitlement.ErrQuotaDisabled) {
		t.Fatal(err)
	}
	_ = store.SetLimit(t.Context(), "org_a", domain.EntitlementLimit{Key: key, State: domain.Integer, Value: 2})
	if _, err := s.ConsumeOnce(t.Context(), "org_a", key, 1, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeOnce(t.Context(), "org_a", key, 1, "third"); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal(err)
	}
	october, _ := entitlement.NewQuotaService(store, func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) })
	if _, err := october.ConsumeOnce(t.Context(), "org_a", key, 1, "same"); !errors.Is(err, entitlement.ErrQuotaConflict) {
		t.Fatal("duplicate across month")
	}
	if _, err := october.ConsumeOnce(t.Context(), "org_a", key, 1, "new_month"); err != nil {
		t.Fatal(err)
	}
	if usage(t, store, "org_a", key, "2026-09").Committed != 2 {
		t.Fatal("history lost")
	}
	_ = store.SetLimit(t.Context(), "org_a", domain.EntitlementLimit{Key: key, State: domain.Unlimited})
	for i := range 5 {
		if _, err := october.ConsumeOnce(t.Context(), "org_a", key, 1, fmt.Sprintf("custom_%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ConsumeOnce(t.Context(), "org_b", key, 1, "foreign"); !errors.Is(err, entitlement.ErrQuotaDisabled) {
		t.Fatal("tenant limit leak", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ConsumeOnce(ctx, "org_a", key, 1, "cancel"); err == nil {
		t.Fatal("canceled")
	}
	if _, err := s.ConsumeOnce(t.Context(), "org_a", domain.LimitRepositories, 1, "capacity"); err == nil {
		t.Fatal("non-monthly claim")
	}
}
