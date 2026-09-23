package authorization_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"safepatch/internal/authorization"
)

type membershipReader func(context.Context, string, authorization.Principal) (authorization.Membership, error)

func (f membershipReader) GetMembership(ctx context.Context, org string, actor authorization.Principal) (authorization.Membership, error) {
	return f(ctx, org, actor)
}
func actor() authorization.Principal {
	return authorization.Principal{ID: "user_1", Kind: authorization.User}
}

func TestAllPermissionKeysForOwnerAndViewer(t *testing.T) {
	keys := authorization.Keys()
	if len(keys) != 11 {
		t.Fatal("permission catalog changed")
	}
	views := map[authorization.Key]bool{authorization.KeyProofView: true, authorization.KeyPolicyView: true, authorization.KeyReplayView: true, authorization.KeyAuditView: true}
	for _, role := range []authorization.Role{authorization.RoleOwner, authorization.RoleViewer} {
		s, err := authorization.NewPermissionService(membershipReader(func(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
			return authorization.Membership{OrganizationID: org, Principal: p, Role: role, Active: true}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			allowed, err := s.Allowed(context.Background(), actor(), "org_a", key)
			want := role == authorization.RoleOwner || views[key]
			if err != nil || allowed != want {
				t.Fatalf("%s %s: %v %v", role, key, allowed, err)
			}
		}
		if ok, err := s.Allowed(context.Background(), actor(), "org_a", "proof.*"); ok || !errors.Is(err, authorization.ErrUnknownPermission) {
			t.Fatal("wildcard granted")
		}
	}
}

func TestPermissionFailsClosedAndBindsActorAndTenant(t *testing.T) {
	backend := errors.New("storage unavailable")
	for _, mode := range []string{"missing", "inactive", "other tenant", "other actor", "other kind", "unknown role", "backend", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, _ := authorization.NewPermissionService(membershipReader(func(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
				m := authorization.Membership{OrganizationID: org, Principal: p, Role: authorization.RoleOwner, Active: true}
				switch mode {
				case "missing":
					return m, authorization.ErrMembershipNotFound
				case "inactive":
					m.Active = false
				case "other tenant":
					m.OrganizationID = "org_b"
				case "other actor":
					m.Principal.ID = "user_2"
				case "other kind":
					m.Principal.Kind = authorization.ServiceAccount
				case "unknown role":
					m.Role = "superadmin"
				case "backend":
					return m, backend
				case "cancelled":
					cancel()
				}
				return m, nil
			}))
			ok, err := s.Allowed(ctx, actor(), "org_a", authorization.KeyProofExecute)
			if ok {
				t.Fatal("invalid membership granted")
			}
			if mode != "missing" && mode != "inactive" && err == nil {
				t.Fatal("error hidden")
			}
			if mode == "backend" && !errors.Is(err, backend) {
				t.Fatal("cause lost")
			}
		})
	}
}

func TestPermissionRevocationIsNotCached(t *testing.T) {
	role, active := authorization.RoleOwner, true
	s, _ := authorization.NewPermissionService(membershipReader(func(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
		return authorization.Membership{OrganizationID: org, Principal: p, Role: role, Active: active}, nil
	}))
	if ok, err := s.Allowed(context.Background(), actor(), "org_a", authorization.KeyProofExecute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	role = authorization.RoleViewer
	if ok, err := s.Allowed(context.Background(), actor(), "org_a", authorization.KeyProofExecute); err != nil || ok {
		t.Fatal("role change cached", err)
	}
	active = false
	if ok, err := s.Allowed(context.Background(), actor(), "org_a", authorization.KeyProofView); err != nil || ok {
		t.Fatal("revocation cached", err)
	}
}

func TestPermissionValidationAndConcurrency(t *testing.T) {
	s, _ := authorization.NewPermissionService(membershipReader(func(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
		return authorization.Membership{OrganizationID: org, Principal: p, Role: authorization.RoleOwner, Active: true}, nil
	}))
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() {
			if ok, err := s.Allowed(context.Background(), authorization.Principal{ID: "svc", Kind: authorization.ServiceAccount}, "org_a", authorization.KeyProofExecute); err != nil || !ok {
				t.Error(ok, err)
			}
		})
	}
	wg.Wait()
	for _, p := range []authorization.Principal{{}, {ID: "a", Kind: "UNKNOWN"}, {ID: "../user", Kind: authorization.User}} {
		if ok, err := s.Allowed(context.Background(), p, "org_a", authorization.KeyProofView); ok || err == nil {
			t.Fatal("invalid actor accepted")
		}
	}
	if ok, err := s.Allowed(nil, actor(), "org_a", authorization.KeyProofView); ok || err == nil {
		t.Fatal("nil context accepted")
	}
	if ok, err := s.Allowed(context.Background(), actor(), "../org", authorization.KeyProofView); ok || err == nil {
		t.Fatal("invalid organization accepted")
	}
	var zero authorization.PermissionService
	if ok, err := zero.Allowed(context.Background(), actor(), "org_a", authorization.KeyProofView); ok || err == nil {
		t.Fatal("zero service accepted")
	}
	if _, err := authorization.NewPermissionService(nil); err == nil {
		t.Fatal("nil repository accepted")
	}
}
