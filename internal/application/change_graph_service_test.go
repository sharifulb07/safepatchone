package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"safepatch/internal/tenant"
)

// A recording boundary double, not another graph engine. PostgreSQL persistence
// behavior is tested in graphstore and native migration assertions.
type changeStoreSpy struct {
	calls int
	node  graph.ChangeNode
	scope graph.RevisionScope
	err   error
}

func (s *changeStoreSpy) PutChangeNode(_ context.Context, scope graph.RevisionScope, n graph.ChangeNode) error {
	s.calls++
	s.scope = scope
	if s.err != nil {
		return s.err
	}
	s.node = n
	return nil
}
func (s *changeStoreSpy) GetChangeNode(_ context.Context, scope graph.RevisionScope, _ graph.EntityRef) (graph.ChangeNode, error) {
	s.calls++
	s.scope = scope
	if s.err != nil {
		return graph.ChangeNode{}, s.err
	}
	f, _ := s.node.Fields()
	if !scope.Includes(f.Ref, f.CreatedAt) {
		return graph.ChangeNode{}, graph.ErrNotFound
	}
	return s.node, nil
}
func (s *changeStoreSpy) PutChangeLink(_ context.Context, scope graph.RevisionScope, _ graph.ChangeLink) error {
	s.calls++
	s.scope = scope
	return s.err
}
func (s *changeStoreSpy) ChangeNeighbors(_ context.Context, scope graph.RevisionScope, _ graph.ChangeQuery) (graph.ChangePage, error) {
	s.calls++
	s.scope = scope
	return graph.ChangePage{Links: []graph.ChangeLink{}}, s.err
}

func TestChangeGraphServiceRechecksAllScopeCapabilitiesAndPermission(t *testing.T) {
	for _, view := range []application.GraphView{application.GraphLocal, application.GraphOrganization, application.GraphCrossRepository} {
		t.Run(string(view), func(t *testing.T) {
			now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
			var calls []string
			limits := &graphLimitStore{t: t, limit: domain.EntitlementLimit{Key: domain.LimitRetentionDays, State: domain.Unlimited}, calls: &calls}
			grants := map[feature.Key]bool{feature.KeyGraphLocal: true, feature.KeyGraphOrganization: true, feature.KeyGraphCrossRepo: true}
			allowed := true
			foreign := false
			actor := authorization.Principal{ID: "worker", Kind: authorization.ServiceAccount}
			permission := authorization.KeyProofExecute
			access, err := application.NewGraphAccess(tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				if p != actor {
					t.Fatal("actor changed")
				}
				if foreign {
					s.OrganizationID = "foreign"
				}
				return s, nil
			}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
				if org != "org" {
					t.Fatal("tenant changed")
				}
				return grants[k], nil
			}), permissionFunc(func(_ context.Context, p authorization.Principal, org string, k authorization.Key) (bool, error) {
				if p != actor || org != "org" || k != permission {
					t.Fatal("permission boundary", k)
				}
				return allowed, nil
			}), limits, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			store := &changeStoreSpy{}
			service, err := application.NewChangeGraphService(access, store, view)
			if err != nil {
				t.Fatal(err)
			}
			pins := []graph.RevisionPin{{RepositoryID: "repo_a", Revision: "rev1"}}
			if view == application.GraphCrossRepository {
				pins = append(pins, graph.RevisionPin{RepositoryID: "repo_b", Revision: "rev2"})
			}
			node, err := graph.NewChangeNode(graph.ChangeNodeFields{Schema: graph.ChangeNodeSchema, TenantID: "org", Ref: graph.EntityRef{RepositoryID: "repo_a", Revision: "rev1", EntityID: "main"}, Kind: graph.SymbolEntity, Name: "main", InputHash: evidence.Digest([]byte("source")), CreatedAt: now.AddDate(0, 0, -30)})
			if err != nil {
				t.Fatal(err)
			}
			f, _ := node.Fields()
			link, _ := graph.NewChangeLink(graph.ChangeLinkFields{Schema: graph.ChangeLinkSchema, TenantID: "org", LinkID: "link", From: f.Ref, To: graph.EntityRef{RepositoryID: "repo_a", Revision: "rev1", EntityID: "target"}, Relation: graph.Calls, Basis: graph.BasisUnknown, EvidenceID: "ev", CreatedAt: now})
			operations := []struct {
				name  string
				write bool
				call  func() error
			}{
				{"put_node", true, func() error { return service.PutNode(t.Context(), actor, "org", pins, node) }},
				{"put_link", true, func() error { return service.PutLink(t.Context(), actor, "org", pins, link) }},
				{"get_node", false, func() error { _, err := service.GetNode(t.Context(), actor, "org", pins, f.Ref); return err }},
				{"neighbors", false, func() error {
					_, err := service.Neighbors(t.Context(), actor, "org", pins, graph.ChangeQuery{Root: f.Ref, Direction: graph.Both, Limit: 10})
					return err
				}},
			}
			required := []feature.Key{feature.KeyGraphLocal}
			if view != application.GraphLocal {
				required = append(required, feature.KeyGraphOrganization)
			}
			if view == application.GraphCrossRepository {
				required = append(required, feature.KeyGraphCrossRepo)
			}
			for _, op := range operations {
				t.Run(op.name, func(t *testing.T) {
					permission = authorization.KeyProofView
					if op.write {
						permission = authorization.KeyProofExecute
					}
					if err := op.call(); err != nil {
						t.Fatal("entitled operation denied", err)
					}
					before := store.calls
					for _, key := range required {
						grants[key] = false
						if err := op.call(); !errors.Is(err, entitlement.ErrNotEntitled) {
							t.Fatal("feature bypass", key, err)
						}
						grants[key] = true
					}
					allowed = false
					if err := op.call(); !errors.Is(err, application.ErrPermissionDenied) {
						t.Fatal("permission bypass", err)
					}
					allowed = true
					foreign = true
					if err := op.call(); err == nil {
						t.Fatal("tenant bypass")
					}
					foreign = false
					if store.calls != before {
						t.Fatal("denied operation touched graph")
					}
					if err := op.call(); err != nil {
						t.Fatal("grant restoration failed", err)
					}
				})
			}
			hash := store.node.ContentHash()
			permission = authorization.KeyProofView
			limits.limit = domain.EntitlementLimit{Key: domain.LimitRetentionDays, State: domain.Integer, Value: 7}
			if _, err := service.GetNode(t.Context(), actor, "org", pins, f.Ref); !errors.Is(err, graph.ErrNotFound) {
				t.Fatal("retention did not constrain query", err)
			}
			limits.limit = domain.EntitlementLimit{Key: domain.LimitRetentionDays, State: domain.Unlimited}
			recovered, err := service.GetNode(t.Context(), actor, "org", pins, f.Ref)
			if err != nil || recovered.ContentHash() != hash {
				t.Fatal("plan change corrupted history", err)
			}
			pins[0].Revision = "mutated"
			if store.scope.Revisions[0].Revision != "rev1" {
				t.Fatal("scope aliases caller")
			}
		})
	}
}

