package living

import (
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
)

func TestPassportPinsReuseSchemaAndNeverPromoteUnknown(t *testing.T) {
	g, _, now := fixture(t)
	gf, _ := g.Fields()
	digest := evidence.Digest([]byte("evidence"))
	p, err := passport.New(passport.Fields{Change: gf.Change, ChangeID: gf.Change.ID(), RunID: "run", Commit: gf.Change.HeadSHA, IntentDigest: digest, ProofPlanDigest: digest, EvidenceGraphDigest: digest, ProofLevel: passport.UnknownLevel, VerificationDecision: trust.Unknown, PolicyDecision: passport.PolicyDecision{PolicyDigest: digest, ResultDigest: digest, Status: evidence.StatusUnknown}, IssuedAt: now, ArtifactDigest: &digest, SBOM: &passport.Reference{Digest: digest, MediaType: "application/json"}, Provenance: &passport.Reference{Digest: digest, MediaType: "application/json"}})
	if err != nil {
		t.Fatal(err)
	}
	original := p.Digest()
	g, err = FromPassport(p, "graph", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := g.Fields()
	if len(f.Nodes) != 10 || p.Digest() != original {
		t.Fatal("references missing or Passport changed")
	}
	s := Snapshot{GraphDigest: g.Digest(), ObservedAt: now, ValidUntil: now.Add(time.Minute), Nodes: []Observation{}}
	for _, n := range f.Nodes {
		s.Nodes = append(s.Nodes, Observation{ID: n.ID, Available: true, Digest: n.Digest})
	}
	result, err := Evaluate(t.Context(), g, s, now)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := result.Fields()
	if fields.State != Unknown {
		t.Fatal("UNKNOWN promoted", fields)
	}
	if _, err := FromPassport(passport.Passport{}, "graph", now); err == nil {
		t.Fatal("invalid Passport")
	}
}
