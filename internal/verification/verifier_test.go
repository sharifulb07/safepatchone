package verification

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/evidence"
)

type testVerifier struct {
	id, version  string
	capabilities []Capability
	detect       func(context.Context, Target) (Detection, error)
	plan         func(context.Context, PlanRequest) (Plan, error)
	execute      func(context.Context, Plan, Runner) error
	normalize    func(context.Context, NormalizationRequest) (evidence.Report, error)
}

func (v *testVerifier) ID() string                 { return v.id }
func (v *testVerifier) Version() string            { return v.version }
func (v *testVerifier) Capabilities() []Capability { return v.capabilities }
func (v *testVerifier) Detect(c context.Context, t Target) (Detection, error) {
	if v.detect != nil {
		return v.detect(c, t)
	}
	return Detection{State: Detected, Capabilities: []Capability{"test"}}, nil
}
func (v *testVerifier) Plan(c context.Context, r PlanRequest) (Plan, error) {
	if v.plan != nil {
		return v.plan(c, r)
	}
	return Plan{Steps: []Step{{ID: "run", Request: r.BaseExecution}}}, nil
}
func (v *testVerifier) Execute(c context.Context, p Plan, r Runner) error {
	if v.execute != nil {
		return v.execute(c, p, r)
	}
	return ExecutePlan(c, p, r)
}
func (v *testVerifier) Normalize(c context.Context, r NormalizationRequest) (evidence.Report, error) {
	if v.normalize != nil {
		return v.normalize(c, r)
	}
	return evidence.DecodeReport(r.Results[0].Result.Stdout)
}

type runnerFunc func(context.Context, ExecutionRequest) (ExecutionResult, error)

func (f runnerFunc) Run(c context.Context, r ExecutionRequest) (ExecutionResult, error) {
	return f(c, r)
}

func sdkFixture(t *testing.T) (*testVerifier, PlanRequest, runnerFunc) {
	t.Helper()
	v := &testVerifier{id: "test.tool", version: "1.0.0", capabilities: []Capability{"test", "lint"}}
	base := validRequest()
	base.Mode = Sandbox
	request := PlanRequest{Target: Target{Subject: evidence.Subject{RepositoryID: "repo_a", Revision: "revision_a"}, Files: []string{"go.mod", "main.go"}}, Capability: "test", BaseExecution: base}
	runner := runnerFunc(func(context.Context, ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Status: Completed, ExitCode: 0, Sandboxed: true, Stdout: []byte(`{"status":"PASS","summary":"All checks passed","findings":[]}`)}, nil
	})
	return v, request, runner
}

