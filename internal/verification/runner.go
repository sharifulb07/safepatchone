package verification

import (
	"context"
	"errors"
	"math"
	"path"
	"regexp"
	"strings"
	"time"
)

// Runner is shared infrastructure, not an entitlement, policy or verification
// verdict. Callers must pass trusted executable/argument policy and enforce
// ownership and actor permission before running repository-derived workloads.
type Runner interface {
	Run(context.Context, ExecutionRequest) (ExecutionResult, error)
}

type ExecutionMode string

const (
	Sandbox          ExecutionMode = "SANDBOX"
	TrustedLocal     ExecutionMode = "TRUSTED_LOCAL"
	MaxCaptureBytes  int64         = 16 * 1024 * 1024
	MaxArgumentBytes               = 128 * 1024
)

type ExecutionLimits struct {
	Timeout        time.Duration `json:"timeout"`
	StdoutBytes    int64         `json:"stdout_bytes"`
	StderrBytes    int64         `json:"stderr_bytes"`
	InputBytes     int64         `json:"input_bytes"`
	InputEntries   int64         `json:"input_entries"`
	WorkspaceBytes int64         `json:"workspace_bytes"`
	TempBytes      int64         `json:"temp_bytes"`
	CPUMillis      int64         `json:"cpu_millis"` // 1000 = one CPU, not CPU time.
	MemoryBytes    int64         `json:"memory_bytes"`
	PIDs           int64         `json:"pids"`
}

type ExecutionRequest struct {
	// Executable is an adapter-configured tool name, never an arbitrary host path.
	Executable       string            `json:"executable"`
	Arguments        []string          `json:"arguments"`
	SourceDirectory  string            `json:"source_directory"`
	WorkingDirectory string            `json:"working_directory"` // portable relative path, default "."
	Environment      map[string]string `json:"environment"`
	Mode             ExecutionMode     `json:"mode"` // empty means SANDBOX
	Limits           ExecutionLimits   `json:"limits"`
}

type ExecutionStatus string

const (
	Completed     ExecutionStatus = "COMPLETED"
	Failed        ExecutionStatus = "FAILED"
	TimedOut      ExecutionStatus = "TIMED_OUT"
	Cancelled     ExecutionStatus = "CANCELLED"
	OutputLimited ExecutionStatus = "OUTPUT_LIMIT"
	Unknown       ExecutionStatus = "UNKNOWN"
)

// Exit code zero and COMPLETED only describe process execution. Scanner output
// remains untrusted and must never itself authorize production or imply PASS.
type ExecutionResult struct {
	Status          ExecutionStatus `json:"status"`
	ExitCode        int             `json:"exit_code"` // -1 if no reliable exit status exists
	Stdout          []byte          `json:"stdout"`
	Stderr          []byte          `json:"stderr"`
	StdoutTruncated bool            `json:"stdout_truncated"`
	StderrTruncated bool            `json:"stderr_truncated"`
	Sandboxed       bool            `json:"sandboxed"`
	Duration        time.Duration   `json:"duration"`
}

var (
	ErrInvalidExecution  = errors.New("invalid execution request or runner configuration")
	ErrSandboxRequired   = errors.New("untrusted execution requires a container sandbox")
	ErrUnsupportedLimits = errors.New("runner cannot enforce requested resource limits")
	ErrUnsafeWorkspace   = errors.New("unsafe execution workspace")
	ErrInputLimit        = errors.New("execution input bound exceeded")
	ErrOutputLimit       = errors.New("execution output bound exceeded")
	ErrExecutionFailed   = errors.New("execution failed")
	ErrRunnerUnavailable = errors.New("runner unavailable")
	ErrCleanupFailed     = errors.New("execution cleanup failed")
)

var toolName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

func ValidToolName(name string) bool        { return len(name) <= 64 && toolName.MatchString(name) }
func ValidEnvironmentName(name string) bool { return len(name) <= 128 && envName.MatchString(name) }

func ValidWorkingDirectory(dir string) bool {
	if dir == "" || dir == "." {
		return true
	}
	if len(dir) > 4096 || strings.ContainsAny(dir, "\\:\x00\r\n") || strings.HasPrefix(dir, "/") || path.Clean(dir) != dir {
		return false
	}
	for _, part := range strings.Split(dir, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
}

func (r ExecutionRequest) Validate() error {
	if !ValidToolName(r.Executable) || r.SourceDirectory == "" || strings.ContainsRune(r.SourceDirectory, 0) || !ValidWorkingDirectory(r.WorkingDirectory) {
		return ErrInvalidExecution
	}
	if r.Mode != "" && r.Mode != Sandbox && r.Mode != TrustedLocal {
		return ErrInvalidExecution
	}
	l := r.Limits
	if l.Timeout <= 0 || l.Timeout > 24*time.Hour || l.StdoutBytes < 1 || l.StdoutBytes > MaxCaptureBytes || l.StderrBytes < 1 || l.StderrBytes > MaxCaptureBytes || l.InputBytes < 1 || l.InputBytes == math.MaxInt64 || l.InputEntries < 1 || l.InputEntries == math.MaxInt64 {
		return ErrInvalidExecution
	}
	if l.CPUMillis < 0 || l.MemoryBytes < 0 || l.PIDs < 0 || l.WorkspaceBytes < 0 || l.TempBytes < 0 {
		return ErrInvalidExecution
	}
	if r.Mode != TrustedLocal && (l.CPUMillis == 0 || l.MemoryBytes == 0 || l.PIDs == 0 || l.WorkspaceBytes <= l.InputBytes || l.TempBytes == 0) {
		return ErrInvalidExecution
	}
	if len(r.Arguments) > 4096 || len(r.Environment) > 128 {
		return ErrInvalidExecution
	}
	size := 0
	for _, arg := range r.Arguments {
		size += len(arg)
		if strings.ContainsRune(arg, 0) || size > MaxArgumentBytes {
			return ErrInvalidExecution
		}
	}
	for key, value := range r.Environment {
		size += len(key) + len(value)
		if !ValidEnvironmentName(key) || strings.ContainsRune(value, 0) || size > MaxArgumentBytes {
			return ErrInvalidExecution
		}
	}
	return nil
}
