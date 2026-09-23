package globalpatchstore_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
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
	"safepatch/internal/graph"
	"safepatch/internal/platform/evidencestore"
	"safepatch/internal/platform/globalpatchstore"
	"safepatch/internal/tenant"
)

type grantRepo struct {
	entitlement.EntitlementRepository
	record grants.Entitlement
}

func (r *grantRepo) List(ctx context.Context, org string, q entitlement.ListQuery) (entitlement.Page, error) {
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
	viewer, revoked, quota bool
	forbidden              string
	checks                 int
}

func (i *identity) Resolve(_ context.Context, actor authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	if actor.ID != "owner" || s.OrganizationID != "org" || !slices.Contains([]string{"a", "b", "c"}, s.ResourceID) || s.ResourceID == i.forbidden || i.revoked {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return s, nil
}
func (i *identity) GetMembership(_ context.Context, org string, actor authorization.Principal) (authorization.Membership, error) {
	role := authorization.RoleOwner
	if i.viewer {
		role = authorization.RoleViewer
	}
	return authorization.Membership{OrganizationID: org, Principal: actor, Role: role, Active: !i.revoked}, nil
}
func (i *identity) Check(context.Context, application.Execution) error {
	i.checks++
	if i.quota {
		return errors.New("quota")
	}
	return nil
}

type archive struct {
	inner globalpatchstore.Archive
	calls int
	mode  string
	hook  func()
}

func (a *archive) Save(ctx context.Context, p graph.GlobalPatch) (application.GlobalPatchLocator, error) {
	a.calls++
	if a.mode == "error" {
		return application.GlobalPatchLocator{}, errors.New("private storage secret")
	}
	l, err := a.inner.Save(ctx, p)
	if a.mode == "poison" {
		l.GlobalChangeID = "globalchange_" + graph.GlobalChangeID(strings.Repeat("0", 64))
	}
	if a.hook != nil {
		a.hook()
	}
	return l, err
}
func (a *archive) Read(ctx context.Context, l application.GlobalPatchLocator) (graph.GlobalPatch, error) {
	a.calls++
	if a.mode == "error" {
		return graph.GlobalPatch{}, errors.New("private storage secret")
	}
	p, err := a.inner.Read(ctx, l)
	if a.mode == "poison" {
		f, _ := p.Fields()
		f.IntentDigest = strings.Repeat("d", 64)
		p, _ = graph.NewGlobalPatch(f)
	}
	if a.hook != nil {
		a.hook()
	}
	return p, err
}
func patch(t *testing.T) graph.GlobalPatch {
	t.Helper()
	a := domain.Change{OrganizationID: "org", RepositoryID: "a", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	b := a
	b.RepositoryID = "b"
	p, err := graph.NewGlobalPatch(graph.GlobalPatchFields{Schema: graph.GlobalPatchSchema, OrganizationID: "org", IntentDigest: strings.Repeat("c", 64), Changes: []domain.Change{a, b}, Dependencies: []graph.PatchDependency{}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func setup(t *testing.T, enabled bool) (*application.GlobalPatchService, *archive, *identity, *grantRepo, func() *application.GlobalPatchService) {
	t.Helper()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	fc, _ := feature.NewBuiltinCatalog("1.0.0", now)
	record, err := grants.NewEntitlement(grants.Entitlement{EntitlementID: "grant", OrganizationID: "org", FeatureID: "feat_0043", Source: grants.SourceSubscription, SourceReference: "sub", Enabled: enabled, StartsAt: now, CreatedAt: now}, fc)
	if err != nil {
		t.Fatal(err)
	}
	repo := &grantRepo{record: record}
	id := &identity{}
	p := patch(t)
	store, err := evidencestore.NewLocal(t.TempDir(), evidence.ArchiveScope{OrganizationID: "org", Subject: evidence.Subject{RepositoryID: "a", Revision: string(p.ID())}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	a := &archive{inner: globalpatchstore.Archive{Store: store}}
	build := func() *application.GlobalPatchService {
		resolver, _ := entitlement.NewEntitlementResolver(repo, fc, func() time.Time { return now })
		permissions, _ := authorization.NewPermissionService(id)
		s, err := application.NewGlobalPatchService(id, resolver, permissions, id, a)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	return build(), a, id, repo, build
}

var actor = authorization.Principal{ID: "owner", Kind: authorization.User}

func TestGlobalPatchAllEditionsAndDowngrade(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	matrix, err := config.GlobalPatchEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range matrix.Editions {
		t.Run(string(entry.Bundle.Version.EditionKey), func(t *testing.T) {
			enabled := false
			for _, f := range entry.Bundle.Features {
				if f.FeatureID == "feat_0043" {
					enabled = f.Enabled
				}
			}
			if enabled != (entry.Bundle.Version.EditionKey == edition.Enterprise) {
				t.Fatal("wrong initial bundle")
			}
			s, a, id, repo, build := setup(t, enabled)
			p := patch(t)
			raw, _ := p.MarshalJSON()
			l, err := s.Save(t.Context(), actor, p)
			if !enabled {
				if err == nil || a.calls != 0 {
					t.Fatal("unentitled archive access", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if id.checks != 1 {
				t.Fatal("quota charged per repository")
			}
			read, err := s.Read(t.Context(), actor, l)
			if err != nil || read.ID() != p.ID() {
				t.Fatal("round trip", err)
			}
			again, err := s.Save(t.Context(), actor, p)
			if err != nil || again.GlobalChangeID != l.GlobalChangeID || again.Reference != l.Reference {
				t.Fatal("non-idempotent save", err)
			}
			id.viewer = true
			if _, err = s.Save(t.Context(), actor, p); !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal("entitlement bypassed permissions", err)
			}
			id.viewer = false
			repo.record.Enabled = false
			denied := build()
			calls := a.calls
			if _, err = denied.Read(t.Context(), actor, l); err == nil || a.calls != calls {
				t.Fatal("downgrade read bypass")
			}
			repo.record.Enabled = true
			restored := build()
			read, err = restored.Read(t.Context(), actor, l)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := read.MarshalJSON()
			if !bytes.Equal(raw, after) {
				t.Fatal("plan change corrupted patch")
			}
		})
	}
	// Commercial identity is not evaluated by the service: the same capability
	// grant admits any organization with independently authorized repositories.
	s, _, _, _, _ := setup(t, true)
	if _, err := s.Save(t.Context(), actor, patch(t)); err != nil {
		t.Fatal(err)
	}
}
func TestGlobalPatchServiceAdversarialBoundaries(t *testing.T) {
	for _, mode := range []string{"foreign_repo", "quota", "revocation", "error", "poison", "canceled", "nil_context", "unauthenticated"} {
		t.Run("save_"+mode, func(t *testing.T) {
			s, a, id, _, _ := setup(t, true)
			ctx := t.Context()
			who := actor
			switch mode {
			case "foreign_repo":
				id.forbidden = "b"
			case "quota":
				id.quota = true
			case "revocation":
				a.hook = func() { id.revoked = true }
			case "error", "poison":
				a.mode = mode
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "nil_context":
				ctx = nil
			case "unauthenticated":
				who = authorization.Principal{}
			}
			l, err := s.Save(ctx, who, patch(t))
			if err == nil || l.GlobalChangeID != "" || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe save", err)
			}
			if (mode == "foreign_repo" || mode == "quota" || mode == "canceled" || mode == "nil_context" || mode == "unauthenticated") && a.calls != 0 {
				t.Fatal("archive touched before admission")
			}
		})
	}
	for _, mode := range []string{"foreign_org", "subset", "substitute", "duplicate", "foreign_repo", "wrong_id", "bad_hash", "oversize", "revocation", "error", "poison"} {
		t.Run("read_"+mode, func(t *testing.T) {
			s, a, id, _, _ := setup(t, true)
			l, err := s.Save(t.Context(), actor, patch(t))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "foreign_org":
				l.OrganizationID = "other"
			case "subset":
				l.RepositoryIDs = l.RepositoryIDs[:1]
			case "substitute":
				l.RepositoryIDs[1] = "c"
			case "duplicate":
				l.RepositoryIDs[1] = "a"
			case "foreign_repo":
				id.forbidden = "b"
			case "wrong_id":
				l.GlobalChangeID = "globalchange_" + graph.GlobalChangeID(strings.Repeat("0", 64))
			case "bad_hash":
				l.Reference.SHA256 = strings.Repeat("0", 64)
			case "oversize":
				l.Reference.Bytes = graph.MaxGlobalPatchBytes + 1
			case "revocation":
				a.hook = func() { id.revoked = true }
			case "error", "poison":
				a.mode = mode
			}
			p, err := s.Read(t.Context(), actor, l)
			if err == nil || p.ID() != "" || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe read", err)
			}
		})
	}
}

type corruptedStore struct {
	evidence.OriginalStore
	raw []byte
}

func (s corruptedStore) Get(context.Context, evidence.ArchiveScope, evidence.OriginalReference) ([]byte, error) {
	return s.raw, nil
}
func TestGlobalPatchArchiveFailsClosed(t *testing.T) {
	p := patch(t)
	s, a, _, _, _ := setup(t, true)
	l, err := s.Save(t.Context(), actor, p)
	if err != nil {
		t.Fatal(err)
	}
	a.inner.Store = corruptedStore{raw: []byte(`{"status":"PASS"}`)}
	if got, err := a.inner.Read(t.Context(), l); err == nil || got.ID() != "" {
		t.Fatal("corrupted store")
	}
	if _, err := (globalpatchstore.Archive{}).Save(t.Context(), p); err == nil {
		t.Fatal("nil store")
	}
	if _, err := a.inner.Read(nil, l); err == nil {
		t.Fatal("nil context")
	}
}
