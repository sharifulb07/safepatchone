package application_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/intent"
	"safepatch/internal/tenant"
)

func TestIntentRequirementUsesOnlyRegisteredIntentCapabilities(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	for _, key := range []feature.Key{feature.KeyIntentBasic, feature.KeyIntentAdvanced, feature.KeyIntentSpecforge} {
		r, err := application.IntentRequirement(fc, key)
		if err != nil || r.Feature != key || r.Permission != authorization.KeyProofExecute {
			t.Fatal(r, err)
		}
	}
	for _, key := range []feature.Key{"", "intent.unknown", feature.KeyRuntimeReplay} {
		if _, err := application.IntentRequirement(fc, key); !errors.Is(err, application.ErrInvalidGuard) {
			t.Fatal(err)
		}
	}
	if _, err := application.IntentRequirement(nil, feature.KeyIntentBasic); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal(err)
	}
	empty, _ := feature.NewFeatureCatalog(nil)
	if _, err := application.IntentRequirement(empty, feature.KeyIntentBasic); !errors.Is(err, feature.ErrFeatureNotFound) {
		t.Fatal(err)
	}
}

func fullIntent(t *testing.T) intent.IntentContract {
	t.Helper()
	c, err := intent.NewIntentContract(intent.IntentContractFields{Goal: "Preserve checkout safety", Requirements: []string{"Keep retries idempotent"}, AcceptanceCriteria: []string{"One receipt per identity"}, RequiredBehaviors: []string{"Return existing receipt"}, ForbiddenBehaviors: []string{"Repeat a charge"}, AllowedComponents: []string{"payments"}, ForbiddenComponents: []string{"auth"}, ProtectedProperties: []string{"Receipt identity"}, SecurityConstraints: []string{"Authorize request"}, DatabaseConstraints: []string{"Preserve rows"}, APIConstraints: []string{"Preserve schema"}, BusinessConstraints: []string{"One charge per identity"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIntentWorkerGatesPreserveSameContractAcrossAllEditions(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	contract := fullIntent(t)
	original, _ := contract.CanonicalJSON()
	hash, _ := contract.ContentHash()
	for _, ed := range edition.Editions() {
		for _, key := range []feature.Key{feature.KeyIntentBasic, feature.KeyIntentAdvanced, feature.KeyIntentSpecforge} {
			t.Run(ed.Name+"/"+string(key), func(t *testing.T) {
				// The edition label is never passed into a model, guard or operation.
				// This operation exercises shared serialization, not SpecForge generation.
				granted, allowed := true, true
				runs := 0
				actor := authorization.Principal{ID: "actor", Kind: authorization.ServiceAccount}
				scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
				requirement, err := application.IntentRequirement(fc, key)
				if err != nil {
					t.Fatal(err)
				}
				worker, err := application.NewWorkerOperation(workloadRepo(func(_ context.Context, org, id string) (application.Workload, error) {
					if org != "org" || id != "job" {
						return application.Workload{}, application.ErrWorkloadNotFound
					}
					return application.Workload{ID: id, Kind: "intent", Actor: actor, Target: scope}, nil
				}), tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					if p != actor || s != scope {
						return tenant.Scope{}, tenant.ErrInvalidScope
					}
					return s, nil
				}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
					if org != "org" || k != key {
						t.Fatal("wrong grant input")
					}
					return granted, nil
				}), permissionFunc(func(_ context.Context, p authorization.Principal, org string, k authorization.Key) (bool, error) {
					if p != actor || org != "org" || k != authorization.KeyProofExecute {
						t.Fatal("wrong permission input")
					}
					return allowed, nil
				}), quotaFunc(func(context.Context, application.Execution) error { return nil }), "intent", requirement, func(_ context.Context, e application.Execution) error {
					if e.Scope != scope {
						t.Fatal("scope changed")
					}
					restored, err := intent.DecodeIntentContract(original)
					if err != nil {
						return err
					}
					raw, _ := restored.CanonicalJSON()
					gotHash, _ := restored.ContentHash()
					if !bytes.Equal(raw, original) || gotHash != hash {
						t.Fatal("edition/capability changed contract")
					}
					runs++
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := worker.Start(t.Context(), "org", "job"); err != nil || runs != 1 {
					t.Fatal(runs, err)
				}
				granted = false
				if err := worker.Start(t.Context(), "org", "job"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 1 {
					t.Fatal("grant revocation bypass", runs, err)
				}
				granted = true
				allowed = false
				if err := worker.Start(t.Context(), "org", "job"); !errors.Is(err, application.ErrPermissionDenied) || runs != 1 {
					t.Fatal("permission bypass", runs, err)
				}
				allowed = true
				if err := worker.Start(t.Context(), "org", "job"); err != nil || runs != 2 {
					t.Fatal("restored grant corrupted data", runs, err)
				}
				if err := worker.Start(t.Context(), "other", "job"); !errors.Is(err, application.ErrWorkloadNotFound) || runs != 2 {
					t.Fatal("tenant bypass", runs, err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := worker.Start(ctx, "org", "job"); !errors.Is(err, context.Canceled) || runs != 2 {
					t.Fatal("cancellation bypass", err)
				}
				after, _ := contract.CanonicalJSON()
				if !bytes.Equal(after, original) {
					t.Fatal("plan change mutated contract")
				}
			})
		}
	}
}

func TestCommunityIntentOwnershipUsesExistingProvider(t *testing.T) {
	provider, err := entitlement.NewCommunityEntitlementProvider()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []feature.Key{feature.KeyIntentBasic, feature.KeyIntentAdvanced, feature.KeyIntentSpecforge} {
		yes, err := provider.Has(t.Context(), entitlement.LocalOrganizationID, key)
		if err != nil || yes != (key == feature.KeyIntentBasic) {
			t.Fatal(key, yes, err)
		}
	}
	// A basic owner uses exactly the same full field schema; field storage itself
	// is not a grant to perform an advanced analysis or generation operation.
	c := fullIntent(t)
	f, err := c.Fields()
	if err != nil || len(f.BusinessConstraints) != 1 || len(f.SecurityConstraints) != 1 || len(f.DatabaseConstraints) != 1 {
		t.Fatal("basic contract lost constraints", err)
	}
}
