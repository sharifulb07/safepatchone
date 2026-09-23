package entitlement_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

type snapshotSource struct {
	mu                    sync.Mutex
	input                 entitlement.SnapshotInput
	loads, checks         int
	loadError, stateError error
	loadHook              func()
	stateHook             func(int)
}

func snapshotState(org string) domain.SnapshotState {
	return domain.SnapshotState{OrganizationID: org, Edition: edition.Team, EditionVersion: "team-2026-v1", SourceVersions: domain.SourceVersions{Subscription: 1, Trial: 1, License: 1, ManualOverride: 1, EditionConfiguration: 1, CommunityDefault: 1, Promotion: 1}}
}

func newSnapshotSource(records ...domain.Entitlement) *snapshotSource {
	return &snapshotSource{input: entitlement.SnapshotInput{State: snapshotState("org_a"), Entitlements: records}}
}

func (p *snapshotSource) CurrentState(ctx context.Context, org string) (domain.SnapshotState, error) {
	p.mu.Lock()
	p.checks++
	checks, hook := p.checks, p.stateHook
	p.mu.Unlock()
	if hook != nil {
		hook(checks)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.State, p.stateError
}

func (p *snapshotSource) Load(ctx context.Context, org string) (entitlement.SnapshotInput, error) {
	p.mu.Lock()
	p.loads++
	result := p.input
	result.Entitlements = make([]domain.Entitlement, len(p.input.Entitlements))
	for i, record := range p.input.Entitlements {
		result.Entitlements[i] = record.Clone()
	}
	hook, err := p.loadHook, p.loadError
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
	return result, err
}

func snapshotService(t *testing.T, p entitlement.SnapshotSource, clock func() time.Time, capacity int) *entitlement.SnapshotService {
	t.Helper()
	s, err := entitlement.NewSnapshotService(p, featureCatalog(t), clock, 30*time.Second, capacity)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSnapshotReusesOneLoadAndResolverPrecedence(t *testing.T) {
	paid := grant("paid", domain.SourceEnterpriseLicense, true, 10)
	p := newSnapshotSource(paid)
	s := snapshotService(t, p, at, 2)
	first, err := s.Snapshot(context.Background(), "org_a")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Features[feature.KeyVerifyBasic] || *first.Limits[feature.KeyVerifyBasic] != 10 || first.Edition != edition.Team || first.EditionVersion != "team-2026-v1" || !first.ValidUntil.Equal(at().Add(30*time.Second)) {
		t.Fatalf("incorrect snapshot: %+v", first)
	}
	for range 100 {
		ok, err := s.Has(context.Background(), "org_a", feature.KeyVerifyBasic)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if p.loads != 1 || p.checks != 102 {
		t.Fatalf("rows were reloaded: loads=%d checks=%d", p.loads, p.checks)
	}
	limit, err := s.Limit(context.Background(), "org_a", feature.KeyVerifyBasic)
	if err != nil || limit != 10 {
		t.Fatal(limit, err)
	}
	want, err := resolver(t, recordsRepository([]domain.Entitlement{paid})).Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
	got, gotErr := s.Resolve(context.Background(), "org_a", feature.KeyVerifyBasic)
	if err != nil || gotErr != nil || got.Explanation != want.Explanation || got.Source != want.Source {
		t.Fatal(got, want, err, gotErr)
	}
	// Returned maps, limit pointers, and resolution pointers cannot poison cache.
	first.Features[feature.KeyVerifyBasic] = false
	*first.Limits[feature.KeyVerifyBasic] = 999
	*got.EffectiveLimit = 888
	again, err := s.Snapshot(context.Background(), "org_a")
	if err != nil || !again.Features[feature.KeyVerifyBasic] || *again.Limits[feature.KeyVerifyBasic] != 10 {
		t.Fatal("cache was mutated", err)
	}
}

func TestSnapshotEverySourceRevisionInvalidatesWithoutEvent(t *testing.T) {
	changes := []struct {
		name string
		bump func(*domain.SourceVersions)
	}{
		{"subscription", func(v *domain.SourceVersions) { v.Subscription++ }},
		{"trial", func(v *domain.SourceVersions) { v.Trial++ }},
		{"license", func(v *domain.SourceVersions) { v.License++ }},
		{"manual", func(v *domain.SourceVersions) { v.ManualOverride++ }},
		{"edition", func(v *domain.SourceVersions) { v.EditionConfiguration++ }},
		{"defaults", func(v *domain.SourceVersions) { v.CommunityDefault++ }},
		{"promotion", func(v *domain.SourceVersions) { v.Promotion++ }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			p := newSnapshotSource(grant("paid", domain.SourceSubscription, true, 5))
			s := snapshotService(t, p, at, 2)
			old, err := s.Snapshot(context.Background(), "org_a")
			if err != nil {
				t.Fatal(err)
			}
			history, _ := json.Marshal(old)
			p.input.Entitlements = append(p.input.Entitlements, grant("restriction", domain.SourceManualOverride, false, 0))
			tc.bump(&p.input.State.SourceVersions)
			fresh, err := s.Snapshot(context.Background(), "org_a")
			if err != nil || fresh.Features[feature.KeyVerifyBasic] || fresh.ContentHash == old.ContentHash || p.loads != 2 {
				t.Fatal("revision failed to invalidate", err)
			}
			after, _ := json.Marshal(old)
			if string(after) != string(history) {
				t.Fatal("history was overwritten")
			}
		})
	}
}

func TestSnapshotScheduledTransitionsAndTTL(t *testing.T) {
	for _, kind := range []string{"grant expiration", "restriction start", "restriction expiration", "ttl"} {
		t.Run(kind, func(t *testing.T) {
			now := at()
			transition := now.Add(10 * time.Second)
			paid := grant("paid", domain.SourceSubscription, true, 3)
			restriction := grant("restriction", domain.SourceManualOverride, false, 0)
			records := []domain.Entitlement{paid}
			wantBefore, wantAfter := true, false
			switch kind {
			case "grant expiration":
				records[0].ExpiresAt = &transition
			case "restriction start":
				restriction.StartsAt = transition
				records = append(records, restriction)
			case "restriction expiration":
				restriction.ExpiresAt = &transition
				records = append(records, restriction)
				wantBefore, wantAfter = false, true
			case "ttl":
				transition = now.Add(30 * time.Second)
				wantAfter = true
			}
			p := newSnapshotSource(records...)
			s := snapshotService(t, p, func() time.Time { return now }, 1)
			old, err := s.Snapshot(context.Background(), "org_a")
			if err != nil || old.Features[feature.KeyVerifyBasic] != wantBefore || !old.ValidUntil.Equal(transition) {
				t.Fatal(old, err)
			}
			now = transition.Add(-time.Nanosecond)
			if _, err := s.Snapshot(context.Background(), "org_a"); err != nil || p.loads != 1 {
				t.Fatal("premature expiry", err)
			}
			now = transition
			if !errors.Is(old.ValidateAt("org_a", now), domain.ErrExpiredSnapshot) {
				t.Fatal("expired snapshot validated")
			}
			fresh, err := s.Snapshot(context.Background(), "org_a")
			if err != nil || fresh.Features[feature.KeyVerifyBasic] != wantAfter || p.loads != 2 {
				t.Fatal("transition failed", fresh, err)
			}
		})
	}
}

func TestSnapshotFailsClosedOnReadFailureAndSlowRefresh(t *testing.T) {
	backend := errors.New("private backend details")
	for _, mode := range []string{"fresh state failure", "expired load failure", "expired state failure", "slow load", "changed while loading", "invalidated while loading", "cancelled while loading"} {
		t.Run(mode, func(t *testing.T) {
			now := at()
			p := newSnapshotSource(grant("paid", domain.SourceEnterpriseLicense, true, 8))
			s := snapshotService(t, p, func() time.Time { return now }, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if ok, err := s.Has(ctx, "org_a", feature.KeyVerifyBasic); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if mode != "fresh state failure" {
				now = now.Add(30 * time.Second)
			}
			switch mode {
			case "fresh state failure", "expired state failure":
				p.stateError = backend
			case "expired load failure":
				p.loadError = backend
			case "slow load":
				p.loadHook = func() { now = now.Add(30 * time.Second) }
			case "changed while loading":
				p.loadHook = func() { p.input.State.SourceVersions.ManualOverride++ }
			case "invalidated while loading":
				p.loadHook = func() {
					if err := s.Invalidate(ctx, "org_a", entitlement.ManualOverrideChanged); err != nil {
						t.Fatal(err)
					}
				}
			case "cancelled while loading":
				p.loadHook = cancel
			}
			result, err := s.Resolve(ctx, "org_a", feature.KeyVerifyBasic)
			if err == nil || result.Enabled || result.EffectiveLimit != nil || strings.Contains(result.Explanation, "private") {
				t.Fatal("unsafe fallback", result, err)
			}
			if strings.Contains(mode, "failure") && !errors.Is(err, backend) {
				t.Fatal("lost error cause", err)
			}
		})
	}
}

func TestSnapshotRejectsPoisonedInput(t *testing.T) {
	for _, mode := range []string{"tenant state", "tenant record", "duplicate", "unknown feature", "invalid record", "zero revision", "invalid edition", "record bound", "metadata bound"} {
		t.Run(mode, func(t *testing.T) {
			p := newSnapshotSource(grant("paid", domain.SourceSubscription, true, 2))
			switch mode {
			case "tenant state":
				p.input.State.OrganizationID = "org_b"
			case "tenant record":
				p.input.Entitlements[0].OrganizationID = "org_b"
			case "duplicate":
				p.input.Entitlements = append(p.input.Entitlements, p.input.Entitlements[0])
			case "unknown feature":
				p.input.Entitlements[0].FeatureID = "unknown"
			case "invalid record":
				p.input.Entitlements[0].Source = "UNKNOWN"
			case "zero revision":
				p.input.State.SourceVersions.ManualOverride = 0
			case "invalid edition":
				p.input.State.EditionVersion = "enterprise-2026-v1"
			case "record bound":
				p.input.Entitlements = make([]domain.Entitlement, entitlement.MaxResolutionRecords+1)
			case "metadata bound":
				p.input.Entitlements = nil
				for i := range 150 {
					record := grant(fmt.Sprintf("id_%04d", i), domain.SourceSubscription, true, 2)
					record.Metadata = json.RawMessage(`{"value":"` + strings.Repeat("x", 15000) + `"}`)
					p.input.Entitlements = append(p.input.Entitlements, record)
				}
			}
			s := snapshotService(t, p, at, 1)
			if ok, err := s.Has(context.Background(), "org_a", feature.KeyVerifyBasic); err == nil || ok {
				t.Fatal("poisoned input allowed", ok, err)
			}
		})
	}
}

func TestSnapshotEventsEvictionAndEditionMetadata(t *testing.T) {
	p := newSnapshotSource(grant("paid", domain.SourceSubscription, true, 1))
	s := snapshotService(t, p, at, 1)
	ctx := context.Background()
	for _, change := range []entitlement.SnapshotChange{entitlement.SubscriptionChanged, entitlement.TrialChanged, entitlement.LicenseChanged, entitlement.ManualOverrideChanged, entitlement.EditionConfigurationChanged, entitlement.CommunityDefaultsChanged, entitlement.PromotionChanged} {
		if _, err := s.Snapshot(ctx, "org_a"); err != nil {
			t.Fatal(err)
		}
		before := p.loads
		if err := s.Invalidate(ctx, "org_a", change); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Snapshot(ctx, "org_a"); err != nil || p.loads != before+1 {
			t.Fatal("event did not evict", err)
		}
	}
	// Changing commercial metadata never manufactures grants.
	p.input.State.Edition, p.input.State.EditionVersion = edition.Enterprise, "enterprise-2027-v2"
	p.input.State.SourceVersions.EditionConfiguration++
	p.input.Entitlements = nil
	if ok, err := s.Has(ctx, "org_a", feature.KeyVerifyBasic); err != nil || ok {
		t.Fatal("edition granted access", err)
	}
	// Capacity one forces a load when returning to an evicted tenant.
	p.input.State = snapshotState("org_b")
	if _, err := s.Snapshot(ctx, "org_b"); err != nil {
		t.Fatal(err)
	}
	p.input.State = snapshotState("org_a")
	before := p.loads
	if _, err := s.Snapshot(ctx, "org_a"); err != nil || p.loads != before+1 {
		t.Fatal("capacity bound not enforced", err)
	}
}

func TestSnapshotConcurrentReadersAndInvalidation(t *testing.T) {
	p := newSnapshotSource(grant("paid", domain.SourceSubscription, true, 2))
	s := snapshotService(t, p, at, 2)
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			if i%4 == 0 {
				if err := s.Invalidate(context.Background(), "org_a", entitlement.TrialChanged); err != nil {
					t.Error(err)
				}
				return
			}
			ok, err := s.Has(context.Background(), "org_a", feature.KeyVerifyBasic)
			if err != nil && !errors.Is(err, entitlement.ErrSnapshotChanged) {
				t.Error(err)
			}
			if err == nil && !ok {
				t.Error("grant lost")
			}
		})
	}
	wg.Wait()
	if ok, err := s.Has(context.Background(), "org_a", feature.KeyVerifyBasic); err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestSnapshotStableHashAndMultiplePages(t *testing.T) {
	var records []domain.Entitlement
	for i := range 250 {
		records = append(records, grant(fmt.Sprintf("id_%04d", i), domain.SourceSubscription, true, 2))
	}
	records = append(records, grant("zzz_restriction", domain.SourceManualOverride, false, 0))
	a, err := snapshotService(t, newSnapshotSource(records...), at, 1).Snapshot(context.Background(), "org_a")
	if err != nil || a.Features[feature.KeyVerifyBasic] {
		t.Fatal(a, err)
	}
	slices.Reverse(records)
	b, err := snapshotService(t, newSnapshotSource(records...), at, 1).Snapshot(context.Background(), "org_a")
	if err != nil || a.ContentHash != b.ContentHash {
		t.Fatal("order affected snapshot hash", err)
	}
}

func TestSnapshotValidationAndLimitErrors(t *testing.T) {
	p := newSnapshotSource(grant("paid", domain.SourceSubscription, true, 0))
	p.input.Entitlements[0].LimitValue = nil
	s := snapshotService(t, p, at, 1)
	if value, err := s.Limit(context.Background(), "org_a", feature.KeyVerifyBasic); value != 0 || !errors.Is(err, entitlement.ErrLimitUnspecified) {
		t.Fatal(value, err)
	}
	if value, err := s.Limit(context.Background(), "org_a", feature.KeyProofLiving); value != 0 || !errors.Is(err, entitlement.ErrNotEntitled) {
		t.Fatal(value, err)
	}
	if ok, err := s.Has(context.Background(), "org_a", "unknown.feature"); ok || !errors.Is(err, entitlement.ErrUnknownFeature) {
		t.Fatal(ok, err)
	}
	if _, err := s.Snapshot(nil, "org_a"); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := s.Snapshot(context.Background(), "../org"); err == nil {
		t.Fatal("invalid tenant accepted")
	}
	if err := s.Invalidate(context.Background(), "org_a", "unknown"); err == nil {
		t.Fatal("unknown event accepted")
	}
	var zero entitlement.SnapshotService
	if ok, err := zero.Has(context.Background(), "org_a", feature.KeyVerifyBasic); ok || err == nil {
		t.Fatal("zero service allowed")
	}
	for _, ttl := range []time.Duration{0, -1, domain.MaxSnapshotTTL + 1} {
		if _, err := entitlement.NewSnapshotService(p, featureCatalog(t), at, ttl, 1); err == nil {
			t.Fatal("invalid TTL accepted")
		}
	}
	for _, capacity := range []int{0, entitlement.MaxCachedOrganizations + 1} {
		if _, err := entitlement.NewSnapshotService(p, featureCatalog(t), at, time.Second, capacity); err == nil {
			t.Fatal("invalid capacity accepted")
		}
	}
}

func TestSnapshotRejectsClockRollbackAndMidReadChanges(t *testing.T) {
	for _, mode := range []string{"rollback", "cache hit invalidation", "cache hit cancellation", "load state mismatch"} {
		t.Run(mode, func(t *testing.T) {
			now := at()
			p := newSnapshotSource(grant("paid", domain.SourceSubscription, true, 2))
			s := snapshotService(t, p, func() time.Time { return now }, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if _, err := s.Snapshot(ctx, "org_a"); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "rollback":
				now = now.Add(-time.Second)
			case "cache hit invalidation":
				p.stateHook = func(int) {
					if err := s.Invalidate(ctx, "org_a", entitlement.ManualOverrideChanged); err != nil {
						t.Fatal(err)
					}
				}
			case "cache hit cancellation":
				p.stateHook = func(int) { cancel() }
			case "load state mismatch":
				now = now.Add(30 * time.Second)
				// Initial state is copied, then the provider changes before Load.
				p.stateHook = func(int) {}
				p.loadHook = nil
				wrapper := mismatchedSnapshotSource{p}
				s = snapshotService(t, wrapper, func() time.Time { return now }, 1)
			}
			if ok, err := s.Has(ctx, "org_a", feature.KeyVerifyBasic); ok || err == nil {
				t.Fatal("unsafe read accepted", ok, err)
			}
		})
	}
}

type mismatchedSnapshotSource struct{ *snapshotSource }

func (p mismatchedSnapshotSource) Load(ctx context.Context, org string) (entitlement.SnapshotInput, error) {
	input, err := p.snapshotSource.Load(ctx, org)
	input.State.SourceVersions.Subscription++
	return input, err
}
