package organizationstore

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

func stamp() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }
func memberStep(organizationID string, role authorization.Role, active bool) step {
	return step{sql: "WHERE m.organization_id=$1 AND m.user_id=$2", args: []driver.Value{organizationID, "user"}, rows: [][]driver.Value{{organizationID, "user", string(role), active}}}
}
func TestOneUserHasIndependentMembershipsAndPermissions(t *testing.T) {
	actor := authorization.Principal{ID: "user", Kind: authorization.User}
	store := scripted(t, memberStep("org_a", authorization.RoleOwner, true), memberStep("org_b", authorization.RoleViewer, true), memberStep("org_b", authorization.RoleViewer, true), memberStep("org_a", authorization.RoleOwner, false))
	permissions, _ := authorization.NewPermissionService(store)
	for _, tc := range []struct {
		org  string
		key  authorization.Key
		want bool
	}{{"org_a", authorization.KeyBillingManage, true}, {"org_b", authorization.KeyBillingManage, false}, {"org_b", authorization.KeyProofView, true}, {"org_a", authorization.KeyProofExecute, false}} {
		got, err := permissions.Allowed(t.Context(), actor, tc.org, tc.key)
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	actor.Kind = authorization.ServiceAccount
	if _, err := store.GetMembership(t.Context(), "org_a", actor); !errors.Is(err, authorization.ErrMembershipNotFound) {
		t.Fatal("principal kind collision", err)
	}
}

type grants bool

func (g grants) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, errors.New("unexpected resolution")
}

func (g grants) Limit(context.Context, string, feature.Key) (int64, error) {
	return 0, errors.New("unexpected limit")
}

func (g grants) Has(context.Context, string, feature.Key) (bool, error) { return bool(g), nil }

type unmetered struct{}

