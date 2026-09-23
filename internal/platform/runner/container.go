package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"safepatch/internal/verification"
)

type ContainerSandboxConfig struct {
	Config
	DockerExecutable string // absolute path to a trusted Docker CLI
	Image            string // locally provisioned immutable image digest
	UID, GID         uint32 // both must be nonzero
}

type processExecutor func(context.Context, string, []string, []string, string, int64, int64) (verification.ExecutionResult, error)

type ContainerSandboxRunner struct {
	config        configuration
	docker, image string
	uid, gid      uint32
	execute       processExecutor
}

var digestImage = regexp.MustCompile(`^(?:[a-zA-Z0-9][a-zA-Z0-9._:/-]*@)?sha256:[a-f0-9]{64}$`)

func NewContainerSandboxRunner(config ContainerSandboxConfig) (*ContainerSandboxRunner, error) {
	c, err := configure(config.Config, true)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(config.DockerExecutable) || len(config.Image) > 512 || !digestImage.MatchString(config.Image) || config.UID == 0 || config.GID == 0 || strings.ContainsAny(c.temporary, ",\r\n") {
		return nil, verification.ErrInvalidExecution
	}
	info, err := os.Stat(config.DockerExecutable)
	if err != nil || !info.Mode().IsRegular() {
		return nil, verification.ErrRunnerUnavailable
	}
	return &ContainerSandboxRunner{config: c, docker: config.DockerExecutable, image: config.Image, uid: config.UID, gid: config.GID, execute: runProcess}, nil
}

// The fixed script never interpolates request arguments. /input is immutable;
// all tool writes go to size-bounded tmpfs mounts, never a writable host bind.
const sandboxScript = `cp -R /input/. /workspace/ && cd "$1" && shift && exec "$@"`

