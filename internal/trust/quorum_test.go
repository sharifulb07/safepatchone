package trust_test

import (
	"encoding/json"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"safepatch/internal/policy"
	"safepatch/internal/proof/quorum"
	"safepatch/internal/trust"
	"testing"
)

func TestTrustKernelReevaluatesQuorumAndCoverage(t *testing.T) {
	for _, mode := range []string{"satisfied", "missing_family", "runtime_failure", "graph_contradiction", "llm_provenance", "incomplete", "forged_coverage"} {
		t.Run(mode, func(t *testing.T) {
			in := kernelFixture(t)
			in.QuorumComplete = mode != "incomplete"
			qf := quorum.PolicyFields{Schema: quorum.Schema, ID: "payment", Version: "1", Sources: []quorum.Source{{ID: "tests", Producer: "runner", Type: "tests", Family: quorum.Tests, IndependenceID: "unit", Capability: "go.test"}, {ID: "replay", Producer: "replay", Type: "check", Family: quorum.Replay, IndependenceID: "replay", Capability: "runtime.replay"}, {ID: "ai", Producer: "ai", Type: "review", Family: quorum.AIReview, IndependenceID: "ai", Capability: "ai.review"}}, Requirements: []quorum.QuorumRequirement{{ID: "payment", Sources: []string{"tests", "replay", "ai"}, RequiredFamilies: 2, MandatoryFamilies: []quorum.Family{quorum.Replay}}}}
			for _, source := range qf.Sources[1:] {
				f, _ := in.Evidence[0].Fields()
				f.ContentHash = ""
				f.EvidenceID = source.ID
				f.Producer = source.Producer
				f.Type = source.Type
				f.Deterministic = source.Family != quorum.AIReview
				if mode == "runtime_failure" && source.Family == quorum.Replay {
					f.Status = evidence.StatusFail
				}
				n, err := evidence.NewEvidence(f)
				if err != nil {
					t.Fatal(err)
				}
				in.Evidence = append(in.Evidence, n)
			}
			if mode == "missing_family" || mode == "forged_coverage" {
				in.Evidence = append(in.Evidence[:1], in.Evidence[2])
			}
			if mode == "graph_contradiction" {
				in.EvidenceEdges = []graph.Edge{{TenantID: "org", EdgeID: "edge", FromEvidenceID: "replay", ToEvidenceID: "ev", Relationship: graph.Contradicts, CreatedAt: in.AsOf}}
			}
			q, err := quorum.New(qf)
			if err != nil {
				t.Fatal(err)
			}
			result, err := (quorum.EvidenceQuorum{}).Evaluate(t.Context(), q, quorum.Input{TenantID: in.Change.OrganizationID, RepositoryID: in.Change.RepositoryID, ChangeID: in.Change.ID(), RunID: in.RunID, AsOf: in.AsOf, Complete: mode != "incomplete", Evidence: in.Evidence, Edges: in.EvidenceEdges})
			if err != nil {
				t.Fatal(err)
			}
			in.ProofCoverage, err = trust.QuorumCoverage(result)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "forged_coverage" {
				in.ProofCoverage = trust.Coverage{Complete: true, Required: []string{"payment"}, Observed: map[string][]string{"payment": {"ev", "ai"}}}
			}
			p, err := policy.New(policy.Document{Schema: policy.Schema, PolicyID: "policy", TenantID: "org", Version: "1", Rules: []policy.Rule{{ID: "payment", Require: policy.Requirements{Quorum: &qf}}}})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(p)
			p, err = policy.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			in.Policy = p
			in.Provenance.PolicyDigest = p.Hash()
			in.Provenance.EvidenceDigest, _ = trust.EvidenceDigest(in.Evidence)
			if mode == "llm_provenance" {
				in.Provenance.Origin = trust.OriginLLM
			}
			got, err := (trust.TrustKernel{}).Evaluate(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			want := trust.Reverify
			switch mode {
			case "satisfied":
				want = trust.Authorize
			case "runtime_failure", "graph_contradiction", "llm_provenance":
				want = trust.Block
			}
			if got.Decision != want {
				t.Fatalf("got %+v want %s", got, want)
			}
			if got.Policy.Rules[0].Checks[0].Quorum == nil {
				t.Fatal("missing exact quorum explanation")
			}
			if got.Policy.Rules[0].Checks[0].Quorum.Complete != in.QuorumComplete {
				t.Fatal("collection completeness confused with proof satisfaction")
			}
		})
	}
}
