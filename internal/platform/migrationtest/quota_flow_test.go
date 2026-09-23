package migrationtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/quotastore"
	"safepatch/internal/tenant"
)

type quotaAdmission struct {
	service *entitlement.QuotaService
	id      string
}

func (a quotaAdmission) Check(ctx context.Context, e application.Execution) error {
	_, err := a.service.Reserve(ctx, e.Scope.OrganizationID, domain.LimitProofCreditsMonthly, 1, a.id)
	return err
}

// Real seeded entitlement resolver, permission service, guard and quota service
// share the same organization. Authentication and membership remain fixtures.
func TestQuotaAdmissionRequiresOwnershipAndAuthorizationAcrossAllEditions(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fc, err := feature.NewBuiltinCatalog("1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range initialBundles(t) {
		t.Run(string(bundle.Version.EditionKey), func(t *testing.T) {
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
			ownership, err := entitlement.NewSnapshotService(source, fc, func() time.Time { return now }, time.Minute, 2)
			if err != nil {
				t.Fatal(err)
			}
			permissions, err := authorization.NewPermissionService(&fixtureMemberships{})
			if err != nil {
				t.Fatal(err)
			}
			store, _ := quotastore.NewMemory(1, 10)
			if err := store.SetLimit(t.Context(), "org_customer", domain.EntitlementLimit{Key: domain.LimitProofCreditsMonthly, State: domain.Integer, Value: 1}); err != nil {
				t.Fatal(err)
			}
			quota, _ := entitlement.NewQuotaService(store, func() time.Time { return now })
			runs := 0
			execute := func(actor, id string) error {
				guard, err := application.NewGuardedOperation(fixtureIdentity{}, fixtureIdentity{}, ownership, permissions, quotaAdmission{service: quota, id: id}, application.Requirement{Feature: feature.KeyProofPlanner, Permission: authorization.KeyProofExecute}, func(ctx context.Context, e application.Execution) error {
					runs++
					_, err := quota.Commit(ctx, e.Scope.OrganizationID, id)
					return err
				})
				if err != nil {
					return err
				}
				return guard.Execute(t.Context(), application.Request{Credential: actor, Target: tenant.Scope{OrganizationID: "org_customer", ResourceID: "proof_1"}})
			}
			want := application.ErrPermissionDenied
			if !entitled {
				want = application.ErrEntitlementDenied
			}
			if err := execute("viewer", "viewer_request"); !errors.Is(err, want) || runs != 0 {
				t.Fatal("viewer admitted", err)
			}
			if err := quota.Check(t.Context(), "org_customer", domain.LimitProofCreditsMonthly, 1); err != nil {
				t.Fatal("denial consumed quota", err)
			}
			err = execute("owner", "owner_request")
			if !entitled {
				if !errors.Is(err, application.ErrEntitlementDenied) || runs != 0 {
					t.Fatal(err)
				}
				return
			}
			if err != nil || runs != 1 {
				t.Fatal(err)
			}
			if err := execute("owner", "over_quota"); !errors.Is(err, entitlement.ErrQuotaExceeded) || runs != 1 {
				t.Fatal(err)
			}
			// Revocation changes admission without resetting consumed usage.
			source.records = nil
			source.state.SourceVersions.Subscription++
			if err := execute("owner", "revoked"); !errors.Is(err, application.ErrEntitlementDenied) || runs != 1 {
				t.Fatal(err)
			}
			if err := quota.Check(t.Context(), "org_customer", domain.LimitProofCreditsMonthly, 1); !errors.Is(err, entitlement.ErrQuotaExceeded) {
				t.Fatal("revocation reset usage", err)
			}
		})
	}
}
