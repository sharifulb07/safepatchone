package application

import (
	"context"
	"encoding/hex"
	"fmt"
	"safepatch/internal/authorization"
	"safepatch/internal/domain"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

type PullRequest struct {
	InstallationID int64  `json:"installation_id"`
	RepositoryID   int64  `json:"repository_id"`
	Number         int64  `json:"number"`
	BaseSHA        string `json:"base_sha"`
	HeadSHA        string `json:"head_sha"`
	Private        bool   `json:"private"`
}

func (p PullRequest) Validate() error {
	if p.InstallationID <= 0 || p.RepositoryID <= 0 || p.Number <= 0 || !domain.ValidCommitSHA(p.BaseSHA) || !domain.ValidCommitSHA(p.HeadSHA) {
		return ErrChangeWorkflow
	}
	return nil
}

type PullRequestBinding struct {
	OrganizationID string
	RepositoryID   string
	PolicyHash     string
	Actor          authorization.Principal
}
type PullRequestJob struct {
	ID      string             `json:"id"`
	Binding PullRequestBinding `json:"binding"`
	Request PullRequest        `json:"request"`
}

func (j PullRequestJob) Change() domain.Change {
	return domain.Change{OrganizationID: j.Binding.OrganizationID, RepositoryID: j.Binding.RepositoryID, BaseSHA: j.Request.BaseSHA, HeadSHA: j.Request.HeadSHA}
}

type PullRequestStore interface {
	Resolve(context.Context, PullRequest) (PullRequestBinding, error)
	Accept(context.Context, string, string, PullRequestJob) error
}
type PullRequestAdmission struct {
	store       PullRequestStore
	tenants     TenantResolver
	grants      entitlement.EntitlementService
	permissions authorization.PermissionChecker
}

func NewPullRequestAdmission(store PullRequestStore, tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker) (*PullRequestAdmission, error) {
	if store == nil || tenants == nil || grants == nil || permissions == nil {
		return nil, ErrChangeWorkflow
	}
	return &PullRequestAdmission{store, tenants, grants, permissions}, nil
}
func (s *PullRequestAdmission) Authorize(ctx context.Context, p PullRequest) (PullRequestBinding, error) {
	if s == nil || ctx == nil || p.Validate() != nil {
		return PullRequestBinding{}, ErrChangeWorkflow
	}
	binding, err := s.store.Resolve(ctx, p)
	if err != nil {
		return binding, err
	}
	scope := tenant.Scope{OrganizationID: binding.OrganizationID, ResourceID: binding.RepositoryID}
	hash, hashErr := hex.DecodeString(binding.PolicyHash)
	if binding.Actor.Validate() != nil || scope.Validate() != nil || hashErr != nil || len(hash) != 32 || hex.EncodeToString(hash) != binding.PolicyHash {
		return PullRequestBinding{}, ErrChangeWorkflow
	}
	resolved, err := s.tenants.Resolve(ctx, binding.Actor, scope)
	if err != nil {
		return PullRequestBinding{}, err
	}
	if resolved != scope {
		return PullRequestBinding{}, tenant.ErrInvalidScope
	}
	required := []feature.Key{feature.KeyVerifyBasic, feature.KeyVerifyPREnforcement, feature.KeyProofCloudHistory}
	if p.Private {
		required = append(required, feature.KeyIntegrationGitHubPrivate)
	}
	for _, key := range required {
		if err = entitlement.RequireEntitlement(ctx, s.grants, binding.OrganizationID, key); err != nil {
			return PullRequestBinding{}, err
		}
	}
	allowed, err := s.permissions.Allowed(ctx, binding.Actor, binding.OrganizationID, authorization.KeyProofExecute)
	if err != nil {
		return PullRequestBinding{}, err
	}
	if !allowed {
		return PullRequestBinding{}, ErrPermissionDenied
	}
	return binding, ctx.Err()
}

// Accept is called only by verified webhook ingress. Sender/login/organization
// names are never principals or tenant authority. Resolve uses installation AND
// repository identities and independently configured automation membership.
func (s *PullRequestAdmission) Accept(ctx context.Context, delivery, hash string, p PullRequest) error {
	if !evidence.ValidIdentifier(delivery) || len(hash) != 64 {
		return ErrChangeWorkflow
	}
	binding, err := s.Authorize(ctx, p)
	if err != nil {
		return err
	}
	change := domain.Change{OrganizationID: binding.OrganizationID, RepositoryID: binding.RepositoryID, BaseSHA: p.BaseSHA, HeadSHA: p.HeadSHA}
	id := "pr_" + evidence.Digest([]byte(fmt.Sprintf("%s:%d:%d:%d:%s", change.ID(), p.InstallationID, p.RepositoryID, p.Number, binding.PolicyHash)))
	return s.store.Accept(ctx, delivery, hash, PullRequestJob{ID: id, Binding: binding, Request: p})
}
