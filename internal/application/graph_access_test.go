package application_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

type graphLimitStore struct {
	t     *testing.T
	limit domain.EntitlementLimit
	err   error
	calls *[]string
}

func (s *graphLimitStore) WithinOrganization(_ context.Context, org string, f func(entitlement.QuotaTransaction) error) error {
	if org != "org" {
		s.t.Fatal("unscoped retention")
	}
	*s.calls = append(*s.calls, "retention")
	if s.err != nil {
		return s.err
	}
	return f(graphLimitTx{store: s})
}

type graphLimitTx struct {
	entitlement.QuotaTransaction
	store *graphLimitStore
}

func (tx graphLimitTx) Limit(k domain.LimitKey) (domain.EntitlementLimit, error) {
	if k != domain.LimitRetentionDays {
		tx.store.t.Fatal("wrong limit")
	}
	return tx.store.limit, nil
}

func TestGraphAccessEntitlementPermissionAndRetentionBoundaries(t *testing.T) {
	private := errors.New("backend credential detail")
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	for _, scenario := range []string{"success", "local", "organization", "missing_local", "missing_organization", "missing_cross_repo", "permission", "tenant", "entitlement_error", "permission_error", "retention_error", "disabled", "zero", "missing_policy", "invalid_policy", "huge", "cancel", "invalid_view", "local_multiple"} {
		t.Run(scenario, func(t *testing.T) {
			var calls []string
			store := &graphLimitStore{t: t, limit: domain.EntitlementLimit{Key: domain.LimitRetentionDays, State: domain.Integer, Value: 30}, calls: &calls}
			switch scenario {
			case "retention_error":
				store.err = private
			case "disabled":
				store.limit.State = domain.Disabled
				store.limit.Value = 0
			case "zero":
				store.limit.Value = 0
			case "missing_policy":
				store.limit = domain.EntitlementLimit{}
			case "invalid_policy":
				store.limit.State = "UNKNOWN"
			case "huge":
				store.limit.Value = math.MaxInt64
			}
			access, err := application.NewGraphAccess(tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				calls = append(calls, "tenant:"+s.ResourceID)
				if p != actor || s.OrganizationID != "org" {
					t.Fatal("wrong tenant inputs")
				}
				if scenario == "tenant" && s.ResourceID == "repo_b" {
					s.OrganizationID = "other"
				}
				return s, nil
			}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
				calls = append(calls, string(k))
				if org != "org" {
					t.Fatal(org)
				}
				if scenario == "entitlement_error" {
					return true, private
				}
				denied := map[string]feature.Key{"missing_local": feature.KeyGraphLocal, "missing_organization": feature.KeyGraphOrganization, "missing_cross_repo": feature.KeyGraphCrossRepo}
				return denied[scenario] != k, nil
			}), permissionFunc(func(_ context.Context, p authorization.Principal, org string, k authorization.Key) (bool, error) {
				calls = append(calls, "permission")
				if p != actor || org != "org" || k != authorization.KeyProofView {
					t.Fatal("wrong permission inputs")
				}
				if scenario == "permission_error" {
					return true, private
				}
				return scenario != "permission", nil
			}), store, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			repos := []string{"repo_a", "repo_b"}
			view := application.GraphCrossRepository
			switch scenario {
			case "local":
				view = application.GraphLocal
				repos = repos[:1]
			case "organization":
				view = application.GraphOrganization
				repos = repos[:1]
			case "invalid_view":
				view = "UNKNOWN"
			case "local_multiple":
				view = application.GraphLocal
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancel" {
				cancel()
			}
			scope, err := access.Authorize(ctx, actor, "org", repos, view, false)
			ok := scenario == "success" || scenario == "local" || scenario == "organization" || scenario == "huge"
			if (err == nil) != ok {
				t.Fatal(scope, err, calls)
			}
			if !ok {
				if scope.TenantID != "" {
					t.Fatal("denial leaked usable scope")
				}
				if scenario == "permission" && !errors.Is(err, application.ErrPermissionDenied) {
					t.Fatal(err)
				}
				if scenario == "missing_cross_repo" && (!errors.Is(err, entitlement.ErrNotEntitled) || slices.Contains(calls, "permission")) {
					t.Fatal(err, calls)
				}
				if slices.Contains([]string{"tenant", "permission", "entitlement_error", "permission_error", "missing_local", "missing_organization"}, scenario) && slices.Contains(calls, "retention") {
					t.Fatal("admission continued after denial", calls)
				}
				return
			}
			if scope.AsOf != now || scenario != "huge" && scope.NotBefore != now.AddDate(0, 0, -30) || scenario == "huge" && !scope.NotBefore.IsZero() {
				t.Fatal(scope)
			}
			if scenario == "success" && !slices.Equal(calls, []string{"tenant:repo_a", "tenant:repo_b", "graph.local", "graph.organization", "graph.cross_repo", "permission", "retention"}) {
				t.Fatal(calls)
			}
			repos[0] = "other"
			if scope.RepositoryIDs[0] != "repo_a" {
				t.Fatal("caller changed admitted scope")
			}
		})
	}
}

func TestGraphPlanChangesOnlyChangeAdmissionView(t *testing.T) {
	var calls []string
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store := &graphLimitStore{t: t, limit: domain.EntitlementLimit{Key: domain.LimitRetentionDays, State: domain.Unlimited}, calls: &calls}
	granted := true
	a, _ := application.NewGraphAccess(tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), entitlementFunc(func(_ context.Context, _ string, k feature.Key) (bool, error) {
		return k == feature.KeyGraphLocal || granted, nil
	}), permissionFunc(func(_ context.Context, _ authorization.Principal, _ string, k authorization.Key) (bool, error) {
		if k != authorization.KeyProofExecute {
			t.Fatal(k)
		}
		return true, nil
	}), store, func() time.Time { return now })
	actor := authorization.Principal{ID: "worker", Kind: authorization.ServiceAccount}
	read := func() (bool, error) {
		s, e := a.Authorize(t.Context(), actor, "org", []string{"repo_a", "repo_b"}, application.GraphCrossRepository, true)
		return s.Contains("repo_a", now.AddDate(0, 0, -60)), e
	}
	if visible, err := read(); !visible || err != nil {
		t.Fatal(visible, err)
	}
	store.limit = domain.EntitlementLimit{Key: domain.LimitRetentionDays, State: domain.Integer, Value: 7}
	if visible, err := read(); visible || err != nil {
		t.Fatal(visible, err)
	}
	granted = false
	if _, err := read(); !errors.Is(err, entitlement.ErrNotEntitled) {
		t.Fatal(err)
	}
	granted = true
	store.limit = domain.EntitlementLimit{Key: domain.LimitRetentionDays, State: domain.Unlimited}
	if visible, err := read(); !visible || err != nil {
		t.Fatal(visible, err)
	}
	// The admission object has no graph mutation dependency. The store double
	// exposes only Limit; any usage/reservation mutation panics via nil embedding.
}
