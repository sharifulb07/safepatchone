package application

import (
	"context"
	"slices"

	"safepatch/internal/authorization"
	"safepatch/internal/graph"
)

// ChangeGraphService reuses GraphAccess for every request/worker dispatch. View
// is fixed by trusted composition; no request can downgrade a multi-repo view.
type ChangeGraphService struct {
	access *GraphAccess
	store  graph.ChangeGraph
	view   GraphView
}

func NewChangeGraphService(access *GraphAccess, store graph.ChangeGraph, view GraphView) (*ChangeGraphService, error) {
	if access == nil || store == nil || !slices.Contains([]GraphView{GraphLocal, GraphOrganization, GraphCrossRepository}, view) {
		return nil, ErrInvalidGuard
	}
	return &ChangeGraphService{access, store, view}, nil
}
func (s *ChangeGraphService) admit(ctx context.Context, actor authorization.Principal, org string, revisions []graph.RevisionPin, write bool) (graph.RevisionScope, error) {
	if s == nil || s.access == nil || s.store == nil || len(revisions) == 0 || len(revisions) > graph.MaxRepositories {
		return graph.RevisionScope{}, ErrInvalidGuard
	}
	pins := slices.Clone(revisions)
	repos := make([]string, len(pins))
	for i, pin := range pins {
		repos[i] = pin.RepositoryID
	}
	access, err := s.access.Authorize(ctx, actor, org, repos, s.view, write)
	if err != nil {
		return graph.RevisionScope{}, err
	}
	scope := graph.RevisionScope{Access: access, Revisions: pins}
	if err := scope.Validate(); err != nil {
		return graph.RevisionScope{}, err
	}
	return scope, nil
}
func (s *ChangeGraphService) PutNode(ctx context.Context, actor authorization.Principal, org string, pins []graph.RevisionPin, node graph.ChangeNode) error {
	scope, err := s.admit(ctx, actor, org, pins, true)
	if err != nil {
		return err
	}
	f, err := node.Fields()
	if err != nil || f.TenantID != org || !scope.Includes(f.Ref, f.CreatedAt) {
		return graph.ErrInvalidGraph
	}
	return stageResult(ctx, "change graph write", s.store.PutChangeNode(ctx, scope, node))
}
func (s *ChangeGraphService) PutLink(ctx context.Context, actor authorization.Principal, org string, pins []graph.RevisionPin, link graph.ChangeLink) error {
	scope, err := s.admit(ctx, actor, org, pins, true)
	if err != nil {
		return err
	}
	f, err := link.Fields()
	if err != nil || f.TenantID != org || !scope.Includes(f.From, f.CreatedAt) || !scope.Includes(f.To, f.CreatedAt) {
		return graph.ErrInvalidGraph
	}
	return stageResult(ctx, "change graph link", s.store.PutChangeLink(ctx, scope, link))
}
func (s *ChangeGraphService) GetNode(ctx context.Context, actor authorization.Principal, org string, pins []graph.RevisionPin, ref graph.EntityRef) (graph.ChangeNode, error) {
	scope, err := s.admit(ctx, actor, org, pins, false)
	if err != nil {
		return graph.ChangeNode{}, err
	}
	if ref.Validate() != nil || !scope.Includes(ref, scope.Access.AsOf) {
		return graph.ChangeNode{}, graph.ErrNotFound
	}
	n, err := s.store.GetChangeNode(ctx, scope, ref)
	if err := stageResult(ctx, "change graph read", err); err != nil {
		return graph.ChangeNode{}, err
	}
	return n, nil
}
func (s *ChangeGraphService) Neighbors(ctx context.Context, actor authorization.Principal, org string, pins []graph.RevisionPin, q graph.ChangeQuery) (graph.ChangePage, error) {
	failure := graph.ChangePage{Links: []graph.ChangeLink{}}
	scope, err := s.admit(ctx, actor, org, pins, false)
	if err != nil {
		return failure, err
	}
	if q.Validate() != nil || !scope.Includes(q.Root, scope.Access.AsOf) {
		return failure, graph.ErrInvalidGraph
	}
	page, err := s.store.ChangeNeighbors(ctx, scope, q)
	if err := stageResult(ctx, "change graph query", err); err != nil {
		return failure, err
	}
	return page, nil
}
