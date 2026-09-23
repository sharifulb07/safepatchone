package application

import (
	"context"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/trial"
	"safepatch/internal/tenant"
)

// TrialService accepts authenticated principals and requires organization
// membership plus billing.manage. A premium grant is not needed to start a trial.
type TrialService struct {
	repository  trial.Repository
	tenants     TenantResolver
	permissions authorization.PermissionChecker
}

func NewTrialService(r trial.Repository, tenants TenantResolver, permissions authorization.PermissionChecker) (*TrialService, error) {
	if r == nil || tenants == nil || permissions == nil {
		return nil, trial.ErrInvalid
	}
	return &TrialService{r, tenants, permissions}, nil
}
func (s *TrialService) authorize(ctx context.Context, actor authorization.Principal, org string) error {
	if s == nil || s.repository == nil || s.tenants == nil || s.permissions == nil || ctx == nil || actor.Validate() != nil || !tenant.ValidID(org) {
		return trial.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target := tenant.Scope{OrganizationID: org, ResourceID: org}
	scope, err := s.tenants.Resolve(ctx, actor, target)
	if err == nil && scope != target {
		err = tenant.ErrInvalidScope
	}
	if err = stageResult(ctx, "tenant", err); err != nil {
		return err
	}
	allowed, err := s.permissions.Allowed(ctx, actor, org, authorization.KeyBillingManage)
	if err == nil && !allowed {
		err = ErrPermissionDenied
	}
	return stageResult(ctx, "permission", err)
}
func (s *TrialService) Start(ctx context.Context, actor authorization.Principal, org string, key edition.Key) (trial.Trial, error) {
	if err := s.authorize(ctx, actor, org); err != nil {
		return trial.Trial{}, err
	}
	v, err := s.repository.Start(ctx, org, key)
	if err == nil && (v.Validate() != nil || v.OrganizationID != org || v.Edition != key) {
		err = trial.ErrInvalid
	}
	if err = stageResult(ctx, "trial", err); err != nil {
		return trial.Trial{}, err
	}
	return v, nil
}
func (s *TrialService) Get(ctx context.Context, actor authorization.Principal, org string) (trial.Trial, error) {
	if err := s.authorize(ctx, actor, org); err != nil {
		return trial.Trial{}, err
	}
	v, err := s.repository.Get(ctx, org)
	if err == nil && (v.Validate() != nil || v.OrganizationID != org) {
		err = trial.ErrInvalid
	}
	if err = stageResult(ctx, "trial", err); err != nil {
		return trial.Trial{}, err
	}
	return v, nil
}
