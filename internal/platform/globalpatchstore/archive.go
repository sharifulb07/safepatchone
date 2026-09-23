// Package globalpatchstore reuses immutable tenant-scoped evidence storage.
package globalpatchstore

import (
	"context"
	"slices"

	"safepatch/internal/application"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
)

type Archive struct{ Store evidence.OriginalStore }

var _ application.GlobalPatchArchive = Archive{}

// The lexically first repository anchors storage, but grants no access to the
// remaining repositories. Use GlobalPatchService for external/worker admission.
func scope(l application.GlobalPatchLocator) evidence.ArchiveScope {
	return evidence.ArchiveScope{OrganizationID: l.OrganizationID, Subject: evidence.Subject{RepositoryID: l.RepositoryIDs[0], Revision: string(l.GlobalChangeID)}}
}
func (a Archive) Save(ctx context.Context, p graph.GlobalPatch) (application.GlobalPatchLocator, error) {
	bad := application.GlobalPatchLocator{}
	f, err := p.Fields()
	if ctx == nil || a.Store == nil || err != nil {
		return bad, application.ErrGlobalPatchUnavailable
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	l := application.GlobalPatchLocator{OrganizationID: f.OrganizationID, GlobalChangeID: p.ID(), RepositoryIDs: []string{}}
	for _, c := range f.Changes {
		l.RepositoryIDs = append(l.RepositoryIDs, c.RepositoryID)
	}
	raw, _ := p.MarshalJSON()
	l.Reference, err = a.Store.Put(ctx, scope(l), raw)
	if err != nil || ctx.Err() != nil || l.Validate() != nil || l.Reference.SHA256 != evidence.Digest(raw) || l.Reference.Bytes != int64(len(raw)) {
		return bad, application.ErrGlobalPatchUnavailable
	}
	return l, nil
}
func (a Archive) Read(ctx context.Context, l application.GlobalPatchLocator) (graph.GlobalPatch, error) {
	bad := graph.GlobalPatch{}
	l.RepositoryIDs = slices.Clone(l.RepositoryIDs)
	if ctx == nil || a.Store == nil || l.Validate() != nil {
		return bad, application.ErrGlobalPatchUnavailable
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	raw, err := a.Store.Get(ctx, scope(l), l.Reference)
	if err != nil || ctx.Err() != nil || int64(len(raw)) != l.Reference.Bytes || evidence.Digest(raw) != l.Reference.SHA256 {
		return bad, application.ErrGlobalPatchUnavailable
	}
	p, err := graph.DecodeGlobalPatch(raw)
	if err != nil || p.ID() != l.GlobalChangeID {
		return bad, application.ErrGlobalPatchUnavailable
	}
	f, _ := p.Fields()
	repos := []string{}
	for _, c := range f.Changes {
		repos = append(repos, c.RepositoryID)
	}
	if f.OrganizationID != l.OrganizationID || !slices.Equal(repos, l.RepositoryIDs) {
		return bad, application.ErrGlobalPatchUnavailable
	}
	return p, nil
}
