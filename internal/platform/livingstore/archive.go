// Package livingstore uses the existing immutable, tenant-scoped evidence store.
package livingstore

import (
	"context"

	"safepatch/internal/evidence"
	"safepatch/internal/proof/living"
)

type Archive struct{ Store evidence.OriginalStore }

// Save preserves graph and evaluation as independent content-addressed objects.
// A failed second write may leave an immutable graph, never a partial success.
// Callers must apply entitlement, permission and storage/retention admission.
func (a Archive) Save(ctx context.Context, g living.ProofDependencyGraph, p living.LivingProof) (evidence.OriginalReference, error) {
	bad := evidence.OriginalReference{}
	f, err := g.Fields()
	if ctx == nil || a.Store == nil || err != nil {
		return bad, living.ErrInvalid
	}
	raw, err := p.MarshalJSON()
	if err != nil {
		return bad, err
	}
	if _, err := living.Decode(ctx, g, raw); err != nil {
		return bad, err
	}
	scope := evidence.ArchiveScope{OrganizationID: f.Change.OrganizationID, Subject: evidence.Subject{RepositoryID: f.Change.RepositoryID, Revision: f.Change.HeadSHA}}
	graphRaw, _ := g.MarshalJSON()
	for _, data := range [][]byte{graphRaw, raw} {
		if err := ctx.Err(); err != nil {
			return bad, err
		}
		ref, err := a.Store.Put(ctx, scope, data)
		if err != nil || ref.Validate() != nil || ref.SHA256 != evidence.Digest(data) || ref.Bytes != int64(len(data)) || ctx.Err() != nil {
			return bad, evidence.ErrOriginalEvidence
		}
		bad = ref
	}
	return bad, nil
}

// Read returns historical evidence only, never a current entitlement or proof
// freshness decision. Reevaluate via LivingProofService for current observations.
func (a Archive) Read(ctx context.Context, scope evidence.ArchiveScope, graphRef, proofRef evidence.OriginalReference) (living.ProofDependencyGraph, living.LivingProof, error) {
	badGraph, badProof := living.ProofDependencyGraph{}, living.LivingProof{}
	if ctx == nil || a.Store == nil || scope.Validate() != nil {
		return badGraph, badProof, living.ErrInvalid
	}
	read := func(ref evidence.OriginalReference) ([]byte, error) {
		if ref.Validate() != nil || ref.Bytes > living.MaxBytes {
			return nil, living.ErrInvalid
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := a.Store.Get(ctx, scope, ref)
		if err != nil || int64(len(raw)) != ref.Bytes || evidence.Digest(raw) != ref.SHA256 || ctx.Err() != nil {
			return nil, evidence.ErrOriginalEvidence
		}
		return raw, nil
	}
	raw, err := read(graphRef)
	if err != nil {
		return badGraph, badProof, err
	}
	g, err := living.DecodeGraph(raw)
	if err != nil {
		return badGraph, badProof, err
	}
	f, _ := g.Fields()
	if f.Change.OrganizationID != scope.OrganizationID || f.Change.RepositoryID != scope.Subject.RepositoryID || f.Change.HeadSHA != scope.Subject.Revision {
		return badGraph, badProof, evidence.ErrOriginalEvidence
	}
	raw, err = read(proofRef)
	if err != nil {
		return badGraph, badProof, err
	}
	p, err := living.Decode(ctx, g, raw)
	if err != nil {
		return badGraph, badProof, err
	}
	return g, p, nil
}
