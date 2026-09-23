package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"safepatch/internal/verification"
)

func localFixture(t *testing.T) (*LocalRunner, verification.ExecutionRequest, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base, source := t.TempDir(), t.TempDir()
	r, err := NewLocalRunner(Config{TemporaryDirectory: base, Tools: map[string]string{"helper": executable}, EnvironmentAllowlist: []string{"SAFEPATCH_RUNNER_HELPER", "ALLOWED"}})
	if err != nil {
		t.Fatal(err)
	}
	request := verification.ExecutionRequest{Executable: "helper", SourceDirectory: source, Mode: verification.TrustedLocal, Environment: map[string]string{"SAFEPATCH_RUNNER_HELPER": "1"}, Limits: verification.ExecutionLimits{Timeout: 10 * time.Second, StdoutBytes: 4096, StderrBytes: 4096, InputBytes: 1024 * 1024, InputEntries: 100}}
	return r, request, base
}

func helperArgs(mode string, extra ...string) []string {
	return append([]string{"-test.run=^TestRunnerHelper$", "--", mode}, extra...)
}

// A real subprocess exercises platform process trees and pipes without a shell
// or external test tools. It runs only in a separately launched test binary.
func TestRunnerHelper(t *testing.T) {
	if os.Getenv("SAFEPATCH_RUNNER_HELPER") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 || index+1 >= len(os.Args) {
		os.Exit(90)
	}
	args := os.Args[index+1:]
	switch args[0] {
	case "echo":
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": args[1:], "cwd": cwd, "allowed": os.Getenv("ALLOWED"), "secret": os.Getenv("SAFEPATCH_HOST_SECRET"), "path": os.Getenv("PATH")})
	case "write":
		value, err := os.ReadFile("source.txt")
		if err != nil {
			os.Exit(91)
		}
		if os.WriteFile("source.txt", []byte("changed"), 0600) != nil {
			os.Exit(92)
		}
		fmt.Print(string(value))
	case "exit":
		os.Exit(7)
	case "stdout", "stderr":
		output := os.Stdout
		if args[0] == "stderr" {
			output = os.Stderr
		}
		for {
			_, _ = output.Write([]byte(strings.Repeat("x", 8192)))
		}
	case "sleep":
		time.Sleep(time.Minute)
	case "child":
		time.Sleep(2 * time.Second)
		_ = os.WriteFile(args[1], []byte("survived"), 0600)
	case "spawn", "spawnexit":
		executable, _ := os.Executable()
		child := exec.Command(executable, helperArgs("child", args[1])...)
		child.Env = os.Environ()
		if child.Start() != nil {
			os.Exit(93)
		}
		_ = os.WriteFile(args[2], []byte("ready"), 0600)
		if args[0] == "spawn" {
			time.Sleep(time.Minute)
		}
	default:
		os.Exit(94)
	}
	os.Exit(0)
}

