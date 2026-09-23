package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/replay"
	behavior "safepatch/internal/runtime"
	"safepatch/internal/tenant"
)

type behaviorReader struct {
	d     replay.ReplayDataset
	err   error
	calls int
}

func (r *behaviorReader) Read(context.Context, authorization.Principal, replay.Scope, string) (replay.ReplayDataset, error) {
	r.calls++
	return r.d, r.err
}
func TestBehaviorAndInvariantAdmission(t *testing.T) {
	for _, operation := range []string{"describe", "compile", "evaluate", "compare"} {
		for _, mode := range []string{"allowed", "not_entitled", "not_permitted", "tenant", "quota", "source_error", "source_forged", "expired", "cancel"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
				scope := replay.Scope{OrganizationID: "org", RepositoryID: "repo"}
				actor := authorization.Principal{ID: "owner", Kind: authorization.User}
				redactor, _ := replay.NewRedactor([]string{"/amount"})
				d, _ := replay.NewDataset(scope, "dataset", now, now.Add(time.Hour), []replay.Record{{ID: "case", Input: json.RawMessage(`{}`), Expected: replay.Observation{Complete: true, Status: 200, Output: json.RawMessage(`{"amount":1}`)}}}, redactor)
				metrics := []behavior.Metric{{Name: "amount", Source: "OUTPUT", Pointer: "/amount"}}
				dna, _ := behavior.BuildBehaviorDNA(t.Context(), d, metrics)
				p, _ := behavior.NewInvariantPolicy(behavior.InvariantFields{Schema: "safepatch.business-invariants.v1", Scope: scope, ID: "positive", Version: "1", Rules: []behavior.BusinessInvariant{{ID: "nonnegative", Terms: []behavior.InvariantTerm{{Metric: "amount", Coefficient: "1"}}, Operator: "GE", Bound: "0"}}})
				raw, _ := json.Marshal(p)
				reader := &behaviorReader{d: d}
				permissionCalls := 0
				service, err := application.NewBehaviorService(tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					if mode == "tenant" {
						s.OrganizationID = "foreign"
					}
					return s, nil
				}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
					want := feature.KeyBusinessInvariants
					if operation == "describe" || operation == "compare" {
						want = feature.KeyRuntimeBehaviorDNA
					}
					if org != "org" || k != want {
						t.Fatal("wrong entitlement", org, k)
					}
					return mode != "not_entitled", nil
				}), permissionFunc(func(_ context.Context, _ authorization.Principal, _ string, k authorization.Key) (bool, error) {
					permissionCalls++
					want := authorization.KeyReplayView
					if operation == "compile" {
						want = authorization.KeyPolicyManage
					}
					if operation == "evaluate" {
						want = authorization.KeyProofExecute
					}
					if k != want {
						t.Fatal("wrong permission", k)
					}
					return mode != "not_permitted", nil
				}), quotaFunc(func(context.Context, application.Execution) error {
					if mode == "quota" {
						return errors.New("private quota failure")
					}
					return nil
				}), reader, metrics, func() time.Time { return now })
				if err != nil {
					t.Fatal(err)
				}
				if mode == "source_error" {
					reader.err = errors.New("private credential")
				}
				if mode == "expired" {
					now = now.Add(time.Hour)
				}
				if mode == "source_forged" {
					foreign := scope
					foreign.OrganizationID = "foreign"
					reader.d, _ = replay.NewDataset(foreign, "dataset", now, now.Add(time.Hour), []replay.Record{{ID: "case", Input: json.RawMessage(`{}`), Expected: replay.Observation{Complete: true, Status: 200, Output: json.RawMessage(`{"amount":1}`)}}}, redactor)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "cancel" {
					cancel()
				}
				switch operation {
				case "describe":
					_, err = service.Describe(ctx, actor, scope, "dataset")
				case "compile":
					_, err = service.CompileInvariants(ctx, actor, scope, raw)
				case "evaluate":
					var r behavior.InvariantResult
					r, err = service.Evaluate(ctx, actor, scope, dna, p)
					if err != nil && r.Decision != behavior.InvariantUnknown {
						t.Fatal("error passed")
					}
				case "compare":
					var r replay.Decision
					r, err = service.Compare(ctx, actor, scope, dna, dna)
					if err != nil && r != replay.Unknown {
						t.Fatal("error matched")
					}
				}
				success := mode == "allowed" || operation == "compile" && (mode == "source_error" || mode == "source_forged" || mode == "expired")
				if success != (err == nil) {
					t.Fatal("unexpected admission", err)
				}
				if err != nil && bytes.Contains([]byte(err.Error()), []byte("private")) {
					t.Fatal("secret error escaped")
				}
				if mode == "not_entitled" && permissionCalls != 0 {
					t.Fatal("permission checked before entitlement")
				}
			})
		}
	}
}