func (r *ContainerSandboxRunner) createArguments(name, source string, request verification.ExecutionRequest, env []string) []string {
	l := request.Limits
	decimal := func(n int64) string { return strconv.FormatInt(n, 10) }
	working := "/workspace"
	if request.WorkingDirectory != "" && request.WorkingDirectory != "." {
		working += "/" + request.WorkingDirectory
	}
	args := []string{"create", "--name", name, "--pull", "never", "--user", strconv.FormatUint(uint64(r.uid), 10) + ":" + strconv.FormatUint(uint64(r.gid), 10),
		"--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--init", "--ipc", "private",
		"--cpus", decimal(l.CPUMillis/1000) + "." + strconv.FormatInt(1000+l.CPUMillis%1000, 10)[1:], "--memory", decimal(l.MemoryBytes), "--memory-swap", decimal(l.MemoryBytes), "--pids-limit", decimal(l.PIDs),
		"--ulimit", "core=0:0", "--log-driver", "none",
		"--mount", "type=bind,source=" + source + ",target=/input,readonly,bind-recursive=disabled",
		"--tmpfs", "/workspace:rw,exec,nosuid,nodev,size=" + decimal(l.WorkspaceBytes) + ",mode=1777",
		"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=" + decimal(l.TempBytes) + ",mode=1777",
		"--workdir", "/workspace", "--entrypoint", "/usr/bin/env", r.image,
		"-i", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/workspace", "TMPDIR=/tmp"}
	args = append(args, env...)
	args = append(args, "/bin/sh", "-c", sandboxScript, "safepatch", working, r.config.tools[request.Executable])
	return append(args, request.Arguments...)
}

func (r *ContainerSandboxRunner) Run(ctx context.Context, request verification.ExecutionRequest) (result verification.ExecutionResult, err error) {
	started := time.Now()
	result = verification.ExecutionResult{Status: verification.Unknown, ExitCode: -1}
	if r == nil || ctx == nil || r.execute == nil {
		return result, verification.ErrInvalidExecution
	}
	if request.Mode == verification.TrustedLocal {
		return result, verification.ErrInvalidExecution
	}
	if err = request.Validate(); err != nil {
		return result, err
	}
	if _, found := r.config.tools[request.Executable]; !found {
		return result, verification.ErrInvalidExecution
	}
	env, err := r.config.environmentFor(request)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, request.Limits.Timeout)
	defer cancel()
	w, err := snapshot(ctx, r.config.temporary, request.SourceDirectory, request.Limits)
	if err != nil {
		return contextResult(ctx, result), err
	}
	defer func() {
		if cleanup := w.close(); cleanup != nil {
			result.Status = verification.Unknown
			err = errors.Join(err, cleanup)
		}
		result.Duration = time.Since(started)
	}()
	// The private parent prevents other host users accessing the staging tree;
	// the bind root itself must be traversable by the non-root container user.
	if err = os.Chmod(w.directory, 0755); err != nil {
		return result, verification.ErrRunnerUnavailable
	}
	control, err := os.MkdirTemp(r.config.temporary, "safepatch-run-")
	if err != nil {
		return result, verification.ErrRunnerUnavailable
	}
	parent, err := openRootNoLinks(r.config.temporary)
	if err != nil {
		_ = os.Remove(control)
		return result, verification.ErrRunnerUnavailable
	}
	controlWorkspace := &workspace{base: parent, name: filepath.Base(control), directory: control}
	defer func() {
		if cleanup := controlWorkspace.close(); cleanup != nil {
			result.Status = verification.Unknown
			err = errors.Join(err, cleanup)
		}
	}()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return result, verification.ErrRunnerUnavailable
	}
	name := "safepatch-" + hex.EncodeToString(random[:])
	host := "unix:///var/run/docker.sock"
	if runtime.GOOS == "windows" {
		host = "npipe:////./pipe/docker_engine"
	}
	call := func(callCtx context.Context, args []string, stdout, stderr int64) (verification.ExecutionResult, error) {
		prefix := []string{"--config", control, "--host", host}
		return r.execute(callCtx, r.docker, append(prefix, args...), []string{}, control, stdout, stderr)
	}
	// Image-declared volumes would introduce unbounded writable storage even
	// with a read-only root. Refuse them before creating any container.
	imageInfo, imageErr := call(ctx, []string{"image", "inspect", "--format", "{{json .Config.Volumes}}", r.image}, 4096, 4096)
	var volumes map[string]json.RawMessage
	if imageErr != nil || json.Unmarshal(imageInfo.Stdout, &volumes) != nil || len(volumes) != 0 {
		return contextResult(ctx, result), errors.Join(verification.ErrRunnerUnavailable, ctx.Err())
	}
	// Even cancelled or failed creation gets a removal attempt under a fresh
	// context. An uncertain cleanup is never reported as successful execution.
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cleanupCancel()
		_, cleanupErr := call(cleanupCtx, []string{"rm", "--force", "--volumes", name}, 4096, 4096)
		if cleanupErr != nil {
			result.Status = verification.Unknown
			err = errors.Join(err, verification.ErrCleanupFailed)
		}
	}()
	created, createErr := call(ctx, r.createArguments(name, w.directory, request, env), 4096, 4096)
	// Docker reports unsupported kernel limits as warnings on some hosts.
	// Treat any creation diagnostic as uncertainty, never silently ignore it.
	if createErr != nil || len(created.Stderr) != 0 {
		return contextResult(ctx, result), errors.Join(verification.ErrRunnerUnavailable, ctx.Err())
	}
	result, err = call(ctx, []string{"start", "--attach", name}, request.Limits.StdoutBytes, request.Limits.StderrBytes)
	result.Sandboxed = true
	// Nonzero CLI status, cancellation and output overflow are never accepted.
	if err != nil && !errors.Is(err, verification.ErrExecutionFailed) {
		return result, err
	}
	inspection, inspectErr := call(ctx, []string{"inspect", "--format", "{{json .State}}", name}, 4096, 4096)
	if inspectErr != nil {
		result.Status = verification.Unknown
		result.ExitCode = -1
		return contextResult(ctx, result), errors.Join(verification.ErrRunnerUnavailable, ctx.Err())
	}
	var state struct {
		Status    string
		Running   bool
		Dead      bool
		OOMKilled bool
		ExitCode  *int
		Error     string
	}
	if json.Unmarshal(inspection.Stdout, &state) != nil || state.Status != "exited" || state.Running || state.Dead || state.ExitCode == nil || *state.ExitCode < 0 || state.Error != "" {
		result.Status = verification.Unknown
		result.ExitCode = -1
		return result, verification.ErrRunnerUnavailable
	}
	result.ExitCode = *state.ExitCode
	if err != nil && result.ExitCode == 0 {
		result.Status = verification.Unknown
		result.ExitCode = -1
		return result, verification.ErrRunnerUnavailable
	}
	if state.OOMKilled || result.ExitCode != 0 {
		result.Status = verification.Failed
		return result, verification.ErrExecutionFailed
	}
	result.Status = verification.Completed
	return result, nil
}

var _ verification.Runner = (*ContainerSandboxRunner)(nil)
