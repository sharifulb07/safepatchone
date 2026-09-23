package githubapp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/config"
	"safepatch/internal/domain"
	"safepatch/internal/domain/edition"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/quotastore"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
	"safepatch/internal/verification"
	"safepatch/internal/verification/upgrade"
)

type upgradeSource struct {
	candidate      upgrade.Candidate
	calls, current int
	mode           string
	hook           func()
}

func (s *upgradeSource) LoadUpgrade(context.Context, tenant.Scope, string) (upgrade.Candidate, error) {
	s.calls++
	if s.hook != nil {
		s.hook()
	}
	if s.mode == "error" {
		return upgrade.Candidate{}, errors.New("private registry credential")
	}
	return s.candidate, nil
}
func (s *upgradeSource) IsCurrent(context.Context, domain.Change) (bool, error) {
	s.current++
	return s.mode != "stale" && !(s.mode == "moved" && s.current > 1), nil
}
func upgradeCandidate(t *testing.T, c domain.Change) upgrade.Candidate {
	t.Helper()
	p, err := upgrade.New(upgrade.Fields{Schema: upgrade.Schema, Change: c, Dependency: upgrade.Dependency{Ecosystem: "go", Name: "example.test/lib", FromVersion: "v1.0.0", ToVersion: "v1.0.1"}, Files: []upgrade.File{{Path: "go.mod", BeforeDigest: strings.Repeat("a", 64), AfterDigest: strings.Repeat("b", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func upgradeService(t *testing.T, core *application.ChangeWorkflow, source *upgradeSource, access *changeAccess, enabled bool, limit limits.EntitlementLimit) (*application.SafeUpgradeService, *quotastore.Memory, flowEntitlements) {
	t.Helper()
	grants := flowEntitlements{enabled: map[feature.Key]bool{feature.KeyAutomationSafeUpgrade: enabled}}
	tenants := tenantFunc(func(_ context.Context, a authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		if a.ID != "user" || s.OrganizationID != "org_a" || s.ResourceID != "repo" {
			return tenant.Scope{}, tenant.ErrInvalidScope
		}
		return s, nil
	})
	store, _ := quotastore.NewMemory(4, 100)
	if err := store.SetLimit(t.Context(), "org_a", limit); err != nil {
		t.Fatal(err)
	}
	meter, _ := entitlement.NewQuotaService(store, func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) })
	s, err := application.NewSafeUpgradeService(tenants, grants, access, access, meter, source, core)
	if err != nil {
		t.Fatal(err)
	}
	return s, store, grants
}
func TestSafeUpgradeAllEditionsSharedWorkflowAndHistory(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	matrix, err := config.SafeUpgradeEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range matrix.Editions {
		t.Run(string(entry.Bundle.Version.EditionKey), func(t *testing.T) {
			enabled := false
			limit := limits.EntitlementLimit{Key: limits.LimitSafeUpgradesMonthly, State: limits.Disabled}
			for _, f := range entry.Bundle.Features {
				if f.FeatureID == "feat_0044" {
					enabled = f.Enabled
				}
			}
			for _, l := range entry.Limits {
				if l.Key == limit.Key {
					limit = l
				}
			}
			if enabled != (entry.Bundle.Version.EditionKey != edition.Community) {
				t.Fatal("wrong initial allocation")
			}
			core, change, _, runner, archive, access := workflowFixture(t)
			source := &upgradeSource{candidate: upgradeCandidate(t, change)}
			s, store, grants := upgradeService(t, core, source, access, enabled, limit)
			actor := authorization.Principal{ID: "user", Kind: authorization.User}
			scope := tenant.Scope{OrganizationID: "org_a", ResourceID: "repo"}
			result, err := s.Verify(t.Context(), actor, scope, source.candidate.ID(), "upgrade_run")
			if !enabled {
				if err == nil || source.calls != 0 || runner.calls != 0 {
					t.Fatal("unentitled work")
				}
				return
			}
			if err != nil || result.Decision != trust.Pass || len(archive.saved) != 1 || archive.saved[0].Passport.Digest() == "" {
				t.Fatal("shared pipeline not verified", result, err)
			}
			previous, _ := json.Marshal(archive.saved)
			if _, err := s.Verify(t.Context(), actor, scope, source.candidate.ID(), "upgrade_run"); !errors.Is(err, entitlement.ErrQuotaConflict) || runner.calls != 1 {
				t.Fatal("duplicate execution", err)
			}
			access.allowed = false
			if _, err := s.Verify(t.Context(), actor, scope, source.candidate.ID(), "denied"); !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal(err)
			}
			access.allowed = true
			grants.enabled[feature.KeyAutomationSafeUpgrade] = false
			if _, err := s.Verify(t.Context(), actor, scope, source.candidate.ID(), "downgrade"); err == nil {
				t.Fatal("downgrade bypass")
			}
			grants.enabled[feature.KeyAutomationSafeUpgrade] = true
			_ = store.SetLimit(t.Context(), "org_a", limits.EntitlementLimit{Key: limit.Key, State: limits.Integer, Value: 1})
			if _, err := s.Verify(t.Context(), actor, scope, source.candidate.ID(), "over_limit"); !errors.Is(err, entitlement.ErrQuotaExceeded) {
				t.Fatal(err)
			}
			after, _ := json.Marshal(archive.saved)
			if !bytes.Equal(previous, after) {
				t.Fatal("history corrupted")
			}
		})
	}
}
func TestSafeUpgradeErrorsNeverPass(t *testing.T) {
	for _, mode := range []string{"source", "stale", "moved", "foreign", "revoke", "unknown", "storage", "integrity", "nil_context", "canceled", "wrong_id", "quota"} {
		t.Run(mode, func(t *testing.T) {
			core, change, control, runner, archive, access := workflowFixture(t)
			source := &upgradeSource{candidate: upgradeCandidate(t, change)}
			s, _, grants := upgradeService(t, core, source, access, true, limits.EntitlementLimit{Key: limits.LimitSafeUpgradesMonthly, State: limits.Integer, Value: 1})
			ctx := t.Context()
			scope := tenant.Scope{OrganizationID: "org_a", ResourceID: "repo"}
			id := source.candidate.ID()
			switch mode {
			case "source":
				source.mode = "error"
			case "stale", "moved":
				source.mode = mode
			case "foreign":
				scope.OrganizationID = "org_b"
			case "revoke":
				source.hook = func() { grants.enabled[feature.KeyAutomationSafeUpgrade] = false }
			case "unknown":
				runner.status = verification.TimedOut
			case "storage":
				archive.fail = true
			case "integrity":
				control.integrityError = true
			case "nil_context":
				ctx = nil
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "wrong_id":
				id = "upgrade_" + strings.Repeat("0", 64)
			case "quota":
				access.quota = false
			}
			result, err := s.Verify(ctx, authorization.Principal{ID: "user", Kind: authorization.User}, scope, id, "attempt")
			if result.Decision != trust.Unknown || (mode != "unknown" && err == nil) || err != nil && strings.Contains(err.Error(), "credential") {
				t.Fatal("unsafe result", result, err)
			}
		})
	}
}
