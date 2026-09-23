package guard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/intent/guard"
	"safepatch/internal/policy"
	"safepatch/internal/spec"
	"safepatch/internal/tenant"
)

func diff(status string, paths ...string) []byte {
	oldMode, newMode := "100644", "100644"
	oldHash, newHash := strings.Repeat("a", 40), strings.Repeat("b", 40)
	if status == "A" {
		oldMode, oldHash = "000000", strings.Repeat("0", 40)
	}
	if status == "D" {
		newMode, newHash = "000000", strings.Repeat("0", 40)
	}
	return []byte(":" + oldMode + " " + newMode + " " + oldHash + " " + newHash + " " + status + "\x00" + strings.Join(paths, "\x00") + "\x00")
}

func fixture(t *testing.T) guard.Input {
	t.Helper()
	c, err := intent.NewIntentContract(intent.IntentContractFields{Goal: "Preserve receipt identity", AllowedComponents: []string{"payments"}, ForbiddenComponents: []string{"auth"}, ProtectedProperties: []string{"One receipt per identity"}})
	if err != nil {
		t.Fatal(err)
	}
	id := guard.Identity{TenantID: "org", RepositoryID: "repo", ChangeID: "change", RunID: "run", CreatedAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)}
	raw := diff("M", "payments/receipt.go")
	sem := guard.SemanticChanges{TenantID: id.TenantID, RepositoryID: id.RepositoryID, ChangeID: id.ChangeID, RunID: id.RunID, DiffHash: evidence.Digest(raw), Complete: true, Deterministic: true, Changes: []guard.SemanticChange{{Path: "payments/receipt.go", Component: "payments"}}}
	in := guard.Input{Identity: id, Contract: c, SemanticChanges: sem, GitDiff: guard.GitDiff{Raw: raw, Complete: true}}
	a, _ := spec.Assertions(c)
	b := []spec.Binding{}
	for _, p := range a {
		if p.Field == "protected_properties" {
			b = append(b, spec.Binding{AssertionID: p.ID, Evidence: policy.EvidenceRequirement{Producer: "receipt-test", Type: "receipt.identity", MinConfidence: 0.9}})
		}
	}
	s, err := (spec.SpecForge{}).Generate(t.Context(), c, tenant.Scope{OrganizationID: id.TenantID, ResourceID: id.RepositoryID}, b)
	if err != nil {
		t.Fatal(err)
	}
	in.Properties = &s
	refreshEvidence(t, &in, nil)
	return in
}

func refreshEvidence(t *testing.T, in *guard.Input, edit func(*evidence.EvidenceFields)) {
	t.Helper()
	hash, _ := in.Contract.ContentHash()
	semHash, err := in.SemanticChanges.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	f := evidence.EvidenceFields{EvidenceID: "source", TenantID: in.Identity.TenantID, ChangeID: in.Identity.ChangeID, RunID: in.Identity.RunID, Producer: "receipt-test", ProducerVersion: "1.0.0", Category: "verification", Type: "receipt.identity", Deterministic: true, Status: evidence.StatusPass, Severity: evidence.SeverityInfo, Confidence: 1, Summary: "Receipt property check", CreatedAt: in.Identity.CreatedAt, InputHashes: map[string]string{spec.ContractInputHash: hash, "git_diff": evidence.Digest(in.GitDiff.Raw), "semantic_changes": semHash}}
	if edit != nil {
		edit(&f)
	}
	node, err := evidence.NewEvidence(f)
	if err != nil {
		t.Fatal(err)
	}
	in.Evidence = []evidence.Evidence{node}
}

func TestIntentGuardProducesBoundImmutableEvidence(t *testing.T) {
	in := fixture(t)
	r, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
	if err != nil || r.IntentCompliance.Status != evidence.StatusPass || r.ScopeDrift.Status != evidence.StatusPass || len(r.ProtectedProperties) != 1 || r.ProtectedProperties[0].Status != evidence.StatusPass || !slices.Equal(r.ProtectedProperties[0].SourceEvidenceIDs, []string{"source"}) {
		t.Fatal(r, err)
	}
	if len(r.EvidenceIDs) != 3 || len(r.Evidence) != 3 {
		t.Fatal(r)
	}
	for i, node := range r.Evidence {
		f, _ := node.Fields()
		if f.EvidenceID != r.EvidenceIDs[i] || f.TenantID != "org" || f.ChangeID != "change" || f.RunID != "run" || f.Producer != "intentguard" || !f.Deterministic || f.ContentHash == "" || f.InputHashes["git_diff"] != evidence.Digest(in.GitDiff.Raw) {
			t.Fatal(f)
		}
		raw, _ := node.CanonicalJSON()
		if _, err := evidence.DecodeEvidence(raw); err != nil {
			t.Fatal(err)
		}
	}
	again, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
	if err != nil || !slices.Equal(r.EvidenceIDs, again.EvidenceIDs) {
		t.Fatal("unstable IDs", err)
	}
	first, _ := r.Evidence[1].CanonicalJSON()
	r.ScopeDrift.Drifts = append(r.ScopeDrift.Drifts, guard.Drift{Path: "injected"})
	r.ProtectedProperties[0].Status = evidence.StatusFail
	second, _ := r.Evidence[1].CanonicalJSON()
	if !bytes.Equal(first, second) {
		t.Fatal("mutable evidence")
	}
}

