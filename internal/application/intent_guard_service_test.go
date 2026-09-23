package application_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/intent/guard"
	"safepatch/internal/tenant"
)

func guardInput(org string) guard.Input {
	c, _ := intent.NewIntentContract(intent.IntentContractFields{Goal: "Restrict changes to payments", AllowedComponents: []string{"payments"}})
	raw := []byte(":100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + " M\x00payments/receipt.go\x00")
	return guard.Input{Identity: guard.Identity{TenantID: org, RepositoryID: "repo", ChangeID: "change", RunID: "run", CreatedAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)}, Contract: c, GitDiff: guard.GitDiff{Raw: raw, Complete: true}, SemanticChanges: guard.SemanticChanges{TenantID: org, RepositoryID: "repo", ChangeID: "change", RunID: "run", DiffHash: evidence.Digest(raw), Complete: true, Deterministic: true, Changes: []guard.SemanticChange{{Path: "payments/receipt.go", Component: "payments"}}}}
}

func TestIntentGuardSharedAcrossEditionsAndGrantChanges(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	in := guardInput("org")
	var canonicalIDs []string
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			granted, allowed := true, true
			var calls []string
			s, err := application.NewIntentGuardService(fc,
				tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					calls = append(calls, "tenant")
					if p != actor || s != scope {
						return tenant.Scope{}, tenant.ErrInvalidScope
					}
					return s, nil
				}),
				entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
					calls = append(calls, "entitlement")
					if org != "org" || k != feature.KeyIntentBasic {
						t.Fatal("wrong capability", k)
					}
					return granted, nil
				}),
				permissionFunc(func(_ context.Context, p authorization.Principal, org string, k authorization.Key) (bool, error) {
					calls = append(calls, "permission")
					if p != actor || org != "org" || k != authorization.KeyProofExecute {
						t.Fatal("wrong permission")
					}
					return allowed, nil
				}),
				quotaFunc(func(_ context.Context, e application.Execution) error {
					calls = append(calls, "quota")
					if e.Requirement.Feature != feature.KeyIntentBasic || e.Scope != scope {
						t.Fatal("wrong quota")
					}
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Evaluate(t.Context(), actor, scope, in)
			if err != nil || r.IntentCompliance.Status != evidence.StatusPass || !slices.Equal(calls, []string{"tenant", "entitlement", "permission", "quota"}) {
				t.Fatal(r, err, calls)
			}
			if canonicalIDs == nil {
				canonicalIDs = append([]string{}, r.EvidenceIDs...)
			} else if !slices.Equal(canonicalIDs, r.EvidenceIDs) {
				t.Fatal("edition changed evidence")
			}
			granted = false
			calls = nil
			denied, err := s.Evaluate(t.Context(), actor, scope, in)
			if !errors.Is(err, entitlement.ErrNotEntitled) || len(denied.EvidenceIDs) != 0 || !slices.Equal(calls, []string{"tenant", "entitlement"}) {
				t.Fatal(denied, err, calls)
			}
			granted, allowed = true, false
			calls = nil
			denied, err = s.Evaluate(t.Context(), actor, scope, in)
			if !errors.Is(err, application.ErrPermissionDenied) || len(denied.Evidence) != 0 || slices.Contains(calls, "quota") {
				t.Fatal(denied, err, calls)
			}
			allowed = true
			restored, err := s.Evaluate(t.Context(), actor, scope, in)
			if err != nil || !slices.Equal(restored.EvidenceIDs, r.EvidenceIDs) {
				t.Fatal("revocation corrupted data", err)
			}
		})
	}
}

func TestCommunityIntentGuardUsesOfflineProvider(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	provider, _ := entitlement.NewCommunityEntitlementProvider()
	actor := authorization.Principal{ID: "local-user", Kind: authorization.User}
	s, err := application.NewIntentGuardService(fc, tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), provider, permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
		return true, nil
	}), quotaFunc(func(context.Context, application.Execution) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	in := guardInput(entitlement.LocalOrganizationID)
	r, err := s.Evaluate(t.Context(), actor, tenant.Scope{OrganizationID: entitlement.LocalOrganizationID, ResourceID: "repo"}, in)
	if err != nil || r.IntentCompliance.Status != evidence.StatusPass {
		t.Fatal(r, err)
	}
	for _, key := range []feature.Key{feature.KeyIntentAdvanced, feature.KeyIntentSpecforge} {
		if yes, err := provider.Has(t.Context(), entitlement.LocalOrganizationID, key); err != nil || yes {
			t.Fatal("basic evaluation granted premium work", key, err)
		}
	}
}

