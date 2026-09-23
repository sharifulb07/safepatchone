package application_test

import (
	"context"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
	"strings"
	"testing"
)

type prStore struct {
	foreign bool
	jobs    []application.PullRequestJob
}

func (s *prStore) Resolve(_ context.Context, p application.PullRequest) (application.PullRequestBinding, error) {
	if p.InstallationID != 7 || p.RepositoryID != 1 {
		return application.PullRequestBinding{}, errors.New("unknown installation binding")
	}
	org := "org_a"
	if s.foreign {
		org = "org_b"
	}
	return application.PullRequestBinding{OrganizationID: org, RepositoryID: "gh_1", PolicyHash: strings.Repeat("d", 64), Actor: authorization.Principal{ID: "user", Kind: authorization.User}}, nil
}
func (s *prStore) Accept(_ context.Context, _ string, _ string, j application.PullRequestJob) error {
	s.jobs = append(s.jobs, j)
	return nil
}
func TestPrivatePRRequiresEntitlementAndIndependentPermission(t *testing.T) {
	for _, mode := range []string{"entitled", "not_entitled", "no_pr_enforcement", "no_cloud_history", "viewer", "wrong_installation", "wrong_repository", "downgrade"} {
		t.Run(mode, func(t *testing.T) {
			store := &prStore{}
			grants := flowEntitlements{enabled: map[feature.Key]bool{feature.KeyVerifyBasic: true, feature.KeyIntegrationGitHubPrivate: true, feature.KeyVerifyPREnforcement: true, feature.KeyProofCloudHistory: true}}
			permissions, _ := authorization.NewPermissionService(billingMemberships{active: true})
			tenants := tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				return s, nil
			})
			service, _ := application.NewPullRequestAdmission(store, tenants, grants, permissions)
			p := application.PullRequest{InstallationID: 7, RepositoryID: 1, Number: 2, Private: true, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
			if mode == "downgrade" {
				if err := service.Accept(t.Context(), "prior", strings.Repeat("c", 64), p); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "no_pr_enforcement":
				grants.enabled[feature.KeyVerifyPREnforcement] = false
			case "no_cloud_history":
				grants.enabled[feature.KeyProofCloudHistory] = false
			case "not_entitled", "downgrade":
				grants.enabled[feature.KeyIntegrationGitHubPrivate] = false
			case "viewer":
				store.foreign = true
			case "wrong_installation":
				p.InstallationID = 8
			case "wrong_repository":
				p.RepositoryID = 2
			}
			err := service.Accept(t.Context(), "delivery", strings.Repeat("c", 64), p)
			if mode == "entitled" {
				if err != nil || len(store.jobs) != 1 {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unauthorized PR accepted")
			}
			if mode == "not_entitled" && !errors.Is(err, entitlement.ErrNotEntitled) {
				t.Fatal(err)
			}
			if mode == "viewer" && !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal(err)
			}
			if mode == "downgrade" && (len(store.jobs) != 1 || store.jobs[0].Request.HeadSHA != strings.Repeat("b", 40)) {
				t.Fatal("plan change corrupted existing job")
			}
		})
	}
}
