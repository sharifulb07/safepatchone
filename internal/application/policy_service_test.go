package application_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"safepatch/internal/policy"
	"safepatch/internal/tenant"
)

var centralDocument = []byte(`{"schema":"safepatch.policy.v1","policy_id":"baseline","tenant_id":"org","version":"1","rules":[{"id":"risk","when":{},"require":{"max_risk_score":20}}]}`)

func TestCentralPolicyAdmissionBoundaries(t *testing.T) {
	catalog, _ := feature.NewBuiltinCatalog("1.0.0", time.Now())
	actor := authorization.Principal{ID: "owner", Kind: authorization.User}
	private := errors.New("database connection credential")
	for _, scenario := range []string{"success", "tenant_error", "tenant_mismatch", "entitlement_error", "not_entitled", "permission_error", "not_permitted", "quota_error", "foreign_document", "malformed", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			var calls []string
			service, err := application.NewPolicyService(catalog,
				tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					calls = append(calls, "tenant")
					if p != actor || s.OrganizationID != "org" || s.ResourceID != "org" {
						t.Fatal("wrong tenant scope")
					}
					if scenario == "tenant_error" {
						return s, private
					}
					if scenario == "tenant_mismatch" {
						s.OrganizationID = "other"
					}
					return s, nil
				}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
					calls = append(calls, "entitlement")
					if org != "org" || k != feature.KeyPolicyCentral {
						t.Fatal("wrong capability")
					}
					if scenario == "entitlement_error" {
						return true, private
					}
					return scenario != "not_entitled", nil
				}), permissionFunc(func(_ context.Context, p authorization.Principal, org string, k authorization.Key) (bool, error) {
					calls = append(calls, "permission")
					if p != actor || org != "org" || k != authorization.KeyPolicyManage {
						t.Fatal("wrong permission")
					}
					if scenario == "permission_error" {
						return true, private
					}
					return scenario != "not_permitted", nil
				}), quotaFunc(func(_ context.Context, e application.Execution) error {
					calls = append(calls, "quota")
					if e.Requirement.Feature != feature.KeyPolicyCentral {
						t.Fatal(e)
					}
					if scenario == "quota_error" {
						return private
					}
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			raw := bytes.Clone(centralDocument)
			if scenario == "foreign_document" {
				raw = []byte(strings.Replace(string(raw), `"tenant_id":"org"`, `"tenant_id":"other"`, 1))
			}
			if scenario == "malformed" {
				raw = []byte(`{"schema":"future"}`)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancel" {
				cancel()
			}
			p, err := service.CompileCentral(ctx, actor, "org", raw)
			if scenario == "success" {
				if err != nil || p.Hash() == "" || !slices.Equal(calls, []string{"tenant", "entitlement", "permission", "quota"}) {
					t.Fatal(p, err, calls)
				}
				return
			}
			if err == nil || p.Hash() != "" || strings.Contains(err.Error(), private.Error()) {
				t.Fatal("failure exposed output or secrets", err)
			}
			if scenario == "not_entitled" && (!errors.Is(err, entitlement.ErrNotEntitled) || slices.Contains(calls, "permission")) {
				t.Fatal(err, calls)
			}
			if scenario == "not_permitted" && (!errors.Is(err, application.ErrPermissionDenied) || slices.Contains(calls, "quota")) {
				t.Fatal(err, calls)
			}
			if scenario == "foreign_document" && !errors.Is(err, tenant.ErrInvalidScope) {
				t.Fatal(err)
			}
			if scenario == "cancel" && len(calls) != 0 {
				t.Fatal(calls)
			}
		})
	}
}

func TestAllEditionsUseIdenticalPolicyAndGrantChangesPreserveIt(t *testing.T) {
	catalog, _ := feature.NewBuiltinCatalog("1.0.0", time.Now())
	actor := authorization.Principal{ID: "owner", Kind: authorization.User}
	var reference []byte
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			// Explicit grants may be configured for any organization. Edition labels
			// never enter either the management service or the policy evaluator.
			granted, permitted := true, true
			service, err := application.NewPolicyService(catalog, tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				return s, nil
			}), entitlementFunc(func(_ context.Context, _ string, _ feature.Key) (bool, error) { return granted, nil }), permissionFunc(func(_ context.Context, _ authorization.Principal, _ string, _ authorization.Key) (bool, error) {
				return permitted, nil
			}), quotaFunc(func(context.Context, application.Execution) error { return nil }))
			if err != nil {
				t.Fatal(err)
			}
			p, err := service.CompileCentral(t.Context(), actor, "org", centralDocument)
			if err != nil {
				t.Fatal(err)
			}
			original, _ := json.Marshal(p)
			hash := p.Hash()
			risk := 21
			in := policy.Input{TenantID: "org", RepositoryID: "repo", ChangeID: "change", RunID: "run", Criticality: policy.CriticalityUnknown, RiskScore: &risk, AsOf: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
			result, err := (policy.Engine{}).Evaluate(t.Context(), p, in)
			if err != nil || result.Status != evidence.StatusFail {
				t.Fatal(result, err)
			}
			encoded, _ := json.Marshal(result)
			if reference == nil {
				reference = encoded
			}
			if !bytes.Equal(reference, encoded) {
				t.Fatal("edition changed policy semantics")
			}
			granted = false
			if _, err := service.CompileCentral(t.Context(), actor, "org", centralDocument); !errors.Is(err, entitlement.ErrNotEntitled) {
				t.Fatal(err)
			}
			stillRequired, err := (policy.Engine{}).Evaluate(t.Context(), p, in)
			if err != nil || stillRequired.Status != evidence.StatusFail {
				t.Fatal("downgrade bypassed required policy", stillRequired, err)
			}
			granted = true
			permitted = false
			if _, err := service.CompileCentral(t.Context(), actor, "org", centralDocument); !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal(err)
			}
			permitted = true
			again, err := service.CompileCentral(t.Context(), actor, "org", centralDocument)
			after, _ := json.Marshal(p)
			if err != nil || again.Hash() != hash || !bytes.Equal(after, original) {
				t.Fatal("plan change corrupted configuration", err)
			}
		})
	}
}

func TestPolicyServiceRejectsMissingDependencies(t *testing.T) {
	if _, err := application.NewPolicyService(nil, nil, nil, nil, nil); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal(err)
	}
	var service application.PolicyService
	if _, err := service.CompileCentral(t.Context(), authorization.Principal{}, "org", centralDocument); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal(err)
	}
	catalog, _ := feature.NewBuiltinCatalog("1.0.0", time.Now())
	if _, err := application.NewPolicyService(catalog, nil, nil, nil, nil); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal(err)
	}
}
