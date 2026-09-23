package livingstore_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/config"
	"safepatch/internal/domain"
	"safepatch/internal/domain/edition"
	grants "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/platform/evidencestore"
	"safepatch/internal/platform/livingstore"
	"safepatch/internal/proof/living"
	"safepatch/internal/tenant"
)

type repository struct {
	entitlement.EntitlementRepository
	record grants.Entitlement
}

func (r *repository) List(ctx context.Context, org string, q entitlement.ListQuery) (entitlement.Page, error) {
	if err := entitlement.ValidateList(org, q); err != nil {
		return entitlement.Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return entitlement.Page{}, err
	}
	p := entitlement.Page{Entitlements: []grants.Entitlement{}}
	if r.record.OrganizationID == org && r.record.FeatureID == q.FeatureID && q.AfterID == "" {
		p.Entitlements = append(p.Entitlements, r.record.Clone())
	}
	return p, nil
}

type identity struct {
	org                    string
	viewer, quota, foreign bool
}

func (i *identity) Resolve(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	if i.foreign || s.OrganizationID != i.org || s.ResourceID != "repo" {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return s, nil
}
func (i *identity) GetMembership(_ context.Context, org string, actor authorization.Principal) (authorization.Membership, error) {
	role := authorization.RoleOwner
	if i.viewer {
		role = authorization.RoleViewer
	}
	return authorization.Membership{OrganizationID: org, Principal: actor, Role: role, Active: org == i.org && actor.ID == "owner"}, nil
}
func (i *identity) Check(context.Context, application.Execution) error {
	if i.quota {
		return errors.New("quota denied")
	}
	return nil
}

type source struct {
	g     living.ProofDependencyGraph
	s     living.Snapshot
	calls int
	hook  func()
	err   error
}

func (s *source) LoadLivingProof(context.Context, tenant.Scope, string) (living.ProofDependencyGraph, living.Snapshot, error) {
	s.calls++
	if s.hook != nil {
		s.hook()
	}
	return s.g, s.s, s.err
}

func graph(t *testing.T, org string) (living.ProofDependencyGraph, living.Snapshot, time.Time, evidence.ArchiveScope) {
	t.Helper()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	c := domain.Change{OrganizationID: org, RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	d := evidence.Digest([]byte("input"))
	p := evidence.Digest([]byte("completed-proof"))
	g, err := living.NewGraph(living.GraphFields{Schema: living.GraphSchema, ID: "graph", Change: c, CreatedAt: now, Nodes: []living.Node{{ID: "input", Kind: living.Input, Digest: d, Dependencies: []string{}, Status: evidence.StatusUnknown, RecordedAt: now, ValidUntil: now.Add(time.Hour)}, {ID: "proof", Kind: living.Proof, Digest: p, Dependencies: []string{"input"}, Status: evidence.StatusPass, RecordedAt: now, ValidUntil: now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	s := living.Snapshot{GraphDigest: g.Digest(), ObservedAt: now, ValidUntil: now.Add(time.Minute), Nodes: []living.Observation{{ID: "input", Available: true, Digest: d}, {ID: "proof", Available: true, Digest: p}}}
	return g, s, now, evidence.ArchiveScope{OrganizationID: org, Subject: evidence.Subject{RepositoryID: "repo", Revision: c.HeadSHA}}
}

func TestAllEditionsLivingProofCapabilityPermissionAndHistory(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC))
	matrix, err := config.V02Editions(fc)
	if err != nil {
		t.Fatal(err)
	}
	for _, ed := range matrix.Editions {
		t.Run(string(ed.Bundle.Version.EditionKey), func(t *testing.T) {
			org := "org_" + string(ed.Bundle.Version.EditionKey)
			g, s, now, scope := graph(t, org)
			before, _ := g.MarshalJSON()
			enabled := false
			for _, m := range ed.Bundle.Features {
				if m.FeatureID == "feat_0013" {
					enabled = m.Enabled
				}
			}
			// Allocation assertion lives in tests/configuration, never execution code.
			if enabled != (ed.Bundle.Version.EditionKey == edition.Enterprise) {
				t.Fatal("unexpected initial allocation")
			}
			record, err := grants.NewEntitlement(grants.Entitlement{EntitlementID: "living", OrganizationID: org, FeatureID: "feat_0013", Source: grants.SourceSubscription, SourceReference: ed.Bundle.Version.EditionVersionID, Enabled: enabled, StartsAt: now, CreatedAt: now}, fc)
			if err != nil {
				t.Fatal(err)
			}
			repo := &repository{record: record}
			resolver, err := entitlement.NewEntitlementResolver(repo, fc, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			identity := &identity{org: org}
			permissions, _ := authorization.NewPermissionService(identity)
			store, err := evidencestore.NewLocal(t.TempDir(), scope)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			archive := livingstore.Archive{Store: store}
			src := &source{g: g, s: s}
			service, err := application.NewLivingProofService(identity, resolver, permissions, identity, src, archive, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			actor := authorization.Principal{ID: "owner", Kind: authorization.User}
			target := tenant.Scope{OrganizationID: org, ResourceID: "repo"}
			_, _, err = service.Reevaluate(t.Context(), actor, target, "graph")
			if (err == nil) != enabled {
				t.Fatal(err)
			}
			if !enabled && src.calls != 0 {
				t.Fatal("unentitled source read")
			}
			// Provisioning the capability alone enables Business or any later bundle.
			repo.record.Enabled = true
			proof, ref, err := service.Reevaluate(t.Context(), actor, target, "graph")
			if err != nil {
				t.Fatal(err)
			}
			original, _ := proof.MarshalJSON()
			identity.viewer = true
			if _, _, err := service.Reevaluate(t.Context(), actor, target, "graph"); !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal("grant bypassed permissions", err)
			}
			identity.viewer = false
			identity.quota = true
			if _, _, err := service.Reevaluate(t.Context(), actor, target, "graph"); err == nil {
				t.Fatal("quota bypass")
			}
			identity.quota = false
			repo.record.Enabled = false
			if _, _, err := service.Reevaluate(t.Context(), actor, target, "graph"); err == nil {
				t.Fatal("downgrade allowed")
			}
			graphRef := evidence.OriginalReference{SHA256: g.Digest(), Bytes: int64(len(before))}
			_, historical, err := archive.Read(t.Context(), scope, graphRef, ref)
			if err != nil {
				t.Fatal(err)
			}
			history, _ := historical.MarshalJSON()
			if !bytes.Equal(history, original) {
				t.Fatal("downgrade destroyed evidence")
			}
			repo.record.Enabled = true
			_, retry, err := service.Reevaluate(t.Context(), actor, target, "graph")
			if err != nil || retry != ref {
				t.Fatal("immutable retry", err)
			}
			for _, victim := range edition.Editions() {
				foreign := target
				foreign.OrganizationID = "victim_" + string(victim.Key)
				if _, _, err := service.Reevaluate(t.Context(), actor, foreign, "graph"); err == nil {
					t.Fatal("cross tenant service access")
				}
				foreignScope := scope
				foreignScope.OrganizationID = foreign.OrganizationID
				if _, _, err := archive.Read(t.Context(), foreignScope, graphRef, ref); err == nil {
					t.Fatal("cross tenant archive access")
				}
			}
			// A downgrade while the authoritative source is loading must stop save.
			src.hook = func() { repo.record.Enabled = false }
			if _, _, err := service.Reevaluate(t.Context(), actor, target, "graph"); err == nil {
				t.Fatal("revocation during read")
			}
			after, _ := g.MarshalJSON()
			if !bytes.Equal(before, after) {
				t.Fatal("graph changed")
			}
		})
	}
}

type archiveFunc func(context.Context, living.ProofDependencyGraph, living.LivingProof) (evidence.OriginalReference, error)

func (f archiveFunc) Save(c context.Context, g living.ProofDependencyGraph, p living.LivingProof) (evidence.OriginalReference, error) {
	return f(c, g, p)
}
func TestLivingProofSourceAndStorageErrorsFailClosed(t *testing.T) {
	for _, mode := range []string{"source_error", "foreign_source", "wrong_id", "stale_snapshot", "quota", "foreign_tenant", "storage_error", "poisoned_reference", "cancel", "slow_storage", "clock_rollback"} {
		t.Run(mode, func(t *testing.T) {
			g, s, now, _ := graph(t, "org")
			src := &source{g: g, s: s}
			ident := &identity{org: "org"}
			fc, _ := feature.NewBuiltinCatalog("1.0.0", now)
			record, _ := grants.NewEntitlement(grants.Entitlement{EntitlementID: "living", OrganizationID: "org", FeatureID: "feat_0013", Source: grants.SourceSubscription, SourceReference: "contract", Enabled: true, StartsAt: now, CreatedAt: now}, fc)
			resolver, _ := entitlement.NewEntitlementResolver(&repository{record: record}, fc, func() time.Time { return now })
			permissions, _ := authorization.NewPermissionService(ident)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			saved := 0
			switch mode {
			case "source_error":
				src.err = errors.New("private token")
			case "foreign_source":
				src.g, src.s, _, _ = graph(t, "victim")
			case "wrong_id":
				f, _ := g.Fields()
				f.ID = "other"
				src.g, _ = living.NewGraph(f)
				src.s.GraphDigest = src.g.Digest()
			case "stale_snapshot":
				src.s.ValidUntil = now
			case "quota":
				ident.quota = true
			case "foreign_tenant":
				ident.foreign = true
			case "cancel":
				src.hook = cancel
			}
			archive := archiveFunc(func(_ context.Context, _ living.ProofDependencyGraph, p living.LivingProof) (evidence.OriginalReference, error) {
				saved++
				raw, _ := p.MarshalJSON()
				ref := evidence.OriginalReference{SHA256: p.Digest(), Bytes: int64(len(raw))}
				if mode == "storage_error" {
					return ref, errors.New("private token")
				}
				if mode == "poisoned_reference" {
					ref.SHA256 = strings.Repeat("0", 64)
				}
				if mode == "slow_storage" {
					now = now.Add(time.Minute)
				}
				if mode == "clock_rollback" {
					now = now.Add(-time.Second)
				}
				return ref, nil
			})
			service, _ := application.NewLivingProofService(ident, resolver, permissions, ident, src, archive, func() time.Time { return now })
			p, ref, err := service.Reevaluate(ctx, authorization.Principal{ID: "owner", Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, "graph")
			if err == nil || p.Digest() != "" || ref.SHA256 != "" || strings.Contains(err.Error(), "private token") {
				t.Fatal("unsafe success", err)
			}
			if mode != "storage_error" && mode != "poisoned_reference" && mode != "slow_storage" && mode != "clock_rollback" && saved != 0 {
				t.Fatal("stored rejected result")
			}
		})
	}
}
