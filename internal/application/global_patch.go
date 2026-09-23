package application

import (
	"context"
	"errors"
	"slices"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"safepatch/internal/tenant"
)

var ErrGlobalPatchUnavailable = errors.New("global patch unavailable")

// GlobalPatchLocator is an untrusted routing hint, never an access token. Every
// repository must be admitted and the complete stored roster must match it.
type GlobalPatchLocator struct {
	OrganizationID string                     `json:"organization_id"`
	RepositoryIDs  []string                   `json:"repository_ids"`
	GlobalChangeID graph.GlobalChangeID       `json:"global_change_id"`
	Reference      evidence.OriginalReference `json:"reference"`
}

func (l GlobalPatchLocator) Validate() error {
	if !tenant.ValidID(l.OrganizationID) || l.GlobalChangeID.Validate() != nil || l.Reference.Validate() != nil || l.Reference.Bytes > graph.MaxGlobalPatchBytes || len(l.RepositoryIDs) < 2 || len(l.RepositoryIDs) > graph.MaxRepositories {
		return ErrGlobalPatchUnavailable
	}
	for i, r := range l.RepositoryIDs {
		if !tenant.ValidID(r) || i > 0 && l.RepositoryIDs[i-1] >= r {
			return ErrGlobalPatchUnavailable
		}
	}
	return nil
}

type GlobalPatchArchive interface {
	Save(context.Context, graph.GlobalPatch) (GlobalPatchLocator, error)
	Read(context.Context, GlobalPatchLocator) (graph.GlobalPatch, error)
}
type GlobalPatchService struct {
	read, write *ExecutionGuard
	archive     GlobalPatchArchive
}

func NewGlobalPatchService(tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, archive GlobalPatchArchive) (*GlobalPatchService, error) {
	if archive == nil {
		return nil, ErrInvalidGuard
	}
	read, err := NewExecutionGuard(tenants, grants, permissions, quotas, Requirement{Feature: feature.KeyGraphGlobalChange, Permission: authorization.KeyProofView})
	if err != nil {
		return nil, err
	}
	write, err := NewExecutionGuard(tenants, grants, permissions, quotas, Requirement{Feature: feature.KeyGraphGlobalChange, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	return &GlobalPatchService{read, write, archive}, nil
}
func globalRoster(p graph.GlobalPatch) (string, []string, error) {
	f, err := p.Fields()
	if err != nil {
		return "", nil, err
	}
	repos := make([]string, len(f.Changes))
	for i, c := range f.Changes {
		repos[i] = c.RepositoryID
	}
	return f.OrganizationID, repos, nil
}
func admitGlobal(ctx context.Context, g *ExecutionGuard, actor authorization.Principal, org string, repos []string, quota bool) error {
	for _, repo := range repos {
		if _, err := g.authorizeAccess(ctx, actor, tenant.Scope{OrganizationID: org, ResourceID: repo}); err != nil {
			return err
		}
	}
	if quota {
		_, err := g.Authorize(ctx, actor, tenant.Scope{OrganizationID: org, ResourceID: repos[0]})
		return err
	}
	return nil
}
func (s *GlobalPatchService) Save(ctx context.Context, actor authorization.Principal, p graph.GlobalPatch) (GlobalPatchLocator, error) {
	bad := GlobalPatchLocator{}
	org, repos, err := globalRoster(p)
	if s == nil || err != nil {
		return bad, ErrGlobalPatchUnavailable
	}
	if err := admitGlobal(ctx, s.write, actor, org, repos, true); err != nil {
		return bad, err
	}
	l, err := s.archive.Save(ctx, p)
	raw, _ := p.MarshalJSON()
	if err != nil || ctx.Err() != nil || l.Validate() != nil || l.OrganizationID != org || !slices.Equal(l.RepositoryIDs, repos) || l.GlobalChangeID != p.ID() || l.Reference.SHA256 != evidence.Digest(raw) || l.Reference.Bytes != int64(len(raw)) {
		return bad, ErrGlobalPatchUnavailable
	}
	if err := admitGlobal(ctx, s.write, actor, org, repos, false); err != nil {
		return bad, err
	}
	return l, nil
}
func (s *GlobalPatchService) Read(ctx context.Context, actor authorization.Principal, l GlobalPatchLocator) (graph.GlobalPatch, error) {
	bad := graph.GlobalPatch{}
	l.RepositoryIDs = slices.Clone(l.RepositoryIDs)
	if s == nil || l.Validate() != nil {
		return bad, ErrGlobalPatchUnavailable
	}
	if err := admitGlobal(ctx, s.read, actor, l.OrganizationID, l.RepositoryIDs, true); err != nil {
		return bad, err
	}
	p, err := s.archive.Read(ctx, l)
	org, repos, validation := globalRoster(p)
	raw, _ := p.MarshalJSON()
	if err != nil || validation != nil || ctx.Err() != nil || org != l.OrganizationID || !slices.Equal(repos, l.RepositoryIDs) || p.ID() != l.GlobalChangeID || evidence.Digest(raw) != l.Reference.SHA256 || int64(len(raw)) != l.Reference.Bytes {
		return bad, ErrGlobalPatchUnavailable
	}
	if err := admitGlobal(ctx, s.read, actor, org, repos, false); err != nil {
		return bad, err
	}
	return p, nil
}
