# Shared Replay architecture

SafePatch has one `replay.Engine`. Redaction, ReplayDataset, fixture admission,
behavior comparison and telemetry ports are edition independent. Hosted
orchestration uses the existing organization resolver, entitlement runtime,
permission service and transactional quota ledger. No BusinessReplay or
EnterpriseReplay implementation exists.

## Privacy and ReplayDataset

`replay.NewRedactor` freezes an explicit list of trusted scalar JSON pointers,
such as `/quantity` or `/response/status`. Capture projects only those paths.
Unlisted fields and their names are dropped. Whole objects or arrays cannot be
allowlisted; ambiguous JSON, duplicate keys, invalid Unicode and oversized
inputs fail closed. Secret-like field names are always replaced with
`[REDACTED]`, even if mistakenly allowlisted. Recognizable bearer/basic tokens,
private-key markers, selected token prefixes and email addresses in allowed
string values are also masked. No original-value hash is retained.

This is a deny-by-default data projection with additional secret detection, not
a universal PII classifier. Operators must review allowlisted paths and use
opaque non-sensitive dataset, record and correlation identifiers. An allowed
scalar can contain sensitive information that pattern detection does not know.
Redaction may remove inputs needed by the candidate; use deidentified fixtures
appropriate to the approved projection rather than recovering secrets at runtime.

`NewDataset` applies that same policy to input, expected output and every
dependency fixture before constructing immutable bytes. The dataset includes:

- Organization/repository scope and immutable dataset ID.
- UTC creation/exclusive-expiry instants and the privacy-policy digest.
- Ordered records containing redacted input, expected observation and fixtures.
- Optional source trace/span IDs for correlation.

`DecodeDataset` requires the same trusted redaction policy and re-applies it.
Changed privacy policy never silently expands a stored dataset. The artifact is
bounded to 4 MiB, 64 records, 32 fixtures per record and 16 KiB per source JSON
object. No executable path, remote URL, credential or arbitrary command appears
in the execution contract. Dataset decoding does not fetch references or extract
archives. Fields and JSON serialization return owned copies.

The shared [OpenAPI specification](../../api/openapi/openapi.yaml) includes
ReplayDataset, observation and result artifact schemas. No separate edition
schema or replay HTTP endpoint is introduced in this architecture milestone.

## Side-effect sandbox and candidate protocol

`SideEffectSandbox.Check` admits only PURE, READ and WRITE fixture classifications.
Reads and writes are recorded fixtures, never permission to contact live systems.
DESTRUCTIVE and UNKNOWN classifications block the case before execution.
Expected dependency effects must reference available fixtures; an unrecorded
effect in the candidate observation makes the comparison UNKNOWN.

The concrete `replayadapter.Sandbox` accepts the existing
`runner.ContainerSandboxRunner`, not a trusted-local runner. It reuses the
container runner's pinned image/tool policy, network isolation, read-only root,
temporary writable workspace, dropped capabilities and CPU/memory/PID/output
bounds. It supplies an empty environment; no ambient production credentials are
passed. A server-owned temporary root must be outside repositories and protected
by operator ACLs. No client-provided paths or executable arguments select a host
resource.

The candidate is a trusted configured executable preinstalled in the pinned
image. It reads `replay.json` from its working directory:

```json
{"input":{"quantity":1},"fixtures":[{"id":"inventory_read","effect":"READ","input":{"quantity":1},"output":{"quantity":2}}]}
```

The expected baseline is deliberately excluded. The executable must apply its
dependency shims using the supplied fixtures and emit exactly one bounded JSON
observation on stdout:

```json
{"complete":true,"status":200,"output":{"quantity":2},"effects":["inventory_read"]}
```

This milestone defines and enforces the generic protocol, not language-specific
application instrumentation or shims. A candidate cannot gain host/network
access by reporting itself sandboxed. Only infrastructure runner results can
establish sandbox execution and confirmed cleanup. Nonzero exit, truncation,
malformed output and execution failures become UNKNOWN. If cleanup is uncertain,
the application retains its concurrency reservation until an operator confirms
the process/container has stopped. Temporary redacted inputs are removed after
execution. No tool output or stderr text becomes an authorization decision.

## Comparison

One engine compares status, the canonical redacted output projection and ordered
dependency effects. It does not compare raw private values or use an LLM judge.
Both observations must be complete and have a known status. Invalid/incomplete
observations are UNKNOWN; a known mismatch is DIFFERENT; matching selected
observations are MATCH. Any UNKNOWN case makes the aggregate UNKNOWN, including
when another case is different. Results contain case IDs and fixed reason codes,
not raw candidate output or backend errors.

MATCH means parity of the observed projection, not full behavior equivalence,
verification PASS, absence of hidden side effects, or production approval. A
malicious candidate can lie in its observation; isolation limits its effects,
while the report remains untrusted evidence. Captured expected observations must
come from an appropriately trusted capture process for the result to be useful.

## Organization access and limits

`application.ReplayService` provides `Capture`, `Read` and `Run`. Capture and Run
require `runtime.replay` and independently `replay.execute`; Read requires the
same feature and `replay.view`. Tenant ownership is resolved first. Feature and
permission checks are refreshed before dispatch. The configured admission hook
remains mandatory and must reserve any additional deployment-specific quotas.

