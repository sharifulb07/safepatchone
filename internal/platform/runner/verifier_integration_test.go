package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

type snapshotVerifier struct{}

func (snapshotVerifier) ID() string      { return "fixture.snapshot" }
func (snapshotVerifier) Version() string { return "1.0.0" }
func (snapshotVerifier) Capabilities() []verification.Capability {
	return []verification.Capability{"fixture.snapshot"}
}
func (snapshotVerifier) Detect(context.Context, verification.Target) (verification.Detection, error) {
	return verification.Detection{State: verification.Detected, Capabilities: []verification.Capability{"fixture.snapshot"}}, nil
}
func (snapshotVerifier) Plan(_ context.Context, input verification.PlanRequest) (verification.Plan, error) {
	r := input.BaseExecution
	r.Arguments = helperArgs("write")
	return verification.Plan{Steps: []verification.Step{{ID: "snapshot", Request: r}}}, nil
}
func (snapshotVerifier) Execute(ctx context.Context, p verification.Plan, r verification.Runner) error {
	return verification.ExecutePlan(ctx, p, r)
}
func (snapshotVerifier) Normalize(ctx context.Context, input verification.NormalizationRequest) (evidence.Report, error) {
	if err := ctx.Err(); err != nil {
		return evidence.Report{}, err
	}
	status := evidence.StatusFail
	if len(input.Results) == 1 && string(input.Results[0].Result.Stdout) == "original" {
		status = evidence.StatusPass
	}
	return evidence.Report{Status: status, Summary: "Fixture source-content comparison"}, nil
}

func TestVerifierSDKWithRealLocalRunner(t *testing.T) {
	r, execution, base := localFixture(t)
	file := filepath.Join(execution.SourceDirectory, "source.txt")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := verification.NewRegistry(snapshotVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	input := verification.PlanRequest{Target: verification.Target{Subject: evidence.Subject{RepositoryID: "fixture", Revision: "revision"}, Files: []string{"source.txt"}}, Capability: "fixture.snapshot", BaseExecution: execution}
	record, err := registry.Run(t.Context(), "fixture.snapshot", input, r)
	if err != nil || record.Report.Status != evidence.StatusPass || len(record.Executions) != 1 || record.Executions[0].Sandboxed || record.Executions[0].StdoutSHA256 != evidence.Digest([]byte("original")) {
		t.Fatal(record, err)
	}
	if _, err := record.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(file)
	if err != nil || string(actual) != "original" {
		t.Fatal("SDK changed source", err)
	}
	assertEmpty(t, base)
}