func TestScopeDriftChecksBothRenameSidesAndExactComponents(t *testing.T) {
	for _, scenario := range []string{"forbidden", "outside", "rename_from_forbidden", "rename_into_forbidden", "copy_from_forbidden", "exact_path_forbidden", "missing_mapping", "partial_diff", "partial_semantics", "nondeterministic"} {
		t.Run(scenario, func(t *testing.T) {
			in := fixture(t)
			want := evidence.StatusFail
			switch scenario {
			case "forbidden":
				in.SemanticChanges.Changes[0].Component = "auth"
			case "outside":
				in.SemanticChanges.Changes[0].Component = "payments-extra"
			case "rename_from_forbidden", "copy_from_forbidden":
				status := "R100"
				if scenario == "copy_from_forbidden" {
					status = "C100"
				}
				in.GitDiff.Raw = diff(status, "auth/receipt.go", "payments/receipt.go")
				in.SemanticChanges.Changes = append(in.SemanticChanges.Changes, guard.SemanticChange{Path: "auth/receipt.go", Component: "auth"})
			case "rename_into_forbidden":
				in.GitDiff.Raw = diff("R100", "payments/receipt.go", "auth/receipt.go")
				in.SemanticChanges.Changes = append(in.SemanticChanges.Changes, guard.SemanticChange{Path: "auth/receipt.go", Component: "auth"})
			case "exact_path_forbidden":
				in.GitDiff.Raw = diff("M", "auth")
				in.SemanticChanges.Changes = []guard.SemanticChange{{Path: "auth", Component: "payments"}}
			case "missing_mapping":
				in.SemanticChanges.Changes = nil
				want = evidence.StatusUnknown
			case "partial_diff":
				in.GitDiff.Complete = false
				want = evidence.StatusUnknown
			case "partial_semantics":
				in.SemanticChanges.Complete = false
				want = evidence.StatusUnknown
			case "nondeterministic":
				in.SemanticChanges.Deterministic = false
				want = evidence.StatusUnknown
			}
			in.SemanticChanges.DiffHash = evidence.Digest(in.GitDiff.Raw)
			refreshEvidence(t, &in, nil)
			r, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
			if err != nil || r.ScopeDrift.Status != want || r.IntentCompliance.Status != want {
				t.Fatal(r, err)
			}
		})
	}
}

func TestProtectedPropertiesNeverInferPassFromMissingOrFailedChecks(t *testing.T) {
	for _, status := range []evidence.Status{evidence.StatusPass, evidence.StatusFail, evidence.StatusWarn, evidence.StatusError, evidence.StatusSkipped, evidence.StatusUnavailable, evidence.StatusUnknown} {
		t.Run(string(status), func(t *testing.T) {
			in := fixture(t)
			refreshEvidence(t, &in, func(f *evidence.EvidenceFields) { f.Status = status })
			r, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
			want := evidence.StatusUnknown
			if status == evidence.StatusPass {
				want = evidence.StatusPass
			}
			if status == evidence.StatusFail || status == evidence.StatusWarn {
				want = evidence.StatusFail
			}
			if err != nil || r.ProtectedProperties[0].Status != want || r.IntentCompliance.Status != want {
				t.Fatal(r, err)
			}
		})
	}
	for _, scenario := range []string{"no_spec", "no_evidence", "non_deterministic_evidence", "low_confidence", "additional_constraint", "goal_only"} {
		t.Run(scenario, func(t *testing.T) {
			in := fixture(t)
			switch scenario {
			case "no_spec":
				in.Properties = nil
				in.Evidence = nil
			case "no_evidence":
				in.Evidence = nil
			case "non_deterministic_evidence":
				refreshEvidence(t, &in, func(f *evidence.EvidenceFields) { f.Deterministic = false })
			case "low_confidence":
				refreshEvidence(t, &in, func(f *evidence.EvidenceFields) { f.Confidence = 0.2 })
			case "additional_constraint", "goal_only":
				f, _ := in.Contract.Fields()
				if scenario == "additional_constraint" {
					f.SecurityConstraints = []string{"Only the receipt owner can read it"}
				} else {
					f = intent.IntentContractFields{Goal: "Secure receipts"}
				}
				in.Contract, _ = intent.NewIntentContract(f)
				in.Properties = nil
				in.Evidence = nil
			}
			r, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
			if err != nil || r.IntentCompliance.Status != evidence.StatusUnknown {
				t.Fatal(r, err)
			}
		})
	}
}

