package living

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
)

func fixture(t testing.TB) (ProofDependencyGraph, Snapshot, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	f := GraphFields{Schema: GraphSchema, ID: "graph", Change: domain.Change{OrganizationID: "org", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}, CreatedAt: now, Nodes: []Node{}}
	for _, n := range []Node{{ID: "input", Kind: Input}, {ID: "other_input", Kind: Input}, {ID: "first", Kind: Proof, Dependencies: []string{"input"}}, {ID: "second", Kind: Proof, Dependencies: []string{"first"}}, {ID: "other", Kind: Proof, Dependencies: []string{"other_input"}}} {
		n.Digest = evidence.Digest([]byte(n.ID))
		n.RecordedAt = now
		n.ValidUntil = now.Add(time.Hour)
		n.Status = evidence.StatusUnknown
		if n.Kind == Proof {
			n.Status = evidence.StatusPass
		}
		f.Nodes = append(f.Nodes, n)
	}
	g, err := NewGraph(f)
	if err != nil {
		t.Fatal(err)
	}
	s := Snapshot{GraphDigest: g.Digest(), ObservedAt: now, ValidUntil: now.Add(time.Minute), Nodes: []Observation{}}
	fields, _ := g.Fields()
	for _, n := range fields.Nodes {
		s.Nodes = append(s.Nodes, Observation{n.ID, true, n.Digest})
	}
	return g, s, now
}

func TestTransitiveInvalidationAndImmutableHistory(t *testing.T) {
	g, s, now := fixture(t)
	original, _ := g.MarshalJSON()
	p, err := Evaluate(t.Context(), g, s, now)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := p.MarshalJSON()
	fields, _ := p.Fields()
	if fields.State != Current || len(fields.NeedsReverification) != 0 {
		t.Fatal(fields)
	}
	for i := range s.Nodes {
		if s.Nodes[i].ID == "input" {
			s.Nodes[i].Digest = evidence.Digest([]byte("changed"))
		}
	}
	stale, err := Evaluate(t.Context(), g, s, now)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := stale.Fields()
	if f.State != Stale || !slices.Equal(f.NeedsReverification, []string{"first", "second"}) {
		t.Fatal(f)
	}
	for _, proof := range f.Proofs {
		if proof.ID == "other" && proof.State != Current {
			t.Fatal(proof)
		}
	}
	unchanged, _ := p.MarshalJSON()
	graphBytes, _ := g.MarshalJSON()
	if !bytes.Equal(before, unchanged) || !bytes.Equal(original, graphBytes) {
		t.Fatal("history rewritten")
	}
	decoded, err := Decode(t.Context(), g, unchanged)
	if err != nil || decoded.Digest() != p.Digest() {
		t.Fatal(err)
	}
	// Historical decoding is not a claim that the old result is still fresh now.
	if _, err := Evaluate(t.Context(), g, s, now.Add(time.Minute)); err == nil {
		t.Fatal("expired snapshot accepted")
	}
}

func TestMissingFailureExpiryAndPoisonedClaimsNeverCurrent(t *testing.T) {
	for _, mode := range []string{"missing", "unavailable", "failed", "unknown", "expired"} {
		t.Run(mode, func(t *testing.T) {
			g, s, now := fixture(t)
			f, _ := g.Fields()
			want := Unknown
			switch mode {
			case "missing":
				s.Nodes = nil
			case "unavailable":
				for i := range s.Nodes {
					if s.Nodes[i].ID == "input" {
						s.Nodes[i].Available = false
						s.Nodes[i].Digest = ""
					}
				}
			case "failed", "unknown", "expired":
				for i := range f.Nodes {
					if f.Nodes[i].ID == "first" {
						if mode == "failed" {
							f.Nodes[i].Status = evidence.StatusFail
							want = Failed
						}
						if mode == "unknown" {
							f.Nodes[i].Status = evidence.StatusUnknown
						}
						if mode == "expired" {
							f.Nodes[i].RecordedAt = now.Add(-time.Hour)
							f.Nodes[i].ValidUntil = now
							want = Expired
						}
					}
				}
				g, _ = NewGraph(f)
				s.GraphDigest = g.Digest()
			}
			p, err := Evaluate(t.Context(), g, s, now)
			if err != nil {
				t.Fatal(err)
			}
			fields, _ := p.Fields()
			if fields.State != want {
				t.Fatal(fields)
			}
			raw, _ := p.MarshalJSON()
			raw = bytes.Replace(raw, []byte(`"state":"`+string(want)+`"`), []byte(`"state":"CURRENT"`), 1)
			if _, err := Decode(t.Context(), g, raw); err == nil {
				t.Fatal("poisoned result accepted")
			}
		})
	}
}

