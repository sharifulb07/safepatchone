# BehaviorDNA and business invariants

`internal/runtime` implements one immutable BehaviorDNA format and one
`BusinessInvariantEngine`. Business and Enterprise use exactly the same types,
decimal arithmetic and execution code. Software version, edition and release
channel never enter either evaluator.

## BehaviorDNA

`BuildBehaviorDNA` derives `safepatch.behaviordna.v1` from the existing redacted
ReplayDataset. It describes its recorded `Expected` observations; it does not
pretend to capture new candidate outcomes. A caller can capture a separate
redacted dataset of candidate observations through the existing capture flow.

Trusted configuration selects named numeric scalars from INPUT or OUTPUT using
literal JSON pointers. Each case records completion, status, ordered effect IDs
and the selected measurements. No raw strings, request payloads, trace baggage or
unredacted source hashes are added. Missing, null, redacted, nonnumeric and
out-of-range numbers remain absent; they are not coerced to zero. The existing
privacy allowlist must approve numeric fields too: account numbers and other
numeric identifiers are not automatically safe to retain.

The profile binds organization, repository, dataset ID/digest, privacy digest and
the source retention window. Canonical JSON and SHA-256 provide immutable content
identity, not authentication. Decode rejects duplicate keys, unknown fields,
case aliases, noncanonical rationals and invalid bounds. Accessors return copies.

`CompareBehavior` compares profiles using the same scope, projection and privacy
policy. It compares cases by ID, numeric values exactly, status and ordered
effects. Its result is MATCH, DIFFERENT or UNKNOWN. Missing cases or measurements
and incomplete observations dominate differences with UNKNOWN. MATCH describes
only the selected observations, not unobserved behavior or production safety.

## BusinessInvariant engine

`safepatch.business-invariants.v1` is an immutable, explicitly versioned policy
bound to an organization and repository. Every rule evaluates a linear numeric
expression against a constant with EQ, LE or GE, for every recorded case.

For example, this rule checks that amount plus tax equals total:

```json
{
  "schema": "safepatch.business-invariants.v1",
  "scope": {"organization_id": "org", "repository_id": "repo"},
  "policy_id": "accounting",
  "version": "1",
  "rules": [{
    "id": "conservation",
    "terms": [
      {"metric": "amount", "coefficient": "1"},
      {"metric": "tax", "coefficient": "1"},
      {"metric": "total", "coefficient": "-1"}
    ],
    "operator": "EQ",
    "bound": "0"
  }]
}
```

Configure the corresponding metrics as OUTPUT `/amount`, `/tax` and `/total`.
Exact rational arithmetic makes 0.1 + 0.2 = 0.3 without floating point rounding.
Units and currencies must be chosen consistently by the trusted policy author;
the evaluator does not infer them. Rule constants and coefficients are bounded
decimal strings, including exponents with at most two digits. Zero coefficients,
duplicate metric terms, empty policies and unknown operators are rejected.

Results are SATISFIED, VIOLATED or UNKNOWN, with one check per rule and case.
Missing measurements, incomplete records or missing required metrics produce
UNKNOWN. UNKNOWN dominates the overall result while individual violations remain
visible. Invalid inputs, scope mismatch, expiration and cancellation return an
error and UNKNOWN. No result is an execution authorization. The engine contains
no script interpreter, shell command, network client or LLM authorizer.

## Application admission and authoritative source

`application.BehaviorService` provides four guarded operations:

| Operation | Organization capability | Actor permission |
| --- | --- | --- |
| Describe / Compare | `runtime.behaviordna` | `replay.view` |
| CompileInvariants | `business.invariants` | `policy.manage` |
| Evaluate | `business.invariants` | `proof.execute` |

Every call uses the existing tenant resolver, EntitlementService, independent
permission checker and mandatory QuotaChecker. No edition comparison grants
access. Metrics are trusted configuration supplied at construction, not user
prompt instructions. Compile validates a proposed policy; it neither activates
it nor replaces an approved organization policy. Evaluation of a supplied policy
is diagnostic, including when that policy was decoded outside the service.

