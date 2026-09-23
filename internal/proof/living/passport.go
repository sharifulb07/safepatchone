package living

import (
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
)

// FromPassport pins existing verification artifacts without changing Passport's
// schema. The caller must load an authoritative completed Passport or verify its
// signature against organization trust policy first. Decoding alone is not trust.
// Only present optional references are included; this is not a completeness claim.
func FromPassport(p passport.Passport, id string, validUntil time.Time) (ProofDependencyGraph, error) {
	f, err := p.Fields()
	if err != nil {
		return ProofDependencyGraph{}, err
	}
	nodes := []Node{}
	dependencies := []string{}
	add := func(id, digest string) {
		nodes = append(nodes, Node{ID: id, Kind: Input, Digest: digest, Dependencies: []string{}, Status: evidence.StatusUnknown, RecordedAt: f.IssuedAt, ValidUntil: validUntil})
		dependencies = append(dependencies, id)
	}
	add("commit", evidence.Digest([]byte(f.Commit)))
	add("intent", f.IntentDigest)
	add("proof_plan", f.ProofPlanDigest)
	add("policy", f.PolicyDecision.PolicyDigest)
	add("policy_result", f.PolicyDecision.ResultDigest)
	add("evidence_graph", f.EvidenceGraphDigest)
	if f.ArtifactDigest != nil {
		add("artifact", *f.ArtifactDigest)
	}
	if f.SBOM != nil {
		add("sbom", f.SBOM.Digest)
	}
	if f.Provenance != nil {
		add("provenance", f.Provenance.Digest)
	}
	decision := trust.Decide(f.ProofLevel != passport.UnknownLevel, evidence.Status(f.VerificationDecision), f.PolicyDecision.Status)
	nodes = append(nodes, Node{ID: "proof", Kind: Proof, Digest: p.Digest(), Dependencies: dependencies, Status: evidence.Status(decision), RecordedAt: f.IssuedAt, ValidUntil: validUntil})
	return NewGraph(GraphFields{Schema: GraphSchema, ID: id, Change: f.Change, CreatedAt: f.IssuedAt, Nodes: nodes})
}
