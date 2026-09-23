package policy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"safepatch/internal/policy"
)

func ptr[T any](v T) *T { return &v }
func document() policy.Document {
	return policy.Document{Schema: policy.Schema, PolicyID: "baseline", TenantID: "org", Version: "1", Rules: []policy.Rule{{ID: "critical_go", When: policy.Selector{RepositoryIDs: []string{"repo"}, Criticalities: []policy.Criticality{policy.CriticalityHigh, policy.CriticalityCritical}}, Require: policy.Requirements{MaxRiskScore: ptr(30), MinProofLevel: ptr(uint32(2)), Evidence: []policy.EvidenceRequirement{{Producer: "go", Type: "go.test", MinConfidence: 0.8, Deterministic: true}}}}}}
}
func TestPolicyIsImmutableAndVersioned(t *testing.T) {
	d := document()
	p, err := policy.New(d)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	hash := p.Hash()
	*d.Rules[0].Require.MaxRiskScore = 100
	d.Rules[0].When.RepositoryIDs[0] = "other"
	d.Rules[0].Require.Evidence[0].Producer = "injected"
	copy, err := p.Document()
	if err != nil {
		t.Fatal(err)
	}
	*copy.Rules[0].Require.MinProofLevel = 0
	after, _ := json.Marshal(p)
	if !bytes.Equal(raw, after) || hash != p.Hash() {
		t.Fatal("policy changed through an alias")
	}
	restored, err := policy.Decode(raw)
	if err != nil || restored.Hash() != hash {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &p); !errors.Is(err, policy.ErrImmutablePolicy) {
		t.Fatal(err)
	}
	changed, _ := restored.Document()
	changed.Version = "2"
	v2, err := policy.New(changed)
	if err != nil || v2.Hash() == hash {
		t.Fatal("version not bound to hash")
	}
	if _, err := json.Marshal(policy.Policy{}); err == nil {
		t.Fatal("zero value serialized")
	}
}

func TestPolicyRejectsUnsafeOrAmbiguousConfiguration(t *testing.T) {
	p, _ := policy.New(document())
	valid, _ := json.Marshal(p)
	for _, raw := range []string{
		"", "null", "[]", string(valid) + "{}",
		strings.Replace(string(valid), `"schema":`, `"Schema":`, 1),
		strings.Replace(string(valid), `"version":"1"`, `"version":"1","version":"2"`, 1),
		strings.Replace(string(valid), `"when":`, `"WHEN":`, 1),
		strings.Replace(string(valid), `"max_risk_score":30`, `"max_risk_score":null`, 1),
		strings.Replace(string(valid), `"max_risk_score":30`, `"max_risk_score":30,"MAX_RISK_SCORE":0`, 1),
		strings.Replace(string(valid), `"min_proof_level":2`, `"min_proof_level":-1`, 1),
		strings.Replace(string(valid), `"min_proof_level":2`, `"min_proof_level":4294967296`, 1),
		strings.Replace(string(valid), `"producer":"go"`, `"producer":"go","exec":"curl secret"`, 1),
		strings.Replace(string(valid), `"type":"go.test"`, `"type":"\ud800"`, 1),
		strings.Replace(string(valid), `"type":"go.test"`, `"type":"go.test","expression":"plan == enterprise"`, 1),
		strings.Replace(string(valid), `"tenant_id":"org"`, `"tenant_id":"../secret"`, 1),
		strings.Repeat(" ", policy.MaxPolicyBytes) + string(valid),
	} {
		if p, err := policy.Decode([]byte(raw)); !errors.Is(err, policy.ErrInvalidPolicy) || p.Hash() != "" {
			t.Fatalf("accepted invalid policy: %.140s: %v", raw, err)
		}
	}
	for _, change := range []func(*policy.Document){
		func(d *policy.Document) { d.Rules = nil }, func(d *policy.Document) { d.Schema = "future" },
		func(d *policy.Document) { d.Rules = append(d.Rules, d.Rules[0]) },
		func(d *policy.Document) { d.Rules[0].When.RepositoryIDs = []string{"repo", "repo"} },
		func(d *policy.Document) {
			d.Rules[0].When.Criticalities = []policy.Criticality{policy.CriticalityUnknown}
		},
		func(d *policy.Document) { d.Rules[0].Require = policy.Requirements{} },
		func(d *policy.Document) { d.Rules[0].Require.MaxRiskScore = ptr(101) },
		func(d *policy.Document) { d.Rules[0].Require.Evidence[0].MinConfidence = math.NaN() },
		func(d *policy.Document) { d.Rules[0].Require.Evidence[0].MinConfidence = math.Inf(1) },
		func(d *policy.Document) {
			d.Rules[0].Require.Evidence = append(d.Rules[0].Require.Evidence, d.Rules[0].Require.Evidence[0])
		},
		func(d *policy.Document) { d.Rules = make([]policy.Rule, policy.MaxRules+1) },
	} {
		d := document()
		change(&d)
		if _, err := policy.New(d); !errors.Is(err, policy.ErrInvalidPolicy) {
			t.Fatal(err)
		}
	}
}

func FuzzDecodePolicy(f *testing.F) {
	p, _ := policy.New(document())
	raw, _ := json.Marshal(p)
	f.Add(raw)
	f.Add([]byte(`{"schema":"future"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := policy.Decode(raw)
		if err != nil {
			return
		}
		out, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		q, err := policy.Decode(out)
		if err != nil || q.Hash() != p.Hash() {
			t.Fatal("unstable decoded policy", err)
		}
	})
}
