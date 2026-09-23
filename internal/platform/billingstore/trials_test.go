package billingstore

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/trial"
	"safepatch/internal/platform/trialstore"
	"safepatch/internal/tenant"
)

func trialPolicy() *trial.Policy {
	p, _ := trial.NewPolicy("developer-2026-v2", "team-2026-v2", "business-2026-v2", 30)
	return p
}
func trialValue() trial.Trial {
	o, _ := trialPolicy().Offer(edition.Team)
	v, _ := trial.New("org", o, stamp())
	return v
}
func trialRow(v trial.Trial) []driver.Value {
	return []driver.Value{v.OrganizationID, v.TrialID, string(v.Edition), v.EditionVersionID, v.StartsAt, v.ExpiresAt}
}
func startSteps(t *testing.T) []step {
	t.Helper()
	v := trialValue()
	steps := []step{{query: "BEGIN", args: []driver.Value{int64(2), false}}, {query: "FOR UPDATE", args: []driver.Value{"org"}, rows: [][]driver.Value{{"ACTIVE"}}}, {query: "FROM organization_trials", args: []driver.Value{"org"}}, {query: "source='TRIAL'", args: []driver.Value{"org"}, rows: [][]driver.Value{{false}}}}
	steps = append(steps, bundleSteps(v.EditionVersionID, "team")...)
	return append(steps,
		step{query: "INSERT INTO organization_trials", args: trialRow(v), affected: 1},
		step{query: "INSERT INTO entitlements", affected: 1, check: func(a []driver.NamedValue) {
			var e domain.Entitlement
			if json.Unmarshal([]byte(a[5].Value.(string)), &e) != nil || e.Validate() != nil || e.OrganizationID != "org" || e.Source != domain.SourceTrial || e.SourceReference != v.TrialID || !e.ExpiresAt.Equal(v.ExpiresAt) {
				t.Fatal("incorrect trial grant", e)
			}
		}},
		step{query: "INSERT INTO organization_trial_grants", affected: 1}, step{query: "COMMIT"})
}
func TestTrialStartAtomicIdempotentAndErrorPaths(t *testing.T) {
	for _, mode := range []string{"new", "retry", "changed_offer_retry", "expired_retry", "other_edition", "legacy", "suspended", "foreign", "begin_error", "grant_error", "link_error", "commit_error", "unsealed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			steps := startSteps(t)
			v := trialValue()
			key := edition.Team
			policy := trialPolicy()
			switch mode {
			case "retry", "changed_offer_retry", "expired_retry", "other_edition":
				steps = steps[:3]
				steps[2].rows = [][]driver.Value{trialRow(v)}
				end := "COMMIT"
				if mode == "other_edition" {
					key = edition.Developer
					end = "ROLLBACK"
				}
				steps = append(steps, step{query: end})
				if mode == "changed_offer_retry" {
					policy, _ = trial.NewPolicy("developer-2026-v2", "team-2026-v3", "", 0)
				}
			case "legacy":
				steps = steps[:4]
				steps[3].rows = [][]driver.Value{{true}}
				steps = append(steps, step{query: "ROLLBACK"})
			case "suspended":
				steps = steps[:2]
				steps[1].rows = [][]driver.Value{{"SUSPENDED"}}
				steps = append(steps, step{query: "ROLLBACK"})
			case "foreign":
				steps = steps[:3]
				row := trialRow(v)
				row[0] = "foreign"
				steps[2].rows = [][]driver.Value{row}
				steps = append(steps, step{query: "ROLLBACK"})
			case "begin_error":
				steps = steps[:1]
				steps[0].err = errors.New("begin failed")
			case "grant_error", "link_error":
				i := 7
				if mode == "link_error" {
					i = 8
				}
				steps = steps[:i+1]
				steps[i].err = errors.New("write failed")
				steps = append(steps, step{query: "ROLLBACK"})
			case "commit_error":
				steps[9].err = errors.New("commit failed")
			case "unsealed":
				steps = steps[:5]
				steps[4].rows[0][3] = false
				steps = append(steps, step{query: "ROLLBACK"})
			case "unknown":
				key = edition.Enterprise
				steps = append(steps[:4], step{query: "ROLLBACK"})
			}
			s, _ := scripted(t, steps...)
			now := stamp
			if mode == "expired_retry" {
				now = func() time.Time { return v.ExpiresAt.Add(time.Hour) }
			}
			r, err := trialstore.NewPostgres(s.db, policy, s.features, now)
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.Start(t.Context(), "org", key)
			want := mode == "new" || mode == "retry" || mode == "changed_offer_retry" || mode == "expired_retry"
			if (err == nil) != want || (want && got != v) || (!want && got != (trial.Trial{})) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestTrialReadScopesAndValidates(t *testing.T) {
	for _, mode := range []string{"found", "missing", "foreign", "database"} {
		t.Run(mode, func(t *testing.T) {
			v := trialValue()
			st := step{query: "o.state='ACTIVE'", args: []driver.Value{"org"}, rows: [][]driver.Value{trialRow(v)}}
			if mode == "missing" {
				st.rows = nil
			}
			if mode == "foreign" {
				st.rows[0][0] = "foreign"
			}
			if mode == "database" {
				st.err = errors.New("failed")
			}
			s, _ := scripted(t, st)
			r, _ := trialstore.NewPostgres(s.db, trialPolicy(), s.features, stamp)
			got, err := r.Get(t.Context(), "org")
			if (err == nil) != (mode == "found") || (err != nil && got != (trial.Trial{})) {
				t.Fatal(got, err)
			}
		})
	}
}

func runtimeTrialSteps(t *testing.T, paid bool) ([]step, domain.Entitlement) {
	t.Helper()
	mode := "non_entitled"
	if paid {
		mode = "entitled"
	}
	steps := runtimeSteps(mode)
	v := trialValue()
	features, _ := feature.NewBuiltinCatalog("1.0.0", stamp())
	limit := int64(5)
	b := edition.Bundle{Version: edition.EditionVersion{EditionVersionID: v.EditionVersionID, EditionKey: v.Edition, CreatedAt: stamp()}, Features: []edition.EditionFeature{{EditionVersionID: v.EditionVersionID, FeatureID: "feat_0011", Enabled: true, LimitValue: &limit}}}
	grants, err := trial.Derive(v, b, features)
	if err != nil {
		t.Fatal(err)
	}
	e := grants[0]
	raw, _ := json.Marshal(e)
	steps[4].rows = [][]driver.Value{{e.EntitlementID, string(raw), e.FeatureID, string(e.Source), e.SourceReference}}
	if paid {
		steps[5].rows[0][10] = v.ExpiresAt.Add(trial.Day)
	}
	return steps, e
}
func TestOrganizationTrialRuntimeEntitlementPermissionAndExpiry(t *testing.T) {
	for _, mode := range []string{"active", "viewer", "expired", "paid_after_expiry", "none", "community_after_expiry"} {
		t.Run(mode, func(t *testing.T) {
			steps, _ := runtimeTrialSteps(t, mode == "paid_after_expiry")
			if mode == "none" {
				steps[4].rows = nil
			}
			s, _ := scripted(t, steps...)
			now := stamp
			if mode == "expired" || mode == "paid_after_expiry" || mode == "community_after_expiry" {
				now = func() time.Time { return trialValue().ExpiresAt }
			}
			runtime, err := NewOrganizationRuntime(s.db, s.features, "community-2026-v3", now)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "community_after_expiry" {
				ok, err := runtime.Has(t.Context(), "org", feature.KeyVerifyBasic)
				if err != nil || !ok {
					t.Fatal(ok, err)
				}
				return
			}
			role := authorization.RoleOwner
			if mode == "viewer" {
				role = authorization.RoleViewer
			}
			permissions, _ := authorization.NewPermissionService(members{role})
			guard, _ := application.NewExecutionGuard(tenants{}, runtime, permissions, unmetered{}, application.Requirement{Feature: feature.KeyProofPlanner, Permission: authorization.KeyProofExecute})
			_, err = guard.Authorize(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"})
			if (err == nil) != (mode == "active" || mode == "paid_after_expiry") {
				t.Fatal(err)
			}
			if mode == "viewer" && !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal(err)
			}
		})
	}
}
func TestTrialExpirationIsVisibleWithoutWritesOrRestart(t *testing.T) {
	steps, grant := runtimeTrialSteps(t, false)
	next, _ := runtimeTrialSteps(t, false)
	s, _ := scripted(t, append(steps, next...)...)
	at := stamp()
	runtime, _ := NewOrganizationRuntime(s.db, s.features, "community-2026-v3", func() time.Time { return at })
	before := grant.Clone()
	if ok, err := runtime.Has(t.Context(), "org", feature.KeyProofPlanner); err != nil || !ok {
		t.Fatal(ok, err)
	}
	at = trialValue().ExpiresAt
	if ok, err := runtime.Has(t.Context(), "org", feature.KeyProofPlanner); err != nil || ok {
		t.Fatal(ok, err)
	}
	if !reflect.DeepEqual(before, grant) {
		t.Fatal("trial history changed")
	}
}
