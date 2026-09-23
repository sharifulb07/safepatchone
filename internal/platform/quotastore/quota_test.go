package quotastore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/entitlement"
)

func setup(t *testing.T, key domain.LimitKey, state domain.LimitState, value int64) (*Memory, *entitlement.QuotaService) {
	t.Helper()
	store, err := NewMemory(10, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetLimit(t.Context(), "org_a", domain.EntitlementLimit{Key: key, State: state, Value: value}); err != nil {
		t.Fatal(err)
	}
	service, err := entitlement.NewQuotaService(store, func() time.Time { return time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	return store, service
}

func usage(t *testing.T, store entitlement.QuotaStore, org string, key domain.LimitKey, period string) entitlement.Usage {
	t.Helper()
	var result entitlement.Usage
	if err := store.WithinOrganization(t.Context(), org, func(tx entitlement.QuotaTransaction) error {
		var err error
		result, err = tx.Usage(key, period)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAllQuotaKeysAndStates(t *testing.T) {
	for _, key := range domain.LimitKeys() {
		for _, state := range []domain.LimitState{domain.Disabled, domain.Integer, domain.Unlimited} {
			t.Run(string(key)+"/"+string(state), func(t *testing.T) {
				value := int64(0)
				if state == domain.Integer {
					value = 2
				}
				_, service := setup(t, key, state, value)
				err := service.Check(t.Context(), "org_a", key, 2)
				if state == domain.Disabled {
					if !errors.Is(err, entitlement.ErrQuotaDisabled) {
						t.Fatal(err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				kind, _ := key.Accounting()
				if kind == domain.Ceiling {
					if _, err := service.Reserve(t.Context(), "org_a", key, 1, "r"); !errors.Is(err, entitlement.ErrInvalidQuota) {
						t.Fatal(err)
					}
					if err := service.Check(t.Context(), "org_a", key, 2); err != nil {
						t.Fatal("ceiling accumulated", err)
					}
				} else {
					if _, err := service.Reserve(t.Context(), "org_a", key, 2, "r"); err != nil {
						t.Fatal(err)
					}
					if _, err := service.Commit(t.Context(), "org_a", "r"); err != nil {
						t.Fatal(err)
					}
				}
				err = service.Check(t.Context(), "org_a", key, 3)
				if state == domain.Integer && !errors.Is(err, entitlement.ErrQuotaExceeded) {
					t.Fatal(err)
				}
				if state == domain.Unlimited && err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestCapacityLifecycleAndIdempotency(t *testing.T) {
	key := domain.LimitRepositories
	store, service := setup(t, key, domain.Integer, 2)
	ctx := t.Context()
	first, err := service.Reserve(ctx, "org_a", key, 2, "create_repo")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		r, err := service.Reserve(ctx, "org_a", key, 2, "create_repo")
		if err != nil || r != first {
			t.Fatal(r, err)
		}
	}
	if _, err := service.Reserve(ctx, "org_a", key, 1, "create_repo"); !errors.Is(err, entitlement.ErrQuotaConflict) {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, "org_a", domain.LimitDevelopers, 2, "create_repo"); !errors.Is(err, entitlement.ErrQuotaConflict) {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := service.Commit(ctx, "org_a", "create_repo"); err != nil {
			t.Fatal(err)
		}
	}
	if got := usage(t, store, "org_a", key, "lifetime"); got != (entitlement.Usage{Committed: 2}) {
		t.Fatal(got)
	}
	if r, err := service.Reserve(ctx, "org_a", key, 2, "create_repo"); err != nil || r.State != entitlement.Committed {
		t.Fatal(r, err)
	}
	for range 2 {
		if _, err := service.Release(ctx, "org_a", "create_repo"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Commit(ctx, "org_a", "create_repo"); !errors.Is(err, entitlement.ErrQuotaConflict) {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, "org_a", key, 2, "create_repo"); !errors.Is(err, entitlement.ErrQuotaConflict) {
		t.Fatal(err)
	}
	if got := usage(t, store, "org_a", key, "lifetime"); got != (entitlement.Usage{}) {
		t.Fatal(got)
	}
	if _, err := service.Reserve(ctx, "org_a", key, 2, "new_repo"); err != nil {
		t.Fatal(err)
	}
}

func TestMonthlyRolloverAndPlanChangePreserveAccounting(t *testing.T) {
	key := domain.LimitProofCreditsMonthly
	store, service := setup(t, key, domain.Integer, 5)
	ctx := t.Context()
	if _, err := service.Reserve(ctx, "org_a", key, 5, "sept_job"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLimit(ctx, "org_a", domain.EntitlementLimit{Key: key, State: domain.Disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, "org_a", key, 1, "blocked"); !errors.Is(err, entitlement.ErrQuotaDisabled) {
		t.Fatal(err)
	}
	// A new service/process and UTC month do not erase the old reservation.
	october, _ := entitlement.NewQuotaService(store, func() time.Time { return time.Date(2026, 10, 1, 6, 0, 0, 0, time.FixedZone("Dhaka", 6*3600)) })
	if _, err := october.Commit(ctx, "org_a", "sept_job"); err != nil {
		t.Fatal(err)
	}
	if _, err := october.Release(ctx, "org_a", "sept_job"); !errors.Is(err, entitlement.ErrQuotaConflict) {
		t.Fatal(err)
	}
	if got := usage(t, store, "org_a", key, "2026-09"); got != (entitlement.Usage{Committed: 5}) {
		t.Fatal(got)
	}
	if err := store.SetLimit(ctx, "org_a", domain.EntitlementLimit{Key: key, State: domain.Integer, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, "org_a", key, 1, "sept_overdrawn"); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal(err)
	}
	if _, err := october.Reserve(ctx, "org_a", key, 1, "oct_job"); err != nil {
		t.Fatal(err)
	}
	if _, err := october.Release(ctx, "org_a", "oct_job"); err != nil {
		t.Fatal(err)
	}
	if got := usage(t, store, "org_a", key, "2026-10"); got != (entitlement.Usage{}) {
		t.Fatal(got)
	}
	if err := store.SetLimit(ctx, "org_a", domain.EntitlementLimit{Key: key, State: domain.Integer, Value: 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, "org_a", key, 1, "upgrade"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentWorkersCannotOverbook(t *testing.T) {
	key := domain.LimitVerificationConcurrency
	store, first := setup(t, key, domain.Integer, 7)
	second, _ := entitlement.NewQuotaService(store, time.Now)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			service := first
			if i%2 == 0 {
				service = second
			}
			_, err := service.Reserve(t.Context(), "org_a", key, 1, fmt.Sprintf("job_%d", i))
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, entitlement.ErrQuotaExceeded) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 7 || usage(t, store, "org_a", key, "lifetime").Reserved != 7 {
		t.Fatal(accepted.Load())
	}
}

func TestConcurrentRetriesAndSettlement(t *testing.T) {
	key := domain.LimitDevelopers
	store, service := setup(t, key, domain.Integer, 1)
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.Reserve(t.Context(), "org_a", key, 1, "same"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if u := usage(t, store, "org_a", key, "lifetime"); u.Reserved != 1 {
		t.Fatal(u)
	}
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = service.Commit(t.Context(), "org_a", "same")
			} else {
				_, err = service.Release(t.Context(), "org_a", "same")
			}
			if err != nil && !errors.Is(err, entitlement.ErrQuotaConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if u := usage(t, store, "org_a", key, "lifetime"); u != (entitlement.Usage{}) {
		t.Fatal(u)
	}
}

func TestFailClosedInputsIsolationAndOverflow(t *testing.T) {
	key := domain.LimitArtifactStorageBytes
	store, service := setup(t, key, domain.Unlimited, 0)
	ctx := t.Context()
	if _, err := service.Reserve(ctx, "org_a", key, math.MaxInt64, "huge"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, "org_a", key, 1, "overflow"); !errors.Is(err, entitlement.ErrQuotaBound) {
		t.Fatal(err)
	}
	if _, err := service.Commit(ctx, "org_a", "huge"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(ctx, "org_b", "huge"); !errors.Is(err, entitlement.ErrQuotaNotFound) {
		t.Fatal("tenant escape", err)
	}
	if _, err := service.Release(ctx, "org_b", "huge"); !errors.Is(err, entitlement.ErrQuotaNotFound) {
		t.Fatal("tenant escape", err)
	}
	if err := service.Check(ctx, "org_b", key, 1); !errors.Is(err, entitlement.ErrQuotaDisabled) {
		t.Fatal("missing limit granted", err)
	}
	if err := store.SetLimit(ctx, "org_b", domain.EntitlementLimit{Key: key, State: domain.Integer, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, "org_b", key, 1, "huge"); err != nil {
		t.Fatal("id namespace not tenant scoped", err)
	}
	for _, org := range []string{"", "../org_a", "org'; DROP TABLE quota_usage;--"} {
		if err := service.Check(ctx, org, key, 1); !errors.Is(err, entitlement.ErrInvalidQuota) {
			t.Fatal(err)
		}
	}
	for _, amount := range []int64{-1, 0} {
		if _, err := service.Reserve(ctx, "org_a", key, amount, "bad"); !errors.Is(err, entitlement.ErrInvalidQuota) {
			t.Fatal(err)
		}
		if err := service.Check(ctx, "org_a", key, amount); !errors.Is(err, entitlement.ErrInvalidQuota) {
			t.Fatal(err)
		}
	}
	if err := service.Check(ctx, "org_a", "unknown", 1); !errors.Is(err, entitlement.ErrInvalidQuota) {
		t.Fatal(err)
	}
	if err := service.Check(nil, "org_a", key, 1); !errors.Is(err, entitlement.ErrInvalidQuota) {
		t.Fatal(err)
	}
	var missing *entitlement.QuotaService
	if err := missing.Check(ctx, "org_a", key, 1); !errors.Is(err, entitlement.ErrInvalidQuota) {
		t.Fatal(err)
	}
	if _, err := service.Commit(ctx, "org_a", "../bad"); !errors.Is(err, entitlement.ErrInvalidQuota) {
		t.Fatal(err)
	}
	zeroClock, _ := entitlement.NewQuotaService(store, func() time.Time { return time.Time{} })
	if err := zeroClock.Check(ctx, "org_a", key, 1); !errors.Is(err, entitlement.ErrInvalidQuota) {
		t.Fatal(err)
	}
	if _, err := entitlement.NewQuotaService(nil, time.Now); !errors.Is(err, entitlement.ErrInvalidQuota) {
		t.Fatal(err)
	}
	if _, err := entitlement.NewQuotaService(store, nil); !errors.Is(err, entitlement.ErrInvalidQuota) {
		t.Fatal(err)
	}
	_, zero := setup(t, domain.LimitRepositories, domain.Integer, 0)
	if err := zero.Check(ctx, "org_a", domain.LimitRepositories, 1); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal(err)
	}
}

func TestMemoryBoundsRollbackAndCancellation(t *testing.T) {
	ctx := t.Context()
	key := domain.LimitRepositories
	var unconfigured Memory
	if err := unconfigured.WithinOrganization(ctx, "org_a", func(entitlement.QuotaTransaction) error { return nil }); !errors.Is(err, entitlement.ErrInvalidQuota) {
		t.Fatal("unconfigured store did not fail closed", err)
	}
	store, _ := NewMemory(1, 1)
	if err := store.SetLimit(ctx, "org_a", domain.EntitlementLimit{Key: key, State: domain.Unlimited}); err != nil {
		t.Fatal(err)
	}
	service, _ := entitlement.NewQuotaService(store, time.Now)
	if _, err := service.Reserve(ctx, "org_a", key, 1, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, "org_a", key, 1, "second"); !errors.Is(err, entitlement.ErrQuotaBound) {
		t.Fatal(err)
	}
	if u := usage(t, store, "org_a", key, "lifetime"); u.Reserved != 1 {
		t.Fatal("partial write", u)
	}
	if err := service.Check(ctx, "org_b", key, 1); !errors.Is(err, entitlement.ErrQuotaBound) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.Commit(cancelled, "org_a", "first"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := service.Reserve(cancelled, "org_a", key, 1, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	inflight, cancelInflight := context.WithCancel(ctx)
	err := store.WithinOrganization(inflight, "org_a", func(tx entitlement.QuotaTransaction) error {
		if err := tx.PutUsage(key, "lifetime", entitlement.Usage{}); err != nil {
			return err
		}
		cancelInflight()
		return nil
	})
	if !errors.Is(err, context.Canceled) || usage(t, store, "org_a", key, "lifetime").Reserved != 1 {
		t.Fatal("cancel did not roll back", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic")
			}
		}()
		_ = store.WithinOrganization(ctx, "org_a", func(tx entitlement.QuotaTransaction) error {
			_ = tx.PutUsage(key, "lifetime", entitlement.Usage{})
			panic("fixture")
		})
	}()
	if usage(t, store, "org_a", key, "lifetime").Reserved != 1 {
		t.Fatal("panic did not roll back")
	}
	store.lock <- struct{}{}
	err = store.WithinOrganization(cancelled, "org_a", func(entitlement.QuotaTransaction) error { t.Error("ran cancelled transaction"); return nil })
	<-store.lock
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type faultyStore struct {
	store   entitlement.QuotaStore
	failure error
	stage   string
}

func (s faultyStore) WithinOrganization(ctx context.Context, org string, fn func(entitlement.QuotaTransaction) error) error {
	if s.stage == "transaction" {
		return s.failure
	}
	return s.store.WithinOrganization(ctx, org, func(tx entitlement.QuotaTransaction) error {
		return fn(faultyTx{QuotaTransaction: tx, stage: s.stage, failure: s.failure})
	})
}

type faultyTx struct {
	entitlement.QuotaTransaction
	stage   string
	failure error
}

func (t faultyTx) Limit(key domain.LimitKey) (domain.EntitlementLimit, error) {
	if t.stage == "limit" {
		return domain.EntitlementLimit{}, t.failure
	}
	if t.stage == "poison_limit" {
		return domain.EntitlementLimit{Key: key, State: "UNKNOWN"}, nil
	}
	if t.stage == "wrong_limit" {
		return domain.EntitlementLimit{Key: domain.LimitDevelopers, State: domain.Unlimited}, nil
	}
	return t.QuotaTransaction.Limit(key)
}
func (t faultyTx) Find(id string) (entitlement.Reservation, error) {
	if t.stage == "find" {
		return entitlement.Reservation{}, t.failure
	}
	r, err := t.QuotaTransaction.Find(id)
	if t.stage == "poison_reservation" {
		r.OrganizationID = "org_other"
	}
	return r, err
}
func (t faultyTx) Usage(key domain.LimitKey, period string) (entitlement.Usage, error) {
	if t.stage == "usage" {
		return entitlement.Usage{}, t.failure
	}
	if t.stage == "poison_usage" {
		return entitlement.Usage{Reserved: -1}, nil
	}
	if t.stage == "overflow_usage" {
		return entitlement.Usage{Reserved: math.MaxInt64, Committed: 1}, nil
	}
	return t.QuotaTransaction.Usage(key, period)
}
func (t faultyTx) PutUsage(key domain.LimitKey, period string, u entitlement.Usage) error {
	if t.stage == "put_usage" {
		return t.failure
	}
	return t.QuotaTransaction.PutUsage(key, period, u)
}
func (t faultyTx) PutReservation(r entitlement.Reservation) error {
	if t.stage == "put_reservation" {
		return t.failure
	}
	return t.QuotaTransaction.PutReservation(r)
}

func TestStorageErrorsAndPoisonedRecordsFailClosed(t *testing.T) {
	failure := errors.New("storage fixture failure")
	for _, stage := range []string{"transaction", "find", "limit", "usage", "put_usage", "put_reservation", "poison_limit", "wrong_limit", "poison_usage", "overflow_usage", "poison_reservation"} {
		t.Run(stage, func(t *testing.T) {
			key := domain.LimitRepositories
			store, normal := setup(t, key, domain.Integer, 3)
			if _, err := normal.Reserve(t.Context(), "org_a", key, 1, "existing"); err != nil {
				t.Fatal(err)
			}
			bad, _ := entitlement.NewQuotaService(faultyStore{store: store, stage: stage, failure: failure}, time.Now)
			id := "new"
			if stage == "poison_reservation" {
				id = "existing"
			}
			r, err := bad.Reserve(t.Context(), "org_a", key, 1, id)
			want := failure
			if stage == "poison_limit" || stage == "wrong_limit" || stage == "poison_usage" || stage == "overflow_usage" || stage == "poison_reservation" {
				want = entitlement.ErrQuotaData
			}
			if !errors.Is(err, want) || r != (entitlement.Reservation{}) {
				t.Fatal(r, err)
			}
			if got := usage(t, store, "org_a", key, "lifetime"); got != (entitlement.Usage{Reserved: 1}) {
				t.Fatal("partial write", got)
			}
			if stage == "put_usage" || stage == "put_reservation" || stage == "poison_reservation" || stage == "poison_usage" || stage == "overflow_usage" || stage == "usage" || stage == "find" || stage == "transaction" {
				if _, err := bad.Commit(t.Context(), "org_a", "existing"); !errors.Is(err, want) {
					t.Fatal(err)
				}
				if _, err := bad.Release(t.Context(), "org_a", "existing"); !errors.Is(err, want) {
					t.Fatal(err)
				}
				if got := usage(t, store, "org_a", key, "lifetime"); got != (entitlement.Usage{Reserved: 1}) {
					t.Fatal("settlement partial write", got)
				}
			}
		})
	}
}
