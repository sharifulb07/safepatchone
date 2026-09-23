# First-class Go verification

`languages/golang` implements the shared Verifier SDK with ID `go`, implementation
version `1.0.0`, and eight technical capabilities. It has no edition, entitlement,
subscription, or user inputs. All processes execute through the injected Runner.

| Capability | Planned operation | Access requirement |
| --- | --- | --- |
| `go.build` | `go build -json ./...` | `verify.basic` |
| `go.test` | `go test -json -count=1 -vet=off ./...` | `verify.basic` |
| `go.test.race` | Test with `-race` | `verify.basic` |
| `go.vet` | `go vet -json ./...` | `verify.basic` |
| `go.staticcheck` | `staticcheck -f=json -checks=all -fail=all -show-ignored ./...` | `verify.security` |
| `go.govulncheck` | `govulncheck -json -scan=symbol -db=file:///... ./...` | `verify.security` |
| `go.fuzz` | Test with an exact configured fuzz target and bounded fuzz duration | `verify.basic` plus applicable fuzz quota |
| `go.benchmark` | Test with `-run=^$ -bench=. -benchmem` and configured iteration count | `verify.basic` |

`application.GoVerificationRequirement(catalog, capability)` resolves these
requirements against the existing FeatureCatalog and requires `proof.execute`
permission separately. Feed the requirement into ExecutionGuard/WorkerOperation;
it is not itself admission. Both security operations have tests for entitled and
unentitled organizations, denied actor permission, tenant isolation, and preserved
evidence after grants change. No catalog keys, edition bundles, or grants change.

Quota admission remains the application's responsibility. Reserve concurrency
and applicable usage before invoking the SDK; for fuzzing, account for the number
of selected targets times the configured seconds. A `verify.basic` grant does not
override a disabled `fuzz_seconds_monthly` limit. Settle through the existing
QuotaService rather than using advisory Check alone.

## Construction and repository coverage

Create `golang.New(golang.Config{...})`, register it with
`verification.NewRegistry`, and call Registry.Run inside admitted work. Config
selects Runner tool IDs, parallelism, CGO availability, fuzz targets/seconds,
benchmark iterations, and an optional vulnerability database. It is trusted
deployment policy, never parsed from repository configuration or queue claims.

Provide a complete, immutable `verification.Target` with repository/revision
identity, repository-relative `Files`, and `Metadata` containing the text of all
relevant `go.mod` and `go.work` files. Metadata must come from the same authorized
source snapshot Runner will execute. SDK validation bounds its size and copies
it between stages. It does not establish tenant ownership or collect files itself.

