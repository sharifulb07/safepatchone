package migrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

func TestCompiledCommunityBundleMatchesMigrationAndPreservesHistory(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root(t), "migrations", "000004_community_local_bundle.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "$bundle$")
	if len(parts) != 3 {
		t.Fatal("expected one canonical bundle")
	}
	var seeded edition.Bundle
	decoder := json.NewDecoder(strings.NewReader(parts[1]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&seeded); err != nil {
		t.Fatal(err)
	}
	prior, fc := initialCatalog(t)
	next, err := prior.Publish(seeded.Version, seeded.Features)
	if err != nil {
		t.Fatal(err)
	}
	want, err := next.GetVersion(edition.CommunityBundleVersion)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := entitlement.NewCommunityEntitlementProvider()
	if err != nil {
		t.Fatal(err)
	}
	got, err := provider.Bundle(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Fatal("compiled/SQL bundle drift", string(a), string(b))
	}
	for _, initial := range initialBundles(t) {
		before, _ := prior.GetVersion(initial.Version.EditionVersionID)
		after, _ := next.GetVersion(initial.Version.EditionVersionID)
		x, _ := json.Marshal(before)
		y, _ := json.Marshal(after)
		if string(x) != string(y) {
			t.Fatal("historical edition changed", initial.Version)
		}
		if initial.Version.EditionKey == edition.Community {
			for _, mapping := range initial.Features {
				definition, _ := fc.GetByID(mapping.FeatureID)
				if yes, err := provider.Has(t.Context(), entitlement.LocalOrganizationID, definition.Key); err != nil || !yes {
					t.Fatal("old Community grant lost", definition.Key, err)
				}
			}
		}
	}
	// A future commercial revision cannot mutate a running local provider.
	future := seeded.Version
	future.EditionVersionID = "community-2027-v3"
	if _, err := next.Publish(future, nil); err != nil {
		t.Fatal(err)
	}
	again, _ := provider.Bundle(t.Context())
	c, _ := json.Marshal(again)
	if string(c) != string(b) {
		t.Fatal("new policy mutated prior provider")
	}
}

func TestPostgresCommunityBundleMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql"}, "community_bundle.sql", "23505", "community_bundle_before.sql", "")
}

// Identity/membership fixtures model trusted local adapter decisions without
// requiring an online account. The actual independent PermissionService and
// GuardedOperation must still deny unauthorized actors after ownership passes.
type localIdentity struct{}

func (localIdentity) Authenticate(_ context.Context, credential string) (authorization.Principal, error) {
	if credential != "owner" && credential != "viewer" {
		return authorization.Principal{}, errors.New("unknown local actor")
	}
	return authorization.Principal{ID: credential, Kind: authorization.User}, nil
}
func (localIdentity) Resolve(_ context.Context, _ authorization.Principal, scope tenant.Scope) (tenant.Scope, error) {
	if scope.OrganizationID != entitlement.LocalOrganizationID || scope.ResourceID != "local_evidence" {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return scope, nil
}
func (localIdentity) GetMembership(_ context.Context, org string, actor authorization.Principal) (authorization.Membership, error) {
	if org != entitlement.LocalOrganizationID {
		return authorization.Membership{}, authorization.ErrMembershipNotFound
	}
	role := authorization.RoleViewer
	if actor.ID == "owner" {
		role = authorization.RoleOwner
	}
	return authorization.Membership{OrganizationID: org, Principal: actor, Role: role, Active: true}, nil
}

func TestCommunityEntitlementDoesNotBypassActorPermission(t *testing.T) {
	provider, err := entitlement.NewCommunityEntitlementProvider()
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := authorization.NewPermissionService(localIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, actor string
		key         feature.Key
		permission  authorization.Key
		denied      error
	}{
		{"entitled owner", "owner", feature.KeyEvidenceLocal, authorization.KeyProofExecute, nil},
		{"entitled viewer denied execution", "viewer", feature.KeyEvidenceLocal, authorization.KeyProofExecute, application.ErrPermissionDenied},
		{"entitled viewer may view", "viewer", feature.KeyEvidenceLocal, authorization.KeyProofView, nil},
		{"owner cannot enable cloud history", "owner", feature.KeyProofCloudHistory, authorization.KeyProofView, application.ErrEntitlementDenied},
		{"owner cannot enable replay", "owner", feature.KeyRuntimeReplay, authorization.KeyReplayExecute, application.ErrEntitlementDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota := &fixtureQuota{}
			runs := 0
			guard, err := application.NewGuardedOperation(localIdentity{}, localIdentity{}, provider, permissions, quota,
				application.Requirement{Feature: tc.key, Permission: tc.permission}, func(context.Context, application.Execution) error { runs++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			err = guard.Execute(t.Context(), application.Request{Credential: tc.actor, Target: tenant.Scope{OrganizationID: entitlement.LocalOrganizationID, ResourceID: "local_evidence"}})
			if tc.denied != nil {
				if !errors.Is(err, tc.denied) || runs != 0 || quota.calls != 0 {
					t.Fatal("gate bypass", err, runs)
				}
			} else if err != nil || runs != 1 || quota.calls != 1 {
				t.Fatal("valid local actor blocked", err)
			}
		})
	}
}