func TestGraphValidationCanonicalCopiesAndCycles(t *testing.T) {
	g, _, _ := fixture(t)
	original, _ := g.MarshalJSON()
	f, _ := g.Fields()
	slices.Reverse(f.Nodes)
	other, err := NewGraph(f)
	if err != nil || other.Digest() != g.Digest() {
		t.Fatal(err)
	}
	f.Nodes[0].Digest = strings.Repeat("0", 64)
	raw, _ := g.MarshalJSON()
	if !bytes.Equal(raw, original) {
		t.Fatal("mutable graph")
	}
	for _, mode := range []string{"cycle", "missing", "duplicate", "self", "bad_id", "bad_digest", "empty", "no_proofs", "bounds", "unknown_status", "unknown_kind", "empty_proof"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := g.Fields()
			switch mode {
			case "cycle":
				for i := range f.Nodes {
					if f.Nodes[i].ID == "first" {
						f.Nodes[i].Dependencies = []string{"second"}
					}
				}
			case "missing":
				f.Nodes[0].Dependencies = []string{"missing"}
			case "duplicate":
				f.Nodes = append(f.Nodes, f.Nodes[0])
			case "self":
				f.Nodes[0].Dependencies = []string{f.Nodes[0].ID}
			case "bad_id":
				f.Nodes[0].ID = "../escape"
			case "bad_digest":
				f.Nodes[0].Digest = "https://attacker.test"
			case "empty":
				f.Nodes = nil
			case "no_proofs":
				f.Nodes = []Node{f.Nodes[1]}
			case "bounds":
				f.Nodes = make([]Node, MaxNodes+1)
			case "unknown_status":
				f.Nodes[0].Status = "PASS_FROM_LLM"
			case "unknown_kind":
				f.Nodes[0].Kind = "script"
			case "empty_proof":
				f.Nodes[0].Dependencies = nil
			}
			if _, err := NewGraph(f); err == nil {
				t.Fatal(mode)
			}
		})
	}
	for _, raw := range [][]byte{append(original, []byte(`{}`)...), bytes.Replace(original, []byte(`"schema":`), []byte(`"Schema":`), 1), bytes.Replace(original, []byte(`"id":"graph"`), []byte(`"id":"graph","id":"evil"`), 1), []byte(`null`), bytes.Repeat([]byte(" "), MaxBytes+1)} {
		if _, err := DecodeGraph(raw); err == nil {
			t.Fatal("unsafe JSON accepted")
		}
	}
}

func TestSnapshotIsolationBoundsAndCancellation(t *testing.T) {
	for _, mode := range []string{"foreign_graph", "future", "old", "wide", "duplicate", "unknown_node", "bad_digest", "unavailable_digest"} {
		t.Run(mode, func(t *testing.T) {
			g, s, now := fixture(t)
			switch mode {
			case "foreign_graph":
				s.GraphDigest = strings.Repeat("a", 64)
			case "future":
				s.ObservedAt = now.Add(time.Second)
			case "old":
				s.ValidUntil = now
			case "wide":
				s.ValidUntil = now.Add(time.Hour)
			case "duplicate":
				s.Nodes = append(s.Nodes, s.Nodes[0])
			case "unknown_node":
				s.Nodes[0].ID = "foreign"
			case "bad_digest":
				s.Nodes[0].Digest = "bad"
			case "unavailable_digest":
				s.Nodes[0].Available = false
			}
			if p, err := Evaluate(t.Context(), g, s, now); err == nil || p.Digest() != "" {
				t.Fatal(mode)
			}
		})
	}
	g, s, now := fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if _, err := Evaluate(ctx, g, s, now); err == nil {
			t.Fatal("invalid context")
		}
	}
	f, _ := g.Fields()
	for i := range f.Nodes {
		if f.Nodes[i].ID == "first" {
			f.Nodes[i].ValidUntil = now.Add(time.Second)
		}
	}
	g, _ = NewGraph(f)
	s.GraphDigest = g.Digest()
	p, err := Evaluate(t.Context(), g, s, now)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := p.Fields()
	if !result.ValidUntil.Equal(now.Add(time.Second)) {
		t.Fatal("result outlives proof")
	}
}

func FuzzGraph(f *testing.F) {
	g, _, _ := fixture(f)
	raw, _ := g.MarshalJSON()
	f.Add(raw)
	f.Fuzz(func(t *testing.T, raw []byte) {
		g, err := DecodeGraph(raw)
		if err == nil {
			encoded, err := json.Marshal(g)
			if err != nil {
				t.Fatal(err)
			}
			again, err := DecodeGraph(encoded)
			if err != nil || again.Digest() != g.Digest() {
				t.Fatal(err)
			}
		}
	})
}