func TestInputAndEvidencePoisoningRejected(t *testing.T) {
	for _, scenario := range []string{"foreign_tenant", "foreign_repo", "foreign_change", "foreign_run", "stale_semantics", "extra_path", "duplicate_path", "unsafe_component", "unsafe_diff", "foreign_evidence", "duplicate_evidence", "stale_diff_evidence", "stale_semantic_evidence", "missing_hash", "wrong_contract", "no_spec_with_evidence", "zero_spec", "zero_contract", "zero_time", "oversized_semantics"} {
		t.Run(scenario, func(t *testing.T) {
			in := fixture(t)
			switch scenario {
			case "foreign_tenant":
				in.SemanticChanges.TenantID = "other"
			case "foreign_repo":
				in.SemanticChanges.RepositoryID = "other"
			case "foreign_change":
				in.SemanticChanges.ChangeID = "other"
			case "foreign_run":
				in.SemanticChanges.RunID = "other"
			case "stale_semantics":
				in.SemanticChanges.DiffHash = evidence.Digest([]byte("other"))
			case "extra_path":
				in.SemanticChanges.Changes[0].Path = "other"
			case "duplicate_path":
				in.SemanticChanges.Changes = append(in.SemanticChanges.Changes, in.SemanticChanges.Changes[0])
			case "unsafe_component":
				in.SemanticChanges.Changes[0].Component = "../secret"
			case "unsafe_diff":
				in.GitDiff.Raw = diff("M", "../secret")
			case "foreign_evidence":
				refreshEvidence(t, &in, func(f *evidence.EvidenceFields) { f.TenantID = "other" })
			case "duplicate_evidence":
				in.Evidence = append(in.Evidence, in.Evidence[0])
			case "stale_diff_evidence":
				refreshEvidence(t, &in, func(f *evidence.EvidenceFields) { f.InputHashes["git_diff"] = evidence.Digest([]byte("old")) })
			case "stale_semantic_evidence":
				refreshEvidence(t, &in, func(f *evidence.EvidenceFields) { f.InputHashes["semantic_changes"] = evidence.Digest([]byte("old")) })
			case "missing_hash":
				refreshEvidence(t, &in, func(f *evidence.EvidenceFields) { delete(f.InputHashes, "semantic_changes") })
			case "wrong_contract":
				f, _ := in.Contract.Fields()
				f.Goal = "Changed intent"
				in.Contract, _ = intent.NewIntentContract(f)
			case "no_spec_with_evidence":
				in.Properties = nil
			case "zero_spec":
				in.Properties = &spec.Specification{}
			case "zero_contract":
				in.Contract = intent.IntentContract{}
			case "zero_time":
				in.Identity.CreatedAt = time.Time{}
			case "oversized_semantics":
				in.SemanticChanges.Changes = make([]guard.SemanticChange, guard.MaxChangedPaths+1)
			}
			r, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
			if err == nil || r.IntentCompliance.Status != evidence.StatusError || len(r.EvidenceIDs) != 0 || len(r.Evidence) != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatal(r, err)
			}
		})
	}
}

func TestBasicScopeOnlyAndProtectedPropertiesWithoutScope(t *testing.T) {
	in := fixture(t)
	in.Contract, _ = intent.NewIntentContract(intent.IntentContractFields{Goal: "Change payments only", AllowedComponents: []string{"payments"}})
	in.Properties = nil
	in.Evidence = nil
	r, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
	if err != nil || r.IntentCompliance.Status != evidence.StatusPass || len(r.ProtectedProperties) != 0 {
		t.Fatal(r, err)
	}
	in = fixture(t)
	f, _ := in.Contract.Fields()
	f.AllowedComponents = nil
	f.ForbiddenComponents = nil
	in.Contract, _ = intent.NewIntentContract(f)
	a, _ := spec.Assertions(in.Contract)
	s, _ := (spec.SpecForge{}).Generate(t.Context(), in.Contract, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, []spec.Binding{{AssertionID: a[0].ID, Evidence: policy.EvidenceRequirement{Producer: "receipt-test", Type: "receipt.identity"}}})
	in.Properties = &s
	refreshEvidence(t, &in, nil)
	r, err = (guard.IntentGuard{}).Evaluate(t.Context(), in)
	if err != nil || r.IntentCompliance.Status != evidence.StatusPass || r.ScopeDrift.Status != evidence.StatusSkipped {
		t.Fatal(r, err)
	}
}