The adapter uses the official [Go module-file parser](https://pkg.go.dev/golang.org/x/mod/modfile).
It discovers root and nested modules, validates workspace use/replace directives,
and runs each module separately so root `./...` cannot silently omit nested
modules. Paths are portable and repository-relative; local replacements/workspace
members must stay inside the snapshot and have a declared go.mod. External,
absolute, missing, malformed, and escaping local references fail before execution.
Vendor, Git metadata and node_modules are excluded from module discovery.

Go's nearest-ancestor workspace semantics remain active. Every discovered module
under a go.work must be included in that workspace; otherwise the adapter rejects
incomplete coverage instead of silently skipping it. To verify intentionally
independent modules excluded by an ancestor workspace, submit a separate immutable
source scope without that workspace. Up to sixteen module/target steps fit one
SDK run; larger repositories must be explicitly partitioned by orchestration.

Each fuzz target specifies module directory, package directory within that module,
and an exact `FuzzName`. Regex/flag injection is rejected. Missing targets/budgets
do not run an unbounded default. Benchmark iterations, fuzz seconds and parallelism
are explicit configuration; Runner timeout/output/CPU/memory/PID/storage limits
remain authoritative. Fuzzing executes only selected targets, not every fuzz
function in the repository.

## Tool and sandbox provisioning

This implementation targets Go 1.26 and its JSON event formats. Configure an
absolute reviewed Go executable in the Runner tool allowlist, or provision Go in
an immutable sandbox image. Race verification requires CGO and a compatible C
compiler. Staticcheck and Govulncheck are separate operator-provisioned tools;
an unconfigured optional tool or vulnerability database returns UNAVAILABLE.
Verification never downloads or installs tool binaries.

Set `PlanRequest.BaseExecution.Environment` to `verifier.Environment()` and allow
those variable names in Runner configuration. The adapter requires exact policy:
GOENV is off, GOFLAGS is empty, toolchain auto-download is disabled, GOPROXY and
GOSUMDB are off, GOWORK uses normal discovery, and CGO/GOMAXPROCS are explicit.
Dependencies must be vendored or pre-provisioned; an offline dependency failure
cannot become PASS. Go files, cgo directives, tests and benchmarks are executable
untrusted input, so production repository execution requires ContainerSandboxRunner.

Runner's optional `TrustedSearchPath` supplies administrative tool lookup paths
for Go, C compilers and tools that invoke Go. Requests still cannot set PATH.
Container paths should include Go's bin directory plus `/usr/bin` and `/bin` so
the sandbox bootstrap utilities remain available. No host PATH is inherited.

LocalRunner's temporary directory is separate from its repository directory:
Go intentionally ignores go.mod at the process temp root. On Windows the runner
also supplies private USERPROFILE/LOCALAPPDATA/APPDATA locations so Go's cache
can be created without inheriting a host profile. The default build cache is
disposable. An optional configured BuildCache is for trusted, private cache
provisioning; never share writable caches across tenants or with untrusted work.

Govulncheck requires a trusted local `file:///...` vulnerability database outside
repository control (for example baked into the reviewed read-only image). Network
URLs and repository-controlled `/workspace` or `/input` database locations are
rejected. The operator owns database authenticity and freshness; this milestone
does not fetch, refresh, sign, or validate a database release. No online fallback
or simulated vulnerability result exists.

## Evidence

All results use `safepatch.verification.v1`, independent of commercial edition.
Go test/race/fuzz normalization validates structured event ordering and package
completion. Arbitrary test Output strings cannot set verification status. Missing
events remain UNKNOWN and no executed tests yields SKIPPED. Benchmarks reassemble
split output events and record validated iterations and numeric metrics as INFO
findings; PASS means measurements completed, not that performance improved.

Build exit status and structured build failures determine build outcomes. Go vet
JSON is accepted on stdout or stderr (with Go's package headings removed).
Staticcheck JSON diagnostics become findings; ignored diagnostics remain WARN.
Govulncheck requires matching protocol/configuration and a nonempty scan inventory;
symbol findings are FAIL, module/package-only findings are WARN. Vulnerability
IDs and fixed versions are retained. Unknown, contradictory, duplicate-key,
case-aliased or malformed output cannot become PASS. A nonzero tool exit never
becomes PASS, and raw output is retained only through Runner's bounded evidence
digests. Diagnostic text remains untrusted and needs normal evidence redaction
and escaped rendering. Absolute ephemeral diagnostic paths are omitted.

Tool protocol references: [Go command](https://pkg.go.dev/cmd/go),
[Staticcheck JSON](https://staticcheck.dev/docs/running-staticcheck/cli/formatters/),
and [Govulncheck protocol](https://raw.githubusercontent.com/golang/vuln/v1.1.4/internal/govulncheck/govulncheck.go).
Reports do not authorize production. Checks do not include generated-code
execution, benchmark regression baselines, fuzz corpus export, or scanner output
signatures. Fuzz discoveries reside in disposable execution storage; durable
artifact export is a separate integration.

## Validation and current integration boundary

```powershell
go test ./...
go test -race ./...
go vet ./...
$env:SAFEPATCH_GO_LIVE = '1'
go test ./languages/golang -run TestNativeGo -v -count=1
```

The opt-in tests run real build/test/race/vet/fuzz/benchmark processes through
LocalRunner against trusted fixtures, including a failing test, cross-module
workspace dependencies and independent modules. They use private caches. To add
real optional-tool checks, set absolute `SAFEPATCH_STATICCHECK` and
`SAFEPATCH_GOVULNCHECK` executable paths and `SAFEPATCH_VULN_DB` to the trusted
local database URL. Missing prerequisites are explicit skips, not successful
scans. Protocol fixtures and entitlement-boundary tests always run.

The language adapter is callable through the SDK. This milestone does not add
filesystem discovery, a CLI verification workflow, API endpoints, durable
workers, or migrations. The CLI continues to state that verification composition
is unimplemented. Live container and optional-tool validation require their
external prerequisites.