| Limit | Accounting and enforcement |
| --- | --- |
| `replay_requests_monthly` | Existing monthly resource. Reserve and commit one accepted execution attempt before dispatch; committed attempts are not refunded. |
| `dataset_retention_days` | New ceiling resource. Capture checks the requested duration; reads and dispatch also check the current window. Missing/disabled policies deny. |
| `replay_concurrency` | New capacity resource. Atomic reservation before execution; release only after known termination. Shared across replicas through the existing ledger. |

Reservation IDs bind organization, repository and run identity. Durable run
claims precede quota admission, preventing concurrent/retried run IDs from
executing twice. A denied or interrupted claim remains inert UNKNOWN. Monthly
denial releases an acquired concurrency slot. Cancellation uses a fresh bounded
cleanup context for quota release after execution has stopped.

The application caps one run at five minutes and at the dataset's expiry.
Expiry is exclusive. Lowering retention makes older datasets inaccessible
without rewriting them; raising it never extends their original expiry.
Downgrades preserve datasets, comparison history and consumed quota. Failed
cleanup, a crashed worker or an uncertain ledger commit requires operational
reconciliation; there is no automatic refund or unsafe rerun.

No edition feature assignment or numeric allowance is invented. The two new
resource keys default disabled until trusted provisioning supplies effective
organization limits. Old v0.2 edition profiles retain their eight historical
keys and immutable bytes; they are not rewritten to add new limits. Existing
feature grants and replay monthly allowances still need explicit provisioning.

## Storage and migration

`replaystore.Store` writes only sealed redacted bytes through the existing
tenant-scoped `evidence.OriginalStore` contract. PostgreSQL stores bounded
metadata and durable run identity/results. The deployment must supply private
immutable object storage implementing that contract; no new public URL fetcher
or second artifact-store architecture is introduced. Object scope uses the
organization, repository and dataset ID, and reads verify scope, digest, length
and privacy-policy identity.

Migration `000014_replay.up.sql` adds the two resource keys to the existing quota
catalog, extends its period constraint for Replay capacity, and adds
`replay_datasets` and `replay_runs`. Composite foreign keys prevent cross-tenant
dataset references. Dataset identity and expiry are immutable; each run can be
claimed once and finalized once. Existing subscriptions, edition limits and
quota history are preserved.

The bounded memory store supports local embedding and integration tests only.
It is not selected as a multi-process durable backend. Replay retention currently
enforces access windows. Physical object deletion, expiry scheduling and cleanup
of orphaned uploads require object-store lifecycle/worker composition; this
milestone does not claim expired bytes have already been erased. Metadata history
remains immutable.

## OpenTelemetry correlation

`replayadapter.NewObserver` accepts the deployment's OpenTelemetry
`trace.TracerProvider`. It emits one span per case with organization, repository,
run, record and comparison decision. Replay spans start new traces and link to
the recorded source span; they do not become children of a caller-controlled
production trace. Baggage is discarded and no raw input, output, credential or
exception message is attached. The implementation uses the
[OpenTelemetry Go tracing API](https://opentelemetry.io/docs/languages/go/instrumentation/)
and is tested with a real SDK in-memory exporter. Exporter destination,
authentication, sampling and lifecycle are deployment configuration.

## Validation and inventory

Tests cover privacy projection and poisoning, immutable round trips, blocked
effects, comparison/UNKNOWN semantics, sandbox result validation, baseline
exclusion, OpenTelemetry links and baggage stripping, entitlement/permission
boundaries, retention downgrade, monthly quota, concurrent capacity, durable
claim semantics and uncertain-cleanup capacity retention. Migration contract and
native SQL fixtures check new keys, tenant references, expiry immutability,
duplicate runs and preserved commercial data. Native SQL remains opt-in.

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'
$env:GO_TELEMETRY_CHILD = '2'
go test ./...
go test -race ./...
go vet ./...
go test ./internal/replay -run '^$' -fuzz FuzzDataset -fuzztime 5s -parallel 2
```

Validation on 2026-09-07: the full Go test suite, race suite and `go vet ./...`
passed. Dataset fuzzing passed with 50,399 executions. The replay migration
contract passed; native PostgreSQL execution was skipped because
`SAFEPATCH_POSTGRES_MIGRATION_TEST=1` and its database environment were not
configured. Live container, object-store and telemetry-exporter behavior has
not been validated in a deployment.

Inspected: replay boundary; verification runner and container implementation;
immutable evidence store; entitlement resource keys, quota service and stores;
edition profile decoder; admission guards; architecture rules; migration harness;
OpenAPI and OpenTelemetry documentation.

Created: `internal/replay/{dataset,engine}.go` and tests;
`internal/application/replay_service.go`;
`internal/platform/replayadapter/{sandbox,telemetry,baggage}.go` and tests;
`internal/platform/replaystore/{store,memory}.go` and service tests; migration
000014, contract/native fixtures; this document.

Modified: `internal/replay/doc.go`, `internal/domain/entitlement/limit.go` and its
test, `internal/config/editions_v02.go`, the migration quota-key contract test,
`api/openapi/openapi.yaml`, `go.mod` and `go.sum` for OpenTelemetry v1.41.0.

Remaining deployment work: a language-specific candidate/shim, actual pinned
container image, private object-store composition, exported telemetry provider,
dataset cleanup and crash reconciliation. No replay HTTP handler, NATS dispatch
worker or new CLI command is introduced. The existing command shell remains
unchanged. Native PostgreSQL and live container behavior must be validated in
the target deployment; mock runner and SDK tests do not establish that gate.

Next prompt: "Wire the shared ReplayService into a durable worker using a
controlled fixture-aware candidate, then validate PostgreSQL limits, container
isolation, object expiry and crash reconciliation end to end."
