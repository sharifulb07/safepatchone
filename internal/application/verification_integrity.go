package application

import (
	"context"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/domain"
	"safepatch/internal/policy"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
	"safepatch/internal/verification"
)

type IntegrityInput struct {
	Base            verification.VerificationControls
	Head            verification.VerificationControls
	Authors         []string
	AuthorsComplete bool
	Review          trust.ControlReview
}

// VerificationIntegritySource is trusted infrastructure: resolve the protected
// baseline and pinned proposed configuration independently of scanner output.
// Reviews/author membership come from authenticated server records, never from
// repository files, prompts or caller-supplied review DTOs. Refresh revocation
// when called again; an unavailable baseline is never synthesized as empty.
type VerificationIntegritySource interface {
	LoadIntegrity(context.Context, domain.Change) (IntegrityInput, error)
}

type integrityRunner struct {
	runner  verification.Runner
	minimum time.Duration
}

func (r integrityRunner) Run(ctx context.Context, request verification.ExecutionRequest) (verification.ExecutionResult, error) {
	if request.Limits.Timeout < r.minimum {
		return verification.ExecutionResult{Status: verification.Unknown, ExitCode: -1}, verification.ErrIntegrity
	}
	return r.runner.Run(ctx, request)
}

func (w *ChangeWorkflow) checkIntegrity(ctx context.Context, actor authorization.Principal, change domain.Change) (verification.IntegrityResult, IntegrityInput, error) {
	input, err := w.c.Integrity.LoadIntegrity(ctx, change)
	if err != nil {
		return verification.UnknownIntegrity(), IntegrityInput{}, verification.ErrIntegrity
	}
	a, e1 := input.Base.Fields()
	b, e2 := input.Head.Fields()
	if e1 != nil || e2 != nil || a.OrganizationID != change.OrganizationID || b.OrganizationID != change.OrganizationID || a.Subject.RepositoryID != change.RepositoryID || b.Subject.RepositoryID != change.RepositoryID || a.Subject.Revision != change.BaseSHA || b.Subject.Revision != change.HeadSHA {
		return verification.UnknownIntegrity(), input, verification.ErrIntegrity
	}
	// The candidate policy never becomes the evaluator for its own change.
	baseline, err := policy.Decode(a.Policy)
	if err != nil || baseline.Hash() != w.c.Policy.Hash() {
		return verification.UnknownIntegrity(), input, verification.ErrIntegrity
	}
	result, err := (verification.VerificationIntegrity{}).Check(ctx, input.Base, input.Head)
	if err != nil || result.Status != "INTACT" {
		return result, input, verification.ErrIntegrity
	}
	if result.Changed {
		if err := (trust.SelfApprovalGuard{}).Check(ctx, change, input.Base.Digest(), input.Head.Digest(), actor.ID, input.Authors, input.AuthorsComplete, input.Review, w.c.Now()); err != nil {
			return result, input, err
		}
		reviewer := authorization.Principal{ID: input.Review.ReviewerID, Kind: authorization.User}
		scope := tenant.Scope{OrganizationID: change.OrganizationID, ResourceID: change.RepositoryID}
		resolved, err := w.c.Guard.tenants.Resolve(ctx, reviewer, scope)
		if err != nil || resolved != scope {
			return result, input, trust.ErrSelfApproval
		}
		allowed, err := w.c.Guard.permissions.Allowed(ctx, reviewer, change.OrganizationID, authorization.KeyPolicyManage)
		if err != nil || !allowed {
			return result, input, trust.ErrSelfApproval
		}
	}
	if err := ctx.Err(); err != nil {
		return verification.UnknownIntegrity(), input, err
	}
	return result, input, nil
}
