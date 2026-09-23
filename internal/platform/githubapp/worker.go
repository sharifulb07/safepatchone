package githubapp

import (
	"context"
	"crypto/rand"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain"
	"safepatch/internal/trust"
	"strconv"
	"time"
)

func formatID(id int64) string { return strconv.FormatInt(id, 10) }

type PullRequestClient interface {
	Current(context.Context, application.PullRequest) (application.PullRequest, bool, error)
	Publish(context.Context, application.PullRequestJob, application.ChangeResult) error
}

// CoreFor resolves an immutable trusted policy/profile for the organization.
// Both local callers and GitHub workers use *application.ChangeWorkflow.
type CoreFor func(context.Context, application.PullRequestJob) (*application.ChangeWorkflow, error)
type Worker struct {
	store     *Store
	admission *application.PullRequestAdmission
	client    PullRequestClient
	core      CoreFor
}

func NewWorker(s *Store, a *application.PullRequestAdmission, c PullRequestClient, core CoreFor) (*Worker, error) {
	if s == nil || a == nil || c == nil || core == nil {
		return nil, application.ErrChangeWorkflow
	}
	return &Worker{s, a, c, core}, nil
}
func (w *Worker) Process(ctx context.Context, org, id string) error {
	if w == nil || ctx == nil {
		return application.ErrChangeWorkflow
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	lease := "lease_" + rand.Text()
	j, claimed, err := w.store.claim(ctx, org, id, lease)
	if err != nil {
		return err
	}
	if claimed {
		result := application.ChangeResult{ChangeID: j.Change().ID(), Decision: trust.Unknown}
		current, valid, e := w.client.Current(ctx, j.Request)
		if e != nil {
			return e
		} // Expired lease can retry a provider outage.
		if valid {
			binding, e := w.admission.Authorize(ctx, current)
			if e == nil && binding == j.Binding {
				core, e := w.core(ctx, j)
				if e == nil && (core == nil || core.PolicyHash() != j.Binding.PolicyHash) {
					e = application.ErrChangeWorkflow
				}
				if e == nil && core != nil {
					result, e = core.RunHosted(ctx, binding.Actor, j.Change(), j.ID)
				}
				if e != nil {
					result = application.ChangeResult{ChangeID: j.Change().ID(), Decision: trust.Unknown}
				}
			}
		}
		finish, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if ctx.Err() != nil {
			result = application.ChangeResult{ChangeID: j.Change().ID(), Decision: trust.Unknown}
		}
		err = w.store.finish(finish, j, lease, result)
		c()
		if err != nil {
			return err
		}
	}
	return w.store.publishResult(ctx, org, id, func(ctx context.Context, j application.PullRequestJob, r application.ChangeResult) error {
		current, valid, err := w.client.Current(ctx, j.Request)
		if err != nil {
			return err
		}
		binding, err := w.admission.Authorize(ctx, current)
		if err != nil || !valid || binding != j.Binding {
			r.Decision = trust.Unknown
			r.EvidenceReference = ""
		}
		return w.client.Publish(ctx, j, r)
	})
}

// Assert the concrete shared core, rather than a provider-specific verifier.
var _ interface {
	Run(context.Context, authorization.Principal, domain.Change, string) (application.ChangeResult, error)
} = (*application.ChangeWorkflow)(nil)
