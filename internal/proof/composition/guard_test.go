package composition

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/evidence"
	"safepatch/internal/proof"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
)

func fixture(t testing.TB) (Manifest, Input, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	d := evidence.Digest([]byte("evidence"))
	f := Fields{Schema: ManifestSchema, ID: "composition", OrganizationID: "org", PolicyDigest: d, Components: []Component{}, Connections: []Connection{{ID: "api", From: "a", To: "b", ContractDigest: d}}}
	input := Input{}
	for _, id := range []string{"a", "b"} {
		c := domain.Change{OrganizationID: "org", RepositoryID: "repo_" + id, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
		p, err := passport.New(passport.Fields{Change: c, ChangeID: c.ID(), RunID: "run_" + id, Commit: c.HeadSHA, IntentDigest: d, ProofPlanDigest: d, EvidenceGraphDigest: d, ProofLevel: proof.L2, VerificationDecision: trust.Pass, PolicyDecision: passport.PolicyDecision{PolicyDigest: d, ResultDigest: d, Status: evidence.StatusPass}, IssuedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		f.Components = append(f.Components, Component{ID: id, RepositoryID: c.RepositoryID, Commit: c.HeadSHA, PassportDigest: p.Digest()})
		input.Components = append(input.Components, ComponentProof{id, p, now.Add(time.Hour)})
	}
	m, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	a := Attestation{SubjectID: "api", ManifestDigest: m.Digest(), PolicyDigest: d, EvidenceDigest: d, Status: evidence.StatusPass, IssuedAt: now, ValidUntil: now.Add(time.Hour)}
	input.Connections = []Attestation{a}
	a.SubjectID = f.ID
	input.Combined = &a
	return m, input, now
}
func TestIndividualPassesDoNotProveComposition(t *testing.T) {
	for _, mode := range []string{"complete", "missing_combined", "missing_component", "missing_connection", "failed_combined", "failed_connection", "unknown", "expired", "expired_component"} {
		t.Run(mode, func(t *testing.T) {
			m, in, now := fixture(t)
			expected := trust.Unknown
			switch mode {
			case "complete":
				expected = trust.Pass
			case "missing_combined":
				in.Combined = nil
			case "missing_component":
				in.Components = nil
			case "missing_connection":
				in.Connections = nil
			case "failed_combined":
				in.Combined.Status = evidence.StatusFail
				expected = trust.Fail
			case "failed_connection":
				in.Connections[0].Status = evidence.StatusFail
				expected = trust.Fail
			case "unknown":
				in.Combined.Status = evidence.StatusUnknown
			case "expired":
				now = now.Add(time.Hour)
			case "expired_component":
				in.Components[0].ValidUntil = now.Add(time.Second)
				now = now.Add(time.Second)
			}
			r, err := (CompositionGuard{}).Evaluate(t.Context(), m, in, now)
			if err != nil {
				t.Fatal(err)
			}
			f, _ := r.Fields()
			if f.Decision != expected || len(f.Checks) != 4 {
				t.Fatal(f)
			}
		})
	}
}
func TestCompositionRejectsForeignReplayedAndPoisonedEvidence(t *testing.T) {
	for _, mode := range []string{"foreign_org", "foreign_repo", "commit", "passport_digest", "manifest", "policy", "contract", "duplicate_component", "extra_component", "duplicate_connection", "extra_connection", "status", "future", "bad_expiry", "bad_digest"} {
		t.Run(mode, func(t *testing.T) {
			m, in, now := fixture(t)
			switch mode {
			case "foreign_org", "foreign_repo", "commit":
				f, _ := in.Components[0].Passport.Fields()
				if mode == "foreign_org" {
					f.Change.OrganizationID = "victim"
				}
				if mode == "foreign_repo" {
					f.Change.RepositoryID = "victim"
				}
				if mode == "commit" {
					f.Change.HeadSHA = strings.Repeat("c", 40)
					f.Commit = f.Change.HeadSHA
				}
				f.ChangeID = f.Change.ID()
				in.Components[0].Passport, _ = passport.New(f)
			case "passport_digest":
				f, _ := m.Fields()
				f.Components[0].PassportDigest = strings.Repeat("0", 64)
				m, _ = New(f)
			case "manifest":
				in.Combined.ManifestDigest = strings.Repeat("0", 64)
			case "policy":
				in.Combined.PolicyDigest = strings.Repeat("0", 64)
			case "contract":
				f, _ := m.Fields()
				f.Connections[0].ContractDigest = strings.Repeat("0", 64)
				m, _ = New(f)
			case "duplicate_component":
				in.Components = append(in.Components, in.Components[0])
			case "extra_component":
				in.Components[0].ComponentID = "foreign"
			case "duplicate_connection":
				in.Connections = append(in.Connections, in.Connections[0])
			case "extra_connection":
				in.Connections[0].SubjectID = "foreign"
			case "status":
				in.Combined.Status = "LLM_PASS"
			case "future":
				in.Combined.IssuedAt = now.Add(time.Second)
			case "bad_expiry":
				in.Combined.ValidUntil = now
			case "bad_digest":
				in.Combined.EvidenceDigest = "https://attacker.test/proof"
			}
			if r, err := (CompositionGuard{}).Evaluate(t.Context(), m, in, now); err == nil || r.Digest() != "" {
				t.Fatal("unsafe composition accepted", mode)
			}
		})
	}
	m, in, now := fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if _, err := (CompositionGuard{}).Evaluate(ctx, m, in, now); err == nil {
			t.Fatal("context")
		}
	}
}
func TestManifestImmutabilityCanonicalizationAndBoundaries(t *testing.T) {
	m, in, now := fixture(t)
	original, _ := m.MarshalJSON()
	f, _ := m.Fields()
	slices.Reverse(f.Components)
	other, err := New(f)
	if err != nil || other.Digest() != m.Digest() {
		t.Fatal(err)
	}
	r, _ := (CompositionGuard{}).Evaluate(t.Context(), m, in, now)
	before, _ := r.MarshalJSON()
	rf, _ := r.Fields()
	rf.Checks[0].Reason = "poison"
	after, _ := r.MarshalJSON()
	if !bytes.Equal(before, after) {
		t.Fatal("mutable result")
	}
	in.Combined.Status = evidence.StatusFail
	_, _ = (CompositionGuard{}).Evaluate(t.Context(), m, in, now)
	after, _ = r.MarshalJSON()
	if !bytes.Equal(before, after) {
		t.Fatal("history changed")
	}
	for _, mode := range []string{"one", "many", "duplicate", "missing_endpoint", "self", "duplicate_connection", "combined_subject_collision", "path", "digest"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := m.Fields()
			switch mode {
			case "one":
				f.Components = f.Components[:1]
			case "many":
				f.Components = make([]Component, MaxComponents+1)
			case "duplicate":
				f.Components = append(f.Components, f.Components[0])
			case "missing_endpoint":
				f.Connections[0].To = "missing"
			case "self":
				f.Connections[0].To = f.Connections[0].From
			case "duplicate_connection":
				f.Connections = append(f.Connections, f.Connections[0])
			case "combined_subject_collision":
				f.Connections[0].ID = f.ID
			case "path":
				f.ID = "../escape"
			case "digest":
				f.PolicyDigest = "unknown"
			}
			if _, err := New(f); err == nil {
				t.Fatal(mode)
			}
		})
	}
	for _, raw := range [][]byte{[]byte(`null`), append(original, []byte(`{}`)...), bytes.Replace(original, []byte(`"schema":`), []byte(`"Schema":`), 1), bytes.Replace(original, []byte(`"id":"composition"`), []byte(`"id":"composition","id":"other"`), 1), bytes.Repeat([]byte(" "), MaxBytes+1)} {
		if _, err := Decode(raw); err == nil {
			t.Fatal("unsafe JSON")
		}
	}
}
func FuzzCompositionManifest(f *testing.F) {
	m, _, _ := fixture(f)
	raw, _ := m.MarshalJSON()
	f.Add(raw)
	f.Fuzz(func(t *testing.T, raw []byte) {
		m, err := Decode(raw)
		if err == nil {
			encoded, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Decode(encoded)
			if err != nil || again.Digest() != m.Digest() {
				t.Fatal(err)
			}
		}
	})
}
