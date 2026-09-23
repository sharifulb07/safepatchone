package quorum

import (
	"context"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
)

// Store records the canonical assessment and explanation edges using an already
// admitted single-repository graph write scope. GraphAccess, artifact metering and
// current retention remain orchestration responsibilities. Exact retries use the
// graph's immutable idempotency contract; a partial write must be retried, never
// treated as a complete stored result.
func (r QuorumResult) Store(ctx context.Context, store graph.EvidenceGraph, scope graph.Scope, id, intentDigest string) (evidence.Evidence, error) {
	bad := evidence.Evidence{}
	if ctx == nil || store == nil || scope.Validate() != nil {
		return bad, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	f, err := r.Fields()
	if err != nil || scope.TenantID != f.TenantID || len(scope.RepositoryIDs) != 1 || scope.RepositoryIDs[0] != f.RepositoryID || !scope.Contains(f.RepositoryID, f.AsOf) {
		return bad, ErrInvalid
	}
	node, err := r.Evidence(id, intentDigest)
	if err != nil {
		return bad, err
	}
	edges, err := r.Relationships(id)
	if err != nil {
		return bad, err
	}
	seen := map[string]bool{}
	for _, req := range f.Requirements {
		for _, observation := range req.Observations {
			if seen[observation.EvidenceID] {
				continue
			}
			seen[observation.EvidenceID] = true
			retained, err := store.GetEvidence(ctx, scope, observation.EvidenceID)
			if err != nil {
				return bad, err
			}
			n, err := retained.Fields()
			if err != nil || n.ContentHash != observation.Digest || n.TenantID != f.TenantID || n.ChangeID != f.ChangeID || n.RunID != f.RunID {
				return bad, ErrInvalid
			}
		}
	}
	if err := store.PutEvidence(ctx, scope, f.RepositoryID, node); err != nil {
		return bad, err
	}
	for _, edge := range edges {
		if err := store.AddRelationship(ctx, scope, edge); err != nil {
			return bad, err
		}
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	return node, nil
}