func TestBehaviorEditionParityAndPlanChangePreservesArtifacts(t *testing.T) {
	var reference []byte
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
			scope := replay.Scope{OrganizationID: "org", RepositoryID: "repo"}
			actor := authorization.Principal{ID: "owner", Kind: authorization.User}
			redactor, _ := replay.NewRedactor([]string{"/amount"})
			dataset, _ := replay.NewDataset(scope, "dataset", now, now.Add(time.Hour), []replay.Record{{ID: "case", Input: json.RawMessage(`{}`), Expected: replay.Observation{Complete: true, Status: 200, Output: json.RawMessage(`{"amount":1}`)}}}, redactor)
			enabled := true
			reader := &behaviorReader{d: dataset}
			service, _ := application.NewBehaviorService(tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				return s, nil
			}), entitlementFunc(func(context.Context, string, feature.Key) (bool, error) { return enabled, nil }), permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
				return true, nil
			}), quotaFunc(func(context.Context, application.Execution) error { return nil }), reader, []behavior.Metric{{Name: "amount", Source: "OUTPUT", Pointer: "/amount"}}, func() time.Time { return now })
			dna, err := service.Describe(t.Context(), actor, scope, "dataset")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(dna)
			if reference == nil {
				reference = raw
			}
			if !bytes.Equal(reference, raw) {
				t.Fatal("edition changed schema")
			}
			p, _ := behavior.NewInvariantPolicy(behavior.InvariantFields{Schema: "safepatch.business-invariants.v1", Scope: scope, ID: "p", Version: "1", Rules: []behavior.BusinessInvariant{{ID: "r", Terms: []behavior.InvariantTerm{{Metric: "amount", Coefficient: "1"}}, Operator: "GE", Bound: "0"}}})
			policyRaw, _ := json.Marshal(p)
			enabled = false
			if _, err := service.Describe(t.Context(), actor, scope, "dataset"); err == nil {
				t.Fatal("revoked DNA")
			}
			if _, err := service.CompileInvariants(t.Context(), actor, scope, policyRaw); err == nil {
				t.Fatal("revoked invariant management")
			}
			if r, err := service.Evaluate(t.Context(), actor, scope, dna, p); err == nil || r.Decision != behavior.InvariantUnknown {
				t.Fatal("revoked invariant evaluation")
			}
			enabled = true
			r, err := service.Evaluate(t.Context(), actor, scope, dna, p)
			if err != nil || r.Decision != behavior.InvariantSatisfied {
				t.Fatal(r, err)
			}
			after, _ := json.Marshal(dna)
			policyAfter, _ := json.Marshal(p)
			if !bytes.Equal(raw, after) || !bytes.Equal(policyRaw, policyAfter) {
				t.Fatal("plan change mutated history")
			}
			reader.err = errors.New("retention downgraded")
			if r, err := service.Evaluate(t.Context(), actor, scope, dna, p); err == nil || r.Decision != behavior.InvariantUnknown {
				t.Fatal("retention bypass")
			}
		})
	}
}
