package application_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
	"safepatch/internal/verification"
)

// This fixture has no commercial inputs. The worker owns the feature/permission
// mapping; the same verifier produces the same evidence across grant changes.
type fixtureVerifier struct{}

func (fixtureVerifier) ID() string      { return "fixture.replay_check" }
func (fixtureVerifier) Version() string { return "1.0.0" }
func (fixtureVerifier) Capabilities() []verification.Capability {
	return []verification.Capability{"replay.check"}
}
func (fixtureVerifier) Detect(context.Context, verification.Target) (verification.Detection, error) {
	return verification.Detection{State: verification.Detected, Capabilities: []verification.Capability{"replay.check"}}, nil
}
func (fixtureVerifier) Plan(_ context.Context, r verification.PlanRequest) (verification.Plan, error) {
	return verification.Plan{Steps: []verification.Step{{ID: "check", Request: r.BaseExecution}}}, nil
}
func (fixtureVerifier) Execute(c context.Context, p verification.Plan, r verification.Runner) error {
	return verification.ExecutePlan(c, p, r)
}
func (fixtureVerifier) Normalize(_ context.Context, r verification.NormalizationRequest) (evidence.Report, error) {
	return evidence.DecodeReport(r.Results[0].Result.Stdout)
}

type sdkRunner func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error)

func (f sdkRunner) Run(c context.Context, r verification.ExecutionRequest) (verification.ExecutionResult, error) {
	return f(c, r)
}

func TestVerifierSDKWorkerAccessAndEvidenceSurviveGrantChanges(t *testing.T) {
	registry, err := verification.NewRegistry(fixtureVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: "org_a", ResourceID: "repo_a"}
	job := application.Workload{ID: "job_a", Kind: "replay", Actor: actor, Target: scope}
	originalJob := job
	entitled, permitted := true, true
	runs := 0
	var records [][]byte
	request := verification.PlanRequest{Target: verification.Target{Subject: evidence.Subject{RepositoryID: "repo_a", Revision: "revision_a"}}, Capability: "replay.check", BaseExecution: verification.ExecutionRequest{Executable: "fixture", SourceDirectory: "/trusted/repo_a", Limits: verification.ExecutionLimits{Timeout: time.Second, StdoutBytes: 1024, StderrBytes: 1024, InputBytes: 1024, InputEntries: 10, WorkspaceBytes: 2048, TempBytes: 1024, CPUMillis: 1000, MemoryBytes: 1024, PIDs: 10}}}
	worker, err := application.NewWorkerOperation(workloadRepo(func(_ context.Context, org, id string) (application.Workload, error) {
		if org != job.Target.OrganizationID || id != job.ID {
			return application.Workload{}, application.ErrWorkloadNotFound
		}
		return job, nil
	}), tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		if p != actor || s != scope {
			return tenant.Scope{}, tenant.ErrInvalidScope
		}
		return s, nil
	}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
		return entitled && org == "org_a" && k == feature.KeyRuntimeReplay, nil
	}), permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
		return permitted, nil
	}), quotaFunc(func(context.Context, application.Execution) error { return nil }), "replay", application.Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute}, func(ctx context.Context, _ application.Execution) error {
		record, err := registry.Run(ctx, "fixture.replay_check", request, sdkRunner(func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error) {
			runs++
			return verification.ExecutionResult{Status: verification.Completed, ExitCode: 0, Sandboxed: true, Stdout: []byte(`{"status":"PASS","summary":"Fixture completed"}`)}, nil
		}))
		if err != nil {
			return err
		}
		data, err := record.CanonicalJSON()
		if err != nil {
			return err
		}
		records = append(records, data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(t.Context(), "org_a", "job_a"); err != nil || runs != 1 {
		t.Fatal(runs, err)
	}
	entitled = false
	if err := worker.Start(t.Context(), "org_a", "job_a"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 1 {
		t.Fatal("unentitled execution", runs, err)
	}
	entitled = true
	permitted = false
	if err := worker.Start(t.Context(), "org_a", "job_a"); !errors.Is(err, application.ErrPermissionDenied) || runs != 1 {
		t.Fatal("entitlement bypassed permission", runs, err)
	}
	permitted = true
	if err := worker.Start(t.Context(), "org_b", "job_a"); !errors.Is(err, application.ErrWorkloadNotFound) || runs != 1 {
		t.Fatal("cross-tenant execution", runs, err)
	}
	if err := worker.Start(t.Context(), "org_a", "job_a"); err != nil || runs != 2 {
		t.Fatal(runs, err)
	}
	if job != originalJob || len(records) != 2 || !bytes.Equal(records[0], records[1]) {
		t.Fatal("grant changes altered work or normalized evidence")
	}
}