func TestLocalExactArgumentsEnvironmentAndSnapshot(t *testing.T) {
	r, request, base := localFixture(t)
	t.Setenv("SAFEPATCH_HOST_SECRET", "must-not-leak")
	request.Environment["ALLOWED"] = "visible"
	arguments := []string{"a b", `$(touch should-not-exist)`, `& echo injected`, `a"b`, ""}
	request.Arguments = helperArgs("echo", arguments...)
	result, err := r.Run(t.Context(), request)
	if err != nil || result.Status != verification.Completed || result.ExitCode != 0 || result.Sandboxed {
		t.Fatal(result, err)
	}
	var actual struct {
		Args                       []string
		Cwd, Allowed, Secret, Path string
	}
	if json.Unmarshal(result.Stdout, &actual) != nil || !slices.Equal(actual.Args, arguments) || actual.Secret != "" || actual.Path != "" || actual.Allowed != "visible" || !strings.HasPrefix(actual.Cwd, base) {
		t.Fatalf("unexpected output: %s", result.Stdout)
	}
	assertEmpty(t, base)
	if err := os.WriteFile(filepath.Join(request.SourceDirectory, "source.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	request.Arguments = helperArgs("write")
	result, err = r.Run(t.Context(), request)
	original, _ := os.ReadFile(filepath.Join(request.SourceDirectory, "source.txt"))
	if err != nil || string(result.Stdout) != "original" || string(original) != "original" {
		t.Fatal(result, string(original), err)
	}
	assertEmpty(t, base)
}

func TestLocalFailureOutputAndDeadline(t *testing.T) {
	for _, mode := range []string{"exit", "stdout", "stderr", "sleep", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r, request, base := localFixture(t)
			ctx := t.Context()
			request.Arguments = helperArgs(mode)
			want := verification.Failed
			switch mode {
			case "stdout", "stderr":
				want = verification.OutputLimited
			case "sleep":
				request.Limits.Timeout = 150 * time.Millisecond
				want = verification.TimedOut
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = verification.Cancelled
			}
			result, err := r.Run(ctx, request)
			if err == nil || result.Status != want || int64(len(result.Stdout)) > request.Limits.StdoutBytes || int64(len(result.Stderr)) > request.Limits.StderrBytes {
				t.Fatal(result, err)
			}
			if mode == "exit" && result.ExitCode != 7 {
				t.Fatal(result.ExitCode)
			}
			if mode == "stdout" && !result.StdoutTruncated || mode == "stderr" && !result.StderrTruncated {
				t.Fatal("missing truncation flag")
			}
			assertEmpty(t, base)
		})
	}
}

func TestLocalTerminatesDescendants(t *testing.T) {
	for _, mode := range []string{"spawn", "spawnexit"} {
		t.Run(mode, func(t *testing.T) {
			r, request, base := localFixture(t)
			external := t.TempDir()
			marker, ready := filepath.Join(external, "escaped"), filepath.Join(external, "ready")
			request.Arguments = helperArgs(mode, marker, ready)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			if mode == "spawn" {
				go func() {
					defer close(done)
					for {
						if _, err := os.Stat(ready); err == nil {
							cancel()
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(10 * time.Millisecond):
						}
					}
				}()
			} else {
				close(done)
			}
			result, err := r.Run(ctx, request)
			cancel()
			<-done
			if mode == "spawn" && !errors.Is(err, context.Canceled) || mode == "spawnexit" && err != nil {
				var pathError *os.PathError
				if errors.As(err, &pathError) {
					t.Logf("cleanup OS error: %v", pathError)
				}
				t.Fatal(result, err)
			}
			if _, err := os.Stat(ready); err != nil {
				t.Fatal("child was not started", err)
			}
			time.Sleep(2200 * time.Millisecond)
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("descendant survived process tree cleanup", err)
			}
			assertEmpty(t, base)
		})
	}
}

func TestLocalRejectsUntrustedOrUnsupportedRequests(t *testing.T) {
	r, original, base := localFixture(t)
	for _, name := range []string{"sandbox", "cpu", "memory", "pids", "workspace", "temp", "tool", "environment", "nil context", "zero runner"} {
		request := original
		request.Environment = map[string]string{}
		runner := r
		ctx := t.Context()
		switch name {
		case "sandbox":
			request.Mode = ""
		case "cpu":
			request.Limits.CPUMillis = 1
		case "memory":
			request.Limits.MemoryBytes = 1
		case "pids":
			request.Limits.PIDs = 1
		case "workspace":
			request.Limits.WorkspaceBytes = 1
		case "temp":
			request.Limits.TempBytes = 1
		case "tool":
			request.Executable = "unconfigured"
		case "environment":
			request.Environment["SECRET"] = "value"
		case "nil context":
			ctx = nil
		case "zero runner":
			runner = &LocalRunner{}
		}
		result, err := runner.Run(ctx, request)
		if err == nil || result.Status == verification.Completed {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
	}
	assertEmpty(t, base)
}

func assertEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("workspace not cleaned", entries, err)
	}
}
