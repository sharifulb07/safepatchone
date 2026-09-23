package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
	"safepatch/internal/verification"
)

type storedWork struct{ job application.Workload }

func (s *storedWork) Get(_ context.Context, org, id string) (application.Workload, error) {
	if s.job.Target.OrganizationID != org || s.job.ID != id {
		return application.Workload{}, application.ErrWorkloadNotFound
	}
	return s.job, nil
}

type membership struct {
	actor authorization.Principal
	scope tenant.Scope
}

func (m membership) Resolve(_ context.Context, actor authorization.Principal, scope tenant.Scope) (tenant.Scope, error) {
	if m.actor != actor || m.scope != scope {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return scope, nil
}

type runnerEntitlements struct {
	entitlement.EntitlementService
	enabled bool
}

func (e *runnerEntitlements) Has(_ context.Context, org string, key feature.Key) (bool, error) {
	return org == "org_a" && key == feature.KeyRuntimeReplay && e.enabled, nil
}

type runnerPermission struct{ allowed bool }

func (p *runnerPermission) Allowed(_ context.Context, _ authorization.Principal, org string, key authorization.Key) (bool, error) {
	return org == "org_a" && key == authorization.KeyReplayExecute && p.allowed, nil
}

type unmeteredTestQuota struct{}

func (unmeteredTestQuota) Check(context.Context, application.Execution) error { return nil }

func TestWorkerRunnerEntitlementAndPermissionBoundary(t *testing.T) {
	r, request, base := containerFixture(t)
	starts := 0
	r.execute = func(_ context.Context, _ string, args, _ []string, _ string, _, _ int64) (result verification.ExecutionResult, err error) {
		if dockerVerb(args) == "start" {
			starts++
		}
		return successfulDocker(args), nil
	}
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: "org_a", ResourceID: "repo_a"}
	repository := &storedWork{application.Workload{ID: "job_a", Kind: "replay", Actor: actor, Target: scope}}
	originalJob := repository.job
	e := &runnerEntitlements{enabled: true}
	p := &runnerPermission{allowed: true}
	worker, err := application.NewWorkerOperation(repository, membership{actor, scope}, e, p, unmeteredTestQuota{}, "replay", application.Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute}, func(ctx context.Context, _ application.Execution) error { _, err := r.Run(ctx, request); return err })
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(request.SourceDirectory, "evidence.json")
	if err := os.WriteFile(file, []byte(`{"existing":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(t.Context(), "org_a", "job_a"); err != nil || starts != 1 {
		t.Fatal(starts, err)
	}
	e.enabled = false
	if err := worker.Start(t.Context(), "org_a", "job_a"); !errors.Is(err, entitlement.ErrNotEntitled) || starts != 1 {
		t.Fatal("downgrade reused enqueue access", starts, err)
	}
	e.enabled = true
	p.allowed = false
	if err := worker.Start(t.Context(), "org_a", "job_a"); !errors.Is(err, application.ErrPermissionDenied) || starts != 1 {
		t.Fatal("entitlement bypassed permission", starts, err)
	}
	p.allowed = true
	if err := worker.Start(t.Context(), "org_b", "job_a"); !errors.Is(err, application.ErrWorkloadNotFound) || starts != 1 {
		t.Fatal("cross-tenant dispatch", starts, err)
	}
	if err := worker.Start(t.Context(), "org_a", "job_a"); err != nil || starts != 2 {
		t.Fatal(starts, err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != `{"existing":true}` || repository.job != originalJob {
		t.Fatal("grant changes modified stored work or evidence", err)
	}
	assertEmpty(t, base)
}
