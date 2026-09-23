package proof

import (
	"safepatch/internal/evidence"
	"safepatch/internal/proof/quorum"
	"safepatch/internal/verification"
	"slices"
)

// NewQuorumProofPlanner adds verification work from a trusted quorum catalog to
// the existing planner. It conservatively schedules all eligible capabilities at
// the supplied level, including contradiction-detecting sources. Provider access,
// readiness and cumulative budgets are still resolved by the shared planner.
// READY only means this work can be scheduled; it never satisfies the quorum.
func NewQuorumProofPlanner(version string, base []Obligation, providers []Provider, configured quorum.QuorumPolicy, level Level) (*ProofPlanner, error) {
	f, err := configured.Fields()
	if err != nil || level.Rank() < 0 {
		return nil, ErrInvalidPlanner
	}
	obligations := slices.Clone(base)
	used := map[string]bool{}
	for _, req := range f.Requirements {
		for _, id := range req.Sources {
			used[id] = true
		}
	}
	for _, s := range f.Sources {
		if used[s.ID] {
			obligations = append(obligations, Obligation{ID: "quorum." + evidence.Digest([]byte(s.ID)), Level: level, Capability: verification.Capability(s.Capability)})
		}
	}
	// Pin the complete policy in configuration identity, even when only a
	// threshold changes and the selected technical work stays the same.
	p, err := NewProofPlanner(version, obligations, providers)
	if err != nil {
		return nil, err
	}
	p.hash = evidence.Digest([]byte(p.hash + "\n" + configured.Digest()))
	return p, nil
}
