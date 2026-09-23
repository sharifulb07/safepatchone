package verification

import (
	"math"
	"strings"
	"testing"
	"time"
)

func validRequest() ExecutionRequest {
	return ExecutionRequest{Executable: "go", SourceDirectory: "/repo", Limits: ExecutionLimits{Timeout: time.Minute, StdoutBytes: 4096, StderrBytes: 4096, InputBytes: 1024, InputEntries: 100, WorkspaceBytes: 4096, TempBytes: 1024, CPUMillis: 500, MemoryBytes: 1024, PIDs: 20}}
}

func TestExecutionRequestValidation(t *testing.T) {
	if err := validRequest().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, modify := range map[string]func(*ExecutionRequest){
		"unknown mode":          func(r *ExecutionRequest) { r.Mode = "UNKNOWN" },
		"host executable":       func(r *ExecutionRequest) { r.Executable = "/bin/sh" },
		"NUL argument":          func(r *ExecutionRequest) { r.Arguments = []string{"a\x00b"} },
		"large argument":        func(r *ExecutionRequest) { r.Arguments = []string{strings.Repeat("a", MaxArgumentBytes+1)} },
		"environment injection": func(r *ExecutionRequest) { r.Environment = map[string]string{"A=B": "x"} },
		"NUL environment":       func(r *ExecutionRequest) { r.Environment = map[string]string{"A": "\x00"} },
		"no timeout":            func(r *ExecutionRequest) { r.Limits.Timeout = 0 },
		"large output":          func(r *ExecutionRequest) { r.Limits.StdoutBytes = MaxCaptureBytes + 1 },
		"unbounded stderr":      func(r *ExecutionRequest) { r.Limits.StderrBytes = 0 },
		"unbounded input":       func(r *ExecutionRequest) { r.Limits.InputBytes = 0 },
		"overflow input":        func(r *ExecutionRequest) { r.Limits.InputBytes = math.MaxInt64 },
		"unbounded entries":     func(r *ExecutionRequest) { r.Limits.InputEntries = 0 },
		"unbounded CPU":         func(r *ExecutionRequest) { r.Limits.CPUMillis = 0 },
		"unbounded RAM":         func(r *ExecutionRequest) { r.Limits.MemoryBytes = 0 },
		"unbounded PID":         func(r *ExecutionRequest) { r.Limits.PIDs = 0 },
		"unbounded tmp":         func(r *ExecutionRequest) { r.Limits.TempBytes = 0 },
		"small workspace":       func(r *ExecutionRequest) { r.Limits.WorkspaceBytes = r.Limits.InputBytes },
	} {
		t.Run(name, func(t *testing.T) {
			r := validRequest()
			modify(&r)
			if r.Validate() == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	for _, dir := range []string{"../repo", "/root", "C:/repo", `..\repo`, "a/../../b", "a/./b", "a//b", "a\x00b"} {
		r := validRequest()
		r.WorkingDirectory = dir
		if r.Validate() == nil {
			t.Errorf("unsafe directory accepted: %q", dir)
		}
	}
}
