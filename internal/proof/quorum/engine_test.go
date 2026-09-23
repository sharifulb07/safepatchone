package quorum_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"safepatch/internal/proof/quorum"
)

func fixture(t testing.TB) (quorum.PolicyFields, quorum.Input) {
	t.Helper()
	f := quorum.PolicyFields{Schema: quorum.Schema, ID: "payment", Version: "1", Sources: []quorum.Source{}, Requirements: []quorum.QuorumRequirement{{ID: "payment.correctness", Sources: []string{}, RequiredFamilies: 3, MandatoryFamilies: []quorum.Family{}, MinConfidence: 0.8}}}
	in := quorum.Input{TenantID: "org", RepositoryID: "repo", ChangeID: "change", RunID: "run", AsOf: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC), Complete: true, Evidence: []evidence.Evidence{}}
	for _, family := range []quorum.Family{quorum.Tests, quorum.Replay, quorum.BusinessGuard, quorum.AIReview} {
		id := string(family)
		f.Sources = append(f.Sources, quorum.Source{ID: id, Producer: id, Type: "check", Family: family, IndependenceID: id, Capability: "verify." + id})
		f.Requirements[0].Sources = append(f.Requirements[0].Sources, id)
		in.Evidence = append(in.Evidence, node(t, id, id, evidence.StatusPass, family != quorum.AIReview, in.AsOf))
	}
	return f, in
}
func node(t testing.TB, id, producer string, status evidence.Status, deterministic bool, now time.Time) evidence.Evidence {
	t.Helper()
	n, err := evidence.NewEvidence(evidence.EvidenceFields{EvidenceID: id, TenantID: "org", ChangeID: "change", RunID: "run", Producer: producer, ProducerVersion: "1.0.0", Category: "verification", Type: "check", Status: status, Deterministic: deterministic, Confidence: 1, Severity: evidence.SeverityInfo, Summary: "fixture observation", Payload: json.RawMessage(`{}`), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func evaluate(t testing.TB, f quorum.PolicyFields, in quorum.Input) quorum.QuorumResult {
	t.Helper()
	p, err := quorum.New(f)
	if err != nil {
		t.Fatal(err)
	}
	r, err := (quorum.EvidenceQuorum{}).Evaluate(t.Context(), p, in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestQuorumIndependentFamiliesAndContradictionPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*quorum.PolicyFields, *quorum.Input)
		status evidence.Status
	}{
		{"satisfied", func(f *quorum.PolicyFields, in *quorum.Input) { in.Evidence = in.Evidence[:3] }, evidence.StatusPass},
		{"runtime_failure_despite_ai", func(f *quorum.PolicyFields, in *quorum.Input) {
			in.Evidence[1] = node(t, "replay", "replay", evidence.StatusFail, true, in.AsOf)
		}, evidence.StatusFail},
		{"low_confidence_failure", func(f *quorum.PolicyFields, in *quorum.Input) {
			n, _ := in.Evidence[1].Fields()
			n.ContentHash = ""
			n.Status = evidence.StatusFail
			n.Confidence = 0
			in.Evidence[1], _ = evidence.NewEvidence(n)
		}, evidence.StatusFail},
		{"duplicate_scanners_count_once", func(f *quorum.PolicyFields, in *quorum.Input) {
			f.Sources = append(f.Sources, quorum.Source{ID: "second", Producer: "second", Type: "check", Family: quorum.Tests, IndependenceID: "tests", Capability: "verify.second"})
			f.Requirements[0].Sources = append(f.Requirements[0].Sources, "second")
			in.Evidence = []evidence.Evidence{in.Evidence[0], in.Evidence[1], node(t, "second", "second", evidence.StatusPass, true, in.AsOf)}
		}, evidence.StatusUnknown},
		{"mandatory_missing", func(f *quorum.PolicyFields, in *quorum.Input) {
			f.Requirements[0].MandatoryFamilies = []quorum.Family{quorum.BusinessGuard}
			in.Evidence = append(in.Evidence[:2], in.Evidence[3])
		}, evidence.StatusUnknown},
		{"incomplete_collection", func(f *quorum.PolicyFields, in *quorum.Input) { in.Complete = false }, evidence.StatusUnknown},
		{"missing", func(f *quorum.PolicyFields, in *quorum.Input) { in.Evidence = nil }, evidence.StatusUnknown},
		{"determinism_required", func(f *quorum.PolicyFields, in *quorum.Input) {
			f.Requirements[0].RequiredFamilies = 4
			f.Requirements[0].Deterministic = true
		}, evidence.StatusUnknown},
		{"graph_contradiction", func(f *quorum.PolicyFields, in *quorum.Input) {
			in.Edges = []graph.Edge{{TenantID: "org", EdgeID: "edge", FromEvidenceID: "replay", ToEvidenceID: "tests", Relationship: graph.Contradicts, CreatedAt: in.AsOf}}
		}, evidence.StatusFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, in := fixture(t)
			tc.mutate(&f, &in)
			r := evaluate(t, f, in)
			out, _ := r.Fields()
			if out.Status != tc.status {
				t.Fatalf("%+v", out)
			}
			if (out.Decision == quorum.Satisfied) != (tc.status == evidence.StatusPass) {
				t.Fatal(out)
			}
			original, _ := json.Marshal(r)
			slices.Reverse(in.Evidence)
			slices.Reverse(f.Sources)
			slices.Reverse(f.Requirements[0].Sources)
			again, _ := json.Marshal(evaluate(t, f, in))
			if !bytes.Equal(original, again) {
				t.Fatal("input order changed result")
			}
			if tc.name == "satisfied" && (len(out.Requirements[0].SatisfiedFamilies) != 3 || len(out.Requirements[0].MissingFamilies) != 0) {
				t.Fatal(out)
			}
			if tc.name == "graph_contradiction" && (slices.Contains(out.Requirements[0].SatisfiedFamilies, quorum.Tests) || slices.Contains(out.Requirements[0].SatisfiedFamilies, quorum.Replay)) {
				t.Fatal("contradicted families counted as satisfied")
			}
			if tc.name == "runtime_failure_despite_ai" {
				found := false
				for _, o := range out.Requirements[0].Observations {
					if o.EvidenceID == "replay" && o.Reason == "CONTRADICTS" {
						found = true
					}
				}
				if !found {
					t.Fatal("missing contradiction explanation")
				}
			}
		})
	}
}

func TestQuorumEveryEvidenceStatus(t *testing.T) {
	for _, status := range []evidence.Status{evidence.StatusPass, evidence.StatusFail, evidence.StatusWarn, evidence.StatusError, evidence.StatusSkipped, evidence.StatusUnavailable, evidence.StatusUnknown} {
		t.Run(string(status), func(t *testing.T) {
			f, in := fixture(t)
			f.Requirements[0].RequiredFamilies = 4
			in.Evidence[1] = node(t, "replay", "replay", status, true, in.AsOf)
			r := evaluate(t, f, in)
			out, _ := r.Fields()
			want := evidence.StatusUnknown
			if status == evidence.StatusPass {
				want = evidence.StatusPass
			}
			if status == evidence.StatusFail || status == evidence.StatusWarn {
				want = evidence.StatusFail
			}
			if out.Status != want {
				t.Fatal(out)
			}
		})
	}
}

func TestQuorumValidationAndSealing(t *testing.T) {
	for _, mode := range []string{"unknown_family", "duplicate_selector", "duplicate_identity", "duplicate_capability", "impossible_count", "duplicate_requirement", "duplicate_source", "unknown_source", "unknown_mandatory", "no_requirements"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := fixture(t)
			switch mode {
			case "unknown_family":
				f.Sources[0].Family = "made_up"
			case "duplicate_selector":
				f.Sources[1].Producer = f.Sources[0].Producer
			case "duplicate_identity":
				f.Sources[1].IndependenceID = f.Sources[0].IndependenceID
			case "duplicate_capability":
				f.Sources[1].Capability = f.Sources[0].Capability
			case "impossible_count":
				f.Requirements[0].RequiredFamilies = 5
			case "duplicate_requirement":
				f.Requirements = append(f.Requirements, f.Requirements[0])
			case "duplicate_source":
				f.Requirements[0].Sources = append(f.Requirements[0].Sources, "tests")
			case "unknown_source":
				f.Requirements[0].Sources[0] = "missing"
			case "unknown_mandatory":
				f.Requirements[0].MandatoryFamilies = []quorum.Family{quorum.Formal}
			case "no_requirements":
				f.Requirements = nil
			}
			if _, err := quorum.New(f); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	f, in := fixture(t)
	p, _ := quorum.New(f)
	original := p.Digest()
	f.Sources[0].Family = quorum.Formal
	copied, _ := p.Fields()
	copied.Requirements[0].Sources[0] = "mutated"
	if p.Digest() != original {
		t.Fatal("mutable policy")
	}
	r, err := (quorum.EvidenceQuorum{}).Evaluate(t.Context(), p, in)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(`{}`), &r) == nil || json.Unmarshal([]byte(`{}`), &p) == nil {
		t.Fatal("unsealed overwrite")
	}
	raw, _ := json.Marshal(p)
	if _, err := quorum.DecodeFields(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"id":`), []byte(`"ID":`), 1), bytes.Replace(raw, []byte(`"schema":`), []byte(`"extra":true,"schema":`), 1), bytes.Replace(raw, []byte(`"id":`), []byte(`"id":"duplicate","id":`), 1)} {
		if _, err := quorum.DecodeFields(bad); err == nil {
			t.Fatal("bad JSON accepted")
		}
	}
	if _, err := quorum.DecodeFields(bytes.Replace(raw, []byte(`"mandatory_families":[]`), []byte(`"mandatory_families":null`), 1)); err == nil {
		t.Fatal("null configuration accepted")
	}
}

func TestQuorumConcurrentEvaluationAndMultipleRequirements(t *testing.T) {
	f, in := fixture(t)
	f.Requirements = append(f.Requirements, quorum.QuorumRequirement{ID: "runtime.correctness", Sources: []string{"replay"}, RequiredFamilies: 1, MandatoryFamilies: []quorum.Family{quorum.Replay}})
	p, err := quorum.New(f)
	if err != nil {
		t.Fatal(err)
	}
	baseline := evaluate(t, f, in).Digest()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			r, err := (quorum.EvidenceQuorum{}).Evaluate(t.Context(), p, in)
			if err != nil || r.Digest() != baseline {
				t.Error("concurrent evaluation changed", err)
			}
			copy, _ := r.Fields()
			copy.Requirements[0].SatisfiedFamilies[0] = quorum.Formal
			if r.Digest() != baseline {
				t.Error("mutable result")
			}
		})
	}
	wg.Wait()
	in.Evidence[1] = node(t, "replay", "replay", evidence.StatusUnknown, true, in.AsOf)
	r := evaluate(t, f, in)
	out, _ := r.Fields()
	if out.Decision != quorum.NotProven || out.Requirements[0].Decision != quorum.Satisfied || out.Requirements[1].Decision != quorum.NotProven {
		t.Fatal("requirements were averaged", out)
	}
}

func TestQuorumInvalidInputCannotProduceResult(t *testing.T) {
	for _, mode := range []string{"tenant", "run", "change", "future", "duplicate", "unsealed", "foreign_edge", "dangling_edge", "cancel", "nil_context", "too_many"} {
		t.Run(mode, func(t *testing.T) {
			f, in := fixture(t)
			p, _ := quorum.New(f)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "tenant":
				in.TenantID = "foreign"
			case "run":
				in.RunID = "foreign"
			case "change":
				in.ChangeID = "foreign"
			case "future":
				in.AsOf = in.AsOf.Add(-time.Second)
			case "duplicate":
				in.Evidence = append(in.Evidence, in.Evidence[0])
			case "unsealed":
				in.Evidence[0] = evidence.Evidence{}
			case "foreign_edge", "dangling_edge":
				in.Edges = []graph.Edge{{TenantID: "org", EdgeID: "edge", FromEvidenceID: "tests", ToEvidenceID: "replay", Relationship: graph.Invalidates, CreatedAt: in.AsOf}}
				if mode == "foreign_edge" {
					in.Edges[0].TenantID = "foreign"
				} else {
					in.Edges[0].ToEvidenceID = "missing"
				}
			case "cancel":
				cancel()
			case "nil_context":
				ctx = nil
			case "too_many":
				in.Evidence = make([]evidence.Evidence, 257)
			}
			r, err := (quorum.EvidenceQuorum{}).Evaluate(ctx, p, in)
			if err == nil || r.Digest() != "" {
				t.Fatal(r, err)
			}
		})
	}
}

func TestCanonicalEvidenceAndGraphExplanation(t *testing.T) {
	f, in := fixture(t)
	in.Evidence[1] = node(t, "replay", "replay", evidence.StatusFail, true, in.AsOf)
	r := evaluate(t, f, in)
	ev, err := r.Evidence("quorum.result", evidence.Digest([]byte("intent")))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := ev.CanonicalJSON()
	decoded, err := evidence.DecodeEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := decoded.Fields()
	if fields.Status != evidence.StatusFail || fields.InputHashes["quorum_result"] != r.Digest() {
		t.Fatal(fields)
	}
	edges, err := r.Relationships("quorum.result")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range edges {
		if edge.Validate() != nil {
			t.Fatal(edge)
		}
		if edge.FromEvidenceID == "replay" && edge.Relationship == graph.Contradicts {
			found = true
		}
	}
	if !found {
		t.Fatal("missing graph contradiction")
	}
}
