package verification

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"

	"safepatch/internal/evidence"
)

const MaxSDKOutputBytes int64 = 32 * 1024 * 1024

type LifecycleError struct {
	Stage string
	cause error
}

func (e *LifecycleError) Error() string   { return "verifier " + e.Stage + " failed" }
func (e *LifecycleError) Unwrap() []error { return []error{ErrVerifierLifecycle, e.cause} }

// Run is a lifecycle helper, not an authorization entrypoint. Invoke it only
// inside an admitted application/worker operation. Registry selection must be
// trusted policy; a caller cannot choose a cheaper entitlement by changing ID.
func (r *Registry) Run(ctx context.Context, id string, request PlanRequest, runner Runner) (record evidence.Record, err error) {
	stage := "request"
	record.Report.Status = StatusUnknown
	fail := func(status Status, cause error) (evidence.Record, error) {
		record.Report = evidence.Report{Status: status, Summary: "Verifier " + stage + " did not complete.", Findings: []evidence.Finding{}}
		return record, &LifecycleError{Stage: stage, cause: cause}
	}
	defer func() {
		if recover() != nil {
			record, err = fail(StatusError, ErrInvalidVerifier)
		}
	}()
	if r == nil || ctx == nil || runner == nil || request.Target.Validate() != nil || !request.Capability.Valid() || request.BaseExecution.Validate() != nil {
		return fail(StatusError, ErrInvalidVerifier)
	}
	entry, exists := r.entries[id]
	if !exists {
		return fail(StatusUnavailable, ErrVerifierNotFound)
	}
	d := entry.descriptor
	record = evidence.Record{Schema: evidence.VerificationSchema, VerifierID: d.ID, VerifierVersion: d.Version, Capability: string(request.Capability), Subject: request.Target.Subject, Executions: []evidence.Execution{}}
	if !slices.Contains(d.Capabilities, request.Capability) {
		return fail(StatusUnavailable, ErrVerifierNotFound)
	}
	if err := ctx.Err(); err != nil {
		return fail(StatusError, err)
	}
	// Apply the execution timeout to the complete cooperative SDK lifecycle.
	ctx, cancel := context.WithTimeout(ctx, request.BaseExecution.Limits.Timeout)
	defer cancel()
	stage = "detect"
	detection, detectErr := entry.verifier.Detect(ctx, cloneTarget(request.Target))
	if ctx.Err() != nil {
		return fail(StatusError, ctx.Err())
	}
	if detectErr != nil {
		return fail(StatusError, detectErr)
	}
	if len(detection.Capabilities) > len(d.Capabilities) {
		return fail(StatusError, ErrInvalidVerifier)
	}
	seen := map[Capability]bool{}
	for _, capability := range detection.Capabilities {
		if seen[capability] || !slices.Contains(d.Capabilities, capability) {
			return fail(StatusError, ErrInvalidVerifier)
		}
		seen[capability] = true
	}
	switch detection.State {
	case NotApplicable, DetectionUnavailable, DetectionUnknown:
		if len(detection.Capabilities) != 0 {
			return fail(StatusError, ErrInvalidVerifier)
		}
		status := StatusSkipped
		if detection.State == DetectionUnavailable {
			status = StatusUnavailable
		}
		if detection.State == DetectionUnknown {
			status = StatusUnknown
		}
		record.Report = evidence.Report{Status: status, Summary: "Verifier detection: " + string(detection.State) + ".", Findings: []evidence.Finding{}}
		return record, nil
	case Detected:
		if !seen[request.Capability] {
			return fail(StatusUnknown, ErrInvalidVerifier)
		}
	default:
		return fail(StatusUnknown, ErrInvalidVerifier)
	}
	stage = "plan"
	planInput := request
	planInput.Target = cloneTarget(request.Target)
	planInput.BaseExecution = cloneRequest(request.BaseExecution)
	plan, planErr := entry.verifier.Plan(ctx, planInput)
	if ctx.Err() != nil {
		return fail(StatusError, ctx.Err())
	}
	if planErr != nil {
		if errors.Is(planErr, ErrVerifierUnavailable) {
			return fail(StatusUnavailable, planErr)
		}
		return fail(StatusError, planErr)
	}
	if err := validatePlan(plan, request.BaseExecution); err != nil {
		return fail(StatusError, err)
	}
	plan = clonePlan(plan)
	stage = "execute"
	observed := &plannedRunner{ctx: ctx, runner: runner, plan: clonePlan(plan)}
	defer func() { cancel(); observed.finish() }()
	executeErr := entry.verifier.Execute(ctx, clonePlan(plan), observed)
	results, executionErr := observed.finish()
	for _, step := range results {
		result := step.Result
		record.Executions = append(record.Executions, evidence.Execution{StepID: step.StepID, Status: string(result.Status), ExitCode: result.ExitCode, StdoutSHA256: evidence.Digest(result.Stdout), StderrSHA256: evidence.Digest(result.Stderr), Sandboxed: result.Sandboxed})
	}
	if ctx.Err() != nil {
		return fail(StatusError, ctx.Err())
	}
	if executionErr != nil {
		status := StatusError
		if errors.Is(executionErr, ErrRunnerUnavailable) {
			status = StatusUnavailable
		} else if errors.Is(executionErr, ErrInvalidPlan) {
			status = StatusUnknown
		}
		return fail(status, executionErr)
	}
	if executeErr != nil && !errors.Is(executeErr, ErrExecutionFailed) {
		return fail(StatusError, executeErr)
	}
	if len(results) != len(plan.Steps) {
		return fail(StatusUnknown, ErrInvalidPlan)
	}
	for _, step := range results {
		switch step.Result.Status {
		case Completed, Failed:
		case Unknown:
			return fail(StatusUnknown, ErrInvalidExecution)
		default:
			return fail(StatusError, ErrInvalidExecution)
		}
	}
	stage = "normalize"
	report, normalizeErr := entry.verifier.Normalize(ctx, NormalizationRequest{Target: cloneTarget(request.Target), Capability: request.Capability, Results: cloneResults(results)})
	if ctx.Err() != nil {
		return fail(StatusError, ctx.Err())
	}
	if normalizeErr != nil {
		return fail(StatusError, normalizeErr)
	}
	report, normalizeErr = report.Canonical()
	if normalizeErr != nil {
		return fail(StatusError, normalizeErr)
	}
	if report.Status == StatusPass && executeErr != nil {
		return fail(StatusError, ErrInvalidExecution)
	}
	record.Report = report
	if _, err := record.CanonicalJSON(); err != nil {
		return fail(StatusError, err)
	}
	return record, nil
}