Inject the existing ReplayService as `BehaviorDatasetReader`. It applies current
`runtime.replay`, source permissions and `dataset_retention_days` checks. Thus this
initial replay-backed integration also requires authorized replay access.
Describe reads that source, and Compare/Evaluate re-read and re-derive profiles.
A copied profile cannot extend source retention, hide a source downgrade, swap
tenants or change measurements. A new projection invalidates old profiles for
hosted evaluation; re-derive from an accessible dataset. A raw storage adapter
does not meet the reader's authoritative admission contract.

Limits and hosting differences remain deployment and organization configuration.
The current hard safety bounds are 64 cases, 64 metrics, 64 rules, 64 terms per
rule, and 1 MiB per artifact; quotas may lower these effective workloads through
admission. Enterprise-specific larger allowances must remain within supported
safety bounds and use the shared quota policy. Self-hosted execution and additional
governance remain separate capability checks at their existing deployment or
operation boundaries. They do not change this schema or engine.

Revoking capabilities denies hosted operations without rewriting existing source
datasets, policies or profiles. Required production policy evaluation must never
silently disappear after a downgrade: orchestration must block when required
evidence cannot be obtained. This milestone does not wire invariant results into
production policy or trust decisions.

## Persistence and API

No migration is needed: these are derived immutable artifacts and pure policy
documents, and source persistence/retention reuses the Replay schema and service.
There is no new subscription, edition bundle, usage ledger or automatic grant.
The single OpenAPI specification defines BehaviorDNA, BusinessInvariantPolicy
and BusinessInvariantResult components; no new HTTP endpoint is claimed.

This milestone does not add an artifact retention store, policy approval registry,
worker dispatcher, dashboard, capture agent, statistical model, learned invariant
generator, or cross-case temporal invariant language. No external infrastructure
is required to evaluate the core. Persisting or publishing artifacts later must
use the existing tenant-scoped immutable evidence storage and authoritative
retention/governance policies. Source expiry access enforcement does not imply
physical deletion of previously exported copies.

## Validation

Unit/security/error tests exercise exact decimal arithmetic, large integers,
unknown inputs, poisoning, bounded exponent parsing, immutable round trips,
comparison, tenant mismatch, expiry and cancellation. Admission tests cover both
capabilities, all four operations, permissions after entitlement, quotas and
identical artifacts across all five editions. Integration tests use the real
ReplayService and quota/memory stores to check altered profile rejection, current
retention downgrade, restored access and preserved source data. Existing migration
contracts remain part of the full suite; native PostgreSQL tests remain opt-in.

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'
$env:GO_TELEMETRY_CHILD = '2'
go test ./...
go test -race ./...
go vet ./...
go test ./internal/runtime -run '^$' -fuzz FuzzBehaviorArtifacts -fuzztime 5s -parallel 2
```

Validation on 2026-09-07: `go test ./...`, `go test -race ./...` and
`go vet ./...` passed. Artifact decoder fuzzing passed with 35,722 executions.
The Replay migration contract passed; native PostgreSQL execution was skipped
because its opt-in database environment is not configured. No live infrastructure
validation or production policy integration is claimed.

Inspected existing files include `internal/replay/dataset.go`,
`internal/application/{execution_guard,policy_service,replay_service}.go`,
`internal/authorization/permission.go`, `internal/domain/feature/builtin.go`,
`internal/domain/jsonobject/object.go`, `internal/evidence/original.go`,
`internal/platform/replaystore/service_test.go`, architecture and migration tests,
edition configuration and the single OpenAPI specification.

Created files:

- `internal/runtime/behavior.go`
- `internal/runtime/invariant.go`
- `internal/runtime/behavior_test.go`
- `internal/application/behavior_service.go`
- `internal/application/behavior_service_test.go`
- `internal/platform/replaystore/behavior_integration_test.go`
- `docs/architecture/behavior-invariants.md`

Modified files: `internal/runtime/doc.go` and `api/openapi/openapi.yaml`.
No dependencies, edition bundles or migrations were changed.

Next prompt: "Connect approved BusinessInvariant policies to the shared proof
and policy workflow, preserving UNKNOWN and blocking when required evidence is
unavailable, with tenant-scoped artifact persistence and retention."
