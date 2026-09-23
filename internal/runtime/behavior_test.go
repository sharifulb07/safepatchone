package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"safepatch/internal/replay"
	behavior "safepatch/internal/runtime"
)

var now = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
var scope = replay.Scope{OrganizationID: "org", RepositoryID: "repo"}
var metrics = []behavior.Metric{{Name: "amount", Source: "OUTPUT", Pointer: "/amount"}, {Name: "tax", Source: "OUTPUT", Pointer: "/tax"}, {Name: "total", Source: "OUTPUT", Pointer: "/total"}}

func dna(t *testing.T, output string, complete bool) behavior.BehaviorDNA {
	t.Helper()
	redactor, _ := replay.NewRedactor([]string{"/amount", "/tax", "/total"})
	d, err := replay.NewDataset(scope, "dataset", now, now.Add(time.Hour), []replay.Record{{ID: "sale", Input: json.RawMessage(`{}`), Expected: replay.Observation{Complete: complete, Status: 200, Output: json.RawMessage(output)}}}, redactor)
	if err != nil {
		t.Fatal(err)
	}
	result, err := behavior.BuildBehaviorDNA(t.Context(), d, metrics)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func rules(t *testing.T) behavior.InvariantPolicy {
	t.Helper()
	p, err := behavior.NewInvariantPolicy(behavior.InvariantFields{Schema: "safepatch.business-invariants.v1", Scope: scope, ID: "accounting", Version: "1", Rules: []behavior.BusinessInvariant{{ID: "conservation", Terms: []behavior.InvariantTerm{{Metric: "amount", Coefficient: "1"}, {Metric: "tax", Coefficient: "1"}, {Metric: "total", Coefficient: "-1"}}, Operator: "EQ", Bound: "0"}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestExactInvariantSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		complete     bool
		want         behavior.InvariantDecision
	}{
		{"exact_decimal", `{"amount":0.1,"tax":0.2,"total":0.3}`, true, behavior.InvariantSatisfied},
		{"large_integer", `{"amount":9007199254740993,"tax":1,"total":9007199254740994}`, true, behavior.InvariantSatisfied},
		{"violation", `{"amount":1,"tax":2,"total":4}`, true, behavior.InvariantViolated},
		{"missing", `{"amount":1,"total":1}`, true, behavior.InvariantUnknown},
		{"null", `{"amount":1,"tax":null,"total":1}`, true, behavior.InvariantUnknown},
		{"redacted", `{"amount":1,"tax":"Bearer private","total":1}`, true, behavior.InvariantUnknown},
		{"numeric_string", `{"amount":1,"tax":"0","total":1}`, true, behavior.InvariantUnknown},
		{"incomplete", `{"amount":1,"tax":2,"total":3}`, false, behavior.InvariantUnknown},
		{"huge_exponent", `{"amount":1e999,"tax":2,"total":3}`, true, behavior.InvariantUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dna(t, tc.output, tc.complete)
			r, err := (behavior.BusinessInvariantEngine{}).Evaluate(t.Context(), d, rules(t), now)
			if err != nil || r.Decision != tc.want || len(r.Checks) != 1 {
				t.Fatal(r, err)
			}
		})
	}
}
func TestBehaviorComparisonAndImmutability(t *testing.T) {
	a := dna(t, `{"amount":0.1,"tax":0.2,"total":0.3,"password":"private"}`, true)
	same := dna(t, `{"total":3e-1,"amount":0.10,"tax":0.20}`, true)
	changed := dna(t, `{"amount":1,"tax":2,"total":3}`, true)
	unknown := dna(t, `{"amount":1,"tax":2}`, true)
	for _, tc := range []struct {
		d    behavior.BehaviorDNA
		want replay.Decision
	}{{same, replay.Match}, {changed, replay.Different}, {unknown, replay.Unknown}} {
		got, err := behavior.CompareBehavior(t.Context(), a, tc.d)
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	raw, _ := json.Marshal(a)
	if bytes.Contains(raw, []byte("private")) || bytes.Contains(raw, []byte("password")) {
		t.Fatal("raw data exposed")
	}
	decoded, err := behavior.DecodeBehaviorDNA(raw)
	if err != nil || decoded.Digest() != a.Digest() {
		t.Fatal(err)
	}
	fields, _ := a.Fields()
	fields.Cases[0].Values["amount"] = "999"
	fields.Metrics[0].Name = "altered"
	after, _ := json.Marshal(a)
	if !bytes.Equal(raw, after) {
		t.Fatal("profile mutated")
	}
	p := rules(t)
	pr, _ := json.Marshal(p)
	decodedPolicy, err := behavior.DecodeInvariantPolicy(pr)
	if err != nil || decodedPolicy.Digest() != p.Digest() {
		t.Fatal(err)
	}
}
func TestArtifactPoisoningAndResourceBounds(t *testing.T) {
	d := dna(t, `{"amount":1,"tax":2,"total":3}`, true)
	raw, _ := json.Marshal(d)
	for _, bad := range [][]byte{
		[]byte(strings.Replace(string(raw), `"schema":`, `"schema":"poison","schema":`, 1)),
		[]byte(strings.Replace(string(raw), `"schema":`, `"Schema":`, 1)),
		[]byte(strings.Replace(string(raw), `"values":{"amount":"1"`, `"values":{"amount":"1e999999999"`, 1)),
		[]byte(strings.Replace(string(raw), `"values":{"amount":"1"`, `"values":{"amount":"2/2"`, 1)),
		append([]byte(`{"unexpected":1,`), raw[1:]...),
		bytes.Repeat([]byte("x"), behavior.MaxArtifactBytes+1),
	} {
		if _, err := behavior.DecodeBehaviorDNA(bad); err == nil {
			t.Fatal("accepted poison")
		}
	}
	f, _ := rules(t).Fields()
	f.Rules[0].Terms[0].Coefficient = "0"
	if _, err := behavior.NewInvariantPolicy(f); err == nil {
		t.Fatal("vacuous term")
	}
	f, _ = rules(t).Fields()
	f.Rules = nil
	if _, err := behavior.NewInvariantPolicy(f); err == nil {
		t.Fatal("empty rules")
	}
	f, _ = rules(t).Fields()
	f.Rules[0].Operator = "exec"
	if _, err := behavior.NewInvariantPolicy(f); err == nil {
		t.Fatal("executable operator")
	}
	f, _ = rules(t).Fields()
	f.Rules[0].Bound = "1e9999999"
	if _, err := behavior.NewInvariantPolicy(f); err == nil {
		t.Fatal("unbounded exponent")
	}
}
func TestUnknownDominatesAndScopeExpiryCancellation(t *testing.T) {
	d := dna(t, `{"amount":1,"tax":2}`, true)
	f, _ := rules(t).Fields()
	f.Rules = append(f.Rules, behavior.BusinessInvariant{ID: "fails", Terms: []behavior.InvariantTerm{{Metric: "amount", Coefficient: "1"}}, Operator: "LE", Bound: "0"})
	p, _ := behavior.NewInvariantPolicy(f)
	r, err := (behavior.BusinessInvariantEngine{}).Evaluate(t.Context(), d, p, now)
	if err != nil || r.Decision != behavior.InvariantUnknown || r.Checks[1].Decision != behavior.InvariantViolated {
		t.Fatal(r, err)
	}
	for _, at := range []time.Time{now.Add(-time.Nanosecond), now.Add(time.Hour), {}} {
		r, err := (behavior.BusinessInvariantEngine{}).Evaluate(t.Context(), d, p, at)
		if err == nil || r.Decision != behavior.InvariantUnknown {
			t.Fatal(r, err)
		}
	}
	f.Scope.OrganizationID = "foreign"
	foreign, _ := behavior.NewInvariantPolicy(f)
	if r, err := (behavior.BusinessInvariantEngine{}).Evaluate(t.Context(), d, foreign, now); err == nil || r.Decision != behavior.InvariantUnknown {
		t.Fatal(r, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := behavior.CompareBehavior(ctx, d, d); err == nil {
		t.Fatal("cancellation ignored")
	}
	if r, err := (behavior.BusinessInvariantEngine{}).Evaluate(ctx, d, p, now); err == nil || r.Decision != behavior.InvariantUnknown {
		t.Fatal(r, err)
	}
}
func FuzzBehaviorArtifacts(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":"safepatch.behaviordna.v1","cases":[]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		d, err := behavior.DecodeBehaviorDNA(raw)
		if err == nil {
			canonical, _ := json.Marshal(d)
			again, err := behavior.DecodeBehaviorDNA(canonical)
			if err != nil || again.Digest() != d.Digest() {
				t.Fatal("unstable profile")
			}
		}
		p, err := behavior.DecodeInvariantPolicy(raw)
		if err == nil {
			canonical, _ := json.Marshal(p)
			again, err := behavior.DecodeInvariantPolicy(canonical)
			if err != nil || again.Digest() != p.Digest() {
				t.Fatal("unstable policy")
			}
		}
	})
}
