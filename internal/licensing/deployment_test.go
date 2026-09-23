package licensing_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/licensing"
	"safepatch/internal/platform/quotastore"
	"safepatch/internal/tenant"
)

func (s *source) CompareAndSwapLicense(ctx context.Context, org string, expected uint64, next licensing.LicenseState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}
	if org != "org" || expected != s.state.Revision || next.Revision != expected+1 {
		return licensing.ErrLicenseConflict
	}
	next.Envelope = bytes.Clone(next.Envelope)
	s.state = next
	return nil
}

type refreshFunc func(context.Context, string, string) (licensing.RefreshResult, error)

func (f refreshFunc) RefreshLicense(ctx context.Context, org, id string) (licensing.RefreshResult, error) {
	return f(ctx, org, id)
}
func unavailable(context.Context, string, string) (licensing.RefreshResult, error) {
	return licensing.RefreshResult{}, licensing.ErrRefreshUnavailable
}
func deployment(t *testing.T, mode licensing.DeploymentMode) *fixture {
	t.Helper()
	f := setup(t)
	f.doc.ExpiresAt = f.now.Add(60 * 24 * time.Hour)
	f.doc.Features = append(f.doc.Features, licensing.FeatureGrant{Key: feature.KeyEnterpriseAirgap, Enabled: true})
	f.source.set(sign(t, f.private, f.doc))
	f.source.state.ConfirmedAt = f.now
	f.config.Deployment = licensing.DeploymentPolicy{Mode: mode}
	if mode == licensing.Connected {
		f.config.Deployment.RefreshInterval = time.Minute
		f.config.Deployment.GracePeriod = 2 * time.Minute
	}
	var err error
	f.p, err = licensing.NewEnterpriseLicenseEntitlementProvider(f.config)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func refresher(t *testing.T, f *fixture, client licensing.LicenseRefreshClient) *licensing.LicenseRefresher {
	t.Helper()
	r, err := licensing.NewLicenseRefresher(licensing.RefreshConfig{Provider: f.config, Store: f.source, Client: client, RetryInterval: time.Second, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func allowed(t *testing.T, f *fixture, want bool) {
	t.Helper()
	ok, err := f.p.Has(t.Context(), "org", feature.KeyVerifyBasic)
	if ok != want || want && err != nil || !want && err == nil {
		t.Fatalf("allowed=%v, err=%v, want %v", ok, err, want)
	}
}
func TestConnectedGraceDeadlineSurvivesRestartAndRepeatedOutages(t *testing.T) {
	f := deployment(t, licensing.Connected)
	start := f.now
	r := refresher(t, f, refreshFunc(unavailable))
	allowed(t, f, true)
	f.now = start.Add(time.Minute)
	for range 3 {
		if err := r.RefreshOnce(t.Context()); !errors.Is(err, licensing.ErrRefreshUnavailable) {
			t.Fatal(err)
		}
		allowed(t, f, true)
	}
	a, err := f.p.Availability(t.Context(), "org")
	if err != nil || a.State != "grace" || !a.ValidUntil.Equal(start.Add(3*time.Minute)) {
		t.Fatal(a, err)
	}
	resolution, _ := f.p.Resolve(t.Context(), "org", feature.KeyVerifyBasic)
	if resolution.Expiration == nil || !resolution.Expiration.Equal(a.ValidUntil) {
		t.Fatal("reported expiry exceeds grace")
	}
	if !f.source.state.ConfirmedAt.Equal(start) {
		t.Fatal("outage moved anchor")
	}
	f.now = start.Add(3*time.Minute - time.Nanosecond)
	allowed(t, f, true)
	f.now = start.Add(3 * time.Minute)
	f.p, _ = licensing.NewEnterpriseLicenseEntitlementProvider(f.config)
	allowed(t, f, false)
	if _, err := f.p.ResourceLimit(t.Context(), "org", limits.LimitVerificationConcurrency); err == nil {
		t.Fatal("quota bypassed grace deadline")
	}
}
func TestAirGapNeverRefreshesAndHonorsExpiryAndCapabilities(t *testing.T) {
	f := deployment(t, licensing.AirGapped)
	spy := refreshFunc(func(context.Context, string, string) (licensing.RefreshResult, error) {
		t.Fatal("air gap phoned home")
		return licensing.RefreshResult{}, nil
	})
	if _, err := licensing.NewLicenseRefresher(licensing.RefreshConfig{Provider: f.config, Store: f.source, Client: spy, RetryInterval: time.Second, Timeout: time.Second}); err == nil {
		t.Fatal("air gap refresher constructed")
	}
	f.source.state.ConfirmedAt = time.Time{}
	f.now = f.now.Add(40 * 24 * time.Hour)
	allowed(t, f, true)
	a, err := f.p.Availability(t.Context(), "org")
	if err != nil || a.State != "offline" || a.RefreshDueAt != nil {
		t.Fatal(a, err)
	}
	f.doc.Features[len(f.doc.Features)-1].Enabled = false
	f.source.set(sign(t, f.private, f.doc))
	allowed(t, f, false)
	f.doc.Features[len(f.doc.Features)-1].Enabled = true
	f.source.set(sign(t, f.private, f.doc))
	allowed(t, f, true)
	f.now = f.doc.ExpiresAt
	allowed(t, f, false)
}
func TestDeploymentPolicyAndExpiryBounds(t *testing.T) {
	for _, policy := range []licensing.DeploymentPolicy{{Mode: "unknown"}, {Mode: licensing.AirGapped, GracePeriod: time.Second}, {Mode: licensing.Connected}, {Mode: licensing.Connected, RefreshInterval: time.Hour, GracePeriod: -1}, {Mode: licensing.Connected, RefreshInterval: 25 * time.Hour}, {Mode: licensing.Connected, RefreshInterval: time.Hour, GracePeriod: 31 * 24 * time.Hour}} {
		if policy.Validate() == nil {
			t.Fatal("invalid policy", policy)
		}
	}
	for _, scenario := range []string{"zero_grace", "expiry", "missing_anchor", "future_anchor", "missing_self_hosted"} {
		t.Run(scenario, func(t *testing.T) {
			f := deployment(t, licensing.Connected)
			switch scenario {
			case "zero_grace":
				f.config.Deployment.GracePeriod = 0
				f.p, _ = licensing.NewEnterpriseLicenseEntitlementProvider(f.config)
				f.now = f.now.Add(time.Minute)
			case "expiry":
				f.doc.ExpiresAt = f.now.Add(time.Minute)
				f.source.set(sign(t, f.private, f.doc))
				f.now = f.doc.ExpiresAt
			case "missing_anchor":
				f.source.state.ConfirmedAt = time.Time{}
			case "future_anchor":
				f.source.state.ConfirmedAt = f.now.Add(time.Second)
			case "missing_self_hosted":
				f.doc.Features[2].Enabled = false
				f.source.set(sign(t, f.private, f.doc))
			}
			allowed(t, f, false)
		})
	}
}
func TestRefreshRejectionsAreNeverOutageGrace(t *testing.T) {
	for _, kind := range []string{"revoked", "denied", "unknown", "forged", "foreign", "error"} {
		t.Run(kind, func(t *testing.T) {
			f := deployment(t, licensing.Connected)
			f.now = f.now.Add(time.Minute)
			result := licensing.RefreshResult{Status: kind}
			if kind == "forged" {
				result.Status = "active"
				result.Envelope = []byte(`{}`)
			}
			if kind == "foreign" {
				doc := f.doc
				doc.OrganizationID = "other"
				result.Status = "active"
				result.Envelope = sign(t, f.private, doc)
			}
			r := refresher(t, f, refreshFunc(func(context.Context, string, string) (licensing.RefreshResult, error) {
				if kind == "error" {
					return result, errors.New("secret upstream error")
				}
				return result, nil
			}))
			if err := r.RefreshOnce(t.Context()); !errors.Is(err, licensing.ErrRefreshRejected) {
				t.Fatal(err)
			}
			allowed(t, f, false)
			r = refresher(t, f, refreshFunc(unavailable))
			_ = r.RefreshOnce(t.Context())
			f.p, _ = licensing.NewEnterpriseLicenseEntitlementProvider(f.config)
			allowed(t, f, false)
		})
	}
}
func TestRefreshRecoveryDowngradePreservesUsageAndAuthorizationDuringGrace(t *testing.T) {
	f := deployment(t, licensing.Connected)
	ledger, _ := quotastore.NewMemory(2, 32)
	store, _ := licensing.NewLicensedQuotaStore(f.p, ledger)
	quota, _ := entitlement.NewQuotaService(store, func() time.Time { return f.now })
	prior, err := quota.Reserve(t.Context(), "org", limits.LimitVerificationConcurrency, 2, "prior")
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	_ = refresher(t, f, refreshFunc(unavailable)).RefreshOnce(t.Context())
	for _, role := range []authorization.Role{authorization.RoleOwner, authorization.RoleViewer} {
		permissions, _ := authorization.NewPermissionService(membership{role})
		guard, _ := application.NewExecutionGuard(tenants{}, f.p, permissions, quotas{}, application.Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute})
		_, err = guard.Authorize(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"})
		if (role == authorization.RoleOwner) != (err == nil) {
			t.Fatal("grace replaced authorization", err)
		}
	}
	f.doc.LicenseID = "renewal"
	f.doc.Limits[0].Value = 1
	f.doc.Features[1].Enabled = false
	raw := sign(t, f.private, f.doc)
	r := refresher(t, f, refreshFunc(func(_ context.Context, org, id string) (licensing.RefreshResult, error) {
		if org != "org" || id != "license_1" {
			t.Fatal("wrong refresh tenant")
		}
		return licensing.RefreshResult{Status: "active", Envelope: raw}, nil
	}))
	if err = r.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	allowed(t, f, true)
	if ok, err := f.p.Has(t.Context(), "org", feature.KeyRuntimeReplay); err != nil || ok {
		t.Fatal("unsigned cached premium grant", err)
	}
	if _, err = quota.Reserve(t.Context(), "org", limits.LimitVerificationConcurrency, 1, "over"); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal(err)
	}
	_ = ledger.WithinOrganization(t.Context(), "org", func(tx entitlement.QuotaTransaction) error {
		got, err := tx.Find("prior")
		if err != nil || got != prior {
			t.Fatal("usage history changed")
		}
		return nil
	})
	f.now = f.now.Add(3 * time.Minute)
	allowed(t, f, false)
	if _, err = quota.Release(t.Context(), "org", "prior"); err != nil {
		t.Fatal("grace expiry blocked cleanup", err)
	}
}
func TestRefreshDoesNotBlockAdmissionOrOverwriteConcurrentRevocation(t *testing.T) {
	f := deployment(t, licensing.Connected)
	f.now = f.now.Add(time.Minute)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	raw := bytes.Clone(f.source.state.Envelope)
	r := refresher(t, f, refreshFunc(func(ctx context.Context, _, _ string) (licensing.RefreshResult, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return licensing.RefreshResult{}, ctx.Err()
		}
		return licensing.RefreshResult{Status: "active", Envelope: raw}, nil
	}))
	done := make(chan error, 1)
	go func() { done <- r.RefreshOnce(t.Context()) }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if ok, err := f.p.Has(ctx, "org", feature.KeyVerifyBasic); err != nil || !ok {
		t.Fatal("network blocked admission", err)
	}
	f.source.mu.Lock()
	f.source.state.Revision++
	f.source.state.Revoked = true
	f.source.mu.Unlock()
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, licensing.ErrLicenseConflict) {
		t.Fatal("refresh overwrote revocation", err)
	}
	allowed(t, f, false)
}
func TestConnectedUsesSameGrantsAcrossAllEditionMetadata(t *testing.T) {
	f := deployment(t, licensing.Connected)
	for _, ed := range edition.Editions() {
		f.doc.Edition = ed.Key
		f.doc.EditionVersionID = string(ed.Key) + "-2026-v1"
		f.source.set(sign(t, f.private, f.doc))
		allowed(t, f, true)
	}
}
func TestPeriodicRefreshCancellationAndTimeout(t *testing.T) {
	f := deployment(t, licensing.Connected)
	f.now = f.now.Add(time.Minute)
	client := refreshFunc(func(ctx context.Context, _, _ string) (licensing.RefreshResult, error) {
		<-ctx.Done()
		return licensing.RefreshResult{}, ctx.Err()
	})
	r, err := licensing.NewLicenseRefresher(licensing.RefreshConfig{Provider: f.config, Store: f.source, Client: client, RetryInterval: time.Second, Timeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RefreshOnce(t.Context()); !errors.Is(err, licensing.ErrRefreshUnavailable) {
		t.Fatal(err)
	}
	allowed(t, f, true)
	ctx, cancel := context.WithCancel(t.Context())
	r, err = licensing.NewLicenseRefresher(licensing.RefreshConfig{Provider: f.config, Store: f.source, Client: refreshFunc(unavailable), RetryInterval: time.Second, Timeout: time.Second, Observe: func(error) { cancel() }})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExpiredLicenseCanRenewBeforeRefreshDue(t *testing.T) {
	f := deployment(t, licensing.Connected)
	f.doc.ExpiresAt = f.now.Add(30 * time.Second)
	f.source.set(sign(t, f.private, f.doc))
	f.now = f.doc.ExpiresAt
	allowed(t, f, false)
	f.doc.LicenseID = "renewed"
	f.doc.ExpiresAt = f.now.Add(24 * time.Hour)
	raw := sign(t, f.private, f.doc)
	r := refresher(t, f, refreshFunc(func(context.Context, string, string) (licensing.RefreshResult, error) {
		return licensing.RefreshResult{Status: "active", Envelope: raw}, nil
	}))
	if err := r.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	allowed(t, f, true)
}

func TestRevocationBeatsTimeoutAndCannotBeUndoneByRefresh(t *testing.T) {
	f := deployment(t, licensing.Connected)
	f.now = f.now.Add(time.Minute)
	r, err := licensing.NewLicenseRefresher(licensing.RefreshConfig{Provider: f.config, Store: f.source, RetryInterval: time.Second, Timeout: time.Millisecond,
		Client: refreshFunc(func(ctx context.Context, _, _ string) (licensing.RefreshResult, error) {
			<-ctx.Done()
			return licensing.RefreshResult{Status: "revoked"}, nil
		})})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RefreshOnce(t.Context()); !errors.Is(err, licensing.ErrRefreshRejected) {
		t.Fatal(err)
	}
	allowed(t, f, false)
	r = refresher(t, f, refreshFunc(func(context.Context, string, string) (licensing.RefreshResult, error) {
		t.Fatal("revoked installation refreshed")
		return licensing.RefreshResult{Status: "active", Envelope: f.source.state.Envelope}, nil
	}))
	if err = r.RefreshOnce(t.Context()); !errors.Is(err, licensing.ErrRefreshRejected) {
		t.Fatal(err)
	}
	allowed(t, f, false)
}
