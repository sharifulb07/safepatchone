package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"safepatch/internal/verification"
)

func containerFixture(t *testing.T) (*ContainerSandboxRunner, verification.ExecutionRequest, string) {
	t.Helper()
	_, request, base := localFixture(t)
	executable, _ := os.Executable()
	r, err := NewContainerSandboxRunner(ContainerSandboxConfig{Config: Config{TemporaryDirectory: base, Tools: map[string]string{"helper": "/usr/bin/tool"}, EnvironmentAllowlist: []string{"ALLOWED"}}, DockerExecutable: executable, Image: "example/tool@sha256:" + strings.Repeat("a", 64), UID: 10001, GID: 10001})
	if err != nil {
		t.Fatal(err)
	}
	request.Mode = ""
	request.Environment = map[string]string{"ALLOWED": "literal $(payload)"}
	request.Arguments = []string{"a b", "; touch /input/injected", "--privileged", ""}
	request.Limits.CPUMillis = 1250
	request.Limits.MemoryBytes = 64 * 1024 * 1024
	request.Limits.PIDs = 32
	request.Limits.WorkspaceBytes = 8 * 1024 * 1024
	request.Limits.TempBytes = 1024 * 1024
	return r, request, base
}

func dockerVerb(args []string) string {
	if len(args) < 5 {
		return ""
	}
	return args[4]
}
func successfulDocker(args []string) verification.ExecutionResult {
	result := verification.ExecutionResult{Status: verification.Completed, ExitCode: 0}
	switch dockerVerb(args) {
	case "image":
		result.Stdout = []byte("null\n")
	case "inspect":
		result.Stdout = []byte(`{"Status":"exited","Running":false,"Dead":false,"OOMKilled":false,"ExitCode":0,"Error":""}`)
	case "start":
		result.Stdout = []byte("tool output")
		result.Stderr = []byte("tool diagnostic")
	}
	return result
}