func TestChangeGraphServiceInvalidInputCancellationAndBackendErrors(t *testing.T) {
	if _, err := application.NewChangeGraphService(nil, nil, application.GraphLocal); err == nil {
		t.Fatal("nil dependencies")
	}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	var calls []string
	limits := &graphLimitStore{t: t, limit: domain.EntitlementLimit{Key: domain.LimitRetentionDays, State: domain.Unlimited}, calls: &calls}
	access, _ := application.NewGraphAccess(tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), entitlementFunc(func(context.Context, string, feature.Key) (bool, error) { return true, nil }), permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
		return true, nil
	}), limits, func() time.Time { return now })
	store := &changeStoreSpy{err: errors.New("credential://private")}
	service, _ := application.NewChangeGraphService(access, store, application.GraphLocal)
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	pins := []graph.RevisionPin{{RepositoryID: "repo", Revision: "rev"}}
	ref := graph.EntityRef{RepositoryID: "repo", Revision: "rev", EntityID: "main"}
	if _, err := service.GetNode(t.Context(), actor, "org", pins, ref); err == nil || strings.Contains(err.Error(), "credential") {
		t.Fatal("backend error leaked", err)
	}
	before := store.calls
	bad := ref
	bad.Revision = "other"
	if _, err := service.GetNode(t.Context(), actor, "org", pins, bad); !errors.Is(err, graph.ErrNotFound) {
		t.Fatal(err)
	}
	if err := service.PutNode(t.Context(), actor, "org", pins, graph.ChangeNode{}); err == nil {
		t.Fatal("zero node")
	}
	if err := service.PutLink(t.Context(), actor, "org", pins, graph.ChangeLink{}); err == nil {
		t.Fatal("zero link")
	}
	if _, err := service.Neighbors(t.Context(), actor, "org", pins, graph.ChangeQuery{Root: ref, Direction: graph.Both, Limit: 10000}); err == nil {
		t.Fatal("unbounded query")
	}
	if _, err := service.GetNode(t.Context(), actor, "org", nil, ref); err == nil {
		t.Fatal("no pins")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.GetNode(ctx, actor, "org", pins, ref); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if store.calls != before {
		t.Fatal("invalid input reached storage")
	}
}
