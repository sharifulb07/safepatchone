package onboardingstore

import (
	"context"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
	"testing"
)

type workerBoundary struct {
	deny                         feature.Key
	permitted, quotaOK, githubOK bool
	calls                        int
}

func (b *workerBoundary) Resolve(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	return s, nil
}
func (b *workerBoundary) Allowed(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
	return b.permitted, nil
}
func (b *workerBoundary) Check(context.Context, application.Execution) error {
	if !b.quotaOK {
		return errors.New("quota denied")
	}
	return nil
}
func (b *workerBoundary) Repositories(context.Context, authorization.Principal, int64) ([]flow.Repository, error) {
	if !b.githubOK {
		return nil, errors.New("GitHub revoked")
	}
	return []flow.Repository{{ID: "gh_1", FullName: "owner/repo", Private: true}}, nil
}
func (b *workerBoundary) Execute(context.Context, authorization.Principal, flow.State) (string, string, error) {
	b.calls++
	return "PASS", "evidence_reference", nil
}

type workerGrants struct{ b *workerBoundary }

func (g workerGrants) Has(_ context.Context, _ string, key feature.Key) (bool, error) {
	return key != g.b.deny, nil
}
func (g workerGrants) Limit(context.Context, string, feature.Key) (int64, error) {
	return 0, entitlement.ErrLimitUnspecified
}
func (g workerGrants) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, nil
}

func TestWorkerRechecksAccessAfterSubmission(t *testing.T) {
	for _, mode := range []string{"allowed", "basic_revoked", "private_revoked", "policy_revoked", "permission_revoked", "quota_denied", "github_revoked"} {
		t.Run(mode, func(t *testing.T) {
			b := &workerBoundary{permitted: true, quotaOK: true, githubOK: true}
			switch mode {
			case "basic_revoked":
				b.deny = feature.KeyVerifyBasic
			case "private_revoked":
				b.deny = feature.KeyIntegrationGitHubPrivate
			case "policy_revoked":
				b.deny = feature.KeyPolicyLocalAdvanced
			case "permission_revoked":
				b.permitted = false
			case "quota_denied":
				b.quotaOK = false
			case "github_revoked":
				b.githubOK = false
			}
			executor, err := NewGuardedExecutor(b, workerGrants{b}, b, b, b, []application.OnboardingPolicy{{ID: "strict-v1", Feature: feature.KeyPolicyLocalAdvanced}}, b)
			if err != nil {
				t.Fatal(err)
			}
			state := flow.State{OrganizationID: "org", Revision: 5, Edition: "team", EditionVersionID: "team-2026-v2", InstallationID: 7, Repositories: []flow.Repository{{ID: "gh_1", FullName: "owner/repo", Private: true}}, PolicyID: "strict-v1"}
			status, _, err := executor.Execute(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, state)
			if mode == "allowed" {
				if err != nil || status != "PASS" || b.calls != 1 {
					t.Fatal(status, err, b.calls)
				}
			} else if err == nil || status != "UNKNOWN" || b.calls != 0 {
				t.Fatal("execution admitted after revocation", status, err, b.calls)
			}
		})
	}
}
