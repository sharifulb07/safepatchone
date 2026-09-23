package spec_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/spec"
	"safepatch/internal/tenant"
)

func contract(t *testing.T) intent.IntentContract {
	t.Helper()
	c, err := intent.NewIntentContract(intent.IntentContractFields{Goal: "Preserve receipts", AcceptanceCriteria: []string{"Return the original receipt"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func bindings(t *testing.T, c intent.IntentContract) []spec.Binding {
	t.Helper()
	a, err := spec.Assertions(c)
	if err != nil {
		t.Fatal(err)
	}
	result := []spec.Binding{}
	for _, item := range a {
		result = append(result, spec.Binding{AssertionID: item.ID, Evidence: policy.EvidenceRequirement{Producer: "receipt-test", Type: "receipt.retry", MinConfidence: 0.9}})
	}
	return result
}
func generate(t *testing.T, c intent.IntentContract, b []spec.Binding) spec.Specification {
	t.Helper()
	s, err := (spec.SpecForge{}).Generate(t.Context(), c, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func observation(t *testing.T, c intent.IntentContract, edit func(*evidence.EvidenceFields)) policy.Input {
	t.Helper()
	hash, _ := c.ContentHash()
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	f := evidence.EvidenceFields{EvidenceID: "ev", TenantID: "org", ChangeID: "change", RunID: "run", Producer: "receipt-test", ProducerVersion: "1.0.0", Category: "verification", Type: "receipt.retry", Deterministic: true, Status: evidence.StatusPass, Severity: evidence.SeverityInfo, Confidence: 1, Summary: "Receipt retry tested", InputHashes: map[string]string{spec.ContractInputHash: hash}, CreatedAt: now}
	if edit != nil {
		edit(&f)
	}
	e, err := evidence.NewEvidence(f)
	if err != nil {
		t.Fatal(err)
	}
	return policy.Input{TenantID: "org", RepositoryID: "repo", ChangeID: "change", RunID: "run", AsOf: now, Criticality: policy.CriticalityUnknown, Evidence: []evidence.Evidence{e}}
}

func TestDoctorExplicitConstraintsAndAdvisoryAnalysis(t *testing.T) {
	c := contract(t)
	raw, _ := c.CanonicalJSON()
	d := spec.SpecDoctor{}
	v, err := d.Validate(t.Context(), raw)
	if err != nil || v.Status != evidence.StatusPass || v.Code != "VALID_EXPLICIT_CONSTRAINTS" || v.ContractHash == "" {
		t.Fatal(v, err)
	}
	bad := bytes.Replace(raw, []byte(`"forbidden_behaviors":[]`), []byte(`"forbidden_behaviors":["charge"]`), 1)
	bad = bytes.Replace(bad, []byte(`"required_behaviors":[]`), []byte(`"required_behaviors":["charge"]`), 1)
	v, err = d.Validate(t.Context(), bad)
	if err == nil || v.Status != evidence.StatusFail {
		t.Fatal(v, err)
	}
	for _, bad := range [][]byte{nil, []byte(`{"goal":"secret","goal":"override"}`), bytes.Repeat([]byte("x"), intent.MaxContractBytes+1)} {
		v, err := d.Validate(t.Context(), bad)
		if err == nil || v.Status == evidence.StatusPass || strings.Contains(err.Error(), "secret") {
			t.Fatal(v, err)
		}
	}
	clear, _ := d.Analyze(t.Context(), c)
	if clear.AmbiguityScore != 0 || len(clear.Suggestions) != 0 {
		t.Fatal(clear)
	}
	vague, _ := intent.NewIntentContract(intent.IntentContractFields{Goal: "Make retries fast", Requirements: []string{"Return receipt", "It should be secure"}, AllowedComponents: []string{"fast/secure"}})
	a, err := d.Analyze(t.Context(), vague)
	if err != nil || a.AmbiguityScore != 67 || a.StatementsExamined != 3 || a.FlaggedStatements != 2 || len(a.Suggestions) != 3 {
		t.Fatal(a, err)
	}
	a.Suggestions[0].Message = "mutated"
	again, _ := d.Analyze(t.Context(), vague)
	if again.Suggestions[0].Message == "mutated" {
		t.Fatal("mutable shared analysis")
	}
	if after, _ := c.CanonicalJSON(); !bytes.Equal(raw, after) {
		t.Fatal("doctor rewrote contract")
	}
}

func TestForgeRunsSharedPolicyWithCompleteCoverage(t *testing.T) {
	c := contract(t)
	s := generate(t, c, bindings(t, c))
	for _, status := range []evidence.Status{evidence.StatusPass, evidence.StatusFail, evidence.StatusWarn, evidence.StatusError, evidence.StatusSkipped, evidence.StatusUnavailable, evidence.StatusUnknown} {
		t.Run(string(status), func(t *testing.T) {
			in := observation(t, c, func(f *evidence.EvidenceFields) { f.Status = status })
			r, err := s.Evaluate(t.Context(), in)
			want := evidence.StatusUnknown
			if status == evidence.StatusPass || status == evidence.StatusFail {
				want = status
			}
			if status == evidence.StatusWarn {
				want = evidence.StatusFail
			}
			if err != nil || r.Status != want || len(r.UnboundAssertions) != 0 {
				t.Fatal(r, err)
			}
		})
	}
	for _, scenario := range []string{"missing", "nondeterministic", "low_confidence", "wrong_producer", "conflicting_results"} {
		t.Run(scenario, func(t *testing.T) {
			in := observation(t, c, func(f *evidence.EvidenceFields) {
				switch scenario {
				case "nondeterministic":
					f.Deterministic = false
				case "low_confidence":
					f.Confidence = 0.1
				case "wrong_producer":
					f.Producer = "llm"
				}
			})
			want := evidence.StatusUnknown
			if scenario == "missing" {
				in.Evidence = nil
			}
			if scenario == "conflicting_results" {
				other := observation(t, c, func(f *evidence.EvidenceFields) { f.EvidenceID = "fail"; f.Status = evidence.StatusFail })
				in.Evidence = append(in.Evidence, other.Evidence...)
				want = evidence.StatusFail
			}
			r, err := s.Evaluate(t.Context(), in)
			if err != nil || r.Status != want {
				t.Fatal(r, err)
			}
		})
	}
}

func TestUnboundConstraintsCannotDisappear(t *testing.T) {
	f, _ := contract(t).Fields()
	f.ForbiddenBehaviors = []string{"Repeat a charge"}
	f.AllowedComponents = []string{"payments"}
	f.SecurityConstraints = []string{"Authorize receipt access"}
	c, _ := intent.NewIntentContract(f)
	b := bindings(t, c)
	s := generate(t, c, b[:1])
	r, err := s.Evaluate(t.Context(), observation(t, c, nil))
	if err != nil || r.Status != evidence.StatusUnknown || r.Code != "UNBOUND_ASSERTIONS" || len(r.UnboundAssertions) != 3 {
		t.Fatal(r, err)
	}
	r, err = s.Evaluate(t.Context(), observation(t, c, func(f *evidence.EvidenceFields) { f.Status = evidence.StatusFail }))
	if err != nil || r.Status != evidence.StatusFail || len(r.UnboundAssertions) != 3 {
		t.Fatal(r, err)
	}
	// Even a check for a forbidden behavior must PASS a reviewed absence test.
	s = generate(t, c, b)
	r, err = s.Evaluate(t.Context(), observation(t, c, func(f *evidence.EvidenceFields) { f.Status = evidence.StatusFail }))
	if err != nil || r.Status != evidence.StatusFail {
		t.Fatal("inverted forbidden condition", r, err)
	}
}

func TestSpecificationRejectsForeignOrUnboundEvidence(t *testing.T) {
	c := contract(t)
	s := generate(t, c, bindings(t, c))
	for _, scenario := range []string{"tenant", "repository", "change", "run", "duplicate", "future", "hash", "missing_hash", "invalid_envelope"} {
		t.Run(scenario, func(t *testing.T) {
			in := observation(t, c, func(f *evidence.EvidenceFields) {
				switch scenario {
				case "tenant":
					f.TenantID = "foreign"
				case "change":
					f.ChangeID = "foreign"
				case "run":
					f.RunID = "foreign"
				case "future":
					f.CreatedAt = f.CreatedAt.Add(time.Hour)
				case "hash":
					f.InputHashes[spec.ContractInputHash] = evidence.Digest([]byte("other"))
				case "missing_hash":
					f.InputHashes = nil
				}
			})
			if scenario == "repository" {
				in.RepositoryID = "foreign"
			}
			if scenario == "duplicate" {
				in.Evidence = append(in.Evidence, in.Evidence[0])
			}
			if scenario == "invalid_envelope" {
				in.Evidence = []evidence.Evidence{{}}
			}
			r, err := s.Evaluate(t.Context(), in)
			if err == nil || r.Status != evidence.StatusError || r.Checks != nil {
				t.Fatal(r, err)
			}
		})
	}
}

func TestSpecificationImmutableRoundTripAndHostileInputs(t *testing.T) {
	c := contract(t)
	b := bindings(t, c)
	s := generate(t, c, b)
	raw, _ := s.MarshalJSON()
	hash := s.ContentHash()
	b[0].Evidence.Producer = "mutated"
	copyRaw, _ := s.MarshalJSON()
	copyRaw[0] = '!'
	after, _ := s.MarshalJSON()
	if !bytes.Equal(raw, after) || s.ContentHash() != hash {
		t.Fatal("mutable specification")
	}
	restored, err := spec.DecodeSpecification(t.Context(), raw)
	if err != nil || restored.ContentHash() != hash {
		t.Fatal(err)
	}
	r, err := restored.Evaluate(t.Context(), observation(t, c, nil))
	if err != nil || r.Status != evidence.StatusPass {
		t.Fatal(r, err)
	}
	if err := json.Unmarshal(raw, &s); !errors.Is(err, spec.ErrImmutableSpecification) || s.ContentHash() != hash {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		nil, []byte(`{}`), []byte(`null`), bytes.Repeat([]byte("x"), spec.MaxSpecificationBytes+1),
		bytes.Replace(raw, []byte(`"schema":`), []byte(`"Schema":`), 1),
		bytes.Replace(raw, []byte(`"tenant_id":"org"`), []byte(`"tenant_id":"org","tenant_id":"evil"`), 1),
		bytes.Replace(raw, []byte(`"producer_version":"1.0.0"`), []byte(`"producer_version":"999.0.0"`), 1),
		bytes.Replace(raw, []byte(`"repository_id":"repo"`), []byte(`"repository_id":"../secret"`), 1),
		bytes.Replace(raw, []byte(`"min_confidence":0.9`), []byte(`"min_confidence":null`), 1),
		bytes.Replace(raw, []byte(`"producer":"receipt-test"`), []byte(`"Producer":"receipt-test"`), 1),
		bytes.Replace(raw, []byte(`"bindings":[`), []byte(`"approved":true,"bindings":[`), 1),
		append(bytes.Clone(raw), []byte(`{}`)...),
	} {
		if _, err := spec.DecodeSpecification(t.Context(), bad); err == nil {
			t.Fatal("accepted hostile specification")
		}
	}
	// Prose is preserved as data, without treating shell text/URLs as programs.
	f, _ := c.Fields()
	f.AcceptanceCriteria = []string{"$(echo secret); https://169.254.169.254/ must remain literal"}
	literal, _ := intent.NewIntentContract(f)
	artifact := generate(t, literal, bindings(t, literal))
	encoded, _ := artifact.MarshalJSON()
	if !bytes.Contains(encoded, []byte("169.254.169.254")) {
		t.Fatal("lost original constraint")
	}
}

func TestInvalidBindingsAndContexts(t *testing.T) {
	c := contract(t)
	valid := bindings(t, c)
	for _, scenario := range []string{"empty", "duplicate", "unknown", "oversized", "producer", "type", "confidence", "nan"} {
		t.Run(scenario, func(t *testing.T) {
			b := append([]spec.Binding{}, valid...)
			switch scenario {
			case "empty":
				b = nil
			case "duplicate":
				b = append(b, b[0])
			case "unknown":
				b[0].AssertionID = "nonexistent"
			case "oversized":
				b = make([]spec.Binding, spec.MaxBindings+1)
			case "producer":
				b[0].Evidence.Producer = "sh -c payload"
			case "type":
				b[0].Evidence.Type = "$(payload)"
			case "confidence":
				b[0].Evidence.MinConfidence = 2
			case "nan":
				b[0].Evidence.MinConfidence = math.NaN()
			}
			if _, err := (spec.SpecForge{}).Generate(t.Context(), c, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, b); !errors.Is(err, spec.ErrInvalidBinding) {
				t.Fatal(err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := generate(t, c, valid)
	raw, _ := s.MarshalJSON()
	contractRaw, _ := c.CanonicalJSON()
	for _, ctx := range []context.Context{nil, ctx} {
		if _, err := (spec.SpecDoctor{}).Validate(ctx, contractRaw); err == nil {
			t.Fatal("context ignored")
		}
		if _, err := (spec.SpecDoctor{}).Analyze(ctx, c); err == nil {
			t.Fatal("context ignored")
		}
		if _, err := (spec.SpecForge{}).Generate(ctx, c, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, valid); err == nil {
			t.Fatal("context ignored")
		}
		if _, err := spec.DecodeSpecification(ctx, raw); err == nil {
			t.Fatal("context ignored")
		}
		if r, err := s.Evaluate(ctx, observation(t, c, nil)); err == nil || r.Status != evidence.StatusError {
			t.Fatal("context ignored")
		}
	}
	if _, err := (spec.SpecDoctor{}).Analyze(t.Context(), intent.IntentContract{}); err == nil {
		t.Fatal("zero contract")
	}
	if _, err := (spec.Specification{}).MarshalJSON(); err == nil {
		t.Fatal("zero specification")
	}
	if _, err := (spec.Specification{}).Evaluate(t.Context(), observation(t, c, nil)); err == nil {
		t.Fatal("zero specification")
	}
}

func TestConcurrentSpecificationReads(t *testing.T) {
	c := contract(t)
	s := generate(t, c, bindings(t, c))
	in := observation(t, c, nil)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 10 {
				r, err := s.Evaluate(t.Context(), in)
				if err != nil || r.Status != evidence.StatusPass {
					t.Error(r, err)
				}
				if _, err := (spec.SpecDoctor{}).Analyze(t.Context(), c); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}

func FuzzDecodeSpecification(f *testing.F) {
	f.Add([]byte(`{}`))
	c, _ := intent.NewIntentContract(intent.IntentContractFields{Goal: "Receipts", Requirements: []string{"Keep receipts"}})
	a, _ := spec.Assertions(c)
	s, _ := (spec.SpecForge{}).Generate(context.Background(), c, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, []spec.Binding{{AssertionID: a[0].ID, Evidence: policy.EvidenceRequirement{Producer: "test", Type: "receipt.check"}}})
	raw, _ := s.MarshalJSON()
	f.Add(raw)
	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := spec.DecodeSpecification(t.Context(), raw)
		if err != nil {
			return
		}
		canonical, err := s.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		again, err := spec.DecodeSpecification(t.Context(), canonical)
		if err != nil || again.ContentHash() != s.ContentHash() {
			t.Fatal("unstable artifact", err)
		}
	})
}
