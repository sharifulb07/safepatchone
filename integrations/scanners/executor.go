package scanners

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

type OriginalExecution struct {
	Stdout          evidence.OriginalReference   `json:"stdout"`
	Stderr          evidence.OriginalReference   `json:"stderr"`
	Status          verification.ExecutionStatus `json:"status"`
	ExitCode        int                          `json:"exit_code"`
	StdoutTruncated bool                         `json:"stdout_truncated"`
	StderrTruncated bool                         `json:"stderr_truncated"`
}

type Result struct {
	Record      evidence.Record       `json:"record"`
	ToolVersion string                `json:"tool_version"`
	Scope       evidence.ArchiveScope `json:"scope"`
	Originals   []OriginalExecution   `json:"originals"`
	// Manifest contains this envelope without its own reference. It binds the
	// immutable subject, normalized record and captured stream references.
	Manifest *evidence.OriginalReference `json:"manifest,omitempty"`
}

// Executor is called only after local/hosted admission. It always archives
// original output before normalization, including malformed output and bounded
// prefixes from timeout/cancellation/output-limit failures. A failed archive
// cannot result in successful verification. It neither grants nor bills access.
type Executor struct {
	adapter            Adapter
	registry           *verification.Registry
	runner             verification.Runner
	store              evidence.OriginalStore
	persistenceTimeout time.Duration
}

func NewExecutor(a Adapter, runner verification.Runner, store evidence.OriginalStore, persistenceTimeout time.Duration) (*Executor, error) {
	if a == nil || runner == nil || store == nil || persistenceTimeout <= 0 || persistenceTimeout > time.Minute {
		return nil, ErrConfiguration
	}
	registry, err := verification.NewRegistry(a)
	if err != nil {
		return nil, err
	}
	return &Executor{a, registry, runner, store, persistenceTimeout}, nil
}

func (e *Executor) Run(ctx context.Context, input verification.PlanRequest, scope evidence.ArchiveScope) (Result, error) {
	result := Result{Scope: scope, Originals: []OriginalExecution{}}
	result.Record.Report.Status = evidence.StatusError
	if e == nil || ctx == nil || scope.Validate() != nil || scope.Subject != input.Target.Subject {
		return result, ErrConfiguration
	}
	result.ToolVersion = e.adapter.ToolVersion()
	capture := &archivingRunner{runner: e.runner, store: e.store, scope: scope, timeout: e.persistenceTimeout}
	record, err := e.registry.Run(ctx, e.adapter.ID(), input, capture)
	result.Record = record
	result.Originals = capture.originals
	if capture.failure != nil {
		result.Record.Report = evidence.Report{Status: evidence.StatusError, Summary: "Original scanner evidence could not be preserved.", Findings: []evidence.Finding{}}
		err = errors.Join(err, evidence.ErrOriginalEvidence)
	}
	// Final persistence gets a separate bounded cleanup context, even on cancel.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.persistenceTimeout)
	defer cancel()
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return result, errors.Join(err, evidence.ErrOriginalEvidence)
	}
	ref, saveErr := e.store.Put(saveCtx, scope, raw)
	if saveErr != nil || ref.Validate() != nil || ref.SHA256 != evidence.Digest(raw) || ref.Bytes != int64(len(raw)) {
		result.Record.Report = evidence.Report{Status: evidence.StatusError, Summary: "Scanner evidence manifest could not be preserved.", Findings: []evidence.Finding{}}
		return result, errors.Join(err, evidence.ErrOriginalEvidence)
	}
	result.Manifest = &ref
	return result, err
}

type archivingRunner struct {
	runner    verification.Runner
	store     evidence.OriginalStore
	scope     evidence.ArchiveScope
	timeout   time.Duration
	originals []OriginalExecution
	failure   error
}

func (r *archivingRunner) Run(ctx context.Context, request verification.ExecutionRequest) (verification.ExecutionResult, error) {
	result, err := r.runner.Run(ctx, request)
	if int64(len(result.Stdout)) > request.Limits.StdoutBytes || int64(len(result.Stderr)) > request.Limits.StderrBytes {
		r.failure = evidence.ErrOriginalEvidence
		return result, errors.Join(err, r.failure)
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
	defer cancel()
	original := OriginalExecution{Status: result.Status, ExitCode: result.ExitCode, StdoutTruncated: result.StdoutTruncated, StderrTruncated: result.StderrTruncated}
	for i, raw := range [][]byte{result.Stdout, result.Stderr} {
		ref, saveErr := r.store.Put(saveCtx, r.scope, slices.Clone(raw))
		if saveErr != nil || ref.Validate() != nil || ref.SHA256 != evidence.Digest(raw) || ref.Bytes != int64(len(raw)) {
			r.failure = evidence.ErrOriginalEvidence
			return result, errors.Join(err, r.failure)
		}
		if i == 0 {
			original.Stdout = ref
		} else {
			original.Stderr = ref
		}
	}
	r.originals = append(r.originals, original)
	return result, err
}
