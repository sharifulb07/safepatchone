package migrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

type fixtureIdentity struct{}

func (fixtureIdentity) Authenticate(_ context.Context, token string) (authorization.Principal, error) {
	if token != "owner" && token != "viewer" {
		return authorization.Principal{}, errors.New("invalid fixture credential")
	}
	return authorization.Principal{ID: token, Kind: authorization.User}, nil
}
func (fixtureIdentity) Resolve(_ context.Context, p authorization.Principal, target tenant.Scope) (tenant.Scope, error) {
	if target.OrganizationID != "org_customer" || target.ResourceID != "proof_1" {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return target, nil
}

type fixtureMemberships struct{ reads int }

func (m *fixtureMemberships) GetMembership(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
	m.reads++
	if org != "org_customer" {
		return authorization.Membership{}, authorization.ErrMembershipNotFound
	}
	role := authorization.RoleViewer
	if p.ID == "owner" {
		role = authorization.RoleOwner
	} else if p.ID != "viewer" {
		return authorization.Membership{}, authorization.ErrMembershipNotFound
	}
	return authorization.Membership{OrganizationID: org, Principal: p, Role: role, Active: true}, nil
}

type fixtureQuota struct{ calls int }

func (q *fixtureQuota) Check(context.Context, application.Execution) error { q.calls++; return nil }

// Uses actual seed data, snapshot/resolver, PermissionService and orchestration.
// Identity/storage/quota adapters and operation are controlled test fixtures.
func TestSeededOwnerAndViewerPermissionsWithIdenticalEntitlements(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fc, err := feature.NewBuiltinCatalog("1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	sawTeam := false
	for _, bundle := range initialBundles(t) {
		t.Run(string(bundle.Version.EditionKey), func(t *testing.T) {
			if bundle.Version.EditionKey == edition.Team {
				sawTeam = true
			}
			source := &seededSnapshotSource{state: domain.SnapshotState{OrganizationID: "org_customer", Edition: bundle.Version.EditionKey, EditionVersion: bundle.Version.EditionVersionID, SourceVersions: domain.SourceVersions{Subscription: 1, Trial: 1, License: 1, ManualOverride: 1, EditionConfiguration: 1, CommunityDefault: 1, Promotion: 1}}}
			entitled := false
			for _, mapping := range bundle.Features {
				record, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: "ent_" + mapping.FeatureID, OrganizationID: "org_customer", FeatureID: mapping.FeatureID, Source: domain.SourceSubscription, SourceReference: bundle.Version.EditionVersionID, Enabled: mapping.Enabled, LimitValue: mapping.LimitValue, StartsAt: bundle.Version.CreatedAt, CreatedAt: bundle.Version.CreatedAt}, fc)
				if err != nil {
					t.Fatal(err)
				}
				source.records = append(source.records, record)
				definition, _ := fc.GetByID(mapping.FeatureID)
				if definition.Key == feature.KeyProofPlanner {
					entitled = mapping.Enabled
				}
			}
			if bundle.Version.EditionKey == edition.Team && !entitled {
				t.Fatal("Team fixture must own proof.planner")
			}
			service, err := entitlement.NewSnapshotService(source, fc, func() time.Time { return now }, 30*time.Second, 2)
			if err != nil {
				t.Fatal(err)
			}
			members := &fixtureMemberships{}
			permissions, err := authorization.NewPermissionService(members)
			if err != nil {
				t.Fatal(err)
			}
			before, err := service.Snapshot(context.Background(), "org_customer")
			if err != nil {
				t.Fatal(err)
			}
			for _, permission := range []authorization.Key{authorization.KeyProofExecute, authorization.KeyProofView} {
				for _, actor := range []string{"owner", "viewer"} {
					quota := &fixtureQuota{}
					executions := 0
					guard, err := application.NewGuardedOperation(fixtureIdentity{}, fixtureIdentity{}, service, permissions, quota, application.Requirement{Feature: feature.KeyProofPlanner, Permission: permission}, func(context.Context, application.Execution) error { executions++; return nil })
					if err != nil {
						t.Fatal(err)
					}
					err = guard.Execute(context.Background(), application.Request{Credential: actor, Target: tenant.Scope{OrganizationID: "org_customer", ResourceID: "proof_1"}})
					want := entitled && (actor == "owner" || permission == authorization.KeyProofView)
					if want {
						if err != nil || executions != 1 || quota.calls != 1 {
							t.Fatal("valid actor blocked", actor, permission, err)
						}
					} else {
						denial := application.ErrPermissionDenied
						if !entitled {
							denial = application.ErrEntitlementDenied
						}
						if !errors.Is(err, denial) || executions != 0 || quota.calls != 0 {
							t.Fatal("gate bypassed", actor, permission, err)
						}
					}
				}
			}
			after, err := service.Snapshot(context.Background(), "org_customer")
			if err != nil || after.ContentHash != before.ContentHash {
				t.Fatal("actor changed entitlement bundle", err)
			}
			history, _ := json.Marshal(source.records)
			original := source.records
			source.records = nil
			source.state.SourceVersions.Subscription++
			quota := &fixtureQuota{}
			executed := false
			reads := members.reads
			guard, _ := application.NewGuardedOperation(fixtureIdentity{}, fixtureIdentity{}, service, permissions, quota, application.Requirement{Feature: feature.KeyProofPlanner, Permission: authorization.KeyProofExecute}, func(context.Context, application.Execution) error { executed = true; return nil })
			err = guard.Execute(context.Background(), application.Request{Credential: "owner", Target: tenant.Scope{OrganizationID: "org_customer", ResourceID: "proof_1"}})
			if !errors.Is(err, application.ErrEntitlementDenied) || executed || members.reads != reads {
				t.Fatal("owner bypassed lost entitlement", err)
			}
			unchanged, _ := json.Marshal(original)
			if string(unchanged) != string(history) {
				t.Fatal("plan change corrupted historical grants")
			}
			ok, err := permissions.Allowed(context.Background(), authorization.Principal{ID: "owner", Kind: authorization.User}, "org_customer", authorization.KeyProofExecute)
			if err != nil || !ok {
				t.Fatal("plan change altered independent permissions", err)
			}
		})
	}
	if !sawTeam {
		t.Fatal("Team fixture not exercised")
	}
}
