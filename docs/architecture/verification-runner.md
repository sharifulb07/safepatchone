# Shared verification runners

`internal/verification` defines `Runner.Run(context.Context, ExecutionRequest)`
and `ExecutionResult`. `internal/platform/runner` implements `LocalRunner` and
`ContainerSandboxRunner`. Both serve all five editions. No new grant, API,
database table, migration, queue consumer, scanner, or verification verdict is
introduced. The CLI's verification command remains unimplemented.

## Trust and composition

The application selects the runner, approved tool, arguments, source directory,
and resource policy after authentication, tenant ownership, entitlement,
permission, and quota admission. Keep these choices in trusted orchestration;
do not deserialize a public request or an untrusted queue message directly into
`ExecutionRequest`. Existing `WorkerOperation.Start` rechecks access on every
dispatch and can invoke this shared runner inside its operation callback.
The runner is execution infrastructure and never grants access by itself.

`Executable` is a symbolic tool key resolved through a constructor allowlist.
Host tool paths are absolute; container tool paths are absolute Linux paths.
Arguments remain individual argv entries. No host shell is invoked. Allowlisting
an interpreter does not make its scripts safe: use the container runner for any
repository-derived execution, including build scripts, plugins, and tool config.

Limits are explicit execution policy, independent of edition. `CPUMillis=1000`
means one CPU of scheduling bandwidth, not one second of CPU time. Timeout covers
snapshot preparation and execution; cleanup has a separate bounded budget.
Stdout and stderr have independent byte caps. Input bytes and entry count bound
snapshot copying; workspace and temporary byte limits bound writable container
storage. The application must derive policy from effective limits and deployment
capacity and reserve quotas atomically. This adapter does not meter or settle
monthly quota usage. Global admission/concurrency limits still apply.

Constructor maps and environment allowlists are copied. Request data must not be
mutated concurrently with Run. Tools, images, temporary-directory ancestors and
the Docker daemon are trusted administrative inputs. Use a private local temporary
directory outside all repository source trees, with enough capacity for the
configured input and concurrency budgets. Source paths must be authorized to the
requesting tenant; path validation alone cannot establish ownership.

## ContainerSandboxRunner

Construct with `NewContainerSandboxRunner(ContainerSandboxConfig{Config: ...,
DockerExecutable: ..., Image: ..., UID: ..., GID: ...})`. Both UID and GID must
be nonzero. Images require a `sha256:` digest or `name@sha256:` reference and must
be provisioned locally: the runner uses `--pull never`. The reviewed image must
provide `/usr/bin/env`, `/bin/sh`, `cp`, and the configured tools. Docker must run
Linux containers and support the requested resource and bind-mount controls.
There is no pull, account, billing, or local-execution fallback.

The runner enforces:

- Non-root UID/GID; all capabilities dropped; no-new-privileges; init process;
  private IPC; network `none`; read-only root filesystem.
- CPU bandwidth, memory with swap disabled, and PID limits. Any creation warning
  fails closed, including warnings about unenforceable kernel limits.
- A read-only, nonrecursive bind of a private input snapshot. No writable host
  bind, host socket, published port, or device is exposed. Image-declared volumes
  are rejected because they could introduce unbounded writable storage.
- Separate size-bounded `/workspace` and `/tmp` tmpfs mounts. Workspace permits
  executing compiled outputs; `/tmp` is noexec. Both are nosuid and nodev.
  The fixed in-container script copies `/input` into `/workspace`, changes to the
  validated relative working directory, then executes literal argv. Its text is
  never built from repository content.
- An empty tool environment, followed by fixed PATH/HOME/TMPDIR and explicitly
  allowed request variables. Loader and shell injection variables and runner
  path variables cannot be allowlisted. Arbitrary tool-specific environment
  configuration still requires review. No host secrets are inherited.
- A fixed local Docker socket or Windows named pipe and an empty private Docker
  configuration directory. Host Docker environment, credentials, contexts and
  proxy settings are not inherited. Remote Docker endpoints are not accepted.
- Bounded capture of attached stdout/stderr, with execution terminated on
  overflow. Container log persistence is disabled.
- Explicit create, attach, exit-state inspection, and force removal (including
  anonymous volumes), with cleanup attempted under a fresh timeout even when
  execution is cancelled. Cleanup failure yields UNKNOWN and an error.

The daemon's exit state is authoritative. Malformed state, missing exit code,
running/dead state, daemon errors or failed cleanup cannot produce COMPLETED.
OOM and nonzero exits fail. Execution output remains untrusted evidence;
COMPLETED is not PASS and cannot authorize production.

