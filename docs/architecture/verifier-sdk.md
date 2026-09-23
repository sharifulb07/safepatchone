# Capability-based Verifier SDK

The shared `internal/verification` package exposes a Go extension contract:

```go
type Verifier interface {
    ID() string
    Version() string
    Capabilities() []Capability
    Detect(context.Context, Target) (Detection, error)
    Plan(context.Context, PlanRequest) (Plan, error)
    Execute(context.Context, Plan, Runner) error
    Normalize(context.Context, NormalizationRequest) (evidence.Report, error)
}
```

IDs are stable bounded identifiers. Version identifies the implementation,
independently of the SafePatch release or edition. Capabilities describe technical
work, such as `test`, `lint`, or `replay.check`; they are not commercial grant keys.
Trusted application composition maps each selected operation to its required
product entitlement, actor permission, tenant scope, and quota reservation.
Discovery alone grants no access.

`NewRegistry(verifiers...)` validates and freezes descriptors, rejects duplicate
IDs/capabilities, and prevents silent replacement of an implementation. Use
`registry.Discover(capability)` for sorted, defensively copied descriptors. Build
a new registry when changing installed implementations. Implementations must
be safe for concurrent use; the SDK does not copy the verifier object itself.

## Lifecycle

Call `registry.Run(ctx, verifierID, planRequest, runner)` inside an admitted
application/worker operation. Existing `WorkerOperation.Start` reloads trusted
work provenance and checks current access on every dispatch. Do not expose SDK
inputs as an unauthenticated API, accept runner policy from repository content,
or let an external caller change the capability-to-entitlement mapping.

1. **Detect:** examine a bounded, validated file manifest and immutable source
   identity from trusted repository discovery. Optional `Target.Metadata` holds
   at most 256 listed manifest files and 1 MiB of UTF-8 content, copied between
   stages. DETECTED lists a subset of the
   registered capabilities. NOT_APPLICABLE produces SKIPPED; unavailable and
   uncertain detection produce UNAVAILABLE and UNKNOWN. Empty/invalid detection
   never implies applicability or success. Detect has no Runner access.
2. **Plan:** produce one to sixteen named execution steps. Reuse the supplied
   `BaseExecution` policy and choose configured tools, literal arguments and
   relative working directories. Unavailable optional tools/data may return
   `ErrVerifierUnavailable`, yielding UNAVAILABLE without execution. Plans cannot change source, environment,
   execution mode or resource limits. Aggregate capture has a separate SDK
   memory ceiling. Empty plans cannot become PASS. Plan does not execute tools.
3. **Execute:** invoke the provided Runner with the exact planned requests in
   order. `verification.ExecutePlan` is the reusable sequential implementation.
   A scoped wrapper records real execution results, rejects changed/additional
   commands, retains errors even if the verifier ignores them, and prevents use
   after execution ends. It retains the parent cancellation/deadline even when
   an implementation supplies another context. Required steps cannot be skipped.
4. **Normalize:** parse the recorded output into an `evidence.Report`. Normalizers
   receive copies and cannot supply the authoritative verifier/source identity
   or execution provenance. Malformed, truncated, unavailable, cancelled, or
   uncertain execution cannot become PASS. A nonzero scanner exit can be
   normalized into findings, but cannot become PASS. Exit zero alone is never a
   verification result: a valid normalized report is still required.

The lifecycle deadline uses the trusted execution timeout. Contexts are checked
between stages and errors are wrapped in stable `LifecycleError` messages;
`errors.Is/As` retain internal diagnostics without printing backend or panic
payloads. Panics on the lifecycle goroutine become ERROR. This is a cooperative
Go API for reviewed extensions, not isolation for arbitrary Go plugin code.
Extensions must honor cancellation and complete all their work before returning.
Use `ContainerSandboxRunner` for repository-derived processes. No dynamic plugin
loading, scanner implementation, CLI verification, or production approval is
introduced by this milestone.

The initial lifecycle helper validates Runner-backed plans. Pure in-process
analyses can implement the interface, but require a separately designed execution
and provenance contract before zero-step plans can produce successful evidence.
This avoids treating absent execution as completed analysis.

## Normalized evidence

`internal/evidence` owns the `safepatch.verification.v1` contract. All verifiers
use exactly these statuses:

| Status | Meaning |
| --- | --- |
| PASS | The required check completed and the normalizer explicitly reports success. |
| FAIL | The check reports a failure. |
| WARN | The check reports warnings without an error-severity finding. |
| ERROR | The verifier or execution infrastructure encountered an error. |
| SKIPPED | The check was not applicable or reports it was skipped. |
| UNAVAILABLE | A required verifier/tool was unavailable. |
| UNKNOWN | The available evidence cannot establish a known outcome. |

Only PASS is success. There is no aggregation rule that converts WARN, SKIPPED,
UNAVAILABLE or UNKNOWN into PASS, and no status directly authorizes production.

Reports contain status, summary, and structured findings with stable rule ID,
severity (INFO/WARNING/ERROR), message, and optional repository-relative location.
Reports reject unknown enum values, traversal/absolute/URL locations, terminal
control characters, inconsistent positions, excessive size, and contradictory
PASS/error findings. HTML is data: downstream renderers must escape it, and
evidence retention must apply redaction/access policy before publishing content.

`evidence.DecodeReport` is an optional strict parser for tools that emit the
exact report schema. It reuses the existing bounded JSON-object validator to
reject duplicate keys, invalid Unicode and excessive nesting, then rejects
unknown fields and invalid reports. Field names must match exactly; case aliases cannot override
statuses or findings. Tools with other output formats need a
reviewed normalizer; do not scan free text for the word PASS.

The SDK attaches schema version, verifier ID/version, requested capability,
repository/revision identity, and ordered execution summaries with output SHA-256
digests. Raw output, argv, environments, host paths, and arbitrary artifact URLs
are not serialized into the record. Digests establish content identity, not
authenticity. Report text remains untrusted and may itself contain sensitive data.

`Record.CanonicalJSON()` validates and returns deterministic JSON: findings are
sorted/deduplicated and empty arrays are `[]`. There are no edition or subscription
fields. The same inputs and normalized results produce identical evidence across
grant changes. Tenant-scoped persistence and signatures remain separate concerns;
record validation alone does not prove repository ownership or authenticity.

## Example extension and validation

For a tool that already emits the normalized report schema, use this pattern:

```go
func (v MyVerifier) Execute(ctx context.Context, p verification.Plan, r verification.Runner) error {
    return verification.ExecutePlan(ctx, p, r)
}

func (v MyVerifier) Normalize(ctx context.Context, input verification.NormalizationRequest) (evidence.Report, error) {
    if err := ctx.Err(); err != nil { return evidence.Report{}, err }
    // This example verifier plans exactly one step.
    return evidence.DecodeReport(input.Results[0].Result.Stdout)
}
```

The SDK tests include a complete fixture implementing all six lifecycle/identity
methods plus capability metadata. Tests cover discovery, identity freezing,
all statuses, poisoning, limits, incomplete execution, policy/argv changes,
cancellation, errors/panics and concurrent use. Worker integration verifies
entitled access, denied grants, independent permission checks, tenant isolation,
and byte-identical evidence after grants are restored. Fixtures do not implement
or enable the premium runtime replay feature.

Run `go test ./...`, `go test -race ./...`, and `go vet ./...`. No schema migration,
edition bundle change, frontend, alternate release stream, or external dependency
is required.
