# TestTrust

`internal/proof/testtrust` owns one shared snapshot schema, result schema and
comparison engine. No edition metadata enters extraction, assertion semantics or
results. `internal/platform/testtrustgo` supplies a Go AST adapter; it does not
execute source, follow imports, open repository paths or invoke a shell.

## Detectors

| Finding | Evidence used |
| --- | --- |
| REMOVED_TEST | A stable baseline test ID is absent from a complete head manifest. |
| REMOVED_ASSERTION | A baseline assertion ID is absent from a complete head test extraction. |
| WEAKENED_ASSERTION | The old numeric predicate strictly implies the new predicate, for the same subject. |
| SKIPPED_TEST | A head test is statically skipped or conditionally skipped, including existing skips. |
| COVERAGE_REDUCTION | The head covered/total ratio is lower under matching instrumentation configuration. |
| NEGATIVE_TEST_REMOVED | A known negative test is removed, or explicitly reclassified as positive. |

Numeric implication supports EQ, NE, GT, GE, LT and LE with exact decimal bounds.
It evaluates the finite truth regions defined by the two predicate boundaries,
using rational arithmetic. This recognizes equality relaxed to a bound, strict
bounds made inclusive, and thresholds relaxed in either direction. Strengthening
and equivalent decimal spellings do not trigger weakening. Incomparable changes
emit ASSERTION_CHANGE_UNCLASSIFIED and incomplete analysis. A changed hash alone
never establishes that an assertion became weaker.

Coverage counts are bounded to one billion, so integer cross multiplication is
exact and cannot overflow. The caller must bind the report to the correct
repository/revision and trusted instrumentation/cohort configuration. Missing,
zero-denominator or differently configured coverage is not equivalent to 100%
coverage. Invalid counts are rejected; unavailable coverage makes analysis UNKNOWN.

The result is NO_REGRESSION, REGRESSION or UNKNOWN. NO_REGRESSION requires complete
discovery, supported assertions, known test classification and comparable coverage.
It is not PASS and does not establish that tests executed successfully. Confirmed
regression findings remain visible even when other analysis is incomplete; the
result then contains REGRESSION with `incomplete: true`. Cancellation and invalid
inputs return an error and UNKNOWN. Empty test suites never produce a clean result.

## Go extraction

Provide the complete pinned `_test.go` file manifest to `testtrustgo.Extract`,
including both manifests when a file is deleted. The `complete` argument is a
trusted manifest guarantee, not a claim that the parser can prove from a slice.
Paths are validated as repository-relative logical identifiers. The extractor
binds sorted file paths and content hashes into the snapshot source digest.

The adapter recognizes native `TestX(*testing.T)` functions, including aliased
testing imports. Test IDs derive from path and function name. Assertion IDs derive
from the normalized subject expression and its occurrence within the test. Renames
are treated as removal/addition; assertion alignment is not semantic refactoring
detection.

Direct `if got != 10 { t.Fatal(...) }` and related numeric comparisons against
literal decimal constants are modeled by inverting the failure predicate. Direct
`t.Skip`, `t.Skipf` and `t.SkipNow` are detected, including conditional calls on
the recognized testing receiver. Comments and failure messages are not scanned
as executable assertions, and their text is not included in findings.

Complex helpers, external assertion libraries, named expected values, nil/string
comparisons, loops, dynamic subtests, deferred work and unrecognized calls remain
incomplete. This is deliberate: parsing source cannot prove arbitrary assertion
semantics. Native predicates outside the supported subset retain only their
subject/predicate hashes and UNKNOWN relation. Build tags, runtime reachability,
successful compilation and test execution are not established by this adapter.

Negative classification can come from a trusted normalized extractor. The initial
Go adapter also reads exact function documentation markers:

```go
// safepatch:testtrust negative
func TestReject(t *testing.T) { /* ... */ }
```

The corresponding `positive` marker is supported. Unmarked tests have UNKNOWN
classification. These markers are source declarations, not verified negative-test
semantics; test names do not establish classification. Deleting a declared negative
test is detected, but inferring arbitrary negative test intent is not implemented.

Snapshots are bounded to 2 MiB, 1024 tests and 4096 assertions. Go source extraction
accepts at most 256 files and 2 MiB aggregate source. Decode rejects duplicate keys,
unknown fields, aliases, invalid paths, malformed predicates and invalid counts.
Snapshots own canonical immutable bytes; accessors return copies. Content hashes
provide integrity, not authenticity, encryption or secret anonymization. Keep
source-derived metadata and artifacts tenant-private.

## Shared application admission

`application.TestTrustService.Compare` uses the existing tenant resolver,
`verify.basic` capability, independent `proof.execute` permission and mandatory
QuotaChecker. Static TestTrust can therefore be used by Community-compatible basic
verification as well as hosted verification. No edition branch or paid-only schema
exists. Admission failure occurs before source loading.

The injected TestTrustSource must load authoritative, scoped, revision-pinned
snapshots and coverage. The service validates organization, repository and exact
requested revision on both returned snapshots, and rejects mixed producer versions.
Repository/scanner errors are replaced with a generic unavailable error. An HTTP
body containing a self-declared snapshot is not an authoritative source adapter.

Mutation execution is outside this milestone. Every result currently records
`mutation_status: NOT_RUN`; there is no stub that reports mutation success. The
existing `testtrust` catalog entry does not by itself start an executor. A future
advanced mutation operation may require configured capabilities, independent
permissions and resource reservations before using the existing secure Runner.
That admission policy must not select another TestTrust schema or comparison engine.

No migrations, dependencies or edition bundles change. These are derived evidence
artifacts, not a new transactional store. The single OpenAPI document contains the
snapshot and result schemas. No new endpoint, worker, CLI command, approved-policy
integration or durable artifact store is claimed by this milestone.

## Validation and file inventory

Tests cover all six findings, exact numeric implication, incomplete evidence,
coverage provenance mismatch, immutable round trips, decoder poisoning, paths,
source bounds, unsupported Go structures, scope/revision substitution, independent
admission and all-five-edition parity. Source-to-result integration tests use actual
Go snippets. Grant revocation/restoration preserves existing snapshots.

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'
$env:GO_TELEMETRY_CHILD = '2'
go test ./...
go test -race ./...
go vet ./...
go test ./internal/proof/testtrust -run '^$' -fuzz FuzzSnapshot -fuzztime 5s -parallel 2
```

Validation on 2026-09-07: the final `go test ./...`, `go test -race ./...` and
`go vet ./...` runs passed. The bounded decoder fuzz smoke test passed with 39
executions; this short run is not extensive fuzz coverage. Existing migration
contracts passed. Native PostgreSQL migration execution was skipped because its
opt-in database environment is not configured. No live workflow validation is
claimed for the unwired PR/local entrypoints.

Inspected: SIR model/extraction, LanguagePack SDK, verification Runner, evidence
validation, feature catalog, ExecutionGuard, proof service admission, existing
test helpers, architecture rules and OpenAPI.

Created: `internal/proof/testtrust/{model,engine,engine_test}.go`,
`internal/platform/testtrustgo/{extract,extract_test}.go`,
`internal/application/{testtrust_service,testtrust_service_test}.go`, and this guide.
Modified: `api/openapi/openapi.yaml`. Migrations: none required.

Next prompt: "Wire TestTrust into the shared PR/local verification workflow using
authoritative base/head manifests and coverage, preserving UNKNOWN and publishing
tenant-scoped evidence through the existing policy pipeline."
