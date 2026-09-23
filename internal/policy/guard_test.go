package policy_test

import (
	"context"
	"testing"

	"safepatch/internal/policy"
)

func guardPolicy(t *testing.T, mutate func(*policy.Document)) policy.Policy {
	t.Helper()
	risk := 20
	level := uint32(2)
	d := policy.Document{Schema: policy.Schema, PolicyID: "baseline", TenantID: "org", Version: "1", Rules: []policy.Rule{{ID: "required", Require: policy.Requirements{MaxRiskScore: &risk, MinProofLevel: &level, Evidence: []policy.EvidenceRequirement{{Producer: "scanner", Type: "security.check", MinConfidence: 0.9, Deterministic: true}}}}}}
	if mutate != nil {
		mutate(&d)
	}
	p, err := policy.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPolicyGuardPreservesRequirements(t *testing.T) {
	for _, mode := range []string{"same", "risk", "proof", "evidence", "confidence", "determinism", "repository", "criticality", "rule_removed", "policy_id", "stronger"} {
		t.Run(mode, func(t *testing.T) {
			base := guardPolicy(t, nil)
			head := guardPolicy(t, func(d *policy.Document) {
				r := &d.Rules[0]
				switch mode {
				case "risk":
					*r.Require.MaxRiskScore = 30
				case "proof":
					r.Require.MinProofLevel = nil
				case "evidence":
					r.Require.Evidence = nil
				case "confidence":
					r.Require.Evidence[0].MinConfidence = 0.8
				case "determinism":
					r.Require.Evidence[0].Deterministic = false
				case "repository":
					r.When.RepositoryIDs = []string{"repo"}
				case "criticality":
					r.When.Criticalities = []policy.Criticality{policy.CriticalityHigh}
				case "rule_removed":
					r.ID = "replacement"
				case "policy_id":
					d.PolicyID = "replacement"
				case "stronger":
					*r.Require.MaxRiskScore = 10
					*r.Require.MinProofLevel = 3
					r.Require.Evidence[0].MinConfidence = 1
				}
			})
			findings, err := (policy.PolicyGuard{}).Check(t.Context(), base, head)
			if err != nil {
				t.Fatal(err)
			}
			clean := mode == "same" || mode == "stronger"
			if clean != (len(findings) == 0) {
				t.Fatal(mode, findings)
			}
		})
	}
}
func TestPolicyGuardScopeAndCancellation(t *testing.T) {
	base := guardPolicy(t, nil)
	foreign := guardPolicy(t, func(d *policy.Document) { d.TenantID = "foreign" })
	if _, err := (policy.PolicyGuard{}).Check(t.Context(), base, foreign); err == nil {
		t.Fatal("foreign policy")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (policy.PolicyGuard{}).Check(ctx, base, base); err == nil {
		t.Fatal("cancellation")
	}
}
