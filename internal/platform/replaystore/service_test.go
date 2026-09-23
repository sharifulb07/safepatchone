package replaystore_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/quotastore"
	"safepatch/internal/platform/replaystore"
	"safepatch/internal/replay"
	"safepatch/internal/tenant"
)

type grants struct{ enabled, allowed bool }

func (g *grants) Has(_ context.Context, org string, key feature.Key) (bool, error) {
	return g.enabled && org == "org" && key == feature.KeyRuntimeReplay, nil
}
func (g *grants) Limit(context.Context, string, feature.Key) (int64, error) {
	return 0, errors.New("unused")
}
func (g *grants) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, errors.New("unused")
}
func (g *grants) Allowed(_ context.Context, a authorization.Principal, org string, k authorization.Key) (bool, error) {
	return g.allowed && a.ID == "owner" && a.Kind == authorization.User && org == "org" && (k == authorization.KeyReplayExecute || k == authorization.KeyReplayView), nil
}
func (g *grants) Check(context.Context, application.Execution) error { return nil }

type tenants struct{}

func (tenants) Resolve(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	if s.OrganizationID != "org" || s.ResourceID != "repo" {
		return tenant.Scope{}, replay.ErrDenied
	}
	return s, nil
}

type sandbox struct {
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
	unsafe  bool
}

func (s *sandbox) Execute(ctx context.Context, _ replay.Record) (replay.Execution, error) {
	s.calls.Add(1)
	if s.entered != nil {
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return replay.Execution{Stopped: true}, ctx.Err()
		}
	}
	if s.unsafe {
		return replay.Execution{}, replay.ErrUnsafe
	}
	return replay.Execution{Stopped: true, Observation: replay.Observation{Complete: true, Status: 200, Output: json.RawMessage(`{"value":2}`), Effects: []string{}}}, nil
}

type observer struct{}

func (observer) Start(c context.Context, _ replay.Scope, _, _ string, _ replay.TraceLink) (context.Context, func(replay.Decision)) {
	return c, func(replay.Decision) {}
}

type fixture struct {
	service *application.ReplayService
	grants  *grants
	quota   *quotastore.Memory
	store   *replaystore.Memory
	sandbox *sandbox
	now     time.Time
	scope   replay.Scope
	actor   authorization.Principal
	dataset replay.ReplayDataset
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: time.Now().UTC().Truncate(time.Microsecond), scope: replay.Scope{OrganizationID: "org", RepositoryID: "repo"}, actor: authorization.Principal{ID: "owner", Kind: authorization.User}, grants: &grants{true, true}, sandbox: &sandbox{}, store: replaystore.NewMemory()}
	q, err := quotastore.NewMemory(2, 64)
	if err != nil {
		t.Fatal(err)
	}
	f.quota = q
	for _, l := range []limits.EntitlementLimit{{Key: limits.LimitDatasetRetentionDays, State: limits.Integer, Value: 7}, {Key: limits.LimitReplayConcurrency, State: limits.Integer, Value: 1}, {Key: limits.LimitReplayRequestsMonthly, State: limits.Integer, Value: 3}} {
		if err := q.SetLimit(t.Context(), "org", l); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := replay.NewRedactor([]string{"/value"})
	engine, _ := replay.NewEngine(f.sandbox, r, observer{})
	f.service, err = application.NewReplayService(tenants{}, f.grants, f.grants, f.grants, q, f.store, r, engine, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	f.dataset, err = f.service.Capture(t.Context(), f.actor, f.scope, "data", 7, []replay.Record{{ID: "case", Input: json.RawMessage(`{"value":1,"password":"private"}`), Expected: replay.Observation{Complete: true, Status: 200, Output: json.RawMessage(`{"value":2}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestReplayOwnershipPermissionAndDowngrade(t *testing.T) {
	for _, mode := range []string{"entitled", "not_entitled", "permission", "tenant", "expiry", "retention_downgrade", "disabled_retention", "monthly_disabled", "concurrency_disabled", "plan_change"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			scope := f.scope
			if mode == "plan_change" {
				if _, err := f.service.Run(t.Context(), f.actor, scope, "data", "prior"); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "not_entitled", "plan_change":
				f.grants.enabled = false
			case "permission":
				f.grants.allowed = false
			case "tenant":
				scope.OrganizationID = "other"
			case "expiry":
				f.now = f.now.Add(7 * 24 * time.Hour)
			case "retention_downgrade":
				f.now = f.now.Add(2 * 24 * time.Hour)
				_ = f.quota.SetLimit(t.Context(), "org", limits.EntitlementLimit{Key: limits.LimitDatasetRetentionDays, State: limits.Integer, Value: 1})
			case "disabled_retention":
				_ = f.quota.SetLimit(t.Context(), "org", limits.EntitlementLimit{Key: limits.LimitDatasetRetentionDays, State: limits.Disabled})
			case "monthly_disabled":
				_ = f.quota.SetLimit(t.Context(), "org", limits.EntitlementLimit{Key: limits.LimitReplayRequestsMonthly, State: limits.Disabled})
			case "concurrency_disabled":
				_ = f.quota.SetLimit(t.Context(), "org", limits.EntitlementLimit{Key: limits.LimitReplayConcurrency, State: limits.Disabled})
			}
			result, err := f.service.Run(t.Context(), f.actor, scope, "data", "run")
			if mode == "entitled" {
				if err != nil || result.Decision != replay.Match {
					t.Fatal(result, err)
				}
			} else if err == nil || result.Decision != replay.Unknown {
				t.Fatal("denial failed", result, err)
			}
			if mode == "permission" && !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal(err)
			}
			if mode == "not_entitled" && !errors.Is(err, entitlement.ErrNotEntitled) {
				t.Fatal(err)
			}
			stored, err := f.store.Get(t.Context(), f.scope, "data")
			if err != nil || stored.Digest() != f.dataset.Digest() {
				t.Fatal("plan/retention change corrupted dataset")
			}
		})
	}
}
func TestReplayConcurrencyMonthlyAccountingAndNoRetry(t *testing.T) {
	f := setup(t)
	f.sandbox.entered = make(chan struct{}, 1)
	f.sandbox.release = make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := f.service.Run(context.Background(), f.actor, f.scope, "data", "first")
		finished <- err
	}()
	<-f.sandbox.entered
	if _, err := f.service.Run(t.Context(), f.actor, f.scope, "data", "second"); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal("concurrent limit ignored", err)
	}
	close(f.sandbox.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	f.sandbox.entered = nil
	if _, err := f.service.Run(t.Context(), f.actor, f.scope, "data", "first"); err == nil {
		t.Fatal("claimed run replayed")
	}
	for _, id := range []string{"third", "fourth"} {
		if _, err := f.service.Run(t.Context(), f.actor, f.scope, "data", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.Run(t.Context(), f.actor, f.scope, "data", "fifth"); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal("monthly limit ignored", err)
	}
	if f.sandbox.calls.Load() != 3 {
		t.Fatal("unexpected execution count")
	}
}
func TestUncertainTerminationKeepsCapacityReserved(t *testing.T) {
	f := setup(t)
	f.sandbox.unsafe = true
	if _, err := f.service.Run(t.Context(), f.actor, f.scope, "data", "unsafe"); !errors.Is(err, replay.ErrUnsafe) {
		t.Fatal(err)
	}
	f.sandbox.unsafe = false
	if _, err := f.service.Run(t.Context(), f.actor, f.scope, "data", "next"); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal("uncertain sandbox freed capacity", err)
	}
}