func TestIntentGuardAdmissionFailuresDoNotEmitEvidence(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	secret := errors.New("private database credential")
	for _, scenario := range []string{"tenant", "tenant_mismatch", "entitlement", "permission", "quota", "foreign_identity", "foreign_repo", "cancel", "nil_context", "invalid_input"} {
		t.Run(scenario, func(t *testing.T) {
			s, err := application.NewIntentGuardService(fc,
				tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					if scenario == "tenant" {
						return s, secret
					}
					if scenario == "tenant_mismatch" {
						s.OrganizationID = "other"
					}
					return s, nil
				}),
				entitlementFunc(func(context.Context, string, feature.Key) (bool, error) {
					if scenario == "entitlement" {
						return true, secret
					}
					return true, nil
				}),
				permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
					if scenario == "permission" {
						return true, secret
					}
					return true, nil
				}),
				quotaFunc(func(context.Context, application.Execution) error {
					if scenario == "quota" {
						return secret
					}
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			in := guardInput("org")
			if scenario == "foreign_identity" {
				in.Identity.TenantID = "other"
			}
			if scenario == "foreign_repo" {
				in.Identity.RepositoryID = "other"
			}
			if scenario == "invalid_input" {
				in.Contract = intent.IntentContract{}
			}
			var ctx context.Context = t.Context()
			if scenario == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if scenario == "nil_context" {
				ctx = nil
			}
			r, err := s.Evaluate(ctx, actor, scope, in)
			if err == nil || r.IntentCompliance.Status != evidence.StatusError || len(r.EvidenceIDs) > 0 || strings.Contains(err.Error(), secret.Error()) {
				t.Fatal(r, err)
			}
		})
	}
	if _, err := application.NewIntentGuardService(nil, nil, nil, nil, nil); err == nil {
		t.Fatal("nil catalog")
	}
	if _, err := application.NewIntentGuardService(fc, nil, nil, nil, nil); err == nil {
		t.Fatal("nil providers")
	}
	for _, s := range []*application.IntentGuardService{nil, {}} {
		if _, err := s.Evaluate(t.Context(), actor, scope, guardInput("org")); !errors.Is(err, application.ErrInvalidGuard) {
			t.Fatal(err)
		}
	}
}

func TestIntentGuardWorkerRechecksBeforeEvaluation(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	r, _ := application.IntentRequirement(fc, feature.KeyIntentBasic)
	actor := authorization.Principal{ID: "worker", Kind: authorization.ServiceAccount}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	granted, allowed := true, true
	runs := 0
	w, err := application.NewWorkerOperation(workloadRepo(func(_ context.Context, org, id string) (application.Workload, error) {
		if org != "org" || id != "job" {
			return application.Workload{}, application.ErrWorkloadNotFound
		}
		return application.Workload{ID: id, Kind: "intentguard", Actor: actor, Target: scope}, nil
	}),
		tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
			return s, nil
		}), entitlementFunc(func(_ context.Context, _ string, key feature.Key) (bool, error) {
			if key != feature.KeyIntentBasic {
				t.Fatal(key)
			}
			return granted, nil
		}), permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
			return allowed, nil
		}), quotaFunc(func(context.Context, application.Execution) error { return nil }), "intentguard", r,
		func(ctx context.Context, e application.Execution) error {
			result, err := (guard.IntentGuard{}).Evaluate(ctx, guardInput(e.Scope.OrganizationID))
			if err == nil && result.IntentCompliance.Status != evidence.StatusPass {
				t.Fatal(result)
			}
			runs++
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	granted = false
	if err := w.Start(t.Context(), "org", "job"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 0 {
		t.Fatal(err)
	}
	granted = true
	allowed = false
	if err := w.Start(t.Context(), "org", "job"); !errors.Is(err, application.ErrPermissionDenied) || runs != 0 {
		t.Fatal(err)
	}
	allowed = true
	if err := w.Start(t.Context(), "org", "job"); err != nil || runs != 1 {
		t.Fatal(err)
	}
	granted = false
	if err := w.Start(t.Context(), "org", "job"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 1 {
		t.Fatal(err)
	}
}
