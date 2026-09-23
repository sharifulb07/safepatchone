package policy_test

import (
	"bytes"
	"encoding/json"
	"safepatch/internal/policy"
	"safepatch/internal/proof/quorum"
	"testing"
)

func TestPolicyQuorumParsingAndRemovalGuard(t *testing.T) {
	q := quorum.PolicyFields{Schema: quorum.Schema, ID: "quorum", Version: "1", Sources: []quorum.Source{{ID: "unit", Producer: "unit", Type: "tests", Family: quorum.Tests, IndependenceID: "unit", Capability: "go.test"}}, Requirements: []quorum.QuorumRequirement{{ID: "tests", Sources: []string{"unit"}, RequiredFamilies: 1, MandatoryFamilies: []quorum.Family{}}}}
	d := policy.Document{Schema: policy.Schema, PolicyID: "policy", TenantID: "org", Version: "1", Rules: []policy.Rule{{ID: "tests", Require: policy.Requirements{Quorum: &q}}}}
	p, err := policy.New(d)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	if _, err := policy.Decode(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"family":`), []byte(`"Family":`), 1), bytes.Replace(raw, []byte(`"independence_id":`), []byte(`"injected":true,"independence_id":`), 1), bytes.Replace(raw, []byte(`"required_families":1`), []byte(`"required_families":0`), 1)} {
		if _, err := policy.Decode(bad); err == nil {
			t.Fatal("bad quorum policy accepted")
		}
	}
	d.Rules[0].Require = policy.Requirements{Evidence: []policy.EvidenceRequirement{{Producer: "unit", Type: "tests"}}}
	next, err := policy.New(d)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := (policy.PolicyGuard{}).Check(t.Context(), p, next)
	if err != nil || len(findings) != 1 || findings[0].Code != "QUORUM_REQUIREMENT_CHANGED" {
		t.Fatal(findings, err)
	}
}
