package application_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

type workloadRepo func(context.Context, string, string) (application.Workload, error)

func (f workloadRepo) Get(ctx context.Context, org, id string) (application.Workload, error) {
	return f(ctx, org, id)
}

func TestWorkerRechecksEveryDispatchAndRejectsForgedWork(t *testing.T) {
	for _, stop := range []string{"success", "lookup", "tenant", "entitlement", "permission", "quota", "operation", "wrong tenant", "wrong id", "wrong kind", "invalid actor", "not entitled", "not allowed"} {
		t.Run(stop, func(t *testing.T) {
			var calls []string
			failure := errors.New("secret backend payload")
			stage := func(name string) error {
				calls = append(calls, name)
				if stop == name {
					return failure
				}
				return nil
			}
			actor := authorization.Principal{ID: "owner", Kind: authorization.User}
			target := tenant.Scope{OrganizationID: "org_a", ResourceID: "repo_a"}
			worker, err := application.NewWorkerOperation(workloadRepo(func(_ context.Context, org, id string) (application.Workload, error) {
				if org != "org_a" || id != "job_a" {
					t.Fatal(org, id)
				}
				job := application.Workload{ID: id, Kind: "replay", Actor: actor, Target: target}
				switch stop {
				case "wrong tenant":
					job.Target.OrganizationID = "org_b"
				case "wrong id":
					job.ID = "job_b"
				case "wrong kind":
					job.Kind = "cheap"
				case "invalid actor":
					job.Actor.Kind = "UNKNOWN"
				}
				return job, stage("lookup")
			}), tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				if p != actor || s != target {
					t.Fatal("stored context changed")
				}
				return s, stage("tenant")
			}), entitlementFunc(func(_ context.Context, org string, key feature.Key) (bool, error) {
				if org != "org_a" || key != feature.KeyRuntimeReplay {
					t.Fatal("caller replaced requirement", org, key)
				}
				return stop != "not entitled", stage("entitlement")
			}), permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
				return stop != "not allowed", stage("permission")
			}), quotaFunc(func(context.Context, application.Execution) error { return stage("quota") }), "replay", application.Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute}, func(context.Context, application.Execution) error { return stage("operation") })
			if err != nil {
				t.Fatal(err)
			}
			err = worker.Start(t.Context(), "org_a", "job_a")
			want := []string{"lookup", "tenant", "entitlement", "permission", "quota", "operation"}
			last := stop
			switch stop {
			case "wrong tenant", "wrong id", "wrong kind", "invalid actor":
				last = "lookup"
			case "not entitled":
				last = "entitlement"
			case "not allowed":
				last = "permission"
			}
			if stop != "success" {
				want = want[:slices.Index(want, last)+1]
			}
			if !slices.Equal(calls, want) || (stop == "success") != (err == nil) {
				t.Fatal(calls, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("leaky worker error")
			}
			if stop == "success" {
				calls = nil
				if err := worker.Start(t.Context(), "org_a", "job_a"); err != nil || !slices.Equal(calls, want) {
					t.Fatal("cached prior dispatch", calls, err)
				}
			}
		})
	}
}

func TestWorkerCancellationAndConfiguration(t *testing.T) {
	var zero application.WorkerOperation
	if !errors.Is(zero.Start(t.Context(), "org_a", "job_a"), application.ErrInvalidGuard) {
		t.Fatal("zero worker allowed")
	}
	if _, err := application.NewWorkerOperation(nil, nil, nil, nil, nil, "replay", application.Requirement{}, nil); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	worker, err := application.NewWorkerOperation(workloadRepo(func(context.Context, string, string) (application.Workload, error) {
		cancel()
		return application.Workload{ID: "job_a", Kind: "replay", Actor: authorization.Principal{ID: "owner", Kind: authorization.User}, Target: tenant.Scope{OrganizationID: "org_a", ResourceID: "repo_a"}}, nil
	}), tenantFunc(func(context.Context, authorization.Principal, tenant.Scope) (tenant.Scope, error) {
		t.Fatal("cancelled lookup reached enforcement")
		return tenant.Scope{}, nil
	}), entitlementFunc(nil), permissionFunc(nil), quotaFunc(nil), "replay", application.Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute}, func(context.Context, application.Execution) error { t.Fatal("cancelled job ran"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx, "org_a", "job_a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := worker.Start(nil, "org_a", "job_a"); !errors.Is(err, application.ErrInvalidWorkload) {
		t.Fatal(err)
	}
	if err := worker.Start(t.Context(), "../org_a", "job_a"); !errors.Is(err, application.ErrInvalidWorkload) {
		t.Fatal(err)
	}
}

func TestRequireFeatureUsesStructuredErrorsWithoutGrantingOperations(t *testing.T) {
	provider, err := entitlement.NewCommunityEntitlementProvider()
	if err != nil {
		t.Fatal(err)
	}
	if err := application.RequireFeature(t.Context(), provider, entitlement.LocalOrganizationID, feature.KeyVerifyBasic); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  feature.Key
		code application.CLIErrorCode
	}{{feature.KeyRuntimeReplay, application.CodeFeatureNotEntitled}, {"unknown.feature", application.CodeUnknownFeature}, {"replay\nPASS", application.CodeInvalidUsage}} {
		err := application.RequireFeature(t.Context(), provider, entitlement.LocalOrganizationID, tc.key)
		var structured *application.CLIError
		if !errors.As(err, &structured) || structured.Code != tc.code || strings.Contains(err.Error(), "PASS") {
			t.Fatal(err)
		}
	}
	backend := errors.New("secret scanner output")
	err = application.RequireFeature(t.Context(), entitlementFunc(func(context.Context, string, feature.Key) (bool, error) { return true, backend }), "org_a", feature.KeyRuntimeReplay)
	if !errors.Is(err, backend) || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}