func TestVerifierLifecycleAndCapabilityDiscovery(t *testing.T) {
	v, request, runner := sdkFixture(t)
	registry, err := NewRegistry(v)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Discover("test")
	descriptors[0].Capabilities[0] = "forged"
	v.capabilities[0] = "changed"
	v.id = "changed"
	v.version = "2.0.0"
	if got := registry.Discover("test"); len(got) != 1 || got[0].ID != "test.tool" || !slices.Contains(got[0].Capabilities, Capability("test")) {
		t.Fatal(got)
	}
	var stages []string
	v.detect = func(context.Context, Target) (Detection, error) {
		stages = append(stages, "detect")
		return Detection{Detected, []Capability{"test"}}, nil
	}
	v.plan = func(_ context.Context, r PlanRequest) (Plan, error) {
		stages = append(stages, "plan")
		return Plan{[]Step{{"run", r.BaseExecution}}}, nil
	}
	v.execute = func(c context.Context, p Plan, r Runner) error {
		stages = append(stages, "execute")
		return ExecutePlan(c, p, r)
	}
	v.normalize = func(_ context.Context, r NormalizationRequest) (evidence.Report, error) {
		stages = append(stages, "normalize")
		r.Target.Subject.RepositoryID = "forged"
		return evidence.DecodeReport(r.Results[0].Result.Stdout)
	}
	record, err := registry.Run(t.Context(), "test.tool", request, runner)
	if err != nil || record.Report.Status != StatusPass || record.VerifierID != "test.tool" || record.VerifierVersion != "1.0.0" || record.Subject != request.Target.Subject || !slices.Equal(stages, []string{"detect", "plan", "execute", "normalize"}) {
		t.Fatal(record, stages, err)
	}
	if _, err := record.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifierDetectionNeverInventsPass(t *testing.T) {
	for _, state := range []DetectionState{NotApplicable, DetectionUnavailable, DetectionUnknown, "", "PASS"} {
		t.Run(string(state), func(t *testing.T) {
			v, request, _ := sdkFixture(t)
			v.detect = func(context.Context, Target) (Detection, error) { return Detection{State: state}, nil }
			registry, _ := NewRegistry(v)
			record, err := registry.Run(t.Context(), v.id, request, runnerFunc(func(context.Context, ExecutionRequest) (ExecutionResult, error) {
				t.Fatal("detection reached execution")
				return ExecutionResult{}, nil
			}))
			want := StatusUnknown
			if state == NotApplicable {
				want = StatusSkipped
			}
			if state == DetectionUnavailable {
				want = StatusUnavailable
			}
			if record.Report.Status != want || len(record.Executions) != 0 {
				t.Fatal(record, err)
			}
		})
	}
}

func TestVerifierRejectsPolicyAndExecutionBypasses(t *testing.T) {
	for _, attack := range []string{"empty plan", "source", "mode", "environment", "limits", "duplicate", "skip", "extra", "changed argv", "swallowed error", "panic", "too many steps", "capture budget"} {
		t.Run(attack, func(t *testing.T) {
			v, request, runner := sdkFixture(t)
			if attack == "capture budget" {
				request.BaseExecution.Limits.StdoutBytes = MaxCaptureBytes
				request.BaseExecution.Limits.StderrBytes = MaxCaptureBytes
			}
			calls := 0
			v.plan = func(_ context.Context, r PlanRequest) (Plan, error) {
				p := Plan{[]Step{{"run", r.BaseExecution}}}
				switch attack {
				case "empty plan":
					p.Steps = nil
				case "source":
					p.Steps[0].Request.SourceDirectory = "/other"
				case "mode":
					p.Steps[0].Request.Mode = TrustedLocal
				case "environment":
					p.Steps[0].Request.Environment = map[string]string{"SECRET": "value"}
				case "limits":
					p.Steps[0].Request.Limits.Timeout *= 2
				case "duplicate":
					p.Steps = append(p.Steps, p.Steps[0])
				case "capture budget":
					p.Steps = append(p.Steps, Step{ID: "second", Request: r.BaseExecution})
				case "too many steps":
					for i := 0; i < MaxSteps; i++ {
						p.Steps = append(p.Steps, p.Steps[0])
					}
				}
				return p, nil
			}
			v.execute = func(c context.Context, p Plan, r Runner) error {
				switch attack {
				case "skip":
					return nil
				case "panic":
					panic("secret panic")
				case "changed argv":
					p.Steps[0].Request.Arguments = []string{"malicious"}
					_, _ = r.Run(c, p.Steps[0].Request)
					return nil
				case "swallowed error":
					_, _ = r.Run(c, p.Steps[0].Request)
					return nil
				case "extra":
					_ = ExecutePlan(c, p, r)
					_, _ = r.Run(c, p.Steps[0].Request)
					return nil
				default:
					return ExecutePlan(c, p, r)
				}
			}
			registry, _ := NewRegistry(v)
			record, err := registry.Run(t.Context(), v.id, request, runnerFunc(func(c context.Context, r ExecutionRequest) (ExecutionResult, error) {
				calls++
				if attack == "swallowed error" {
					return ExecutionResult{Status: Unknown, ExitCode: -1}, ErrRunnerUnavailable
				}
				return runner(c, r)
			}))
			if err == nil || record.Report.Status == StatusPass || strings.Contains(err.Error(), "secret") {
				t.Fatal(record, err)
			}
			if attack != "extra" && attack != "swallowed error" && calls != 0 {
				t.Fatal("unsafe execution occurred", calls)
			}
		})
	}
}

func TestVerifierRejectsPoisonedResultsAndKeepsAllStatuses(t *testing.T) {
	for _, status := range []Status{StatusPass, StatusFail, StatusWarn, StatusError, StatusSkipped, StatusUnavailable, StatusUnknown} {
		v, request, _ := sdkFixture(t)
		registry, _ := NewRegistry(v)
		record, err := registry.Run(t.Context(), v.id, request, runnerFunc(func(context.Context, ExecutionRequest) (ExecutionResult, error) {
			return ExecutionResult{Status: Completed, ExitCode: 0, Sandboxed: true, Stdout: []byte(`{"status":"` + string(status) + `","summary":"reported"}`)}, nil
		}))
		if err != nil || record.Report.Status != status {
			t.Fatal(record, err)
		}
	}
	for _, attack := range []string{"unknown", "nonzero", "overflow", "unsandboxed", "exit mismatch", "duplicate JSON", "forged identity", "empty output", "normalizer error"} {
		t.Run(attack, func(t *testing.T) {
			v, request, runner := sdkFixture(t)
			registry, _ := NewRegistry(v)
			if attack == "normalizer error" {
				v.normalize = func(context.Context, NormalizationRequest) (evidence.Report, error) {
					return evidence.Report{Status: StatusPass}, errors.New("secret scanner data")
				}
			}
			record, err := registry.Run(t.Context(), v.id, request, runnerFunc(func(c context.Context, r ExecutionRequest) (ExecutionResult, error) {
				result, _ := runner(c, r)
				switch attack {
				case "unknown":
					result.Status = Unknown
					result.ExitCode = -1
				case "nonzero":
					result.Status = Failed
					result.ExitCode = 1
					return result, ErrExecutionFailed
				case "overflow":
					result.Stdout = bytes.Repeat([]byte("x"), int(r.Limits.StdoutBytes)+1)
				case "unsandboxed":
					result.Sandboxed = false
				case "exit mismatch":
					result.ExitCode = 7
				case "duplicate JSON":
					result.Stdout = []byte(`{"status":"UNKNOWN","status":"PASS","summary":"ok"}`)
				case "forged identity":
					result.Stdout = []byte(`{"status":"PASS","summary":"ok","subject":{"repository_id":"other"}}`)
				case "empty output":
					result.Stdout = nil
				}
				return result, nil
			}))
			if err == nil || record.Report.Status == StatusPass || strings.Contains(err.Error(), "secret") {
				t.Fatal(record, err)
			}
		})
	}
}

func TestVerifierErrorsCancellationAndConcurrency(t *testing.T) {
	for _, stage := range []string{"detect", "plan", "execute", "normalize"} {
		v, request, runner := sdkFixture(t)
		sentinel := errors.New("secret backend")
		switch stage {
		case "detect":
			v.detect = func(context.Context, Target) (Detection, error) { return Detection{}, sentinel }
		case "plan":
			v.plan = func(context.Context, PlanRequest) (Plan, error) { return Plan{}, sentinel }
		case "execute":
			v.execute = func(context.Context, Plan, Runner) error { return sentinel }
		case "normalize":
			v.normalize = func(context.Context, NormalizationRequest) (evidence.Report, error) {
				return evidence.Report{}, sentinel
			}
		}
		registry, _ := NewRegistry(v)
		record, err := registry.Run(t.Context(), v.id, request, runner)
		if !errors.Is(err, sentinel) || !errors.Is(err, ErrVerifierLifecycle) || record.Report.Status != StatusError || strings.Contains(err.Error(), "secret") {
			t.Fatal(record, err)
		}
	}
	v, request, runner := sdkFixture(t)
	registry, _ := NewRegistry(v)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if record, err := registry.Run(ctx, v.id, request, runner); !errors.Is(err, context.Canceled) || record.Report.Status == StatusPass {
		t.Fatal(record, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record, err := registry.Run(t.Context(), v.id, request, runner)
			if err != nil || record.Report.Status != StatusPass {
				t.Error(record, err)
			}
		}()
	}
	wg.Wait()
	v.execute = func(c context.Context, p Plan, r Runner) error { <-c.Done(); return c.Err() }
	request.BaseExecution.Limits.Timeout = time.Millisecond
	if record, err := registry.Run(t.Context(), v.id, request, runner); !errors.Is(err, context.DeadlineExceeded) || record.Report.Status == StatusPass {
		t.Fatal(record, err)
	}
}

func TestRegistryAndTargetValidation(t *testing.T) {
	v, request, runner := sdkFixture(t)
	if _, err := NewRegistry(v, v); !errors.Is(err, ErrDuplicateVerifier) {
		t.Fatal(err)
	}
	var nilVerifier *testVerifier
	if _, err := NewRegistry(nilVerifier); !errors.Is(err, ErrInvalidVerifier) {
		t.Fatal(err)
	}
	v.capabilities = []Capability{"test", "test"}
	if _, err := NewRegistry(v); err == nil {
		t.Fatal("duplicate capability")
	}
	v.capabilities = []Capability{"test"}
	v.version = "invalid"
	if _, err := NewRegistry(v); err == nil {
		t.Fatal("invalid version")
	}
	v.version = "1.0.0"
	registry, _ := NewRegistry(v)
	request.Target.Files = []string{"../secret"}
	if _, err := registry.Run(t.Context(), v.id, request, runner); err == nil {
		t.Fatal("manifest traversal")
	}
	request.Target.Files = []string{"a", "a"}
	if _, err := registry.Run(t.Context(), v.id, request, runner); err == nil {
		t.Fatal("duplicate manifest paths")
	}
	request.Target.Files = nil
	if _, err := registry.Run(t.Context(), "missing", request, runner); !errors.Is(err, ErrVerifierNotFound) {
		t.Fatal(err)
	}
}

func TestNonzeroScannerExitCanNormalizeFailure(t *testing.T) {
	v, request, _ := sdkFixture(t)
	registry, _ := NewRegistry(v)
	record, err := registry.Run(t.Context(), v.id, request, runnerFunc(func(context.Context, ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Status: Failed, ExitCode: 1, Sandboxed: true, Stdout: []byte(`{"status":"FAIL","summary":"A check failed","findings":[{"rule_id":"rule","severity":"ERROR","message":"Failure"}]}`)}, ErrExecutionFailed
	}))
	if err != nil || record.Report.Status != StatusFail || len(record.Report.Findings) != 1 {
		t.Fatal(record, err)
	}
	if _, err := record.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}
}