func TestContainerSandboxPolicyAndLifecycle(t *testing.T) {
	r, request, base := containerFixture(t)
	var calls []string
	var containerName string
	r.execute = func(ctx context.Context, executable string, args, env []string, cwd string, out, stderr int64) (verification.ExecutionResult, error) {
		verb := dockerVerb(args)
		calls = append(calls, verb)
		if executable != r.docker || len(env) != 0 || env == nil || args[0] != "--config" || args[1] != cwd || args[2] != "--host" || strings.HasPrefix(args[3], "tcp:") {
			t.Fatal("unsafe Docker client", args, env)
		}
		if _, found := ctx.Deadline(); !found {
			t.Fatal("unbounded lifecycle operation")
		}
		if verb == "create" {
			for flag, value := range map[string]string{"--pull": "never", "--user": "10001:10001", "--network": "none", "--cap-drop": "ALL", "--security-opt": "no-new-privileges=true", "--ipc": "private", "--cpus": "1.250", "--memory": "67108864", "--memory-swap": "67108864", "--pids-limit": "32", "--log-driver": "none", "--entrypoint": "/usr/bin/env", "--ulimit": "core=0:0"} {
				index := slices.Index(args, flag)
				if index < 0 || index+1 >= len(args) || args[index+1] != value {
					t.Fatalf("missing %s=%s in %v", flag, value, args)
				}
			}
			for _, flag := range []string{"--read-only", "--init", "/workspace:rw,exec,nosuid,nodev,size=8388608,mode=1777", "/tmp:rw,nosuid,nodev,noexec,size=1048576,mode=1777"} {
				if !slices.Contains(args, flag) {
					t.Fatal("missing", flag)
				}
			}
			mount := args[slices.Index(args, "--mount")+1]
			if !strings.HasPrefix(mount, "type=bind,source="+base) || !strings.HasSuffix(mount, ",target=/input,readonly,bind-recursive=disabled") {
				t.Fatal(mount)
			}
			image := slices.Index(args, r.image)
			want := append([]string{"-i", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/workspace", "TMPDIR=/tmp", "ALLOWED=literal $(payload)", "/bin/sh", "-c", sandboxScript, "safepatch", "/workspace", "/usr/bin/tool"}, request.Arguments...)
			if image < 0 || !slices.Equal(args[image+1:], want) {
				t.Fatal("arguments were interpolated", args)
			}
			containerName = args[slices.Index(args, "--name")+1]
		} else if verb == "start" || verb == "inspect" || verb == "rm" {
			if args[len(args)-1] != containerName || !strings.HasPrefix(containerName, "safepatch-") {
				t.Fatal("container identity mismatch", args)
			}
		}
		return successfulDocker(args), nil
	}
	result, err := r.Run(t.Context(), request)
	if err != nil || result.Status != verification.Completed || !result.Sandboxed || result.ExitCode != 0 || string(result.Stdout) != "tool output" || string(result.Stderr) != "tool diagnostic" {
		t.Fatal(result, err)
	}
	if !slices.Equal(calls, []string{"image", "create", "start", "inspect", "rm"}) {
		t.Fatal(calls)
	}
	assertEmpty(t, base)
}

func TestContainerFailureAndCleanupPaths(t *testing.T) {
	for _, failure := range []string{"image", "volume", "create", "warning", "start", "inspect", "invalid state", "missing exit", "running", "oom", "nonzero", "rm", "cancel", "overflow"} {
		t.Run(failure, func(t *testing.T) {
			r, request, base := containerFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls []string
			r.execute = func(callCtx context.Context, _ string, args, _ []string, _ string, _, _ int64) (verification.ExecutionResult, error) {
				verb := dockerVerb(args)
				calls = append(calls, verb)
				result := successfulDocker(args)
				if verb == "rm" && callCtx.Err() != nil {
					t.Fatal("cleanup inherited cancelled execution context")
				}
				if verb == failure {
					return verification.ExecutionResult{Status: verification.Unknown, ExitCode: -1}, verification.ErrRunnerUnavailable
				}
				if verb == "image" && failure == "volume" {
					result.Stdout = []byte(`{"/unbounded":{}}`)
				}
				if verb == "create" && failure == "warning" {
					result.Stderr = []byte("kernel cannot enforce memory limit")
				}
				if verb == "start" {
					switch failure {
					case "cancel":
						cancel()
						result.Status = verification.Cancelled
						return result, context.Canceled
					case "overflow":
						result.Status = verification.OutputLimited
						result.StdoutTruncated = true
						return result, verification.ErrOutputLimit
					case "nonzero":
						result.Status = verification.Failed
						result.ExitCode = 7
						return result, verification.ErrExecutionFailed
					}
				}
				if verb == "inspect" {
					switch failure {
					case "invalid state":
						result.Stdout = []byte("not JSON")
					case "missing exit":
						result.Stdout = []byte(`{"Status":"exited"}`)
					case "running":
						result.Stdout = []byte(`{"Status":"running","Running":true,"ExitCode":0}`)
					case "oom":
						result.Stdout = []byte(`{"Status":"exited","OOMKilled":true,"ExitCode":0}`)
					case "nonzero":
						result.Stdout = []byte(`{"Status":"exited","ExitCode":7}`)
					}
				}
				return result, nil
			}
			result, err := r.Run(ctx, request)
			if err == nil || result.Status == verification.Completed {
				t.Fatal(result, err)
			}
			if failure != "image" && failure != "volume" && calls[len(calls)-1] != "rm" {
				t.Fatal("no cleanup", calls)
			}
			if failure == "nonzero" && (result.Status != verification.Failed || result.ExitCode != 7) {
				t.Fatal(result)
			}
			if failure == "rm" && !errors.Is(err, verification.ErrCleanupFailed) {
				t.Fatal(err)
			}
			if failure == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			assertEmpty(t, base)
		})
	}
}

func TestRunnerConfigurationIsTrustedAndCopied(t *testing.T) {
	executable, _ := os.Executable()
	config := Config{TemporaryDirectory: t.TempDir(), Tools: map[string]string{"helper": executable}, EnvironmentAllowlist: []string{"ALLOWED"}}
	r, err := NewLocalRunner(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Tools["helper"] = "malicious"
	config.EnvironmentAllowlist[0] = "FORGED"
	if r.config.tools["helper"] != executable || !r.config.environment["ALLOWED"] || r.config.environment["FORGED"] {
		t.Fatal("configuration aliases caller data")
	}
	for _, env := range []string{"PATH", "LD_PRELOAD", "DOCKER_HOST", "BASH_ENV", "IFS", "NODE_OPTIONS", "bad=name"} {
		_, err := NewLocalRunner(Config{TemporaryDirectory: config.TemporaryDirectory, Tools: map[string]string{"helper": executable}, EnvironmentAllowlist: []string{env}})
		if err == nil {
			t.Fatal("dangerous environment allowed", env)
		}
	}
	for _, mode := range []string{"tag", "uid", "gid", "relative docker", "relative tool"} {
		c := ContainerSandboxConfig{Config: Config{TemporaryDirectory: config.TemporaryDirectory, Tools: map[string]string{"helper": "/bin/tool"}}, DockerExecutable: executable, Image: "sha256:" + strings.Repeat("a", 64), UID: 1000, GID: 1000}
		switch mode {
		case "tag":
			c.Image = "tool:latest"
		case "uid":
			c.UID = 0
		case "gid":
			c.GID = 0
		case "relative docker":
			c.DockerExecutable = "docker"
		case "relative tool":
			c.Tools["helper"] = "tool"
		}
		if _, err := NewContainerSandboxRunner(c); err == nil {
			t.Fatal("invalid sandbox config accepted", mode)
		}
	}
}

func TestContainerRejectsBeforeRuntime(t *testing.T) {
	r, request, base := containerFixture(t)
	r.execute = func(context.Context, string, []string, []string, string, int64, int64) (verification.ExecutionResult, error) {
		t.Fatal("invalid request reached Docker")
		return verification.ExecutionResult{}, nil
	}
	request.Mode = verification.TrustedLocal
	if _, err := r.Run(t.Context(), request); err == nil {
		t.Fatal("local mode accepted")
	}
	request.Mode = ""
	request.Environment["SECRET"] = "secret"
	if _, err := r.Run(t.Context(), request); err == nil {
		t.Fatal("unlisted environment accepted")
	}
	delete(request.Environment, "SECRET")
	request.WorkingDirectory = "../escape"
	if _, err := r.Run(t.Context(), request); err == nil {
		t.Fatal("traversal accepted")
	}
	assertEmpty(t, base)
}

// Opt in with a preloaded, reviewed Linux image digest containing env, sh, cp,
// id, cat, grep, and sleep. No image pull or network access is performed.
func TestContainerSandboxLive(t *testing.T) {
	image, docker := os.Getenv("SAFEPATCH_TEST_SANDBOX_IMAGE"), os.Getenv("SAFEPATCH_TEST_DOCKER")
	if image == "" || docker == "" {
		t.Skip("set SAFEPATCH_TEST_SANDBOX_IMAGE and SAFEPATCH_TEST_DOCKER for live Linux Docker validation")
	}
	base, source := t.TempDir(), t.TempDir()
	r, err := NewContainerSandboxRunner(ContainerSandboxConfig{Config: Config{TemporaryDirectory: base, Tools: map[string]string{"sh": "/bin/sh"}}, DockerExecutable: docker, Image: image, UID: 10001, GID: 10001})
	if err != nil {
		t.Fatal(err)
	}
	request := verification.ExecutionRequest{Executable: "sh", SourceDirectory: source, Limits: verification.ExecutionLimits{Timeout: 20 * time.Second, StdoutBytes: 4096, StderrBytes: 4096, InputBytes: 1024, InputEntries: 10, WorkspaceBytes: 8 * 1024 * 1024, TempBytes: 1024 * 1024, CPUMillis: 500, MemoryBytes: 64 * 1024 * 1024, PIDs: 32}, Arguments: []string{"-c", `set -eu; test "$(id -u)" = 10001; test "$(id -g)" = 10001; test ! -e /var/run/docker.sock; ! touch /root-write-check 2>/dev/null; ! touch /input/write-check 2>/dev/null; echo ok > /workspace/write-check; echo ok > /tmp/write-check; test "$(cat /sys/fs/cgroup/memory.max)" = 67108864; test "$(cat /sys/fs/cgroup/pids.max)" = 32; test "$(cat /sys/fs/cgroup/cpu.max)" = "50000 100000"; grep -q '^CapEff:[[:space:]]*0000000000000000$' /proc/self/status; grep -q '^NoNewPrivs:[[:space:]]*1$' /proc/self/status; test "$(ls /sys/class/net)" = lo; echo isolated`}}
	result, err := r.Run(t.Context(), request)
	if err != nil || result.Status != verification.Completed || string(result.Stdout) != "isolated\n" {
		t.Fatalf("sandbox: %+v %v, stderr=%s", result, err, result.Stderr)
	}
	if _, err := os.Stat(filepath.Join(source, "write-check")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source changed", err)
	}
	assertEmpty(t, base)
	request.Arguments = []string{"-c", "sleep 60"}
	request.Limits.Timeout = 2 * time.Second
	result, err = r.Run(t.Context(), request)
	if !errors.Is(err, context.DeadlineExceeded) || result.Status != verification.TimedOut {
		t.Fatal(result, err)
	}
	assertEmpty(t, base)
}