func TestSemanticOrderingAndConcurrentDeterminism(t *testing.T) {
	in := fixture(t)
	in.GitDiff.Raw = append(in.GitDiff.Raw, diff("A", "payments/other.go")...)
	in.SemanticChanges.DiffHash = evidence.Digest(in.GitDiff.Raw)
	in.SemanticChanges.Changes = append(in.SemanticChanges.Changes, guard.SemanticChange{Path: "payments/other.go", Component: "payments"})
	refreshEvidence(t, &in, nil)
	first, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(in.SemanticChanges.Changes)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			r, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
			if err != nil || !slices.Equal(r.EvidenceIDs, first.EvidenceIDs) {
				t.Error("nondeterministic", err)
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		r, err := (guard.IntentGuard{}).Evaluate(ctx, in)
		if err == nil || r.IntentCompliance.Status != evidence.StatusError {
			t.Fatal(err)
		}
	}
}

func TestRawDiffParserSecurityAndFormats(t *testing.T) {
	for _, status := range []string{"A", "D", "M", "M100", "T", "R086", "C100"} {
		paths := []string{"src/a file.go"}
		if status[0] == 'R' || status[0] == 'C' {
			paths = append(paths, "dst/a file.go")
		}
		if got, err := guard.ParseGitDiff(t.Context(), diff(status, paths...)); err != nil || len(got) != len(paths) {
			t.Fatal(status, got, err)
		}
	}
	for _, raw := range [][]byte{
		[]byte("diff --git a/x b/x\n"), diff("U", "a"), diff("X", "a"), diff("R", "a", "b"), diff("R101", "a", "b"), diff("M+1", "a"),
		diff("M", "../x"), diff("M", "/x"), diff("M", "C:\\secret"), diff("M", "a/../x"), diff("M", "a\nforged"), diff("M", "a\x1b[31m"), diff("M", "https://example.com"), diff("M", "a//b"),
		bytes.TrimSuffix(diff("M", "a"), []byte{0}), append(diff("M", "a"), 0),
		bytes.Replace(diff("M", "a"), []byte(strings.Repeat("a", 40)), []byte("abcdef"), 1),
		bytes.Replace(diff("M", "a"), []byte("100644"), []byte("999999"), 1),
		diff("M", strings.Repeat("x", guard.MaxPathBytes+1)), bytes.Repeat([]byte("x"), guard.MaxDiffBytes+1),
	} {
		if _, err := guard.ParseGitDiff(t.Context(), raw); !errors.Is(err, guard.ErrInvalidInput) {
			t.Fatal("accepted malformed diff", err)
		}
	}
	if p, err := guard.ParseGitDiff(t.Context(), nil); err != nil || p == nil || len(p) != 0 {
		t.Fatal(p, err)
	}
	for _, mode := range []string{"120000", "160000"} {
		raw := bytes.ReplaceAll(diff("M", "link"), []byte("100644"), []byte(mode))
		if _, err := guard.ParseGitDiff(t.Context(), raw); err != nil {
			t.Fatal(err)
		}
	}
}

func FuzzRawGitDiff(f *testing.F) {
	f.Add(diff("M", "payments/receipt.go"))
	f.Add(diff("R100", "a", "b"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		paths, err := guard.ParseGitDiff(t.Context(), raw)
		if err != nil {
			return
		}
		if len(paths) > guard.MaxChangedPaths || !slices.IsSorted(paths) {
			t.Fatal("invalid path set")
		}
		for _, p := range paths {
			if !evidence.ValidPath(p) {
				t.Fatal("unsafe path")
			}
		}
	})
}

func TestResultJSONHasMachineReadableStatusesAndIDs(t *testing.T) {
	r, _ := (guard.IntentGuard{}).Evaluate(t.Context(), fixture(t))
	raw, err := json.Marshal(r)
	if err != nil || !bytes.Contains(raw, []byte(`"intent_compliance":{"status":"PASS"`)) || !bytes.Contains(raw, []byte(`"evidence_ids":["ig.`)) {
		t.Fatal(string(raw), err)
	}
}
