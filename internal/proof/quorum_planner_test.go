package proof_test

import (
	"safepatch/internal/evidence"
	"safepatch/internal/proof"
	"safepatch/internal/proof/quorum"
	"safepatch/internal/verification"
	"testing"
)

func TestQuorumPlannerPreservesFamiliesBudgetsAndPolicyIdentity(t *testing.T) {
	f := quorum.PolicyFields{Schema: quorum.Schema, ID: "payment", Version: "1", Sources: []quorum.Source{{ID: "tests", Producer: "unit", Type: "tests", Family: quorum.Tests, IndependenceID: "unit", Capability: "go.test"}, {ID: "replay", Producer: "runtime", Type: "replay", Family: quorum.Replay, IndependenceID: "replay", Capability: "runtime.replay"}}, Requirements: []quorum.QuorumRequirement{{ID: "payment", Sources: []string{"tests", "replay"}, RequiredFamilies: 2}}}
	providers := []proof.Provider{}
	states := []proof.ProviderState{}
	for _, o := range proof.DefaultObligations() {
		providers = append(providers, proof.Provider{ID: o.ID, Version: "1", Capability: o.Capability})
		states = append(states, proof.ProviderState{ID: o.ID, Availability: proof.Available})
	}
	for _, s := range f.Sources {
		providers = append(providers, proof.Provider{ID: "q." + s.ID, Version: "1", Capability: verification.Capability(s.Capability), Costs: []proof.Cost{{Resource: "compute", Amount: 1}}})
		states = append(states, proof.ProviderState{ID: "q." + s.ID, Availability: proof.Available})
	}
	p, _ := quorum.New(f)
	planner, err := proof.NewQuorumProofPlanner("1", proof.DefaultObligations(), providers, p, proof.L2)
	if err != nil {
		t.Fatal(err)
	}
	req := proof.Request{TenantID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: "head"}, Target: proof.L2, Providers: states, Budgets: []proof.Budget{{Resource: "compute", Remaining: 2}}}
	plan, err := planner.Plan(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := plan.Fields()
	if fields.State != proof.Ready || len(fields.Steps) != 5 {
		t.Fatal(fields)
	}
	req.Budgets[0].Remaining = 1
	plan, err = planner.Plan(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ = plan.Fields()
	if fields.State != proof.Blocked {
		t.Fatal("quota lowered proof demand")
	}
	req.Budgets[0].Remaining = 2
	req.Providers[len(req.Providers)-1].Availability = proof.Disabled
	plan, _ = planner.Plan(t.Context(), req)
	fields, _ = plan.Fields()
	if fields.State != proof.Blocked {
		t.Fatal("disabled source disappeared")
	}
	f.Requirements[0].RequiredFamilies = 1
	changed, _ := quorum.New(f)
	other, err := proof.NewQuorumProofPlanner("1", proof.DefaultObligations(), providers, changed, proof.L2)
	if err != nil {
		t.Fatal(err)
	}
	if other.ConfigurationHash() == planner.ConfigurationHash() {
		t.Fatal("quorum policy missing from plan identity")
	}
}
