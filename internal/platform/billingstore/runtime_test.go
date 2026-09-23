package billingstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/feature"
	"safepatch/internal/tenant"
	"testing"
	"time"
)

type tenants struct{}

func (tenants) Resolve(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	return s, nil
}

type members struct{ role authorization.Role }

func (m members) GetMembership(_ context.Context, id string, p authorization.Principal) (authorization.Membership, error) {
	return authorization.Membership{OrganizationID: id, Principal: p, Role: m.role, Active: true}, nil
}

type unmetered struct{}

func (unmetered) Check(context.Context, application.Execution) error { return nil }
func runtimeSteps(mode string) []step {
	steps := []step{{query: "BEGIN", args: []driver.Value{int64(4), true}}, {query: "SELECT state FROM organizations", args: []driver.Value{"org"}, rows: [][]driver.Value{{"ACTIVE"}}}}
	fallback := bundleSteps("community-2026-v3", "community")
	fallback[1].rows = [][]driver.Value{{"feat_0001", true, nil, "{}"}}
	steps = append(steps, fallback...)
	steps = append(steps, step{query: "e.source<>'SUBSCRIPTION'", args: []driver.Value{"org"}})
	sub := step{query: "s.state IN ('ACTIVE','TRIALING')", args: []driver.Value{"org"}}
	if mode != "non_entitled" && mode != "canceled" {
		until := stamp().Add(time.Hour)
		paid := true
		if mode == "expired" {
			until = stamp().Add(-time.Second)
		}
		if mode == "unpaid" {
			paid = false
		}
		sub.rows = [][]driver.Value{{"org", "sub_internal", "team-2026-v2", "stripe", "sub_provider", "ACTIVE", int64(2), stamp().Add(-time.Hour), stamp(), int64(2), until, paid}}
	}
	steps = append(steps, sub)
	if len(sub.rows) > 0 {
		steps = append(steps, bundleSteps("team-2026-v2", "team")...)
	}
	return append(steps, step{query: "COMMIT"})
}
func TestDatabaseRuntimePreservesEntitlementAndPermissionBoundaries(t *testing.T) {
	for _, mode := range []string{"entitled", "non_entitled", "permission_denied", "canceled", "expired", "unpaid"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := scripted(t, runtimeSteps(mode)...)
			runtime, _ := NewRuntimeEntitlements(s, "community-2026-v3")
			role := authorization.RoleOwner
			if mode == "permission_denied" {
				role = authorization.RoleViewer
			}
			permissions, _ := authorization.NewPermissionService(members{role})
			guard, _ := application.NewExecutionGuard(tenants{}, runtime, permissions, unmetered{}, application.Requirement{Feature: feature.KeyProofPlanner, Permission: authorization.KeyProofExecute})
			_, err := guard.Authorize(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"})
			if (err == nil) != (mode == "entitled") {
				t.Fatal(err)
			}
			if mode == "permission_denied" && !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal(err)
			}
		})
	}
}
func TestDatabaseChangesAreVisibleWithoutStripeOrStaleCache(t *testing.T) {
	steps := append(runtimeSteps("entitled"), runtimeSteps("canceled")...)
	s, _ := scripted(t, steps...)
	runtime, _ := NewRuntimeEntitlements(s, "community-2026-v3")
	first, err := runtime.Has(t.Context(), "org", feature.KeyProofPlanner)
	if err != nil || !first {
		t.Fatal(first, err)
	}
	next, err := runtime.Has(t.Context(), "org", feature.KeyProofPlanner)
	if err != nil || next {
		t.Fatal("stale paid grant", next, err)
	}
}
func TestRuntimeErrorsNeverBecomeAccess(t *testing.T) {
	for _, mode := range []string{"database", "suspended", "revision", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			steps := runtimeSteps("entitled")
			switch mode {
			case "database":
				steps = steps[:1]
				steps[0].err = errors.New("database failed")
			case "suspended":
				steps = steps[:2]
				steps[1].rows[0][0] = "SUSPENDED"
				steps = append(steps, step{query: "ROLLBACK"})
			default:
				steps = steps[:6]
				if mode == "revision" {
					steps[5].rows[0][9] = int64(1)
				} else {
					steps[5].rows[0][0] = "foreign"
				}
				steps = append(steps, step{query: "ROLLBACK"})
			}
			s, _ := scripted(t, steps...)
			runtime, _ := NewRuntimeEntitlements(s, "community-2026-v3")
			ok, err := runtime.Has(t.Context(), "org", feature.KeyProofPlanner)
			if ok || err == nil {
				t.Fatal(ok, err)
			}
		})
	}
	var empty RuntimeEntitlements
	if _, err := empty.Load(t.Context(), "org"); !errors.Is(err, billing.ErrInvalidConfiguration) {
		t.Fatal(err)
	}
}