Controls follow the [Docker container reference](https://docs.docker.com/reference/cli/docker/container/run/).
The daemon, kernel and image must be reviewed and hardened for the deployment's
threat model; containers share a kernel. A host/daemon crash or an interrupted
create can leave work whose removal cannot be confirmed. Callers must treat
`ErrCleanupFailed` as an operational incident and reconcile `safepatch-*`
containers after restart. This milestone does not add a durable orphan reaper.

## LocalRunner

Construct with `NewLocalRunner(Config{TemporaryDirectory: ..., Tools: ...,
EnvironmentAllowlist: ...})` and explicitly set `Mode: verification.TrustedLocal`.
The default execution mode is sandbox, so accidental use of LocalRunner for a
default request returns `ErrSandboxRequired`.

LocalRunner is for trusted tools and trusted inputs. It runs under the host user,
can access host files and network, and has no kernel resource sandbox. It rejects
nonzero CPU, memory, PID, workspace or temporary-storage limits instead of
claiming to enforce them. It still applies a disposable input copy, literal argv,
environment allowlist, execution timeout, bounded output, and cancellation.
Windows process creation retains the OS-required SystemRoot variable and supplies
private USERPROFILE/LOCALAPPDATA/APPDATA directories; arbitrary host environment
variables are not inherited. Temporary files use a separate private directory
from the input tree. Optional TrustedSearchPath sets reviewed tool directories
without allowing request-controlled PATH overrides.

Linux/macOS use a separate process group and kill the group on cancellation,
timeout, output overflow and normal completion. Trusted children must not detach
into independent sessions. Windows creates the process suspended, attaches it
to a kill-on-close Job Object, and only then resumes it. Assignment failures fail
closed. This removes the child-spawn race before job assignment and terminates
descendants even after their parent exits. Other OS adapters fail unavailable.
Windows cleanup waits until the Job Object reports no active processes before
removing the workspace; requesting termination alone is not sufficient.
Bounded retries handle Windows sharing violations while terminated processes'
directory handles are being released. Persistent removal failures remain errors.
See [Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects).

## Filesystem boundary and results

Both adapters copy source data using Go's handle-relative `os.Root` operations.
Directory roots are opened component by component with identity checks. Static
symlinks (including internal links), special files, portable traversal paths,
and linked source roots are rejected. File identity is checked after opening;
nonblocking Unix reads prevent a raced FIFO from hanging an open. Directory
enumeration uses bounded batches and a depth ceiling. Copy bytes remain bounded
even if files grow after stat. Copies retain basic executable bits, not ownership,
setuid bits or source hardlinks. Cleanup is anchored to the staging parent and
does not follow replaced symlinks.

Supply an immutable, local, tenant-approved source snapshot. These checks do not
provide a Git revision snapshot, prevent content changes during a copy, forbid
host mount points, detect source hardlinks to preexisting sensitive files, or
make blocking remote filesystems cancellable. Do not use attacker-selected host
paths or filesystems. There is no archive extraction or evidence export here.
Source/output content may contain secrets; later evidence handling must apply
the platform's redaction and access policies.

Execution results report COMPLETED, FAILED, TIMED_OUT, CANCELLED, OUTPUT_LIMIT,
or UNKNOWN, plus exit code (`-1` when unavailable), captured bytes, truncation,
sandbox status and duration. Errors support `errors.Is`. Invalid requests and
unavailable infrastructure fail closed. Results are process outcomes only.

## Validation

```powershell
go test ./...
go test -race ./...
go vet ./...
```

Tests include real local processes, argument and environment injection attempts,
copy isolation, output floods, nonzero exits, cancellation/deadlines, descendant
cleanup, traversal and symlink attacks, input limits, sandbox configuration and
Docker lifecycle failure paths. Worker composition tests prove entitlement and
permission denial block runner dispatch, cross-tenant dispatch is denied, and
grant changes preserve stored work and evidence. No premium workflow is added.

Live container validation is opt-in and requires a locally provisioned reviewed
image and absolute Docker CLI path:

```powershell
$env:SAFEPATCH_TEST_DOCKER = 'C:\Program Files\Docker\Docker\resources\bin\docker.exe'
$env:SAFEPATCH_TEST_SANDBOX_IMAGE = '<reviewed-image>@sha256:<64-hex-digest>'
go test ./internal/platform/runner -run TestContainerSandboxLive -v
```

The live test targets Linux containers with cgroup v2 and an image containing
the utilities used by its assertions. It checks identity, capabilities,
no-new-privileges, network namespace, read-only mounts, writable temporary
storage, actual cgroup limits, timeout, and cleanup. Without that configuration
it skips; mocked lifecycle tests are not evidence of kernel isolation.