func validatePlan(plan Plan, base ExecutionRequest) error {
	if len(plan.Steps) == 0 || len(plan.Steps) > MaxSteps {
		return ErrInvalidPlan
	}
	seen := map[string]bool{}
	var capture int64
	for _, step := range plan.Steps {
		r := step.Request
		if !evidence.ValidIdentifier(step.ID) || seen[step.ID] || r.Validate() != nil || r.SourceDirectory != base.SourceDirectory || r.Mode != base.Mode || r.Limits != base.Limits || !maps.Equal(r.Environment, base.Environment) {
			return ErrInvalidPlan
		}
		seen[step.ID] = true
		capture += r.Limits.StdoutBytes + r.Limits.StderrBytes
		if capture > MaxSDKOutputBytes {
			return ErrInvalidPlan
		}
	}
	return nil
}

type plannedRunner struct {
	mu      sync.Mutex
	ctx     context.Context
	runner  Runner
	plan    Plan
	results []StepResult
	failure error
	closed  bool
}

func (p *plannedRunner) Run(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	invalid := ExecutionResult{Status: Unknown, ExitCode: -1}
	if p.closed || p.failure != nil || ctx == nil || len(p.results) >= len(p.plan.Steps) || !sameRequest(request, p.plan.Steps[len(p.results)].Request) {
		p.failure = ErrInvalidPlan
		return invalid, ErrInvalidPlan
	}
	if err := p.ctx.Err(); err != nil {
		p.failure = err
		return invalid, err
	}
	if err := ctx.Err(); err != nil {
		p.failure = err
		return invalid, err
	}
	callCtx, cancel := context.WithCancel(p.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer cancel()
	defer stop()
	result, err := p.runner.Run(callCtx, cloneRequest(request))
	if !validResult(result, request, err) {
		p.failure = ErrInvalidExecution
		return invalid, p.failure
	}
	result.Stdout = slices.Clone(result.Stdout)
	result.Stderr = slices.Clone(result.Stderr)
	p.results = append(p.results, StepResult{StepID: p.plan.Steps[len(p.results)].ID, Result: result})
	if err != nil && (result.Status != Failed || !errors.Is(err, ErrExecutionFailed) || errors.Is(err, ErrCleanupFailed)) {
		p.failure = err
	}
	result.Stdout = slices.Clone(result.Stdout)
	result.Stderr = slices.Clone(result.Stderr)
	return result, err
}
func (p *plannedRunner) finish() ([]StepResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return cloneResults(p.results), p.failure
}
func validResult(r ExecutionResult, request ExecutionRequest, err error) bool {
	if int64(len(r.Stdout)) > request.Limits.StdoutBytes || int64(len(r.Stderr)) > request.Limits.StderrBytes || r.Duration < 0 || r.ExitCode < -1 {
		return false
	}
	if request.Mode != TrustedLocal && (r.Status == Completed || r.Status == Failed) && !r.Sandboxed {
		return false
	}
	if (r.StdoutTruncated || r.StderrTruncated) && r.Status != OutputLimited && r.Status != Unknown {
		return false
	}
	switch r.Status {
	case Completed:
		return r.ExitCode == 0 && err == nil
	case Failed:
		return r.ExitCode != 0 && (err == nil || errors.Is(err, ErrExecutionFailed))
	case TimedOut, Cancelled, OutputLimited:
		return err != nil
	case Unknown:
		return true
	default:
		return false
	}
}
func sameRequest(a, b ExecutionRequest) bool {
	return a.Executable == b.Executable && a.SourceDirectory == b.SourceDirectory && a.WorkingDirectory == b.WorkingDirectory && a.Mode == b.Mode && a.Limits == b.Limits && slices.Equal(a.Arguments, b.Arguments) && maps.Equal(a.Environment, b.Environment)
}
func cloneTarget(t Target) Target {
	t.Files = slices.Clone(t.Files)
	t.Metadata = maps.Clone(t.Metadata)
	return t
}
func cloneRequest(r ExecutionRequest) ExecutionRequest {
	r.Arguments = slices.Clone(r.Arguments)
	r.Environment = maps.Clone(r.Environment)
	return r
}
func clonePlan(p Plan) Plan {
	p.Steps = slices.Clone(p.Steps)
	for i := range p.Steps {
		p.Steps[i].Request = cloneRequest(p.Steps[i].Request)
	}
	return p
}
func cloneResults(r []StepResult) []StepResult {
	r = slices.Clone(r)
	for i := range r {
		r[i].Result.Stdout = slices.Clone(r[i].Result.Stdout)
		r[i].Result.Stderr = slices.Clone(r[i].Result.Stderr)
	}
	return r
}
