// Package runner implements shared OS and container execution adapters.
package runner

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"

	"safepatch/internal/verification"
)

const cleanupTimeout = 5 * time.Second

// Keep transport-facing errors stable while retaining diagnostic causes for
// trusted logs and errors.Is/As. Never serialize unwrapped OS errors to clients.
type cleanupFailure struct{ cause error }

func (e *cleanupFailure) Error() string   { return verification.ErrCleanupFailed.Error() }
func (e *cleanupFailure) Unwrap() []error { return []error{verification.ErrCleanupFailed, e.cause} }

type boundedOutput struct {
	mu        sync.Mutex
	bytes     []byte
	limit     int64
	truncated bool
	cancel    context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(int64(len(p)), b.limit-int64(len(b.bytes)))
	b.bytes = append(b.bytes, p[:n]...)
	if n < int64(len(p)) {
		b.truncated = true
		b.cancel()
	}
	return len(p), nil // Drain pipes while terminating, without allocating more.
}
func (b *boundedOutput) value() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.bytes...), b.truncated
}

func runProcess(ctx context.Context, executable string, args, env []string, cwd string, stdoutLimit, stderrLimit int64) (verification.ExecutionResult, error) {
	started := time.Now()
	result := verification.ExecutionResult{Status: verification.Unknown, ExitCode: -1}
	if err := ctx.Err(); err != nil {
		result.Status = verification.Cancelled
		if errors.Is(err, context.DeadlineExceeded) {
			result.Status = verification.TimedOut
		}
		return result, err
	}
	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &boundedOutput{limit: stdoutLimit, cancel: cancel}
	stderr := &boundedOutput{limit: stderrLimit, cancel: cancel}
	cmd := exec.Command(executable, args...)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = env, cwd, out, stderr, cleanupTimeout
	tree, err := prepareTree(cmd)
	if err != nil {
		return result, verification.ErrRunnerUnavailable
	}
	defer tree.close()
	if err := cmd.Start(); err != nil {
		return result, verification.ErrRunnerUnavailable
	}
	if err := tree.attach(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return result, verification.ErrRunnerUnavailable
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	select {
	case err = <-done:
		waited = true
	case <-processCtx.Done():
		_ = tree.kill()
		_ = cmd.Process.Kill()
		select {
		case err = <-done:
			waited = true
		case <-time.After(cleanupTimeout + time.Second):
			err = verification.ErrCleanupFailed
		}
	}
	cleanupErr := tree.kill() // Kill background descendants even on normal exit.
	result.Stdout, result.StdoutTruncated = out.value()
	result.Stderr, result.StderrTruncated = stderr.value()
	result.Duration = time.Since(started)
	if waited && cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	switch {
	case cleanupErr != nil || errors.Is(err, verification.ErrCleanupFailed):
		return result, &cleanupFailure{cause: errors.Join(cleanupErr, err)}
	case result.StdoutTruncated || result.StderrTruncated:
		result.Status = verification.OutputLimited
		return result, verification.ErrOutputLimit
	case ctx.Err() != nil:
		result.Status = verification.Cancelled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Status = verification.TimedOut
		}
		return result, ctx.Err()
	case err != nil:
		result.Status = verification.Failed
		return result, verification.ErrExecutionFailed
	default:
		result.Status = verification.Completed
		return result, nil
	}
}