func (unmetered) Check(context.Context, application.Execution) error { return nil }
func TestRealTenantAndPermissionAdaptersPreserveEntitlementBoundary(t *testing.T) {
	for _, mode := range []string{"allowed", "feature_denied", "permission_denied", "foreign_repository", "inactive"} {
		t.Run(mode, func(t *testing.T) {
			role := authorization.RoleOwner
			if mode == "permission_denied" {
				role = authorization.RoleViewer
			}
			steps := []step{memberStep("org_a", role, mode != "inactive")}
			if mode != "inactive" {
				repo := step{sql: "WHERE r.organization_id=$1 AND r.repository_id=$2 AND r.active AND p.active", args: []driver.Value{"org_a", "repo"}, rows: [][]driver.Value{{"repo"}}}
				if mode == "foreign_repository" {
					repo.rows = nil
				}
				steps = append(steps, repo)
			}
			if mode == "allowed" || mode == "permission_denied" {
				steps = append(steps, memberStep("org_a", role, true))
			}
			store := scripted(t, steps...)
			permissions, _ := authorization.NewPermissionService(store)
			guard, _ := application.NewExecutionGuard(store, grants(mode != "feature_denied"), permissions, unmetered{}, application.Requirement{Feature: feature.KeyProofPlanner, Permission: authorization.KeyProofExecute})
			_, err := guard.Authorize(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, tenant.Scope{OrganizationID: "org_a", ResourceID: "repo"})
			if (err == nil) != (mode == "allowed") {
				t.Fatal(err)
			}
			if mode == "feature_denied" && !errors.Is(err, entitlement.ErrNotEntitled) {
				t.Fatal(err)
			}
			if mode == "permission_denied" && !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal(err)
			}
		})
	}
}
func TestScopedEntityCreatesAndSubscriptionCAS(t *testing.T) {
	now := stamp()
	sub := org.Subscription{OrganizationID: "org", SubscriptionID: "sub", EditionVersionID: "team-2026-v1", Provider: "manual", ExternalReference: "contract", State: org.Subscribed, Revision: 1, CreatedAt: now, UpdatedAt: now}
	store := scripted(t,
		step{sql: "INSERT INTO users", args: []driver.Value{"user", "Name", true, now}},
		step{sql: "INSERT INTO organizations", args: []driver.Value{"org", "Org", "ACTIVE", now}},
		step{sql: "INSERT INTO organization_members", args: []driver.Value{"org", "user", "owner", true, now}},
		step{sql: "INSERT INTO projects", args: []driver.Value{"org", "project", "Project", true, now}},
		step{sql: "INSERT INTO repositories", args: []driver.Value{"org", "project", "repo", "Repo", true, now}},
		step{sql: "INSERT INTO subscriptions", args: []driver.Value{"org", "sub", "team-2026-v1", "manual", "contract", "ACTIVE", int64(1), now, now}},
		step{sql: "UPDATE subscriptions", args: []driver.Value{"org", "sub", "developer-2026-v1", "ACTIVE", int64(2), now, int64(1), "manual", "contract", now}, affected: 1},
		step{sql: "UPDATE subscriptions", args: []driver.Value{"org", "sub", "developer-2026-v1", "ACTIVE", int64(2), now, int64(1), "manual", "contract", now}, affected: 0})
	operations := []func() error{
		func() error {
			return store.CreateUser(t.Context(), org.User{UserID: "user", DisplayName: "Name", Active: true, CreatedAt: now})
		},
		func() error {
			return store.CreateOrganization(t.Context(), org.Organization{OrganizationID: "org", Name: "Org", State: org.Active, CreatedAt: now})
		},
		func() error {
			return store.CreateMembership(t.Context(), authorization.Membership{OrganizationID: "org", Principal: authorization.Principal{ID: "user", Kind: authorization.User}, Role: authorization.RoleOwner, Active: true}, now)
		},
		func() error {
			return store.CreateProject(t.Context(), "org", org.Project{OrganizationID: "org", ProjectID: "project", Name: "Project", Active: true, CreatedAt: now})
		},
		func() error {
			return store.CreateRepository(t.Context(), "org", org.Repository{OrganizationID: "org", ProjectID: "project", RepositoryID: "repo", Name: "Repo", Active: true, CreatedAt: now})
		},
		func() error { return store.CreateSubscription(t.Context(), "org", sub) },
	}
	for _, op := range operations {
		if err := op(); err != nil {
			t.Fatal(err)
		}
	}
	sub.EditionVersionID = "developer-2026-v1"
	sub.Revision = 2
	if err := store.UpdateSubscription(t.Context(), "org", sub, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSubscription(t.Context(), "org", sub, 1); !errors.Is(err, org.ErrConflict) {
		t.Fatal("stale update accepted", err)
	}
}
func TestScopedReadsRejectWrongOwnerAndMissingData(t *testing.T) {
	for _, mode := range []string{"valid", "foreign", "missing", "backend", "invalid", "invalid_project"} {
		t.Run(mode, func(t *testing.T) {
			row := []driver.Value{"org", "project", "repo", "Repo", true, stamp()}
			st := step{sql: "WHERE organization_id=$1 AND repository_id=$2", args: []driver.Value{"org", "repo"}, rows: [][]driver.Value{row}}
			want := error(nil)
			switch mode {
			case "foreign":
				row[0] = "other"
				want = org.ErrData
			case "missing":
				st.rows = nil
				want = org.ErrNotFound
			case "backend":
				want = errors.New("backend failure")
				st.err = want
			case "invalid":
				row[3] = "../name\n"
				want = org.ErrData
			case "invalid_project":
				row[1] = row[0]
				want = org.ErrData
			}
			s := scripted(t, st)
			_, err := s.GetRepository(t.Context(), "org", "repo")
			if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
		})
	}
}
func entitlementFixture(t *testing.T) (domain.Entitlement, string) {
	t.Helper()
	v := domain.Entitlement{OrganizationID: "org", EntitlementID: "grant", FeatureID: "feat_0011", Source: domain.SourceSubscription, SourceReference: "sub", Enabled: true, StartsAt: stamp(), CreatedAt: stamp(), Metadata: json.RawMessage(`{}`)}
	raw, _ := json.Marshal(v)
	return v, string(raw)
}
func TestEntitlementsReuseCanonicalSchemaAndScope(t *testing.T) {
	v, raw := entitlementFixture(t)
	for _, mode := range []string{"valid", "duplicate", "other_tenant", "poison", "null", "missing"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "duplicate" {
				s := scripted(t, step{sql: "INSERT INTO entitlements", args: []driver.Value{"org", "grant", "feat_0011", "SUBSCRIPTION", "sub", raw}, err: sqlState("23505")})
				if err := s.Create(t.Context(), "org", v); !errors.Is(err, entitlement.ErrAlreadyExists) {
					t.Fatal(err)
				}
				return
			}
			if mode == "valid" {
				s := scripted(t, step{sql: "INSERT INTO entitlements", args: []driver.Value{"org", "grant", "feat_0011", "SUBSCRIPTION", "sub", raw}})
				if err := s.Create(t.Context(), "org", v); err != nil {
					t.Fatal(err)
				}
				return
			}
			text := raw
			want := error(org.ErrData)
			switch mode {
			case "other_tenant":
				text = strings.Replace(raw, `"organization_id":"org"`, `"organization_id":"other"`, 1)
			case "poison":
				text = strings.Replace(raw, `"enabled":true`, `"enabled":false,"enabled":true`, 1)
			case "null":
				text = strings.Replace(raw, `"enabled":true`, `"enabled":null`, 1)
			}
			st := step{sql: "WHERE organization_id=$1 AND entitlement_id=$2", args: []driver.Value{"org", "grant"}, rows: [][]driver.Value{{text, "feat_0011", "SUBSCRIPTION", "sub"}}}
			if mode == "missing" {
				st.rows = nil
				want = entitlement.ErrNotFound
			}
			s := scripted(t, st)
			if _, err := s.Get(t.Context(), "org", "grant"); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
		})
	}
}
func TestListsBoundTenantPaginationAndLateErrors(t *testing.T) {
	for _, mode := range []string{"page", "late", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			rows := [][]driver.Value{{"org_a", "A", "ACTIVE", stamp()}, {"org_b", "B", "ACTIVE", stamp()}}
			if mode == "duplicate" {
				rows[1][0] = "org_a"
			}
			st := step{sql: `ORDER BY o.organization_id COLLATE "C" LIMIT $3`, args: []driver.Value{"user", "", int64(2)}, rows: rows}
			if mode == "late" {
				st.late = errors.New("late error")
			}
			s := scripted(t, st)
			page, err := s.ListOrganizations(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, "", 1)
			if mode == "page" {
				if err != nil || len(page.Organizations) != 1 || page.NextAfterID != "org_a" {
					t.Fatal(page, err)
				}
			} else if err == nil || len(page.Organizations) != 0 {
				t.Fatal("partial success", page, err)
			}
		})
	}
	v, raw := entitlementFixture(t)
	v.Enabled = false
	v.EntitlementID = "grant2"
	raw2, _ := json.Marshal(v)
	s := scripted(t, step{sql: `ORDER BY entitlement_id COLLATE "C" LIMIT $4`, args: []driver.Value{"org", "feat_0011", "", int64(3)}, rows: [][]driver.Value{{"grant", raw, "feat_0011", "SUBSCRIPTION", "sub"}, {"grant2", string(raw2), "feat_0011", "SUBSCRIPTION", "sub"}}})
	p, err := s.List(t.Context(), "org", entitlement.ListQuery{FeatureID: "feat_0011", Limit: 2})
	if err != nil || len(p.Entitlements) != 2 || p.Entitlements[1].Enabled {
		t.Fatal("list hid disabled history", p, err)
	}
}
func TestMalformedAndCrossTenantWritesNeverReachSQL(t *testing.T) {
	s := scripted(t)
	v, _ := entitlementFixture(t)
	if err := s.Create(t.Context(), "other", v); !errors.Is(err, entitlement.ErrOrganizationMismatch) {
		t.Fatal(err)
	}
	r := org.Repository{OrganizationID: "org", ProjectID: "project", RepositoryID: "repo", Name: "Repo", CreatedAt: stamp()}
	if s.CreateRepository(t.Context(), "other", r) == nil {
		t.Fatal("cross-tenant write")
	}
	r.ProjectID = r.OrganizationID
	if err := s.CreateRepository(t.Context(), "org", r); !errors.Is(err, org.ErrInvalidEntity) {
		t.Fatal("invalid project identity reached persistence", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.GetRepository(ctx, "org", "repo"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.List(t.Context(), "org", entitlement.ListQuery{Limit: 100000}); err == nil {
		t.Fatal("unbounded list")
	}
	if _, err := s.GetUser(t.Context(), "../user"); err == nil {
		t.Fatal("invalid ID")
	}
}

func TestIdentityAndSubscriptionReadsValidateStoredProjection(t *testing.T) {
	for _, kind := range []string{"user", "organization", "project", "subscription"} {
		for _, mode := range []string{"valid", "mismatch", "missing", "scan"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				st := step{}
				var read func(*Postgres) error
				switch kind {
				case "user":
					st = step{sql: "WHERE user_id=$1", args: []driver.Value{"user"}, rows: [][]driver.Value{{"user", "User", true, stamp()}}}
					read = func(s *Postgres) error { _, err := s.GetUser(t.Context(), "user"); return err }
				case "organization":
					st = step{sql: "WHERE organization_id=$1", args: []driver.Value{"org"}, rows: [][]driver.Value{{"org", "Org", "ACTIVE", stamp()}}}
					read = func(s *Postgres) error { _, err := s.GetOrganization(t.Context(), "org"); return err }
				case "project":
					st = step{sql: "WHERE organization_id=$1 AND project_id=$2", args: []driver.Value{"org", "project"}, rows: [][]driver.Value{{"org", "project", "Project", true, stamp()}}}
					read = func(s *Postgres) error { _, err := s.GetProject(t.Context(), "org", "project"); return err }
				case "subscription":
					st = step{sql: "WHERE organization_id=$1 AND subscription_id=$2", args: []driver.Value{"org", "sub"}, rows: [][]driver.Value{{"org", "sub", "team-2026-v1", "manual", "contract", "ACTIVE", int64(1), stamp(), stamp()}}}
					read = func(s *Postgres) error { _, err := s.GetSubscription(t.Context(), "org", "sub"); return err }
				}
				switch mode {
				case "mismatch":
					st.rows[0][0] = "other"
				case "missing":
					st.rows = nil
				case "scan":
					st.rows[0] = st.rows[0][:1]
				}
				err := read(scripted(t, st))
				if mode == "valid" && err != nil {
					t.Fatal(err)
				}
				if mode != "valid" && err == nil {
					t.Fatal("invalid stored identity accepted")
				}
				if mode == "missing" && !errors.Is(err, org.ErrNotFound) {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestMembershipUpdatesAndCorruptMembershipFailClosed(t *testing.T) {
	m := authorization.Membership{OrganizationID: "org", Principal: authorization.Principal{ID: "user", Kind: authorization.User}, Role: authorization.RoleViewer, Active: false}
	for _, affected := range []int64{0, 1} {
		s := scripted(t, step{sql: "UPDATE organization_members SET role=$3,active=$4 WHERE organization_id=$1 AND user_id=$2", args: []driver.Value{"org", "user", "viewer", false}, affected: affected})
		err := s.UpdateMembership(t.Context(), m)
		if (err == nil) != (affected == 1) {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"foreign", "role", "absent", "error"} {
		st := memberStep("org", authorization.RoleOwner, true)
		switch mode {
		case "foreign":
			st.rows[0][0] = "other"
		case "role":
			st.rows[0][2] = "superuser"
		case "absent":
			st.rows = nil
		case "error":
			st.err = errors.New("query failed")
		}
		s := scripted(t, st)
		permissions, _ := authorization.NewPermissionService(s)
		allowed, err := permissions.Allowed(t.Context(), m.Principal, "org", authorization.KeyBillingManage)
		if allowed || mode != "absent" && err == nil {
			t.Fatal("bad membership granted access", mode, allowed, err)
		}
	}
}
