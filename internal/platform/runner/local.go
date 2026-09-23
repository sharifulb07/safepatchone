package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"safepatch/internal/verification"
)

// Config is trusted host policy, never repository content or queue payload.
// Tools and EnvironmentAllowlist are copied during construction.
type Config struct {
	TemporaryDirectory   string
	Tools                map[string]string
	EnvironmentAllowlist []string
	// TrustedSearchPath is administrative tool lookup policy, never request PATH.
	TrustedSearchPath []string
}

type configuration struct {
	temporary   string
	tools       map[string]string
	environment map[string]bool
	searchPath  string
}

func configure(c Config, container bool) (configuration, error) {
	result := configuration{temporary: c.TemporaryDirectory, tools: map[string]string{}, environment: map[string]bool{}}
	if !filepath.IsAbs(c.TemporaryDirectory) || len(c.Tools) == 0 || len(c.Tools) > 128 || len(c.EnvironmentAllowlist) > 128 {
		return result, verification.ErrInvalidExecution
	}
	if err := rejectRootLink(c.TemporaryDirectory); err != nil {
		return result, err
	}
	for key, executable := range c.Tools {
		valid := filepath.IsAbs(executable)
		if container {
			valid = strings.HasPrefix(executable, "/") && verification.ValidWorkingDirectory(strings.TrimPrefix(executable, "/")) && executable != "/"
		}
		if !verification.ValidToolName(key) || !valid || strings.ContainsAny(executable, "\x00\r\n") {
			return result, verification.ErrInvalidExecution
		}
		if !container {
			info, err := os.Stat(executable)
			if err != nil || !info.Mode().IsRegular() {
				return result, verification.ErrInvalidExecution
			}
		}
		result.tools[key] = executable
	}
	for _, name := range c.EnvironmentAllowlist {
		// The caller may allow tool-specific variables, but cannot override
		// runner paths, the shell, or dynamic loader injection settings.
		if !verification.ValidEnvironmentName(name) || reservedEnvironment(name) {
			return result, verification.ErrInvalidExecution
		}
		result.environment[name] = true
	}
	if len(c.TrustedSearchPath) > 32 {
		return result, verification.ErrInvalidExecution
	}
	separator := string(os.PathListSeparator)
	if container {
		separator = ":"
	}
	for _, directory := range c.TrustedSearchPath {
		valid := filepath.IsAbs(directory)
		if container {
			valid = strings.HasPrefix(directory, "/") && verification.ValidWorkingDirectory(strings.TrimPrefix(directory, "/"))
		}
		if !valid || strings.ContainsAny(directory, separator+"\x00\r\n") {
			return result, verification.ErrInvalidExecution
		}
	}
	result.searchPath = strings.Join(c.TrustedSearchPath, separator)
	return result, nil
}

func reservedEnvironment(name string) bool {
	if strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") || strings.HasPrefix(name, "DOCKER_") {
		return true
	}
	switch name {
	case "PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "ENV", "BASH_ENV", "SHELLOPTS", "BASHOPTS", "IFS", "NODE_OPTIONS", "PYTHONPATH", "PYTHONHOME":
		return true
	}
	return false
}

func (c configuration) environmentFor(request verification.ExecutionRequest) ([]string, error) {
	env := make([]string, 0, len(request.Environment))
	for key, value := range request.Environment {
		if !c.environment[key] {
			return nil, verification.ErrInvalidExecution
		}
		env = append(env, key+"="+value)
	}
	sort.Strings(env)
	if c.searchPath != "" {
		env = append(env, "PATH="+c.searchPath)
	}
	return env, nil
}

// LocalRunner is for explicitly trusted tools AND trusted inputs. It does not
// isolate filesystem access, network, identity, or kernel resources. Untrusted
// repositories must use ContainerSandboxRunner; there is no automatic fallback.
type LocalRunner struct{ config configuration }

func NewLocalRunner(config Config) (*LocalRunner, error) {
	c, err := configure(config, false)
	if err != nil {
		return nil, err
	}
	return &LocalRunner{config: c}, nil
}

func (r *LocalRunner) Run(ctx context.Context, request verification.ExecutionRequest) (result verification.ExecutionResult, err error) {
	result = verification.ExecutionResult{Status: verification.Unknown, ExitCode: -1}
	if r == nil || ctx == nil {
		return result, verification.ErrInvalidExecution
	}
	if request.Mode != verification.TrustedLocal {
		return result, verification.ErrSandboxRequired
	}
	if err = request.Validate(); err != nil {
		return result, err
	}
	l := request.Limits
	if l.CPUMillis != 0 || l.MemoryBytes != 0 || l.PIDs != 0 || l.WorkspaceBytes != 0 || l.TempBytes != 0 {
		return result, verification.ErrUnsupportedLimits
	}
	executable, found := r.config.tools[request.Executable]
	if !found {
		return result, verification.ErrInvalidExecution
	}
	env, err := r.config.environmentFor(request)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()
	w, err := snapshot(ctx, r.config.temporary, request.SourceDirectory, l)
	if err != nil {
		return contextResult(ctx, result), err
	}
	defer func() {
		if cleanup := w.close(); cleanup != nil {
			result.Status = verification.Unknown
			err = errors.Join(err, cleanup)
		}
	}()
	cwd := filepath.Join(w.directory, filepath.FromSlash(request.WorkingDirectory))
	if err = rejectRootLink(cwd); err != nil {
		return result, err
	}
	// Keep the process temporary root distinct from the repository root. Go
	// intentionally ignores go.mod at the system temp root, and tools may treat
	// that directory differently from an ordinary working tree.
	temporary := filepath.Join(filepath.Dir(w.directory), "tmp")
	if err := w.base.Mkdir(w.name+"/tmp", 0700); err != nil {
		return result, verification.ErrRunnerUnavailable
	}
	env = append(env, "HOME="+w.directory, "TMPDIR="+temporary, "TMP="+temporary, "TEMP="+temporary)
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+w.directory, "LOCALAPPDATA="+filepath.Join(filepath.Dir(w.directory), "localappdata"), "APPDATA="+filepath.Join(filepath.Dir(w.directory), "appdata"))
	}
	return runProcess(ctx, executable, append([]string(nil), request.Arguments...), env, cwd, l.StdoutBytes, l.StderrBytes)
}

func contextResult(ctx context.Context, result verification.ExecutionResult) verification.ExecutionResult {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Status = verification.TimedOut
	} else if ctx.Err() != nil {
		result.Status = verification.Cancelled
	}
	return result
}

var _ verification.Runner = (*LocalRunner)(nil)
